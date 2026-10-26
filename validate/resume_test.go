package validate

import (
	"context"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// lostHead is a log of recorder r that lost seqs 1..2: deposits (amount
// > 0) and withdrawals (amount < 0), each with the balance it saw.
func lostHead(ops ...[2]int64) []qtrace.Step {
	var steps []qtrace.Step
	for i, o := range ops {
		s := qtrace.Step{Action: "deposit", Recorder: "r", Seq: uint64(i + 3), Time: time.Unix(int64(i), 0), Args: map[string]any{"amount": o[0]}, Obs: map[string]any{"balance": o[1]}}
		if o[0] < 0 {
			s.Action, s.Args["amount"] = "withdraw", -o[0]
		}
		steps = append(steps, s)
	}
	return steps
}

// A log that lost its head, with no snapshot, is judged from the states
// the binding's resume admits: a deposit of 5 that saw 8 and a withdrawal
// of 2 that saw 6 started at balance 3, which quint confirms by replaying
// the steps from it; without the resume, from init, the lost prefix is an
// error and the deposit a mismatch. Two observations no start explains
// fail, quint confirming from each start. A start the invariants rule out
// (balance -2, which resumeAny admits) explains nothing: the deposit that
// saw 3 is a mismatch, not a violation in the initial state.
func TestResumeLostPrefix(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/resume.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, x := range []struct {
		name   string
		steps  []qtrace.Step
		reason string
		says   []string
	}{
		{"explained", lostHead([2]int64{5, 8}, [2]int64{-2, 6}), "", []string{
			"info lost-prefix: recorder r lost seqs 1..2: judged from the states resumeAny admits (11 of them); a pass means some admitted state explains the rest",
			"the steps pass from balance = 3, ops = 0, a state resumeAny admits (quint confirms)"}},
		{"inconsistent", lostHead([2]int64{5, 8}, [2]int64{-2, 7}), "mismatch after withdraw", []string{
			"quint confirms: from none of the 11 states resumeAny admits do the steps pass",
			"info resume: withdraw is enabled, but from no state resumeAny admits does its expect hold after it"}},
		{"a start the invariants rule out", lostHead([2]int64{5, 3}), "mismatch after deposit", []string{
			"the steps are explained only by a start that violates nonNegative (balance = -2, ops = 0, which resumeAny admits)"}},
	} {
		rep, err := c.Check(ctx, x.steps)
		if err != nil {
			t.Fatal(err)
		}
		a := rep.Actors[0]
		if a.Reason() != x.reason {
			t.Errorf("%s: %q, want %q:\n%s", x.name, a.Reason(), x.reason, rep.Summary())
		}
		for _, w := range x.says {
			if !strings.Contains(rep.Summary(), w) {
				t.Errorf("%s: the report does not say %q:\n%s", x.name, w, rep.Summary())
			}
		}
		if strings.Contains(rep.Summary(), "module: \n") {
			t.Errorf("%s: an empty module line:\n%s", x.name, rep.Summary())
		}
		// CheckBatch judges it the same
		reps, errs := c.CheckBatch(ctx, [][]qtrace.Step{x.steps}, 0)
		if errs[0] != nil || reps[0].Actors[0].Reason() != x.reason {
			t.Errorf("%s, CheckBatch: %v\n%v", x.name, errs[0], reps[0])
		}
	}
	// without the resume: from init, the lost prefix an error
	b := *c.Binding
	b.Resume = ""
	fromInit := *c
	fromInit.Binding = &b
	explained := lostHead([2]int64{5, 8}, [2]int64{-2, 6})
	rep, err := fromInit.Check(ctx, explained)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || !strings.Contains(rep.Summary(), "error lost-prefix") {
		t.Errorf("without the resume:\n%s", rep.Summary())
	}
	if c.Key(explained) == fromInit.Key(explained) {
		t.Errorf("the key does not name the resume")
	}
	// a search would search every start's orders: refused, with the fix
	if _, err := c.Judge(ctx, explained, SearchOptions{Linearize: true}); err == nil || !strings.Contains(err.Error(), "record a snapshot step at each log's head") {
		t.Errorf("Linearize with a resume: %v", err)
	}
}

// quint confirms the Go evaluator's verdicts from a resume's states: a
// pass it does not confirm (an evaluator that takes every expect as
// holding), or a failure it does not (one that takes no step from any
// state), is the evaluator's error, never a verdict.
func TestResumeFailConfirmedByQuint(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/resume.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer hooks.TamperEval.Store(0)
	for _, x := range []struct {
		tamper int32
		steps  []qtrace.Step
		err    string
	}{
		{5, lostHead([2]int64{5, 8}, [2]int64{-2, 7}), "does not pass in quint's replay"},
		{4, lostHead([2]int64{5, 8}, [2]int64{-2, 6}), "and quint's replay from balance = 3, ops = 0 passes"},
	} {
		hooks.TamperEval.Store(x.tamper)
		_, err := c.Check(context.Background(), x.steps)
		if err == nil || !strings.Contains(err.Error(), x.err) {
			t.Errorf("tamper %d: %v, want %q", x.tamper, err, x.err)
		}
	}
}

// A bounded response open in the state a resume starts from opened in the
// lost prefix, at a time unknown: not judged (a heal before the log began
// is no heal at its first step). One that opens after it is judged from
// the witness start, the only one resumeUp admits.
func TestResumeWithin(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/within_resume.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	step := func(seq uint64, action string, s int) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: "n", Seq: seq, Time: at(s)}
	}
	for _, x := range []struct {
		name   string
		steps  []qtrace.Step
		reason string
	}{
		{"open at the start", []qtrace.Step{step(2, "tick", 0), step(3, "tick", 10)}, ""},
		{"opened after it", []qtrace.Step{step(2, "crash", 0), step(3, "heal", 1), step(4, "tick", 10)}, "violates " + c.Binding.Within[0].Name()},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatal(err)
		}
		if got := rep.Actors[0].Reason(); got != x.reason {
			t.Errorf("%s: %q, want %q:\n%s", x.name, got, x.reason, rep.Summary())
		}
	}
}

