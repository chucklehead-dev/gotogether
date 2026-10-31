package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The calls' app: a command adding one or two amounts in one batch, a
// bank whose pay is Keyed and may decline, and processors paying each
// amount out at least once ("payer") or at most once ("once").
type (
	amount struct {
		N int `json:"n"`
	}
	addAmounts struct {
		A int `json:"a"`
		B int `json:"b"` // 0: one amount
	}
	payReq struct {
		Amount int `json:"amount"`
	}
	payResp struct {
		Ref string `json:"ref"`
	}
)

var errDeclined = grist.Rejection("declined")

// callsApp is the calls' app, its processor of delivery d, its effect
// paying through pay unless declare is false (then the processor does not
// declare it: Uses left out).
func callsApp(d grist.Guarantee, declare bool) (*grist.App, *grist.Dependency, *grist.Call[payReq, payResp]) {
	a := grist.New("calls")
	amounts := grist.NewEvent[amount](a, "amount")
	bank := grist.NewDependency(a, "bank")
	pay := grist.NewCall[payReq, payResp](bank, "pay", grist.Keyed(), grist.Refuses(errDeclined))
	grist.NewCommand(a, "add", func(r *grist.Reads, c addAmounts) ([]grist.Event, error) {
		evs := []grist.Event{amounts.Of(amount{N: c.A})}
		if c.B != 0 {
			evs = append(evs, amounts.Of(amount{N: c.B}))
		}
		return evs, nil
	}, grist.Unconditioned("no reads"), grist.Internal())
	opts := []grist.Option{grist.Delivers(d)}
	if declare {
		opts = append(opts, grist.Uses(pay))
	}
	grist.NewProcessor(a, "payer", amounts, func(ctx context.Context, r *grist.Reads, e amount, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := pay.Do(ctx, payReq{Amount: e.N})
			return err
		}, d)
	}, opts...)
	return a, bank, pay
}

// bankHandlers pay, declining 13 and failing 7 (an error that is no
// declared refusal), answering a key applied before from its first
// answer (Replayed). Safe for concurrent use (ServeCalls' requests).
func bankHandlers(pay *grist.Call[payReq, payResp]) grist.Handlers {
	var mu sync.Mutex
	paid := map[string]payResp{}
	return grist.Handlers{pay.Handler(func(ctx context.Context, r payReq) (payResp, error) {
		mu.Lock()
		defer mu.Unlock()
		if p, ok := paid[grist.CallKey(ctx)]; ok {
			grist.Replayed(ctx)
			return p, nil
		}
		switch r.Amount {
		case 13:
			return payResp{}, errDeclined
		case 7:
			return payResp{}, errors.New("the bank is down")
		}
		p := payResp{Ref: "ref-" + grist.CallKey(ctx)}
		paid[grist.CallKey(ctx)] = p
		return p, nil
	})}
}

