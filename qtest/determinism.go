package qtest

import (
	"bytes"
	"fmt"
	"reflect"

	"github.com/chucklehead-dev/gotogether/qtrace"
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
	ids := map[string]*renaming{"actor": {}, "process": {}, "thread": {}}
	for i := 0; i < len(a.Steps) || i < len(b.Steps); i++ {
		switch {
		case i >= len(a.Steps):
			return fmt.Sprintf("step %d: the second run recorded one more (%s)", i, b.Steps[i].String())
		case i >= len(b.Steps):
			return fmt.Sprintf("step %d: the second run recorded no more (the first: %s)", i, a.Steps[i].String())
		}
		x, y := a.Steps[i], b.Steps[i]
		if !sameStep(x, y) {
			return fmt.Sprintf("step %d: %s, then %s", i, x.String(), y.String())
		}
		for _, f := range [][3]string{{"actor", x.Actor, y.Actor}, {"process", x.Process, y.Process}, {"thread", x.Thread, y.Thread}} {
			if !ids[f[0]].same(f[1], f[2]) {
				return fmt.Sprintf("step %d (%s): %s %q, then %q", i, x.String(), f[0], f[1], f[2])
			}
		}
	}
	return ""
}

// renaming is a one-to-one correspondence between two runs' names of
// one identity (a process's default name is its recorder's random id).
type renaming struct{ ab, ba map[string]string }

// same reports whether a and b name the same identity in their runs: the
// first time either is seen pairs them.
func (r *renaming) same(a, b string) bool {
	if r.ab == nil {
		r.ab, r.ba = map[string]string{}, map[string]string{}
	}
	x, okA := r.ab[a]
	y, okB := r.ba[b]
	if !okA && !okB {
		r.ab[a], r.ba[b] = b, a
		return true
	}
	return okA && okB && x == b && y == a
}

// sameStep compares what the model sees of a step, its identities aside
// (sameRun pairs them).
func sameStep(a, b qtrace.Step) bool {
	return a.Action == b.Action && a.Outcome == b.Outcome && a.Error == b.Error && reflect.DeepEqual(a.Args, b.Args) &&
		reflect.DeepEqual(a.Obs, b.Obs) && reflect.DeepEqual(a.Order, b.Order)
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
