package qtrace

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// genBag is an unordered bag of records as collectors deliver them, of
// everything Reconstruct reads: several recorders, actors, processes and
// threads; gaps, lost prefixes and suffixes, copies and conflicting
// duplicates; invocations completed and pending; settles; watermarks;
// domain clocks with equal values and against program order; clocks
// that step backwards; spans without seqs.
func genBag(r *rand.Rand) []Step {
	at := func(ms int) time.Time { return time.Unix(1_700_000_000, 0).Add(time.Duration(ms) * time.Millisecond) }
	var bag []Step
	recs := 1 + r.IntN(4)
	for rec := range recs {
		name := fmt.Sprintf("r%d", rec)
		actor := fmt.Sprintf("a%d", r.IntN(3))
		proc := fmt.Sprintf("%s-p%d", name, r.IntN(2))
		clock := r.IntN(50)
		seq := uint64(1)
		if r.IntN(6) == 0 {
			seq += uint64(1 + r.IntN(3)) // a lost prefix
		}
		var calls []uint64
		for range r.IntN(12) {
			if r.IntN(10) == 0 {
				seq++ // a gap
			}
			if r.IntN(8) == 0 {
				proc = fmt.Sprintf("%s-p%d", name, r.IntN(3)) // a restart, or an overlap
			}
			if r.IntN(6) == 0 {
				clock -= r.IntN(20) // the wall clock stepped back
			} else {
				clock += r.IntN(20)
			}
			s := Step{Action: []string{"put", "get", "del"}[r.IntN(3)], Seq: seq, Recorder: name, Actor: actor, Process: proc,
				Outcome: "ok", Time: at(clock), Args: map[string]any{"k": int64(r.IntN(3))}}
			if r.IntN(4) != 0 {
				s.Thread = fmt.Sprintf("t%d", r.IntN(2))
			}
			if r.IntN(3) == 0 {
				s.Order = map[string]int64{"lamport": int64(r.IntN(6))}
			}
			if r.IntN(8) == 0 {
				s.Ref = fmt.Sprintf("ref%d", r.IntN(3))
			}
			if r.IntN(8) == 0 {
				s.Invoked = at(clock - r.IntN(40))
			}
			switch r.IntN(12) {
			case 0, 1:
				s.Invocation, s.Outcome = true, ""
				calls = append(calls, seq)
			case 2, 3:
				if len(calls) > 0 {
					k := r.IntN(len(calls))
					s.Call = calls[k]
					calls = append(calls[:k], calls[k+1:]...)
				}
			case 4:
				s.Settled, s.Action = true, "settled"
			case 5:
				s.Ambiguous = true
			case 6:
				if r.IntN(2) == 0 {
					s.Call = seq + 1 // completes an invocation not before it
				}
			}
			bag = append(bag, s)
			if r.IntN(10) == 0 {
				bag = append(bag, s) // a copy
			}
			if r.IntN(25) == 0 {
				d := s
				d.Action = "other" // two records of one seq
				bag = append(bag, d)
			}
			seq++
		}
		switch r.IntN(6) {
		case 0:
			bag = append(bag, Step{Action: "lost", Recorder: name, Actor: actor, Lost: "suffix", LostAfter: seq - 1, Time: at(clock + 5)})
		case 1:
			bag = append(bag, Step{Action: "lost", Recorder: name, Actor: actor, Lost: "suffix", LostAfter: seq - 1 - uint64(r.IntN(2)), LostBefore: at(clock + r.IntN(40)), Time: at(clock)})
		case 2:
			bag = append(bag, Step{Action: "lost", Recorder: name, Lost: "prefix", LostAfter: uint64(r.IntN(3))})
		}
		switch r.IntN(5) {
		case 0:
			bag = append(bag, Step{Action: "complete", Recorder: name, Actor: actor, CompleteThrough: at(clock - r.IntN(30)), Time: at(clock)})
		case 1:
			bag = append(bag, CarryWatermark(name, actor, at(clock-r.IntN(30)), r.IntN(2) == 0))
		}
	}
	if r.IntN(6) == 0 {
		bag = append(bag, Step{Action: "complete", Recorder: AllRecorders, CompleteThrough: at(r.IntN(200))})
	}
	for i := range r.IntN(3) { // spans without seqs, each its own recorder (their order among equal keys is no input's)
		bag = append(bag, Step{Action: "span", Recorder: fmt.Sprintf("span%d", i), Actor: fmt.Sprintf("a%d", r.IntN(3)), Outcome: "ok", Time: at(r.IntN(200) + i)})
	}
	if r.IntN(10) == 0 {
		bag = append(bag, Step{Action: "lost", Recorder: "gone", Actor: "a0", Lost: "suffix", LostAfter: 0}) // a recorder all lost
	}
	r.Shuffle(len(bag), func(i, j int) { bag[i], bag[j] = bag[j], bag[i] })
	return bag
}

// sameReconstruction checks Reconstruct against reconstructRef, the
// implementation that copied steps by value: the same traces, issues
// and all, and an input left as it was.
func sameReconstruction(t *testing.T, steps []Step) {
	t.Helper()
	before := append([]Step(nil), steps...)
	got, gotIssues := Reconstruct(steps)
	if !reflect.DeepEqual(steps, before) {
		t.Fatal("Reconstruct changed its input")
	}
	want, wantIssues := reconstructRef(append([]Step(nil), steps...))
	if !reflect.DeepEqual(gotIssues, wantIssues) {
		t.Fatalf("issues differ:\n got %v\nwant %v", gotIssues, wantIssues)
	}
	if len(got) != len(want) {
		t.Fatalf("%d traces, want %d", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("trace %s differs:\n got %+v\nwant %+v", want[i].Actor, *got[i], *want[i])
		}
	}
}

// Reconstruct works on indices (I38): it must reconstruct every bag as
// the copying implementation did.
func TestReconstructAgainstReference(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 5))
	n := 5_000
	if testing.Short() {
		n = 800
	}
	issues, settled, pending := 0, 0, 0
	for range n {
		bag := genBag(r)
		sameReconstruction(t, bag)
		ts, is := Reconstruct(bag)
		issues += len(is)
		for _, tr := range ts {
			issues += len(tr.Issues)
			pending += len(tr.Pending)
			if tr.Settled {
				settled++
			}
		}
	}
	// the generator reaches what it is for
	if issues == 0 || settled == 0 || pending == 0 {
		t.Fatalf("the bags reached %d issues, %d settled traces, %d pending calls", issues, settled, pending)
	}
}

// ... and the repository's step logs, each, and all of them together.
func TestReconstructOnRepositoryLogs(t *testing.T) {
	var all []Step
	for _, steps := range repositoryLogs(t) {
		sameReconstruction(t, steps)
		all = append(all, steps...)
	}
	sameReconstruction(t, all)
	sameReconstruction(t, reconstructInput(5_000, true))
}