// runCalls runs a's node on a simulated store, its bank served in process
// by h (twice per call when twice: a retransmission), the adds given
// executed in order; it returns the steps recorded and the log.
func runCalls(t *testing.T, a *grist.App, bank *grist.Dependency, h grist.Handlers, twice bool, adds ...addAmounts) ([]qtrace.Step, []grist.Stored) {
	t.Helper()
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(1)
	base.Rates = qenv.NoFaults
	s := qenv.New(qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	defer s.Close()
	s.Check = true
	st := simstore.New(qenv.NewStore(s, a.Name, qenv.Serializable))
	ctx := s.Context()
	if err := st.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	var tr grist.Transport = grist.Local(h)
	if twice {
		tr = retransmit{tr}
	}
	s.Process("node", true, func(ctx context.Context) {
		if err := a.Start(bank.With(ctx, tr), st); err != nil {
			t.Error(err)
		}
	})
	s.Spawn("client", func(ctx context.Context) {
		for i, c := range adds {
			body, _ := json.Marshal(c)
			if _, err := a.Execute(ctx, st, grist.Envelope{ID: fmt.Sprintf("c%d", i), Tenant: "t1", Caller: "u"}, "add", body); err != nil {
				t.Error(err)
			}
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	log, _, err := st.Read(ctx, "t1", grist.Selector{Types: []string{"amount", "grist.failed", "grist.unknown"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return sink.Steps(), log
}

// retransmit sends every call twice, answering the second's answer.
type retransmit struct{ t grist.Transport }

func (r retransmit) Call(ctx context.Context, c grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	r.t.Call(ctx, c, call, req)
	return r.t.Call(ctx, c, call, req)
}

// byAction are steps' actions with their trigger, in recording order.
func byAction(steps []qtrace.Step, action string) []qtrace.Step {
	var out []qtrace.Step
	for _, s := range steps {
		if s.Action == action && !s.Invocation {
			out = append(out, s)
		}
	}
	return out
}

// A call is recorded at both ends. The caller's step bankPay (Call.Do):
// an invocation before the request and its completion with the answer
// (Call names the invocation), its arguments the request's fields, the
// processor, the trigger and the effect's key, the response's fields;
// its outcome ok, the declared refusal's name, or unknown; ordered as the
// effect step it precedes (order.pos 2·H+1, order.p.payer at the effect's
// place). The site's step bankPayServed (the handler's wrapper): the
// same fields, the outcome the handler answered, at the call's cause
// (order.pos 2·H+1) on a thread of its key. A declared refusal is
// NotApplied (at most once: grist.failed), any other error an unknown
// outcome (grist.unknown), and a handler error that is no declared
// refusal is recorded at the site with the fix.
func TestCallSteps(t *testing.T) {
	a, bank, pay := callsApp(grist.AtMostOnce, true)
	steps, log := runCalls(t, a, bank, bankHandlers(pay), false, addAmounts{A: 5}, addAmounts{A: 13}, addAmounts{A: 7})
	calls, served := byAction(steps, "bankPay"), byAction(steps, "bankPayServed")
	if len(calls) != 3 || len(served) != 3 {
		t.Fatalf("calls %v, served %v: want three of each", calls, served)
	}
	wantOutcome := map[int64]string{1: "ok", 2: "declined", 3: "unknown"}
	invocations := 0
	for _, s := range steps {
		if s.Invocation && s.Action == "bankPay" {
			invocations++
		}
	}
	if invocations != 3 {
		t.Errorf("%d invocation records of bankPay: want one per call (qobs.Invoke)", invocations)
	}
	for _, c := range calls {
		tr := c.Args["trigger"].(int64)
		key := fmt.Sprintf("calls/payer/t1/%d@%s", tr, triggerIDAt(log, tr))
		if c.Call == 0 || c.Actor != "calls/t1" || c.Process != "calls" || c.Args["processor"] != "payer" || c.Args["key"] != key || c.Args["triggerId"] != triggerIDAt(log, tr) || c.Args["amount"] == nil {
			t.Errorf("caller step %+v: want a completion (Call set) of actor t1, process calls, processor payer, key %s, the request's amount", c, key)
		}
		if c.Outcome != wantOutcome[tr] {
			t.Errorf("caller step of trigger %d: outcome %q, want %q", tr, c.Outcome, wantOutcome[tr])
		}
		if _, ok := c.Args["ref"]; !ok {
			t.Errorf("caller step %v has no response field ref (a refusal's and an unknown's are zero)", c)
		}
		if c.Order["pos"]%2 != 1 || c.Order["pos"] < 2*tr+1 || c.Order["p.payer"] != 2*tr+1 {
			t.Errorf("caller step %v: want order.pos 2·H+1 with H at least its trigger, order.p.payer 2·T+1 (at most once)", c)
		}
	}
	for _, s := range served {
		tr := s.Args["trigger"].(int64)
		if s.Thread != "grist.served/"+s.Args["key"].(string) || s.Order["pos"]%2 != 1 || s.Order["pos"] < 2*tr+1 {
			t.Errorf("served step %v: want its key's thread, order.pos 2·H+1", s)
		}
		if tr == 3 && (s.Outcome != "error" || !strings.Contains(s.Error, "neither a response nor a refusal it declares")) {
			t.Errorf("served step of the failing handler %v: want its error naming the fix", s)
		}
		if tr != 3 && s.Outcome != wantOutcome[tr] {
			t.Errorf("served step %v: want outcome %q", s, wantOutcome[tr])
		}
	}
	if got := outcomes(log); len(got) != 2 || !strings.HasPrefix(got[0], "grist.failed") || !strings.HasPrefix(got[1], "grist.unknown") {
		t.Errorf("outcome events %v: want the declined payment grist.failed (NotApplied) and the failed one grist.unknown", got)
	}
}

// A call the processor does not declare (Uses), or made outside an
// effect, is an error naming the fix, and records nothing: the effect
// fails (at least once: grist.failed after its tries).
func TestCallDeclared(t *testing.T) {
	a, bank, pay := callsApp(grist.AtLeastOnce, false)
	steps, log := runCalls(t, a, bank, bankHandlers(pay), false, addAmounts{A: 5})
	if n := len(byAction(steps, "bankPay")) + len(byAction(steps, "bankPayServed")); n != 0 {
		t.Errorf("%d steps of the undeclared call: want none (Do refuses it)", n)
	}
	if got := outcomes(log); len(got) != 1 || !strings.HasPrefix(got[0], "grist.failed") {
		t.Errorf("outcome events %v: want the effect failed", got)
	}
	var effects []qtrace.Step
	for _, s := range steps {
		if s.Action == "payerEffect" {
			effects = append(effects, s)
		}
	}
	if len(effects) != 3 || effects[0].Args["ok"] != false {
		t.Errorf("effect steps %v: want three failed tries", effects)
	}
	_, err := pay.Do(context.Background(), payReq{Amount: 1})
	if err == nil || !strings.Contains(err.Error(), "outside an effect") {
		t.Errorf("Do outside an effect: %v, want the error naming it", err)
	}
	// the undeclared call's error, as the effect sees it
	var seen error
	a2 := grist.New("calls2")
	amounts := grist.NewEvent[amount](a2, "amount")
	bank2 := grist.NewDependency(a2, "bank")
	pay2 := grist.NewCall[payReq, payResp](bank2, "pay")
	grist.NewCommand(a2, "add", func(r *grist.Reads, c addAmounts) ([]grist.Event, error) {
		return []grist.Event{amounts.Of(amount{N: c.A})}, nil
	}, grist.Unconditioned("no reads"), grist.Internal())
	grist.NewProcessor(a2, "payer", amounts, func(ctx context.Context, r *grist.Reads, e amount, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, seen = pay2.Do(ctx, payReq{Amount: e.N})
			return seen
		}, grist.AtMostOnce)
	}, grist.Delivers(grist.AtMostOnce))
	runCalls(t, a2, bank2, grist.Handlers{pay2.Handler(func(context.Context, payReq) (payResp, error) { return payResp{}, nil })}, false, addAmounts{A: 1})
	if seen == nil || !strings.Contains(seen.Error(), "processor payer calls bank/pay, which it does not declare: add grist.Uses") {
		t.Errorf("an undeclared call's error %v: want it naming grist.Uses", seen)
	}
}

// The site's record is placed at its cause, after the commit holding its
// trigger: a command appending two amounts in one batch commits at 2·P
// (P its last position), so the first amount's served record, ordered at
// 2·T+1, would come before the commit that holds it; at 2·H+1, H the head
// the poller read, it comes after. The reconstructed trace puts every
// served and caller step after the commit of its trigger.
func TestSiteAfterItsCommit(t *testing.T) {
	a, bank, pay := callsApp(grist.AtLeastOnce, true)
	steps, _ := runCalls(t, a, bank, bankHandlers(pay), false, addAmounts{A: 5, B: 6})
	traces, issues := qtrace.Reconstruct(steps)
	for _, i := range issues {
		if i.Severity == "error" {
			t.Errorf("reconstruction: %v", i)
		}
	}
	var trace *qtrace.Trace
	for _, tr := range traces {
		if tr.Actor == "calls/t1" {
			trace = tr
		}
	}
	if trace == nil {
		t.Fatal("no trace of t1")
	}
	for _, i := range trace.Issues { // a cycle is the trace's issue
		if i.Severity == "error" {
			t.Errorf("reconstruction of t1: %v", i)
		}
	}
	commit, sites := -1, 0
	for i, s := range trace.Steps {
		switch s.Action {
		case "add":
			commit = i
		case "bankPay", "bankPayServed":
			sites++
			if commit < 0 {
				t.Errorf("%s of trigger %v (order.pos %d) comes before the commit holding its trigger", s.Action, s.Args["trigger"], s.Order["pos"])
			}
		}
	}
	if sites != 4 {
		t.Errorf("%d call steps: want a call and a served record per amount", sites)
	}
}

// A Keyed dependency that answers a retry from its first answer says so
// (Replayed): a call sent twice (a retransmission) is applied and
// recorded as served once, its second answer recorded as a replay
// (bankPayReplayed) with the same fields, so the model can judge it: a
// replay of no application, or of another answer, is no earlier served
// record. Both are answered alike.
func TestReplayedRecorded(t *testing.T) {
	a, bank, pay := callsApp(grist.AtMostOnce, true)
	steps, _ := runCalls(t, a, bank, bankHandlers(pay), true, addAmounts{A: 5})
	served, replayed := byAction(steps, "bankPayServed"), byAction(steps, "bankPayReplayed")
	if len(served) != 1 || len(replayed) != 1 {
		t.Fatalf("served steps %v, replayed %v: want one of each (the retransmission replayed)", served, replayed)
	}
	if r, s := replayed[0], served[0]; r.Outcome != s.Outcome || fmt.Sprint(r.Args) != fmt.Sprint(s.Args) || r.Thread != s.Thread || r.Order["pos"] != s.Order["pos"] {
		t.Errorf("replay %+v: want the served record's fields, outcome, thread and place %+v", r, s)
	}
	calls := byAction(steps, "bankPay")
	if len(calls) != 1 || calls[0].Outcome != "ok" || calls[0].Args["ref"] != "ref-"+calls[0].Args["key"].(string) {
		t.Errorf("caller steps %v: want one, answered with the first application's ref", calls)
	}
}

// A site record of a key is never placed before an earlier one of the
// key: a request caused at an earlier head served after a later one's (a
// request delayed past a restarted poller's retry, or two nodes'
// pollers) is placed at the earlier record's 2·H+1, not its own, which
// on the key's thread would contradict program order (a cycle) and put a
// replay before the application it repeats. Other keys keep their own
// cause.
func TestSiteOrderPerKey(t *testing.T) {
	a, _, pay := callsApp(grist.AtLeastOnce, true)
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	// an unkeyed-like double: it applies every request (no dedupe)
	tr := grist.Local(grist.Handlers{pay.Handler(func(ctx context.Context, r payReq) (payResp, error) {
		return payResp{Ref: "r"}, nil
	})})
	body, _ := json.Marshal(payReq{Amount: 5})
	for _, c := range []grist.CallInfo{
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 7, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID}, // the retry, served first
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 4, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID}, // the delayed first try
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 2, Head: 4, TriggerID: transportTriggerID2, Key: "calls/payer/t1/2@" + transportTriggerID2},
	} {
		if _, err := tr.Call(ctx, c, "pay", body); err != nil {
			t.Fatal(err)
		}
	}
	served := byAction(sink.Steps(), "bankPayServed")
	if len(served) != 3 {
		t.Fatalf("served %v: want three", served)
	}
	for i, want := range []int64{15, 15, 9} {
		if got := served[i].Order["pos"]; got != want {
			t.Errorf("served record %d (key %v): order.pos %d, want %d", i, served[i].Args["key"], got, want)
		}
	}
	traces, issues := qtrace.Reconstruct(sink.Steps())
	if len(traces) != 1 || len(traces[0].Steps) != 3 {
		t.Fatalf("%d traces: want one of three steps", len(traces))
	}
	for _, i := range append(issues, traces[0].Issues...) {
		if i.Severity == "error" {
			t.Errorf("reconstruction: %v", i)
		}
	}
	var order []uint64
	for _, s := range traces[0].Steps {
		if s.Args["key"] == "calls/payer/t1/1@"+transportTriggerID {
			order = append(order, s.Seq)
		}
	}
	if len(order) != 2 || order[0] != served[0].Seq || order[1] != served[1].Seq {
		t.Errorf("the key's records in the trace, by seq: %v, want %d then %d (serving order)", order, served[0].Seq, served[1].Seq)
	}
}

// A call's declaration is checked at Verify: Uses on a processor that
// declares no Delivers, a request field named as a call step's own field,
// a field in both request and response, a refusal named unknown, a call
// of another App's dependency.
func TestCallDeclarations(t *testing.T) {
	type identityClash struct {
		ID string `json:"triggerId"`
	}
	type clash struct {
		Key string `json:"key"`
	}
	type ref struct {
		Ref string `json:"ref"`
	}
	a := grist.New("decl")
	ev := grist.NewEvent[amount](a, "amount")
	d := grist.NewDependency(a, "bank")
	ok := grist.NewCall[payReq, payResp](d, "pay")
	grist.NewCall[clash, payResp](d, "keyed-field")
	grist.NewCall[identityClash, payResp](d, "request-trigger-id")
	grist.NewCall[payReq, identityClash](d, "response-trigger-id")
	grist.NewCall[ref, payResp](d, "both")
	grist.NewCall[payReq, payResp](d, "odd", grist.Refuses(grist.Rejection("unknown")))
	other := grist.New("other")
	foreign := grist.NewCall[payReq, payResp](grist.NewDependency(other, "bank"), "pay")
	grist.NewDependency(a, "decl")
	grist.NewProcessor(a, "emits", ev, func(ctx context.Context, r *grist.Reads, e amount, m grist.Meta) grist.Result {
		return grist.Skip()
	}, grist.Emits(), grist.Uses(ok))
	grist.NewProcessor(a, "foreign", ev, func(ctx context.Context, r *grist.Reads, e amount, m grist.Meta) grist.Result {
		return grist.Skip()
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(foreign))
	err := a.Verify()
	for _, want := range []string{
		`processor "emits" uses call bank/pay, but declares no Delivers`,
		`call bank/keyed-field: its request's field Key is named "key"`,
		`call bank/request-trigger-id: its request's field ID is named "triggerId", which every call step records itself`,
		`call bank/response-trigger-id: its response's field ID is named "triggerId", which every call step records itself`,
		`call bank/both: its request and its response both have a field "ref"`,
		`call bank/odd: a refusal named "unknown"`,
		`processor "foreign" uses call bank/pay, of another App's dependency`,
		`dependency "decl": decl is the name of the App's own API service`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify: %v\nwant it to say %s", err, want)
		}
	}
}

// The default double (DefaultHandlers, what gristsim serves a dependency
// given no handlers): a Keyed call is applied once per key, answered with
// its response (one with no fields), and a later try of the key replayed
// from that first answer (bankPayReplayed, the served record's fields and
// place); an unkeyed call is applied every time. A dependency with a call
// answering fields has none: DefaultHandlers panics, naming the call, its
// fields and the fix.
func TestDefaultHandlers(t *testing.T) {
	type ack struct{}
	a := grist.New("defaults")
	bank := grist.NewDependency(a, "bank")
	grist.NewCall[payReq, ack](bank, "pay", grist.Keyed())
	grist.NewCall[payReq, ack](bank, "tip")
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	tr := grist.Local(grist.DefaultHandlers(bank))
	body, _ := json.Marshal(payReq{Amount: 5})
	for _, call := range []string{"pay", "pay", "tip", "tip"} {
		for _, trigger := range []grist.Position{1, 2} {
			id := fmt.Sprintf("%032x", trigger)
			key := fmt.Sprintf("defaults/payer/t1/%d@%s", trigger, id)
			out, err := tr.Call(ctx, grist.CallInfo{App: "defaults", Tenant: "t1", Processor: "payer", Trigger: trigger, TriggerID: id, Head: 2, Key: key}, call, body)
			if err != nil || string(out) != `{}` {
				t.Errorf("%s %s: %s, %v: want the acknowledgement", call, key, out, err)
			}
		}
	}
	refs := grist.NewDependency(a, "refs")
	grist.NewCall[payReq, ack](refs, "ack")
	grist.NewCall[payReq, payResp](refs, "pay", grist.Keyed())
	func() {
		defer func() {
			if r := fmt.Sprint(recover()); !strings.Contains(r, "call refs/pay answers a response with fields (ref), which a default double cannot know: give dependency refs handlers that answer as it would (Call.Handler") {
				t.Errorf("DefaultHandlers of a call answering a ref: panic %q, want it to name the call, its fields and the fix", r)
			}
		}()
		grist.DefaultHandlers(refs)
	}()
	steps := sink.Steps()
	served, replayed, tips := byAction(steps, "bankPayServed"), byAction(steps, "bankPayReplayed"), byAction(steps, "bankTipServed")
	if len(served) != 2 || len(replayed) != 2 || len(tips) != 4 || len(byAction(steps, "bankTipReplayed")) != 0 {
		t.Fatalf("pay served %d, replayed %d; tip served %d: want each pay key applied once and replayed once, every tip applied", len(served), len(replayed), len(tips))
	}
	for i := range served {
		if r, s := replayed[i], served[i]; r.Outcome != "ok" || fmt.Sprint(r.Args) != fmt.Sprint(s.Args) || r.Order["pos"] != s.Order["pos"] {
			t.Errorf("replay %+v: want the served record's fields and place %+v", r, s)
		}
	}
}

// The in-process transport enforces the same immutable cause/key contract as
// HTTP before applying or recording a call. It cannot silently accept an empty
// ID or retain the former node-local rewind suffix.
func TestLocalCallsRefuseUnstableIdentity(t *testing.T) {
	_, _, pay := callsApp(grist.AtLeastOnce, true)
	applied := 0
	tr := grist.Local(grist.Handlers{pay.Handler(func(ctx context.Context, req payReq) (payResp, error) {
		applied++
		return payResp{}, nil
	})})
	sink := &qobs.MemorySink{}
	recorder := qobs.NewRecorder(sink)
	defer recorder.Close()
	ctx := qobs.WithRecorder(context.Background(), recorder)
	for _, info := range []grist.CallInfo{
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 1, Key: "calls/payer/t1/1"},
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 1, TriggerID: transportTriggerID, Key: "calls/payer/t1/1~1"},
		{App: "calls", Tenant: "t1", Processor: "payer", Trigger: 1, Head: 1, TriggerID: transportTriggerID, Key: "calls/payer/t1/1@" + transportTriggerID2},
	} {
		if _, err := tr.Call(ctx, info, "pay", []byte(`{"amount":5}`)); err == nil {
			t.Errorf("accepted unstable identity: %+v", info)
		}
	}
	if applied != 0 || len(sink.Steps()) != 0 {
		t.Fatalf("invalid identity applied %d calls and recorded %d steps", applied, len(sink.Steps()))
	}
}
