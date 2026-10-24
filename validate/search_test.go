package validate

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// On small random traces of two recorders, with pending calls and maybe a
// settle, resolutions enumerates every placement a brute force allows,
// once each, the fewest effects first: a call takes effect after every
// step recorded before its invocation, and before every step recorded
// after the first settle that followed it (the system was quiet there),
// whatever order the actor's steps are in.
func TestResolutionsAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	t0 := time.Unix(0, 0)
	compared, settled, crossed := 0, 0, 0
	for trial := range 2000 {
		recs := []string{"a", "b"}
		seq := map[string]uint64{}
		var steps, pending, settles []qtrace.Step
		at := 0
		for range 1 + r.IntN(6) {
			at += r.IntN(3) // equal times: concurrent across recorders
			rec := recs[r.IntN(2)]
			seq[rec]++
			s := qtrace.Step{Action: "op", Recorder: rec, Seq: seq[rec], Time: t0.Add(time.Duration(at) * 10 * time.Millisecond)}
			switch r.IntN(6) {
			case 0:
				s.Invocation = true
				pending = append(pending, s)
			case 1:
				s.Settled = true
				settles = append(settles, s)
			default:
				steps = append(steps, s)
			}
		}
		if len(pending) == 0 || len(pending) > 2 {
			continue
		}
		// the actor's order, as a search gets it: Reconstruct's
		all := append(append(slices.Clone(steps), pending...), settles...)
		traces, _ := qtrace.Reconstruct(all)
		if len(traces) != 1 {
			t.Fatalf("trial %d: %d traces", trial, len(traces))
		}
		tr := traces[0]
		steps, pending = tr.Steps, tr.Pending
		// the settles a search is given: those no other recorder worked past
		// (times 10 ms apart: equal, or apart by more than RealTimeSlack)
		settles = settlesOf(all)
		got, cut := resolutions(tr, settles, 1<<20)
		compared++
		if len(settles) > 0 {
			settled++
		}
		if cut {
			t.Fatalf("trial %d: cut", trial)
		}
		// brute force: every placement, kept if each call's position is
		// allowed
		allowed := func(p qtrace.Step, pos int) bool {
			for j, s := range steps {
				if before(s, p) && pos <= j {
					return false // a step recorded before its invocation follows it
				}
			}
			for _, st := range settles {
				if !before(p, st) {
					continue
				}
				for j, s := range steps {
					afterSettle := !before(s, st) && !(s.Recorder != st.Recorder && s.Time.Equal(st.Time))
					if afterSettle && pos > j {
						return false // it took effect after a step recorded after the settle
					}
				}
				break
			}
			return true
		}
		var want []placement
		var walk func(i int, cur placement)
		walk = func(i int, cur placement) {
			if i == len(pending) {
				want = append(want, slices.Clone(cur))
				return
			}
			walk(i+1, append(cur, -1))
			for pos := 0; pos <= len(steps); pos++ {
				if allowed(pending[i], pos) {
					walk(i+1, append(cur, pos))
				}
			}
		}
		walk(0, nil)
		if len(want) == 1 {
			crossed++ // no call could take effect anywhere
		}
		key := func(ps []placement) []string {
			var out []string
			for _, p := range ps {
				out = append(out, fmt.Sprint(p))
			}
			slices.Sort(out)
			return out
		}
		if !slices.Equal(key(got), key(want)) {
			t.Fatalf("trial %d: steps %v, pending %v, settles %v:\nresolutions %v\nbrute force %v", trial, show(steps), show(pending), show(settles), key(got), key(want))
		}
		effects := func(p placement) int {
			n := 0
			for _, pos := range p {
				if pos >= 0 {
					n++
				}
			}
			return n
		}
		for i := 1; i < len(got); i++ {
			if effects(got[i]) < effects(got[i-1]) {
				t.Fatalf("trial %d: %v before %v: not the fewest effects first", trial, got[i-1], got[i])
			}
		}
	}
	t.Logf("%d traces compared, %d with a quiet settle, %d whose calls could take effect nowhere", compared, settled, crossed)
}

func show(steps []qtrace.Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, fmt.Sprintf("%s%d@%d", s.Recorder, s.Seq, s.Time.UnixMilli()))
	}
	return out
}
