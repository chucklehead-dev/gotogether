package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

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
		if _, err := a.Execute(ctx, st, grist.Envelope{ID: fmt.Sprint("c", i), Tenant: "t1", Actor: "u"}, "add", body); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(byAction(sink.Steps(), "bankPay")) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("caller steps %v: want three within 10 s", byAction(sink.Steps(), "bankPay"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	calls, served := byAction(sink.Steps(), "bankPay"), byAction(bankSink.Steps(), "bankPayServed")
	if len(served) != 3 || len(byAction(sink.Steps(), "bankPayServed")) != 0 {
		t.Fatalf("served %v: want three, all recorded by the bank's own recorder", served)
	}
	want := map[int64]string{1: "ok", 2: "declined", 3: "unknown"}
	byTrigger := map[int64]qtrace.Step{}
	for _, c := range calls {
		tr := c.Args["trigger"].(int64)
		byTrigger[tr] = c
		if c.Outcome != want[tr] {
			t.Errorf("caller step of trigger %d: outcome %q, want %q", tr, c.Outcome, want[tr])
		}
		if tr == 1 && c.Args["ref"] != "ref-payer/t1/1" {
			t.Errorf("caller step %v: want the bank's ref, decoded from its answer", c)
		}
	}
	for _, s := range served {
		tr := s.Args["trigger"].(int64)
		c := byTrigger[tr]
		if s.Actor != "t1" || s.Process != a.Name || s.Recorder != "bank" || s.Args["key"] != c.Args["key"] || s.Args["processor"] != "payer" || s.Order["pos"] != c.Order["pos"] || s.Args["amount"] != c.Args["amount"] {
			t.Errorf("served step %+v: want the caller's tenant and process, the bank's recorder, the caller's key, processor, request and order.pos (%+v)", s, c)
		}
	}
	log, _, err := st.Read(context.Background(), "t1", grist.Selector{Types: []string{"grist.failed", "grist.unknown"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(log); len(got) != 2 || !strings.HasPrefix(got[0], "grist.failed") || !strings.HasPrefix(got[1], "grist.unknown") {
		t.Errorf("outcome events %v: want the declined payment grist.failed and the failed one grist.unknown (a 500: it may have happened)", got)
	}
}

// ServeCalls answers what it cannot serve without applying or recording
// anything, and OverHTTP takes each answer for what it says: a request
// without its cause or key, of another App, or that does not decode, and
// a call the dependency does not declare, are refusals (NotApplied: the
// call did not happen); a method other than POST is 405. A key answered
// before is the bank's replay (Replayed: <call>Replayed, its first
// answer).
func TestServeCallsRefuses(t *testing.T) {
	_, bank, pay := callsApp(grist.AtMostOnce, true)
	srv, sink := serveBank(t, bank, bankHandlers(pay))
	ctx := context.Background()
	info := grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 3, Key: "payer/t1/1"}
	body, _ := json.Marshal(payReq{Amount: 5})
	tr := grist.OverHTTP(srv.URL, nil)
	for i := range 2 {
		out, err := tr.Call(ctx, info, "pay", body)
		if err != nil || string(out) != `{"ref":"ref-payer/t1/1"}` {
			t.Fatalf("try %d: %s, %v: want the bank's receipt", i, out, err)
		}
	}
	notApplied := func(what string, err error) {
		t.Helper()
		if err == nil || errors.Is(err, grist.ErrOutcomeUnknown) {
			t.Errorf("%s: %v: want a refusal (NotApplied), not an unknown outcome", what, err)
		}
	}
	other := info
	other.App = "another"
	_, err := grist.OverHTTP(srv.URL, nil).Call(ctx, other, "pay", body)
	notApplied("another App's call", err)
	_, err = tr.Call(ctx, info, "pay", []byte("{not json"))
	notApplied("an undecodable request", err)
	_, err = tr.Call(ctx, info, "refund", body)
	notApplied("an undeclared call", err)
	for _, h := range []map[string]string{{}, {grist.CauseHeader: "calls/t1/payer/1/3"}, {"Idempotency-Key": "k"}, {grist.CauseHeader: "calls/t1/payer/5/3", "Idempotency-Key": "k"}} {
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

// OverHTTP maps a dependency's answers as grist/client does, whoever
// answers (a third party's service, a proxy): 2xx the response, 422 with
// a rejection's name the refusal, another 4xx a refusal of no name
// (NotApplied), a 5xx or a dropped connection an unknown outcome
// (ErrOutcomeUnknown). It sends the call's key and cause.
func TestOverHTTPAnswers(t *testing.T) {
	var got http.Header
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
		}
	}))
	defer srv.Close()
	tr := grist.OverHTTP(srv.URL+"/", nil)
	info := grist.CallInfo{App: "my app", Tenant: "t 1", Processor: "payer", Trigger: 4, Head: 9, Key: "payer/t 1/4"}
	ctx := context.Background()
	if out, err := tr.Call(ctx, info, "ok", []byte(`{}`)); err != nil || string(out) != `{"ref":"r1"}` {
		t.Errorf("200: %s, %v", out, err)
	}
	if got.Get("Idempotency-Key") != "payer/t 1/4" || got.Get(grist.CauseHeader) != "my%20app/t%201/payer/4/9" {
		t.Errorf("headers %v: want the key and the cause, each part escaped", got)
	}
	var rej *grist.Rejected
	if _, err := tr.Call(ctx, info, "declined", nil); !errors.As(err, &rej) || rej.Name != "declined" {
		t.Errorf("422: %v, want the rejection declined", err)
	}
	if _, err := tr.Call(ctx, info, "bad", nil); err == nil || errors.Is(err, grist.ErrOutcomeUnknown) || errors.As(err, &rej) {
		t.Errorf("400: %v, want a refusal of no name", err)
	}
	for _, p := range []string{"proxy", "reset"} {
		if _, err := tr.Call(ctx, info, p, nil); !errors.Is(err, grist.ErrOutcomeUnknown) {
			t.Errorf("%s: %v, want an unknown outcome", p, err)
		}
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
	if _, err := node2.Call(ctx, grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 7, Key: "payer/t1/1"}, "pay", body); err != nil {
		t.Fatal(err)
	}
	if _, err := node1.Call(ctx, grist.CallInfo{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 4, Key: "payer/t1/1"}, "pay", body); err != nil {
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
	grist.NewCall[payReq, struct{}](mailer, "send", grist.Keyed())
	srv, sink := serveBank(t, mailer, nil)
	tr := grist.OverHTTP(srv.URL, nil)
	info := grist.CallInfo{App: "acks", Tenant: "t1", Processor: "p", Trigger: 1, Head: 1, Key: "k"}
	for range 2 {
		if out, err := tr.Call(context.Background(), info, "send", []byte(`{"amount":1}`)); err != nil || string(out) != "{}" {
			t.Fatalf("%s, %v: want the acknowledgement", out, err)
		}
	}
	if len(byAction(sink.Steps(), "mailerSendServed")) != 1 || len(byAction(sink.Steps(), "mailerSendReplayed")) != 1 {
		t.Errorf("recorded %v: want the default's one application and its replay", sink.Steps())
	}
}
