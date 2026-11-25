package qtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// payReq is a provider request, as a double records it: its exported
// fields are the effect's (qenv.EffectFields).
type payReq struct {
	Op, Key, Order string
	Amount         int
}

// mail is a notice, published to the mail queue.
type mail struct{ Order string }

// payLog is a run of pay.qnt's steps on one recorder, its effects as qenv's
// doubles record them: o1 placed for 2 units; pay invoked, its capture
// sent (and retried under key) and the notice published under the call's
// scope; its completion. amount is the capture's, key2 the retry's key.
func payLog(amount int64, key2 string, notify bool) []qtrace.Step {
	t0 := time.Unix(900, 0)
	n := 0
	at := func() time.Time { n++; return t0.Add(time.Duration(n) * time.Millisecond) }
	rec := func(s qtrace.Step) qtrace.Step {
		s.Recorder, s.Actor, s.Process, s.Thread, s.Time = "shop", "shop", "shop", "", at()
		return s
	}
	capture := func(seq uint64, key string) qtrace.Step {
		return rec(qtrace.Step{Action: "effect", Seq: seq, Call: 2, Record: qtrace.Record{Effect: "rpc.provider.request"},
			Args: map[string]any{"Op": "capture", "Key": key, "Order": "o1", "Amount": amount}})
	}
	steps := []qtrace.Step{
		rec(qtrace.Step{Action: "place", Seq: 1, Outcome: "ok", EffectsObserved: true, Args: map[string]any{"order": "o1", "qty": int64(2)}}),
		rec(qtrace.Step{Action: "pay", Seq: 2, Record: qtrace.Record{Invocation: true}, Args: map[string]any{"order": "o1"}}),
		capture(3, "cap:o1"),
		capture(4, key2),
	}
	seq := uint64(5)
	if notify {
		steps = append(steps, rec(qtrace.Step{Action: "effect", Seq: 5, Call: 2, Record: qtrace.Record{Effect: "queue.mail.publish"}, Args: map[string]any{"Order": "o1"}}))
		seq++
	}
	return append(steps, rec(qtrace.Step{Action: "pay", Seq: seq, Call: 2, Outcome: "ok", EffectsObserved: true, Args: map[string]any{"order": "o1"}}))
}

// A step's effects are compared with its action's effects def, in its
// expect: the faithful log conforms (its retry, under the first try's key,
// is one capture), and each wrong effect is Mismatch("pay"), the report
// naming the step, the effects made and expected, the first differing
// effect and field, and the fix. Without the effects section (the check
// ablated) every one of them conforms.
func TestEffectsMismatch(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	Validate(t, "testdata/pay.yaml", payLog(20, "cap:o1", true)).Expect(t, Conforms())

	cases := []struct {
		name  string
		steps []qtrace.Step
		want  []string
	}{
		{"units", payLog(2, "cap:o1", true), []string{
			`the effects made are not the model's payEffects("o1")`,
			`made     [Capture({ amount: 2, order: "o1" }), Notify({ order: "o1" })]   (rpc.provider.request at seq 3, queue.mail.publish at seq 5)`,
			`expected [Capture({ amount: 20, order: "o1" }), Notify({ order: "o1" })]`,
			"first difference: effect 1, field amount: 2, expected 20",
			"Fix the code's effect, or payEffects if the model is wrong; the effects' fields come from effects.kinds in"}},
		{"freshKey", payLog(20, "cap:o1:2", true), []string{
			`effect Capture({ amount: 20, order: "o1" }) was tried again under another Key: "cap:o1" (seq 3), then "cap:o1:2" (seq 4)`,
			"first difference: effect 2"}},
		{"noNotice", payLog(20, "cap:o1", false), []string{
			`first difference: effect 2: nothing, expected Notify({ order: "o1" })`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			run := Validate(t, "testdata/pay.yaml", c.steps)
			run.Expect(t, Mismatch("pay"))
			text := run.Report.VerboseSummary()
			for _, w := range c.want {
				if !strings.Contains(text, w) {
					t.Errorf("the report lacks %q:\n%s", w, text)
				}
			}
			// ablated: the same log, no effects compared
			Validate(t, "testdata/pay-unchecked.yaml", c.steps).Expect(t, Conforms())
		})
	}
}

