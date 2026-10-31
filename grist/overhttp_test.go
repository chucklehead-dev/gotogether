package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

const transportTriggerID = "00000000000000000000000000000001"
const transportTriggerID2 = "00000000000000000000000000000002"

func triggerIDAt(log []grist.Stored, pos int64) string {
	for _, event := range log {
		if int64(event.Pos) == pos {
			return event.ID
		}
	}
	return ""
}

// serveBank serves h behind grist.ServeCalls on a loopback server, its
// site recorded into a sink of its own (the bank's recorder).
func serveBank(t *testing.T, bank *grist.Dependency, h grist.Handlers) (*httptest.Server, *qobs.MemorySink) {
	t.Helper()
	sink := &qobs.MemorySink{}
	srv := httptest.NewServer(qobshttp.Handler(grist.ServeCalls(bank, h), qobs.NewRecorder(sink, qobs.WithID("bank"))))
	t.Cleanup(srv.Close)
	return srv, sink
}

// A call over HTTP end to end: the node's effects call the bank through
// grist.OverHTTP, the bank serves them behind grist.ServeCalls and records
// its site with its own recorder. The caller's steps go to the node's
// recorder, the site's to the bank's, each served record in the caller's
// tenant's trace at the caller's place (order.pos 2·H+1, carried in
// Grist-Cause), under the key the caller sent (Idempotency-Key). The
// answers map as Do's: ok with the response (the bank's ref), the
// declared refusal (grist.failed), a handler's error a 500, an unknown
// outcome (grist.unknown).
func TestCallOverHTTP(t *testing.T) {
	a, bank, pay := callsApp(grist.AtMostOnce, true)
	srv, bankSink := serveBank(t, bank, bankHandlers(pay))
	st := newStoreNamed(t, a.Name)
	sink := &qobs.MemorySink{}
	ctx, cancel := context.WithCancel(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink, qobs.WithID("node"))))
	defer cancel()
	if err := a.Start(bank.With(ctx, grist.OverHTTP(srv.URL, nil)), st); err != nil {
		t.Fatal(err)
	}
	for i, n := range []int{5, 13, 7} {
		body, _ := json.Marshal(addAmounts{A: n})
		if _, err := a.Execute(ctx, st, grist.Envelope{ID: fmt.Sprint("c", i), Tenant: "t1", Caller: "u"}, "add", body); err != nil {
			t.Fatal(err)
		}
	}
	// three caller steps, and the two outcome events: the processor
	// appends an outcome after its call's step is recorded, so the third
	// step's grist.unknown could still be on its way (once read before it)
	outcomeTypes := grist.Selector{Types: []string{"grist.failed", "grist.unknown"}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		outs, _, err := st.Read(context.Background(), "t1", outcomeTypes, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(byAction(sink.Steps(), "bankPay")) >= 3 && len(outs) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caller steps %v, outcome events %v: want three and two within 10 s", byAction(sink.Steps(), "bankPay"), outcomes(outs))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	calls, served := byAction(sink.Steps(), "bankPay"), byAction(bankSink.Steps(), "bankPayServed")
	if len(served) != 3 || len(byAction(sink.Steps(), "bankPayServed")) != 0 {
		t.Fatalf("served %v: want three, all recorded by the bank's own recorder", served)
	}
	// Each call is known by its amount, not by its trigger's position: the
	// processor appends its outcome events while the adds are still being
	// executed, so the declined payment's grist.failed may land before the
	// third add's amount (which is then trigger 4, not 3).
	log, _, err := st.Read(context.Background(), "t1", grist.Selector{Types: []string{"amount", "grist.failed", "grist.unknown"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	posOf := map[int64]int64{}
	var outs []grist.Stored
	for _, e := range log {
		var am amount
		if e.Type == "amount" && json.Unmarshal(e.Body, &am) == nil {
			posOf[int64(am.N)] = int64(e.Pos)
		} else if e.Type != "amount" {
			outs = append(outs, e)
		}
	}
	want := map[int64]string{5: "ok", 13: "declined", 7: "unknown"}
	byTrigger := map[int64]qtrace.Step{}
	for _, c := range calls {
		tr, n := c.Args["trigger"].(int64), c.Args["amount"].(int64)
		byTrigger[tr] = c
		if c.Outcome != want[n] || tr != posOf[n] {
			t.Errorf("caller step of amount %d at trigger %d: outcome %q, want %q at the amount's position %d", n, tr, c.Outcome, want[n], posOf[n])
		}
		if n == 5 && c.Args["ref"] != "ref-"+fmt.Sprintf("calls/payer/t1/%d@%s", tr, triggerIDAt(log, tr)) {
			t.Errorf("caller step %v: want the bank's ref, decoded from its answer", c)
		}
	}
	for _, s := range served {
		tr := s.Args["trigger"].(int64)
		c := byTrigger[tr]
		if s.Actor != "calls/t1" || s.Process != a.Name || s.Recorder != "bank" || s.Args["key"] != c.Args["key"] || s.Args["triggerId"] != triggerIDAt(log, tr) || c.Args["triggerId"] != triggerIDAt(log, tr) || s.Args["processor"] != "payer" || s.Order["pos"] != c.Order["pos"] || s.Args["amount"] != c.Args["amount"] {
			t.Errorf("served step %+v: want the caller's tenant and process, the bank's recorder, the caller's key, processor, request and order.pos (%+v)", s, c)
		}
	}
	if got := outcomes(outs); len(got) != 2 || !strings.HasPrefix(got[0], "grist.failed") || !strings.HasPrefix(got[1], "grist.unknown") ||
		!strings.Contains(got[0], fmt.Sprintf(`"trigger":%d`, posOf[13])) || !strings.Contains(got[1], fmt.Sprintf(`"trigger":%d`, posOf[7])) {
		t.Errorf("outcome events %v: want the declined payment's (trigger %d) grist.failed and the failed one's (trigger %d) grist.unknown (a 500: it may have happened)", got, posOf[13], posOf[7])
	}
}

// ServeCalls answers what it cannot serve without applying or recording
// anything, and OverHTTP takes each such answer for an unknown outcome
// (only a declared refusal says the call did not happen): a request
// without its cause or key, of another App, of a processor that does not
// use the call, under a key that is not its effect's, or that does not
// decode, and a call the dependency does not declare; a method other
// than POST is 405. A key answered before is the bank's replay
// (Replayed: <call>Replayed, its first answer).
func TestServeCallsRefuses(t *testing.T) {
	_, bank, pay := callsApp(grist.AtMostOnce, true)
	srv, sink := serveBank(t, bank, bankHandlers(pay))
	ctx := context.Background()
	info := grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 3, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID}
	body, _ := json.Marshal(payReq{Amount: 5})
	tr := grist.OverHTTP(srv.URL, nil)
	for i := range 2 {
		out, err := tr.Call(ctx, info, "pay", body)
		if err != nil || string(out) != `{"ref":"ref-calls/payer/t1/1@`+transportTriggerID+`"}` {
			t.Fatalf("try %d: %s, %v: want the bank's receipt", i, out, err)
		}
	}
	unknown := func(what string, err error) {
		t.Helper()
		var rej *grist.Rejected
		if !errors.Is(err, grist.ErrOutcomeUnknown) || errors.As(err, &rej) {
			t.Errorf("%s: %v: want an unknown outcome (no declared refusal)", what, err)
		}
	}
	other := info
	other.App = "another"
	_, err := grist.OverHTTP(srv.URL, nil).Call(ctx, other, "pay", body)
	unknown("another App's call", err)
	_, err = tr.Call(ctx, info, "pay", []byte("{not json"))
	unknown("an undecodable request", err)
	_, err = tr.Call(ctx, info, "refund", body)
	unknown("an undeclared call", err)
	// a forged or misbuilt cause: a processor that makes no such call, a
	// key that is not the trigger's effect key (its record would be a
	// second application of trigger 1 under a key of its own)
	notUser := info
	notUser.Processor, notUser.Key = "refunder", "calls/refunder/t1/1"
	_, err = tr.Call(ctx, notUser, "pay", body)
	unknown("a processor that does not use the call", err)
	for _, k := range []string{"calls/payer/t1/2@" + transportTriggerID, "calls/payer/t2/1@" + transportTriggerID, "calls/payer/t1/1", "calls/payer/t1/1~1", "calls/payer/t1/1@" + transportTriggerID2, "other-key"} {
		forged := info
		forged.Key = k
		_, err = tr.Call(ctx, forged, "pay", body)
		unknown("key "+k, err)
	}
	for _, id := range []string{"", "short", strings.ToUpper("abcdefabcdefabcdefabcdefabcdefab"), "0000000000000000000000000000000g"} {
		forged := info
		forged.TriggerID = id
		_, err = tr.Call(ctx, forged, "pay", body)
		unknown("invalid trigger identity "+id, err)
	}
	for _, h := range []map[string]string{
		{},
		{grist.CauseHeader: "calls/t1/payer/1/3"},
		{"Idempotency-Key": "k"},
		{grist.CauseHeader: "calls/t1/payer/5/3", "Idempotency-Key": "k"},
		{grist.CauseHeader: "calls/t1/payer/1/3/0", "Idempotency-Key": info.Key},
		{grist.CauseHeader: "calls/t1/payer/1/3/-1/" + transportTriggerID, "Idempotency-Key": info.Key},
		{grist.CauseHeader: "calls/t1/payer/5/3/0/" + transportTriggerID, "Idempotency-Key": info.Key},
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/pay", strings.NewReader(string(body)))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("headers %v: %d, want 400 (no cause, no key, or a trigger past its head)", h, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/pay")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", resp.StatusCode)
	}
	if s, r := byAction(sink.Steps(), "bankPayServed"), byAction(sink.Steps(), "bankPayReplayed"); len(s) != 1 || len(r) != 1 || len(sink.Steps()) != 2 {
		t.Errorf("recorded %v: want the one application and its replay, nothing of the refused requests", sink.Steps())
	}
}

// OverHTTP maps a dependency's answers, whoever answers (a third party's
// service, a proxy): 2xx the response, 422 with a rejection's name the
// refusal, anything else an unknown outcome (ErrOutcomeUnknown): a 5xx,
// a dropped connection, another 4xx (no declared refusal says nothing
// happened) and a redirect, which it does not follow (a POST answered
// 303 may have been applied, and a 307 followed would send the call
// again, its key and cause with it, to wherever it points). It sends the
// call's key and cause.
func TestOverHTTPAnswers(t *testing.T) {
	var got http.Header
	var followed atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"ref":"r1"}`))
		case "/declined":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"error":"no","rejected":"declined"}`))
		case "/bad":
			http.Error(w, "bad request", http.StatusBadRequest)
		case "/proxy":
			http.Error(w, "bad gateway", http.StatusBadGateway)
		case "/reset":
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		case "/moved", "/seeother":
			code := map[string]int{"/moved": http.StatusTemporaryRedirect, "/seeother": http.StatusSeeOther}[r.URL.Path]
			http.Redirect(w, r, "/elsewhere", code)
		case "/elsewhere":
			followed.Add(1)
			w.Write([]byte(`{"ref":"r2"}`))
		}
	}))
	defer srv.Close()
	tr := grist.OverHTTP(srv.URL+"/", nil)
	info := grist.CallInfo{App: "my app", Tenant: "t 1", Processor: "payer", Trigger: 4, Head: 9, Clock: 18, TriggerID: transportTriggerID, Key: "my app/payer/t 1/4@" + transportTriggerID}
	ctx := context.Background()
	if out, err := tr.Call(ctx, info, "ok", []byte(`{}`)); err != nil || string(out) != `{"ref":"r1"}` {
		t.Errorf("200: %s, %v", out, err)
	}
	if got.Get("Idempotency-Key") != "my app/payer/t 1/4@"+transportTriggerID || got.Get(grist.CauseHeader) != "my%20app/t%201/payer/4/9/18/"+transportTriggerID {
		t.Errorf("headers %v: want the key and the cause, each part escaped", got)
	}
	var rej *grist.Rejected
	if _, err := tr.Call(ctx, info, "declined", nil); !errors.As(err, &rej) || rej.Name != "declined" {
		t.Errorf("422: %v, want the rejection declined", err)
	}
	for _, p := range []string{"bad", "proxy", "reset", "moved", "seeother"} {
		_, err := tr.Call(ctx, info, p, []byte(`{}`))
		if !errors.Is(err, grist.ErrOutcomeUnknown) || errors.As(err, &rej) {
			t.Errorf("%s: %v, want an unknown outcome", p, err)
		}
		if err != nil && strings.Contains(err.Error(), "retry it with the same id") {
			t.Errorf("%s: %v: a command's advice, which an at-most-once call must not take", p, err)
		}
	}
	// a client of the caller's own that follows redirects follows none here
	if _, err := grist.OverHTTP(srv.URL, &http.Client{}).Call(ctx, info, "moved", []byte(`{}`)); !errors.Is(err, grist.ErrOutcomeUnknown) {
		t.Errorf("307 with the caller's client: %v, want an unknown outcome", err)
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("redirects followed %d times: want none (a second send of the call)", n)
	}
}