// Against brute force: on random short logs that lost their heads, of
// deposits and withdrawals (amounts 1..2) with observed balances, the
// verdict from resumeAny's states (LIMIT 3) passes exactly when some
// start from 0 to 3 (those the invariants keep) takes every step, sees
// every balance and keeps every invariant, as plain Go computes it.
func TestResumeAgainstBruteForce(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/resume_small.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	const limit = 3
	brute := func(ops [][2]int64) bool {
		for b0 := int64(0); b0 <= limit; b0++ {
			b, ok := b0, true
			for _, o := range ops {
				if o[0] < 0 && -o[0] > b {
					ok = false
					break
				}
				b += o[0]
				if b != o[1] || b < 0 || b > limit {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	r := rand.New(rand.NewPCG(11, 12))
	type trial struct {
		ops  [][2]int64
		want bool
	}
	var trials []trial
	yes := 0
	for len(trials) < 40 {
		b := r.Int64N(limit + 1)
		var ops [][2]int64
		for range 1 + r.IntN(3) {
			a := 1 + r.Int64N(2)
			if r.IntN(2) == 0 {
				a = -a
			}
			b += a
			obs := b
			if r.IntN(4) == 0 {
				obs += 1 + r.Int64N(2) // a balance no start explains, mostly
			}
			ops = append(ops, [2]int64{a, obs})
		}
		want := brute(ops)
		if want {
			yes++
		}
		trials = append(trials, trial{ops, want})
	}
	if yes == 0 || yes == len(trials) {
		t.Fatalf("%d of %d trials pass: the property compares too little", yes, len(trials))
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, tr := range trials {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rep, err := c.Check(context.Background(), lostHead(tr.ops...))
			if err != nil {
				t.Errorf("trial %d: %v", i, err)
				return
			}
			if rep.OK() != tr.want {
				t.Errorf("trial %d %v: OK %v, brute force %v:\n%s", i, tr.ops, rep.OK(), tr.want, rep.Summary())
			}
		}()
	}
	wg.Wait()
}

// A resume that admits more states than the search keeps is no verdict,
// with the fix: narrow it, or record a snapshot.
func TestResumeCapIsNoVerdict(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/resume_cap.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Check(context.Background(), lostHead([2]int64{5, 8}))
	if err != nil {
		t.Fatal(err)
	}
	if a := rep.Actors[0]; a.Conforms != NotEvaluated || !strings.Contains(a.Summary(), "no verdict: resume resumeAny admits 30001 states, past 20000: narrow resume, or record a snapshot step when a log starts") {
		t.Errorf("%s:\n%s", a.Reason(), rep.Summary())
	}
}

// A resume composes with hidden actions: from a start where a knows 1,
// two unrecorded gossip rounds bring it to c, whose read of it conforms;
// a read of 2 no start and no gossip explains does not. The verdict is
// the Go evaluator's alone (quint cannot replay hidden actions).
func TestResumeHidden(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/gossip_resume.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		seen []int
		ok   bool
	}{{[]int{1}, true}, {[]int{2}, false}} {
		rep, err := c.Check(context.Background(), []qtrace.Step{gossipStep(4, "c", 0, x.seen)})
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() != x.ok || !strings.Contains(rep.Summary(), "judged from the states resumeA admits (2 of them)") {
			t.Errorf("c saw %v: OK %v, want %v:\n%s", x.seen, rep.OK(), x.ok, rep.Summary())
		}
	}
}

// A path's start is the one it came from at each step, not the first that
// ever reached its state: from resumeAny's 0..3, an inc that saw 1 passes
// from 0, though state 1 is also a start (and two incs that saw 2 and 3,
// from 1); one that saw 7 explains nothing.
func TestResumeRecurringStates(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/ev_counter.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	inc := func(seq uint64, x int64) qtrace.Step {
		return qtrace.Step{Action: "inc", Recorder: "r", Seq: seq, Time: time.Unix(int64(seq), 0), Obs: map[string]any{"x": x}}
	}
	for _, x := range []struct {
		steps []qtrace.Step
		ok    bool
		from  string
	}{
		{[]qtrace.Step{inc(3, 1)}, true, "from x = 0"},
		{[]qtrace.Step{inc(3, 2), inc(4, 3)}, true, "from x = 1"},
		{[]qtrace.Step{inc(3, 7)}, false, ""},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() != x.ok || !strings.Contains(rep.Summary(), x.from) {
			t.Errorf("%v: OK %v, want %v %s:\n%s", x.steps, rep.OK(), x.ok, x.from, rep.Summary())
		}
	}
}

// Conformance, invariants and eventual vals are judged on one path, as a
// replay judges them: an eventual val that holds only where an invariant
// fails (two, past atMostOne) is no pass, from a resume's starts or with
// hidden actions alone.
func TestHiddenOnePath(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	settled := qtrace.Step{Action: "settled", Recorder: "r", Seq: 9, Settled: true, Time: time.Unix(9, 0)}
	for _, x := range []struct {
		binding string
		steps   []qtrace.Step
	}{
		{"testdata/ev_together.yaml", []qtrace.Step{{Action: "inc", Recorder: "r", Seq: 3, Time: time.Unix(3, 0)}, func() qtrace.Step { s := settled; s.Seq = 4; return s }()}},
		{"testdata/ev_hidden.yaml", []qtrace.Step{{Action: "tick", Recorder: "r", Seq: 1, Time: time.Unix(1, 0)}, func() qtrace.Step { s := settled; s.Seq = 2; return s }()}},
	} {
		c, err := NewChecker(x.binding, Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Errorf("%s: %v", x.binding, err)
			continue
		}
		if got := rep.Actors[0].Reason(); got != "violates "+EventuallyName("two") {
			t.Errorf("%s: %q:\n%s", x.binding, got, rep.Summary())
		}
	}
}