// A step recorded where effects are not (no scope, outside a simulation)
// is the binding's error, not a pass: "none made" and "none known" differ.
func TestEffectsNotObserved(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	steps := payLog(20, "cap:o1", true)
	var unobserved []qtrace.Step
	for _, s := range steps {
		if s.Effect != "" {
			continue // a log recorded where nothing records effects
		}
		s.EffectsObserved = false
		unobserved = append(unobserved, s)
	}
	// renumbered densely: the effects were never recorded
	for i := range unobserved {
		unobserved[i].Seq = uint64(i + 1)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		Validate(f, "testdata/pay.yaml", unobserved)
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "the step recorded no effect scope, and the binding gives pay effects (payEffects)") {
		t.Errorf("want the binding's refusal of an unobserved step: %q", f.errs)
	}
}

// Every effect mutant of a conforming log is killed where the binding
// compares its effect: a capture's amount changed, a notice dropped, the
// two swapped, a capture tried under a fresh key. A copy of a retry is
// the same request under the same key, collapsed by match retried: it
// survives by right. Ablated (pay-unchecked.yaml), each survives.
func TestEffectsMutateSteps(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	ops := StepMutations(qtrace.MutateOps("effect-drop", "effect-dup", "effect-swap", "effect-corrupt"))
	rep := MutateSteps(t, "testdata/pay.yaml", payLog(20, "cap:o1", true), ops)
	amount := StepOp("effect-corrupt").Action("pay").Field("Amount").Change("+1")
	key := StepOp("effect-corrupt").Action("pay").Field("Key").Change("change")
	rep.Expect(t,
		Killed(amount),
		Killed(StepOp("effect-corrupt").Action("pay").Field("Order").Change("change")),
		Killed(StepOp("effect-drop").Index(4)), // the notice
		Killed(StepOp("effect-swap").Action("pay")),
		Killed(key),                             // the retry's key no longer the first try's
		Survived(StepOp("effect-dup").Index(3)), // one more try under the key: one capture
	)
	ablated := MutateSteps(t, "testdata/pay-unchecked.yaml", payLog(20, "cap:o1", true), ops)
	ablated.Expect(t, Survived(amount), Survived(key), Survived(StepOp("effect-drop").Index(4)))
}

// A simulated run's effects come from qenv's doubles at the send and are
// attributed by qobs.Invoke's scope: the capture's request (retried after
// a lost response, under its key) and the notice the provider's reply
// triggers are pay's. A shop that captures units instead of money is
// Mismatch("pay") in every run that pays; the faithful one conforms.
func TestEffectsSimulated(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	shop := func(units bool) System {
		return System{Binding: "testdata/pay.yaml", Rates: qenv.Rates{Kinds: map[string]float64{"rpc.provider.response": 0.3}}, Run: func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			provider := qenv.NewService(s, "provider", nil, func(ctx context.Context, r payReq) (string, error) { return "ok", nil })
			queue := qenv.NewQueue(s, "mail", nil)
			s.Spawn("shop", func(ctx context.Context) {
				qobs.Record(ctx, "place", nil, "order", "o1", "qty", 2)
				call := qobs.Invoke(ctx, "pay", "order", "o1")
				pctx := call.Scope(ctx)
				amount := 20
				if units {
					amount = 2
				}
				for range 3 {
					if _, err := provider.Call(pctx, payReq{Op: "capture", Key: "cap:o1", Order: "o1", Amount: amount}, time.Second); err == nil {
						break
					}
				}
				queue.Publish(pctx, mail{Order: "o1"})
				call.Done(nil)
			})
			return s.Run(10 * time.Second)
		}}
	}
	shop(false).Explore(t, ExploreConfig{Seeds: 8}).Expect(t, Conforms())
	shop(true).Explore(t, ExploreConfig{Seeds: 8}).Expect(t, All(Mismatch("pay")))
}
