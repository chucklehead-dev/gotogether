package qtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// journeySteps are the journey model's steps of actions in order, on one
// recorder, a second apart.
func journeySteps(actions ...string) []qtrace.Step {
	var out []qtrace.Step
	for i, a := range actions {
		s := qtrace.Step{Action: a, Recorder: "r", Seq: uint64(i + 1), Time: time.Unix(100+int64(i), 0)}
		if a == "settled" {
			s.Settled = true
		}
		out = append(out, s)
	}
	return out
}

// A conforming prefix is not a finished journey: three steps of the
// four conform, and only the whole journey, settled, finishes (a settled
// prefix fails its eventual val, Conforms too). Each miss says which part is missing and the fix.
func TestFinishes(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	const journey = "testdata/journey.yaml"
	for _, x := range []struct {
		name          string
		steps         []qtrace.Step
		conforms, met bool
		says          string // what the miss names
	}{
		{"a prefix", journeySteps("submit", "accept", "ready"), true, false, "recorded no settle"},
		{"a settled prefix", journeySteps("submit", "accept", "ready", "settled"), false, false, "eventually journeyEnded is false"},
		{"unsettled", journeySteps("submit", "accept", "ready", "collect"), true, false, "record qobs.Settled after the driver's calls are answered and the collector drained"},
		{"after its settle", journeySteps("submit", "accept", "ready", "collect", "settled", "submit"), false, false, "submit"},
		{"finished", journeySteps("submit", "accept", "ready", "collect", "settled"), true, true, ""},
	} {
		run := Validate(t, journey, x.steps)
		if Conforms().Holds(run) != x.conforms {
			t.Errorf("%s: Conforms %v:\n%s", x.name, !x.conforms, run.Report.Summary())
		}
		v, why := finishes(run.Report)
		if (v == yes) != x.met || !x.met && (v != no || !strings.Contains(why, x.says)) {
			t.Errorf("%s: Finishes %v (%s), want met %v saying %q:\n%s", x.name, v, why, x.met, x.says, run.Report.Summary())
		}
		if Finishes().Holds(run) != x.met {
			t.Errorf("%s: Finishes().Holds %v", x.name, !x.met)
		}
	}
}

// A settled run whose eventual vals hold, with a call never answered, is
// unfinished, naming the call; one whose binding states no eventual val
// is too, saying to add the journey's end as one.
func TestFinishesNamesTheMissingPart(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	steps := journeySteps("submit", "accept", "ready", "collect")
	steps = append(steps,
		qtrace.Step{Action: "submit", Recorder: "r", Seq: 5, Time: time.Unix(110, 0), Invocation: true, Process: "web"},
		qtrace.Step{Action: "settled", Recorder: "r", Seq: 6, Time: time.Unix(111, 0), Settled: true})
	run := Validate(t, "testdata/journey.yaml", steps)
	run.Expect(t, Conforms(), Settles())
	v, why := finishes(run.Report)
	if v != no || !strings.Contains(why, "call submit") || !strings.Contains(why, "process web") || !strings.Contains(why, "never completed") {
		t.Errorf("an unanswered call: %v (%s)", v, why)
	}
	// the failure message carries it
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		run.Expect(f, Finishes())
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "call submit") || !strings.Contains(f.errs[0], "want a finished run") {
		t.Errorf("Expect(Finishes()): %q", f.errs)
	}
	// no eventual val: nothing says where the journey ends
	counter := Validate(t, "testdata/counter.yaml", []qtrace.Step{
		{Action: "inc", Recorder: "r", Seq: 1, Time: time.Unix(1, 0)},
		{Action: "settled", Recorder: "r", Seq: 2, Time: time.Unix(2, 0), Settled: true},
	})
	counter.Expect(t, Conforms(), Settles(), Completes())
	if v, why := finishes(counter.Report); v != no || !strings.Contains(why, "add the journey's end as an eventual val on a ghost flag") {
		t.Errorf("no eventual val: %v (%s)", v, why)
	}
}

// A lost tail may hold the settle and the journey's end: such a run is
// no verdict for Finishes (unknown, not met), so All and None hold of
// none of it, while a run that certainly misses a part misses it whatever
// is lost.
func TestFinishesOverLostTails(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	c, err := validate.NewChecker("testdata/journey.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	check := func(steps []qtrace.Step) *validate.Report {
		rep, err := c.Check(context.Background(), steps)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	lost := check(qtrace.MarkLost(journeySteps("submit", "accept", "ready"), qtrace.Lost{Recorder: "r", After: 3}))
	if v, why := finishes(lost); v != unknown || !strings.Contains(why, "lost its tail") {
		t.Errorf("a lost tail: %v (%s):\n%s", v, why, lost.Summary())
	}
	reps := []*validate.Report{lost}
	if j := All(Finishes()).judge(reps); j.ok || j.unjudged != 1 {
		t.Errorf("All(Finishes()) of a lost tail: %+v", j)
	}
	if j := None(Finishes()).judge(reps); j.ok || j.unjudged != 1 {
		t.Errorf("None(Finishes()) of a lost tail: %+v", j)
	}
	if j := Finishes().judge(reps); j.ok {
		t.Errorf("Finishes() alone of a lost tail: %+v", j)
	}
	// certainly missing: a step the model cannot take before the cut
	bad := check(qtrace.MarkLost(journeySteps("submit", "ready"), qtrace.Lost{Recorder: "r", After: 2}))
	if v, _ := finishes(bad); v != no {
		t.Errorf("a rejected step before a lost tail: %v:\n%s", v, bad.Summary())
	}
}

// A settled run that judged no eventual val finishes by no binding, so
// it is missed whatever else has no verdict (a search out of its
// budget): None(Finishes()) holds of it. AnyOf keeps a Finishes part's
// explanation.
func TestFinishesWithNoEventualValIsMissed(t *testing.T) {
	t.Parallel()
	r := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.NotEvaluated, Settled: true}}}
	if v, why := finishes(r); v != no || !strings.Contains(why, "no eventual val") {
		t.Errorf("settled, no eventual val, no verdict on conformance: %v (%s)", v, why)
	}
	if j := None(Finishes()).judge([]*validate.Report{r}); !j.ok {
		t.Errorf("None(Finishes()): %+v", j)
	}
	if w := AnyOf(Finishes(), Rejects("x")).explain(r); !strings.Contains(w, "no eventual val") {
		t.Errorf("AnyOf(Finishes(), ...) explains %q", w)
	}
}
