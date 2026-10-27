package qtest

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// bankLog is a small bank's log on one recorder: two deposits and a
// withdrawal, each with the balance it left, then a transfer that
// observed nothing.
func bankLog() []qtrace.Step {
	t0 := time.Unix(500, 0)
	step := func(seq int, action string, args map[string]any, obs map[string]any) qtrace.Step {
		return qtrace.Step{Action: action, Seq: uint64(seq), Recorder: "bank", Actor: "bank", Process: "bank", Thread: "bank",
			Time: t0.Add(time.Duration(seq) * time.Millisecond), Args: args, Obs: obs}
	}
	return []qtrace.Step{
		step(1, "deposit", map[string]any{"account": "alice", "amount": int64(3)}, map[string]any{"balance": int64(3)}),
		step(2, "deposit", map[string]any{"account": "bob", "amount": int64(2)}, map[string]any{"balance": int64(2)}),
		step(3, "withdraw", map[string]any{"account": "alice", "amount": int64(1)}, map[string]any{"balance": int64(2)}),
		step(4, "transfer", map[string]any{"src": "alice", "dst": "bob", "amount": int64(1)}, nil),
	}
}

// A deposit's observed balance off by one is killed: the binding's
// expect compares it. Without that expect (bank-unchecked.yaml, the
// check ablated) the same mutant survives, and is marked unread. A
// deposit recorded twice is killed by its copy's balance; the
// withdrawal swapped with the transfer, by the balance it observed. What
// nothing later observes survives by right: the trailing transfer
// dropped or recorded twice, its amount one more (alice can afford it),
// and steps that commute swapped (deposits to two accounts).
func TestMutateStepsBank(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	offByOne := StepOp("corrupt").Action("deposit").Field("obs.balance").Change("+1")
	rep := MutateSteps(t, "testdata/bank.yaml", bankLog())
	rep.Original.Expect(t, Conforms())
	rep.Expect(t,
		Killed(offByOne),
		Killed(StepOp("corrupt").Action("deposit").Field("obs.balance")), // -1, and removed: refused, the expect reads it
		Killed(StepOp("drop").Index(0)),                                  // alice cannot withdraw what was not deposited
		Killed(StepOp("dup").Index(1)),                                   // the copy's balance is 2, the model's 4
		Killed(StepOp("swap").Action("withdraw")),                        // alice's balance observed is another order's
		Killed(StepOp("corrupt").Action("transfer").Field("amount").Change("-1")),
		Survived(StepOp("drop").Action("transfer")),
		Survived(StepOp("dup").Action("transfer")),
		Survived(StepOp("swap").Action("deposit")),
		Survived(StepOp("corrupt").Action("transfer").Field("amount").Change("+1")),
	)
	for _, m := range rep.Mutants {
		if m.Fate == FateKilled && m.Field == "obs.balance" && m.Change == "remove" && !strings.Contains(m.Why, "refused by the binding") {
			t.Errorf("%s: a field the expect reads removed is the binding's refusal", m)
		}
		if m.Op == "corrupt" && m.Unread {
			t.Errorf("%s: marked unread, but the binding reads every field of the log", m)
		}
	}
	if s := rep.String(); !strings.Contains(s, "  corrupt ") || !strings.Contains(s, "survived: drop of transfer") {
		t.Errorf("the table:\n%s", s)
	}

	ablated := MutateSteps(t, "testdata/bank-unchecked.yaml", bankLog())
	ablated.Expect(t, Survived(offByOne))
	for _, m := range ablated.Mutants {
		if offByOne.matches(m) && !m.Unread {
			t.Errorf("%s: the binding no longer reads it, and the report does not say so", m)
		}
	}
}

// A finished original kills a mutant that does not finish: the tick
// call's completion dropped leaves it pending, though the rest conforms
// and the eventual val holds. Judged by conformance alone it survives.
func TestMutateStepsFinish(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	t0 := time.Unix(700, 0)
	steps := []qtrace.Step{
		{Action: "tick", Seq: 1, Recorder: "c", Actor: "ev", Process: "c", Thread: "c", Time: t0, Invocation: true},
		{Action: "tick", Seq: 2, Recorder: "c", Actor: "ev", Process: "c", Thread: "c", Time: t0.Add(time.Millisecond), Call: 1},
		{Action: "inc", Seq: 3, Recorder: "c", Actor: "ev", Process: "c", Thread: "c", Time: t0.Add(2 * time.Millisecond)},
		{Action: "settled", Seq: 4, Recorder: "c", Actor: "ev", Process: "c", Thread: "c", Time: t0.Add(3 * time.Millisecond), Settled: true},
	}
	rep := MutateSteps(t, "testdata/ev-finish.yaml", steps, StepMutations(qtrace.MutateOps("drop", "dup")))
	rep.Original.Expect(t, Finishes())
	rep.Expect(t, Killed(StepOp("drop").Action("tick")), Survived(StepOp("drop").Action("inc")), Survived(StepOp("dup")))
	for _, m := range rep.Mutants {
		if m.Op == "drop" && m.Action == "tick" && !strings.Contains(m.Why, "pending") {
			t.Errorf("%s: killed for its pending call, not for %q", m, m.Why)
		}
	}
}

