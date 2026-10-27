package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
func callsApp(d grist.Delivery, declare bool) (*grist.App, *grist.Dependency, *grist.Call[payReq, payResp]) {
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
// answer (Replayed).
func bankHandlers(pay *grist.Call[payReq, payResp]) grist.Handlers {
	paid := map[string]payResp{}
	return grist.Handlers{pay.Handler(func(ctx context.Context, r payReq) (payResp, error) {
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
			if _, err := a.Execute(ctx, st, grist.Envelope{ID: fmt.Sprintf("c%d", i), Tenant: "t1", Actor: "u"}, "add", body); err != nil {
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
		key := fmt.Sprintf("payer/t1/%d", tr)
		if c.Call == 0 || c.Actor != "t1" || c.Process != "calls" || c.Args["processor"] != "payer" || c.Args["key"] != key || c.Args["amount"] == nil {
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
		if tr.Actor == "t1" {
			trace = tr
		}
	}
	if trace == nil {
		t.Fatal("no trace of t1")
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
// recorded at its site once, and answered twice alike.
func TestReplayedRecordsNothing(t *testing.T) {
	a, bank, pay := callsApp(grist.AtMostOnce, true)
	steps, _ := runCalls(t, a, bank, bankHandlers(pay), true, addAmounts{A: 5})
	if served := byAction(steps, "bankPayServed"); len(served) != 1 {
		t.Errorf("served steps %v: want one (the retransmission replayed)", served)
	}
	calls := byAction(steps, "bankPay")
	if len(calls) != 1 || calls[0].Outcome != "ok" || calls[0].Args["ref"] != "ref-payer/t1/1" {
		t.Errorf("caller steps %v: want one, answered with the first application's ref", calls)
	}
}

// A call's declaration is checked at Verify: Uses on a processor that
// declares no Delivers, a request field named as a call step's own field,
// a field in both request and response, a refusal named unknown, a call
// of another App's dependency.
func TestCallDeclarations(t *testing.T) {
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
	grist.NewCall[ref, payResp](d, "both")
	grist.NewCall[payReq, payResp](d, "odd", grist.Refuses(grist.Rejection("unknown")))
	other := grist.New("other")
	foreign := grist.NewCall[payReq, payResp](grist.NewDependency(other, "bank"), "pay")
	grist.NewDependency(a, "api")
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
		`call bank/both: its request and its response both have a field "ref"`,
		`call bank/odd: a refusal named "unknown"`,
		`processor "foreign" uses call bank/pay, of another App's dependency`,
		`dependency "api": api is the name of the App's own API service`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify: %v\nwant it to say %s", err, want)
		}
	}
}
