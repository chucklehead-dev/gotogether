package qtest

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/chucklehead-dev/gotogether/internal/stepdiff"
)

// A simulated run is deterministic only if every choice goes through the
// Chooser: a map range, math/rand, the wall clock, a `go` statement or a
// select over several ready channels makes another run from the same seed,
// and a failure that does not replay. An exploration therefore runs its
// first seed again and compares the two: the choices offered and made, and
// the steps as the model sees them (not their wall times or recorder).

// sameRun reports where b first differs from a: "" if nowhere.
func sameRun(a, b *Run) string {
	for i := 0; i < len(a.Choices) || i < len(b.Choices); i++ {
		switch {
		case i >= len(a.Choices):
			return fmt.Sprintf("choice %d: the second run made one more (%s)", i, b.Choices[i])
		case i >= len(b.Choices):
			return fmt.Sprintf("choice %d: the second run made no more (the first: %s)", i, a.Choices[i])
		}
		x, y := a.Choices[i], b.Choices[i]
		if x.Kind != y.Kind || x.N != y.N || x.Value != y.Value || x.Step != y.Step || !bytes.Equal(x.Bytes, y.Bytes) {
			return fmt.Sprintf("choice %d: %s of %d after step %d, then %s of %d after step %d", i, x, x.N, x.Step, y, y.N, y.Step)
		}
	}
	if a.Unsettled != b.Unsettled {
		return fmt.Sprintf("settling: %q, then %q", a.Unsettled, b.Unsettled)
	}
	if diff := stepdiff.Diff(a.Steps, b.Steps); diff != "" {
		return diff
	}
	if len(a.Phases) != len(b.Phases) {
		return "workload prerequisites: the declared phase count changed"
	}
	for i := range a.Phases {
		x, xerr := json.Marshal(a.Phases[i])
		y, yerr := json.Marshal(b.Phases[i])
		if xerr != nil || yerr != nil {
			return fmt.Sprintf("workload phase %d: invalid observation JSON (%v, %v)", i, xerr, yerr)
		}
		if !bytes.Equal(x, y) {
			return fmt.Sprintf("workload phase %d (%s): declared prerequisites or observed results changed", i, a.Phases[i].Name)
		}
	}
	return ""
}

// checkDeterminism runs seed again and fails t, naming the first
// difference, if the run is not first's: its error (a failing run is the
// one whose replay matters most), its choices, its steps.
func checkDeterminism(t interface {
	Helper()
	Errorf(string, ...any)
}, first *Run, again func() *Run, name string) {
	t.Helper()
	if first == nil {
		return
	}
	second := again()
	const why = "the system is not deterministic, so no failure of it replays (nondeterminism outside the Chooser: a map " +
		"range, math/rand, the wall clock, a `go` statement, a select over several ready channels; `gotogether lint -sim` " +
		"finds the `go` statements and clocks)"
	if e1, e2 := errText(first.Err), errText(second.Err); e1 != e2 {
		t.Errorf("gotogether: %s ran twice ended differently: %s, then %s: %s", name, e1, e2, why)
		return
	}
	if d := sameRun(first, second); d != "" {
		t.Errorf("gotogether: %s ran twice made two runs, first different at %s: %s", name, d, why)
	}
}

// errText is how a run ended, for comparing two runs.
func errText(err error) string {
	if err == nil {
		return "at its end"
	}
	return "with " + err.Error()
}