// A claim that names no mutant fails, saying what the log has; a claim
// a mutant's fate contradicts fails, naming it and the fix; an original
// that does not conform fails MutateSteps, with nothing to mutate.
func TestMutateStepsClaims(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	rep := MutateSteps(t, "testdata/bank.yaml", bankLog(), StepMutations(qtrace.MutateOps("drop", "corrupt"), qtrace.MutateLimit(4)))
	for _, m := range rep.Mutants {
		if m.Op != "drop" && m.Op != "corrupt" {
			t.Errorf("%s: an op StepMutations left out", m)
		}
	}
	for _, x := range []struct {
		claim StepClaim
		says  string
	}{
		{Killed(StepOp("corrupt").Action("depsit")), `no step is of action "depsit"; the log's: deposit, transfer, withdraw`},
		{Killed(StepOp("corrupt").Field("obs.balanse")), `no step has field "obs.balanse"`},
		{Killed(StepOp("swap")), "the report's ops: drop, corrupt; narrowed by StepMutations"},
		{Survived(StepOp("drop").Index(0)), "a check reads it: it is no equivalent; claim it Killed"},
		{Killed(StepOp("drop").Action("transfer")), "add the check that reads it"},
	} {
		f := &failTB{TB: t}
		rep.Expect(f, x.claim)
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], x.says) {
			t.Errorf("%s: %q, want one saying %q", x.claim, f.errs, x.says)
		}
	}

	bad := bankLog()
	bad[0].Obs = map[string]any{"balance": int64(4)}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		MutateSteps(f, "testdata/bank.yaml", bad)
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "the original steps do not conform (mismatch after deposit)") {
		t.Errorf("a failing original: %q", f.errs)
	}
}

// StepMutations is MutateSteps' alone: Validate refuses it.
func TestStepMutationsRefused(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		Validate(f, "testdata/bank.yaml", bankLog(), StepMutations(qtrace.MutateLimit(1)))
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "StepMutations() does nothing in Validate") {
		t.Errorf("errors %q", f.errs)
	}
}

// A retime tests a within: a node's election moved past another actor's
// later step is 7.5s after its heal, past the 3s bound, and killed by it
// (a verdict, not undecided: no step of its own actor is passed, so its
// times never go back); its heal moved later within its gap to the
// election still meets the bound and survives.
func TestMutateStepsRetimeWithin(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	t0 := time.Unix(900, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "crash", Seq: 1, Recorder: "n", Actor: "node", Process: "n", Thread: "n", Time: at(0)},
		{Action: "heal", Seq: 2, Recorder: "n", Actor: "node", Process: "n", Thread: "n", Time: at(1000)},
		{Action: "elect", Seq: 3, Recorder: "n", Actor: "node", Process: "n", Thread: "n", Time: at(3500)},
		{Action: "tick", Seq: 1, Recorder: "o", Actor: "other", Process: "o", Thread: "o", Time: at(6000)},
	}
	rep := MutateSteps(t, "../validate/testdata/within.yaml", steps, StepMutations(qtrace.MutateOps("retime")))
	rep.Expect(t, Killed(StepOp("retime").Action("elect")), Survived(StepOp("retime").Action("heal")))
	if u := rep.Undecided(); len(u) > 0 {
		t.Errorf("retimes with no verdict: %v", u)
	}
}

// A mutant whose log Reconstruct refused is malformed, not killed: the
// recorder's checks stopped it, and it counts in no score. One that
// survives with its bounds unjudged is undecided.
func TestStepFateMalformed(t *testing.T) {
	t.Parallel()
	overlap := &validate.Report{Actors: []*validate.Result{{Actor: "A", Conforms: validate.Fail,
		Issues: []qtrace.Issue{{Severity: "error", Kind: "overlap", Msg: "two incarnations of A overlap"}}}}}
	conforming := &validate.Report{Actors: []*validate.Result{{Actor: "A", Conforms: validate.Pass}}}
	if fate, why := stepFate(conforming, overlap); fate != FateMalformed || !strings.Contains(why, "refused by Reconstruct: error overlap") {
		t.Errorf("an overlap: %s (%s)", fate, why)
	}
	// a survivor whose bounds went unjudged, where the original's were
	// judged, is undecided: its survival says nothing of them
	unjudged := &validate.Report{Actors: []*validate.Result{{Actor: "A", Conforms: validate.Pass,
		Issues: []qtrace.Issue{{Severity: "warn", Kind: "within", Msg: "bounded-response obligations not judged: the steps' times go back"}}}}}
	if fate, _ := stepFate(conforming, unjudged); fate != FateUndecided {
		t.Errorf("bounds unjudged: %s", fate)
	}
	if fate, _ := stepFate(unjudged, unjudged); fate != FateSurvived {
		t.Errorf("bounds unjudged in the original too: %s", fate)
	}
	rep := &StepMutationReport{Mutants: []StepMutation{{Fate: FateKilled}, {Fate: FateMalformed}, {Fate: FateSurvived}}}
	if rep.Score() != 0.5 || len(rep.Malformed()) != 1 {
		t.Errorf("score %v, %d malformed", rep.Score(), len(rep.Malformed()))
	}
}

// A step lost from the middle of a recorder's log (the gap op: its seq
// left a hole) is no verdict, never killed: the bank's bob deposit or
// alice's withdrawal dropped leaves steps that conform, and what was
// dropped is unknown (validate's "no verdict (dropped steps)"). Once
// such a mutant was malformed (Reconstruct's gap), and with the gap
// read as a FAIL, killed. The first and last steps have no gap mutant.
func TestMutateStepsGap(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	rep := MutateSteps(t, "testdata/bank.yaml", bankLog(), StepMutations(qtrace.MutateOps("gap")))
	if len(rep.Mutants) != 2 {
		t.Fatalf("%d gap mutants, want 2 (seqs 2 and 3):\n%s", len(rep.Mutants), rep)
	}
	for _, m := range rep.Mutants {
		if m.Fate != FateUndecided || m.Why != "no verdict (dropped steps)" {
			t.Errorf("%s: want undecided, no verdict (dropped steps)", m)
		}
	}
}
