package validate

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// cycleCalls is a lasso's cycle as its calls.
func cycleCalls(l *HotLasso) []string {
	var out []string
	for _, st := range l.Cycle {
		out = append(out, st.Call)
	}
	return out
}

// Checker.Graph's eventual vals as cycles, on hot.qnt's queue (its facts
// by hand). Every action weakly fair: a client submitting forever keeps
// it from draining, a fair cycle one submit from init that takes submit
// and process (and retry, enabled throughout). Submissions stopping, the
// worker drains it: every cycle of retries leaves process enabled and
// untaken, so none is fair, and the complete graph proves drained; with
// no fairness (every action unfair) the model may stop wherever work is
// pending, a stutter one submit from init. A storm
// (process blocked by a retry) is a fair cycle of retries alone; with
// retries stopping too, the model stutters where only they were enabled.
// A depth bound short of the whole graph still finds the first lasso.
func TestCheckerGraphEventually(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	r := graphOf(t, "testdata/hot.yaml", GraphSearch{})
	if !r.Complete || len(r.Eventually) != 1 {
		t.Fatalf("%s, eventually %+v", r.Extent(), r.Eventually)
	}
	v := r.Eventually[0]
	if !v.Hot || v.HotStates != 4 || v.Lassos != 1 || v.Lasso == nil {
		t.Fatalf("hot: %+v", v)
	}
	l := v.Lasso
	if len(l.Prefix.Steps) != 2 || l.Prefix.Steps[1].Call != "submit" || l.Stutter || l.Component != 4 {
		t.Fatalf("hot's lasso: prefix %+v, stutter %v, component %d", l.Prefix.Steps, l.Stutter, l.Component)
	}
	if !slices.Equal(l.Obliged, []string{"process", "retry"}) || !slices.Contains(l.Takes, "submit") || !slices.Contains(l.Takes, "retry") {
		t.Fatalf("hot's lasso takes %v, obliged %v", l.Takes, l.Obliged)
	}
	if !strings.HasPrefix(l.Replay, "init.then(submit).then(") || !strings.HasSuffix(l.Replay, ".expect(and { not(drained), flag == false, pending == 1 })") {
		t.Fatalf("hot's replay %s", l.Replay)
	}

	stops := graphOf(t, "testdata/hot_stops.yaml", GraphSearch{})
	if v := stops.Eventually[0]; !stops.Complete || !v.Live() || v.Unfair != 2 || v.UnfairBy != "process" {
		t.Fatalf("stops: %s, %+v", stops.Extent(), v)
	}
	if !slices.Equal(stops.Stops, []string{"submit"}) {
		t.Fatalf("stops: %v", stops.Stops)
	}
	none := graphOf(t, "testdata/hot_stops.yaml", GraphSearch{Unfair: []string{"process", "read", "retry"}})
	if v := none.Eventually[0]; !v.Hot || !v.Lasso.Stutter || len(v.Lasso.Prefix.Steps) != 2 {
		t.Fatalf("stops, no fairness: %+v %v", v, cycleCalls(v.Lasso))
	}

	storm := graphOf(t, "testdata/hot_storm.yaml", GraphSearch{})
	v = storm.Eventually[0]
	if !v.Hot || !slices.Equal(v.Lasso.Takes, []string{"retry"}) || !slices.Equal(v.Lasso.Obliged, []string{"retry"}) {
		t.Fatalf("storm: %+v %+v", v, v.Lasso)
	}
	stuck := graphOf(t, "testdata/hot_storm.yaml", GraphSearch{Stops: []string{"submit", "retry"}})
	v = stuck.Eventually[0]
	if !v.Hot || !v.Lasso.Stutter || len(v.Lasso.Cycle) != 0 || v.Lasso.Repeated[0].Value != "true" {
		t.Fatalf("stuck: %+v %+v", v, v.Lasso)
	}

	d := graphOf(t, "testdata/hot.yaml", GraphSearch{Depth: 3})
	if d.Complete || !d.Eventually[0].Hot {
		t.Fatalf("depth 3: %s, %+v", d.Extent(), d.Eventually[0])
	}

	c, err := NewChecker("testdata/hot.yaml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Graph(GraphSearch{Unfair: []string{"sumbit"}}); err == nil || !strings.Contains(err.Error(), "simulate.unfair names sumbit, which is no action of hot's step") {
		t.Fatalf("a misspelled action: %v", err)
	}
}

// hotRun is a run of hot.qnt's actions, recorded by one recorder.
func hotRun(actions ...string) []qtrace.Step {
	var out []qtrace.Step
	for i, a := range actions {
		out = append(out, qtrace.Step{Action: a, Recorder: "q", Seq: uint64(i + 1), Time: atSec(i)})
	}
	return out
}

// MarkHot on runs of hot.qnt's queue that did not settle: a storm of
// retries after a submit goes round (1, false), (1, true) with drained
// false, reported with its steps (the trailing read a stutter, not part
// of it) and the run's visits to the state; reads alone are stutters, no cycle; a cycle the val is true in
// is none; a settled run gets none, its eventual vals judged instead.
// No verdict changes.
func TestMarkHot(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	c, err := NewChecker("testdata/hot.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check := func(steps []qtrace.Step) *Result {
		t.Helper()
		rep, err := c.Check(context.Background(), steps)
		if err != nil || len(rep.Actors) != 1 {
			t.Fatalf("check: %v, %+v", err, rep)
		}
		ok := rep.OK()
		c.MarkHot(rep, steps)
		if rep.OK() != ok {
			t.Fatalf("MarkHot changed the verdict")
		}
		return rep.Actors[0]
	}
	storm := check(hotRun("submit", "retry", "read", "retry", "retry", "retry", "read"))
	h := storm.Hot
	if h == nil || !slices.Equal(h.Vals, []string{"drained"}) || h.From != 4 || h.To != 6 || h.Repeats != 3 {
		t.Fatalf("storm: %+v", h)
	}
	if !slices.Equal(h.Steps, []string{"retry", "retry"}) || !strings.Contains(storm.Summary(), "  hot cycle (evidence, no verdict: the run did not settle): drained false throughout steps 5-6") {
		t.Fatalf("storm's steps %v\n%s", h.Steps, storm.Summary())
	}
	if r := check(hotRun("submit", "read", "read", "read")); r.Hot != nil {
		t.Fatalf("reads: %+v", r.Hot)
	}
	if r := check(hotRun("submit", "process", "submit", "process")); r.Hot != nil {
		t.Fatalf("drained between: %+v", r.Hot)
	}
	settled := append(hotRun("submit", "retry", "retry"), qtrace.Step{Action: "settled", Settled: true, Recorder: "q", Seq: 4, Time: atSec(4)})
	if r := check(settled); r.Hot != nil || !r.Settled {
		t.Fatalf("settled: %+v", r)
	}
}
