package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// No claim has a verdict over no runs: All and None held of none (All
// once did, vacuously), and Some fails for want of a run. The
// exploration's verdict says so, whatever the claim.
func TestClaimsOverNoRuns(t *testing.T) {
	t.Parallel()
	a := Expectation{desc: "a", check: func(*validate.Report) bool { return true }}
	for _, c := range []Claim{All(a), None(a), Some(a), Conforms(), Settles(), All(Conforms())} {
		if j := c.judge(nil); j.ok {
			t.Errorf("%s over no runs: ok", c)
		}
		x := &Exploration{test: "TestX"}
		if v := x.verdict(c, nil); v.ok || !strings.Contains(v.say(t), "has no runs, so the claim has no verdict") {
			t.Errorf("%s: the verdict over no runs is ok %v: %q", c, v.ok, v.say(t))
		}
	}
	for _, c := range []Claim{Reaches("rpc.db").InAtLeast(0.5), Takes("put").InAtLeast(0.5), Exercised()} {
		x := &Exploration{test: "TestX", actions: map[string]bool{"put": true}}
		if v := x.verdict(c, nil); v.ok {
			t.Errorf("%s over no runs: ok", c)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("TakesEach() of no takes: no panic")
		}
	}()
	TakesEach()
}

// Conforms and Settles are three-valued: a run with no verdict (a search
// out of its budget; a recorder's lost tail, which may hold the settle)
// neither meets nor misses them, so All fails of it as unjudged, None
// does not hold of it, and Some does not count it.
func TestConformsAndSettlesThreeValued(t *testing.T) {
	t.Parallel()
	pass := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass, Settled: true}}}
	undecided := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.NotEvaluated}}}
	failed := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled"}}}}
	lost := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass, Lost: []qtrace.Lost{{Recorder: "r"}}}}}
	quiet := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass}}}
	for _, c := range []struct {
		e    Expectation
		r    *validate.Report
		want tri
	}{
		{Conforms(), pass, yes}, {Conforms(), undecided, unknown}, {Conforms(), failed, no},
		{Settles(), pass, yes}, {Settles(), lost, unknown}, {Settles(), quiet, no},
		{Settles(), &validate.Report{Unsettled: "a task at work", Actors: lost.Actors}, no},
	} {
		if got := c.e.value(c.r); got != c.want {
			t.Errorf("%s of %s: %d, want %d", c.e, c.r.Summary(), got, c.want)
		}
	}
	for _, c := range []struct {
		claim Claim
		rs    []*validate.Report
		ok    bool
		n     int
	}{
		{All(Conforms()), []*validate.Report{pass, undecided}, false, 0},
		{None(Settles()), []*validate.Report{quiet, lost}, false, 0},
		{Some(Settles()), []*validate.Report{lost, pass}, true, 1},
	} {
		if j := c.claim.judge(c.rs); j.ok != c.ok || j.n != c.n || j.unjudged != 1 && !c.ok {
			t.Errorf("%s: %+v, want ok %v, %d meeting, 1 unjudged", c.claim, j, c.ok, c.n)
		}
	}
}