// One ServeCalls orders its records of each key whichever node sent the
// calls: a first try from an earlier head, delayed past the retry another
// node sent from a later head, is recorded at the retry's place, not
// before it (a cycle on the key's thread). Two transports, one server.
func TestServeCallsOrderPerKey(t *testing.T) {
	_, bank, pay := callsApp(grist.AtLeastOnce, true)
	srv, sink := serveBank(t, bank, grist.Handlers{pay.Handler(func(ctx context.Context, r payReq) (payResp, error) {
		return payResp{Ref: "r"}, nil
	})})
	body, _ := json.Marshal(payReq{Amount: 5})
	node1, node2 := grist.OverHTTP(srv.URL, nil), grist.OverHTTP(srv.URL, nil)
	ctx := context.Background()
	if _, err := node2.Call(ctx, grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 7, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID}, "pay", body); err != nil {
		t.Fatal(err)
	}
	if _, err := node1.Call(ctx, grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 4, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID}, "pay", body); err != nil {
		t.Fatal(err)
	}
	served := byAction(sink.Steps(), "bankPayServed")
	if len(served) != 2 || served[0].Order["pos"] != 15 || served[1].Order["pos"] != 15 {
		t.Errorf("served %v: want both at order.pos 15", served)
	}
}

// ServeCalls with no handlers serves the dependency's default double,
// which refuses (panicking, naming the fix) a call whose response has
// fields, as DefaultHandlers does; handlers lacking a call panic too.
func TestServeCallsDefault(t *testing.T) {
	_, bank, _ := callsApp(grist.AtMostOnce, true)
	for name, h := range map[string]grist.Handlers{"nil": nil, "empty": {}} {
		func() {
			defer func() {
				p := fmt.Sprint(recover())
				if name == "nil" && !strings.Contains(p, "answers a response with fields (ref)") ||
					name == "empty" && !strings.Contains(p, "no handler of call bank/pay") {
					t.Errorf("ServeCalls(bank, %s): panic %q, want the fix", name, p)
				}
			}()
			grist.ServeCalls(bank, h)
		}()
	}
	a := grist.New("acks")
	mailer := grist.NewDependency(a, "mailer")
	send := grist.NewCall[payReq, struct{}](mailer, "send", grist.Keyed())
	grist.NewProcessor(a, "p", grist.NewEvent[amount](a, "amount"), func(ctx context.Context, r *grist.Reads, e amount, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error { _, err := send.Do(ctx, payReq{Amount: e.N}); return err }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(send))
	srv, sink := serveBank(t, mailer, nil)
	tr := grist.OverHTTP(srv.URL, nil)
	info := grist.CallInfo{App: "acks", Tenant: "t1", Processor: "p", Trigger: 1, Head: 1, TriggerID: transportTriggerID, Key: "acks/p/t1/1@" + transportTriggerID}
	for range 2 {
		if out, err := tr.Call(context.Background(), info, "send", []byte(`{"amount":1}`)); err != nil || string(out) != "{}" {
			t.Fatalf("%s, %v: want the acknowledgement", out, err)
		}
	}
	if len(byAction(sink.Steps(), "mailerSendServed")) != 1 || len(byAction(sink.Steps(), "mailerSendReplayed")) != 1 {
		t.Errorf("recorded %v: want the default's one application and its replay", sink.Steps())
	}
}

// A dependency's answer that is no declared refusal leaves an at-most-once
// effect's outcome unknown, the caller's step and the outcome event alike:
// a 4xx of no name or a redirect over HTTP, and a transport's own
// NotApplied (grist.failed beside a call recorded unknown once said "not
// paid" of a payment nothing said was not made).
func TestCallAnswerOfNoRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Idempotency-Key"), "calls/payer/t1/2@") {
			http.Redirect(w, r, "/elsewhere", http.StatusSeeOther)
			return
		}
		http.Error(w, "conflict", http.StatusConflict)
	}))
	defer srv.Close()
	notApplied := transportFunc(func(context.Context, grist.CallInfo, string, json.RawMessage) (json.RawMessage, error) {
		return nil, grist.NotApplied(errors.New("the transport's word"))
	})
	for name, tr := range map[string]grist.Transport{"http": grist.OverHTTP(srv.URL, nil), "NotApplied": notApplied} {
		t.Run(name, func(t *testing.T) {
			a, bank, _ := callsApp(grist.AtMostOnce, true)
			st := newStoreNamed(t, a.Name)
			sink := &qobs.MemorySink{}
			ctx, cancel := context.WithCancel(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
			defer cancel()
			if err := a.Start(bank.With(ctx, tr), st); err != nil {
				t.Fatal(err)
			}
			for i, n := range []int{5, 6} {
				body, _ := json.Marshal(addAmounts{A: n})
				if _, err := a.Execute(ctx, st, grist.Envelope{ID: fmt.Sprint("c", i), Tenant: "t1", Caller: "u"}, "add", body); err != nil {
					t.Fatal(err)
				}
			}
			var log []grist.Stored
			for deadline := time.Now().Add(10 * time.Second); len(log) < 2; {
				if time.Now().After(deadline) {
					t.Fatalf("outcomes %v: want two within 10 s", outcomes(log))
				}
				time.Sleep(5 * time.Millisecond)
				var err error
				if log, _, err = st.Read(context.Background(), "t1", grist.Selector{Types: []string{"grist.failed", "grist.unknown"}}, 0); err != nil {
					t.Fatal(err)
				}
			}
			for _, o := range outcomes(log) {
				if !strings.HasPrefix(o, "grist.unknown") {
					t.Errorf("outcome %s: want grist.unknown (no declared refusal)", o)
				}
			}
			for _, c := range byAction(sink.Steps(), "bankPay") {
				if c.Outcome != "unknown" {
					t.Errorf("caller step %v: want outcome unknown", c)
				}
			}
		})
	}
}

// transportFunc is f as a grist.Transport.
type transportFunc func(ctx context.Context, c grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error)

func (f transportFunc) Call(ctx context.Context, c grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	return f(ctx, c, call, req)
}
