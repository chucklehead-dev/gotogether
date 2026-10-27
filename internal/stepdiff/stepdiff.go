// Package stepdiff compares two runs' steps as the model sees them, up
// to the names of their identities: an exploration's determinism check
// (qtest) and a shrink's reset contract (qshrink) ask whether two runs
// recorded the same steps, where a process's default name is its
// recorder's random id and times are the wall clock's.
package stepdiff

import (
	"fmt"
	"reflect"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Diff reports where b's steps first differ from a's: "" if nowhere.
// Steps are compared by what the model sees of them (Same), and their
// actors, processes and threads up to a one-to-one renaming.
func Diff(a, b []qtrace.Step) string {
	ids := map[string]*renaming{"actor": {}, "process": {}, "thread": {}}
	for i := 0; i < len(a) || i < len(b); i++ {
		switch {
		case i >= len(a):
			return fmt.Sprintf("step %d: the second run recorded one more (%s)", i, b[i].String())
		case i >= len(b):
			return fmt.Sprintf("step %d: the second run recorded no more (the first: %s)", i, a[i].String())
		}
		x, y := a[i], b[i]
		if !Same(x, y) {
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

// Same compares what the model sees of a step, its identities aside
// (Diff pairs them).
func Same(a, b qtrace.Step) bool {
	return a.Action == b.Action && a.Outcome == b.Outcome && a.Error == b.Error && reflect.DeepEqual(a.Args, b.Args) &&
		reflect.DeepEqual(a.Obs, b.Obs) && reflect.DeepEqual(a.Order, b.Order) &&
		a.Invocation == b.Invocation && a.Call == b.Call && a.Settled == b.Settled && a.Lost == b.Lost && a.LostAfter == b.LostAfter && a.LostBefore.Equal(b.LostBefore)
}
