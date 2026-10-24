package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The messages for the traps of explorations: every run failing at its
// first step names the constants, and faults a script never made are
// listed.
func TestTrapMessages(t *testing.T) {
	t.Parallel()
	failAt := func(i int) *validate.Report {
		return &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail, Failure: &validate.Failure{Index: i}}}}
	}
	x := &Exploration{constants: map[string]string{"STOCK": "2"}, Runs: []*Run{{Report: failAt(0)}, {Report: failAt(0)}}}
	if m := x.atStart(); !strings.Contains(m, "all 2 runs fail at step 1") || !strings.Contains(m, "STOCK=2") {
		t.Errorf("at start: %q", m)
	}
	x.Runs[1].Report = failAt(3)
	if m := x.atStart(); m != "" {
		t.Errorf("a run failing later: %q", m)
	}
	x.Runs[0].Unscripted = []qenv.Scripted{{Kind: "rpc.api.request", Option: 1, After: 14}}
	if m := x.unscripted(); !strings.Contains(m, "in 1 of 2 runs scripted faults never happened") || !strings.Contains(m, "rpc.api.request=1 after 14 steps") {
		t.Errorf("unscripted: %q", m)
	}
}

// A scripted fault that cannot happen as written fails the exploration,
// once however many runs met it: it never fires, and the claim it was for
// once failed as if the code had survived it, with advice about rates.
func TestUnknownScriptedFault(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: "../validate/testdata/account.yaml", Run: race}.Explore(f, ExploreConfig{Seeds: 2, Chooser: func(uint64) qenv.Chooser {
			return &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.crash.depositr", After: 1}}}
		}})
	}()
	<-done
	all := strings.Join(f.errs, "\n")
	if strings.Count(all, `"nemesis.crash.depositr" is no fault kind or nemesis`) != 1 {
		t.Errorf("errors: %s", all)
	}
}

// A scripted fault a run never came to fails the exploration, though its
// claims hold: a crash "after step 300" of a shorter run once passed
// without crashing.
func TestScriptedFaultNeverMade(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	x := &Exploration{Runs: []*Run{{scripted: true}, {scripted: true, origin: origin{kind: "seed", seed: 2}, Unscripted: []qenv.Scripted{{Kind: "nemesis.crash.payments", After: 300}}}}}
	x.unknownScripted(f)
	if all := strings.Join(f.errs, "\n"); !strings.Contains(all, "in 1 of 2 runs scripted faults never happened (seed 2's: [nemesis.crash.payments=0 after 300 steps])") {
		t.Errorf("errors: %s", all)
	}
}