// A passing exploration's summary states what bounds its claims: an
// exhaustive search's bound and whether it was complete, the sample a
// universal claim rests on (none for a claim some run shows, a witness),
// a PCT schedule's guarantee or why it is void, and the share of passes
// the Go evaluator judged alone. Evidence carries each.
func TestSummaryStatesItsBounds(t *testing.T) {
	t.Parallel()
	ok := func() *Run {
		return &Run{Report: &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass}}}}
	}
	var runs []*Run
	for range 30 {
		runs = append(runs, ok())
	}
	seeded := &Exploration{test: "TestX", Runs: runs}
	sample := "; a sample, no proof: a failure 1 run in 10 shows is missed at most 5% of the time, a rarer one more often, one the runs cannot reach always"
	if s := seeded.summary(true); !strings.Contains(s, sample) {
		t.Errorf("seeded, universal: %q\nwant %q", s, sample)
	}
	if s := seeded.summary(false); strings.Contains(s, "a sample") {
		t.Errorf("seeded, some run: %q states a sample", s)
	}
	if e := seeded.Evidence(); e.Sample == nil || e.Sample.Runs != 30 || e.Sample.OneIn != 10 || !strings.Contains(e.String(), "sample    30 seeded runs, no proof") {
		t.Errorf("seeded evidence: %+v\n%s", e.Sample, e)
	}
	complete := &Exploration{test: "TestX", Runs: runs[:3], search: &search{bound: Bound{Delays: 2, Faults: 1}}}
	if s := complete.summary(true); !strings.Contains(s, "; complete within 2 delays and 1 fault: every run within that bound, none beyond it") || strings.Contains(s, "a sample") {
		t.Errorf("complete search: %q", s)
	}
	if e := complete.Evidence(); e.Sample != nil || !strings.Contains(e.String(), "search    complete within 2 delays and 1 fault") {
		t.Errorf("complete search's evidence:\n%s", e)
	}
	truncated := &Exploration{test: "TestX", Runs: runs[:3], search: &search{bound: Bound{Delays: 1, Faults: 0, Partial: true}, left: 7}}
	if s := truncated.summary(true); !strings.Contains(s, "; truncated within 1 delay and 0 faults: at least 7 runs within it left untried") {
		t.Errorf("truncated search: %q", s)
	}
	for _, c := range []struct {
		steps int
		want  string
	}{
		{400, "; PCT depth 3, Steps 500: a bug of depth up to 3 is found in a run with probability at least 1/(4 tasks·500^2) = 1e-06"},
		{900, "; PCT depth 3, Steps 500: runs took up to 900 scheduling steps, past Steps, so its guarantee holds of none of them"},
	} {
		x := &Exploration{test: "TestX", Runs: []*Run{ok(), ok()}}
		x.Runs[0].pct = &pctRun{depth: 3, bound: 500, steps: 120, tasks: 4}
		x.Runs[1].pct = &pctRun{depth: 3, bound: 500, steps: c.steps, tasks: 2}
		if s := x.summary(false); !strings.Contains(s, c.want) {
			t.Errorf("PCT, runs of %d steps: %q\nwant %q", c.steps, s, c.want)
		}
		if p := x.Evidence().PCT; p == nil || p.MaxSteps != c.steps || p.MaxTasks != 4 || (p.Probability > 0) != (c.steps <= 500) {
			t.Errorf("PCT evidence: %+v", p)
		}
	}
	for _, c := range []struct {
		j    *Judges
		want string
	}{
		{nil, ""},
		{&Judges{Confirmed: 3}, " (quint confirmed 3)"},
		{&Judges{Evaluator: 28, Confirmed: 2}, " (quint confirmed 2, the Go evaluator alone the other 28)"},
		{&Judges{Evaluator: 30}, " (the Go evaluator's, none confirmed by quint)"},
	} {
		if got := c.j.conformWords(30); got != c.want {
			t.Errorf("%+v: %q, want %q", c.j, got, c.want)
		}
	}
}

// A share claim that held by a narrow margin says how likely as many
// seeds are to fall short of it at the rate measured: 16 of 30 runs
// for a half falls short about 36% of the time.
func TestShareRisk(t *testing.T) {
	t.Parallel()
	var runs []*Run
	for i := range 30 {
		r := &Run{Report: &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass}}}}
		if i < 16 {
			r.Choices = append(r.Choices, qenv.Choice{Kind: "fault.rpc.db", N: 2, Value: 1})
		}
		runs = append(runs, r)
	}
	x := &Exploration{test: "TestX", Runs: runs}
	v := x.verdict(Reaches("rpc.db").InAtLeast(0.5), x.reports())
	if !v.ok || v.log == nil || !strings.Contains(v.log(t), "16 of 30 runs (want 15); at that rate 30 runs fall short of it") {
		t.Errorf("a narrow share: ok %v, logs %v", v.ok, v.log != nil)
	}
	if v := x.verdict(Reaches("rpc.db").InAtLeast(0.1), x.reports()); !v.ok || v.log != nil {
		t.Errorf("a wide margin logs a risk: %v", v.log(t))
	}
}
