package qhegel

import (
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Hegel's shrinking stops at a local minimum: it removes what it can
// draw differently, and a fault the failure does not need can survive
// it (hello's double apply shrank to a lost response and a dropped
// request in 3 runs of 10, where the lost response alone is enough).
// Shrink therefore finishes with a pass of its own: each fault in turn
// made nominal, the run replayed with every other choice kept, and the
// change kept if the run still fails the same way.

// byKind replays recorded choices by kind: the k-th choice of a kind
// gets the k-th recorded value of that kind (0 past them, or where the
// recorded value no longer fits). A fault made nominal can change how
// many choices of other kinds follow (a duplicate gone, a handler fewer),
// which a replay by position would misalign.
type byKind struct {
	vals map[string][]int
	next map[string]int
}

func newByKind(cs []qenv.Choice) *byKind {
	b := &byKind{vals: map[string][]int{}, next: map[string]int{}}
	for _, c := range cs {
		b.vals[c.Kind] = append(b.vals[c.Kind], c.Value)
	}
	return b
}

// Choose implements qenv.Chooser.
func (b *byKind) Choose(kind string, n int) int {
	i := b.next[kind]
	b.next[kind]++
	if vs := b.vals[kind]; i < len(vs) && vs[i] < n {
		return vs[i]
	}
	return 0
}

// signature is how a run fails: each actor's failure (kind and action)
// and the invariants it violates.
func signature(rep *validate.Report) string {
	var parts []string
	for _, a := range rep.Actors {
		if f := a.Failure; f != nil {
			call := f.Call
			if i := strings.IndexByte(call, '('); i >= 0 {
				call = call[:i]
			}
			parts = append(parts, a.Actor+":"+f.Kind+":"+call)
		}
		for _, ir := range a.Invariants {
			if ir.Verdict == validate.Fail {
				parts = append(parts, a.Actor+":violates:"+ir.Name)
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// minimize removes the faults v's failure does not need.
func minimize(t testing.TB, sys qtest.System, v *Violation) {
	t.Helper()
	sig := signature(v.Report)
	before := len(v.Faults())
	for changed := true; changed; {
		changed = false
		for i, c := range v.Choices {
			if !qenv.IsFault(c.Kind) || c.Value == 0 {
				continue
			}
			cand := append([]qenv.Choice(nil), v.Choices...)
			cand[i].Value = 0
			r := sys.Judge(t, newByKind(cand))
			if r.Err != nil || r.Report == nil || r.OK() || signature(r.Report) != sig {
				continue
			}
			v.Run.Steps, v.Run.Choices, v.Run.Report = r.Steps, r.Choices, r.Report
			v.Summary = r.Report.Summary()
			changed = true
			break
		}
	}
	if after := len(v.Faults()); after < before {
		var fs []string
		for _, c := range v.Faults() {
			fs = append(fs, c.String())
		}
		t.Logf("qhegel: %d of the shrunk run's %d faults were not needed; the failure needs %d: %s", before-after, before, after, strings.Join(fs, ", "))
	}
}
