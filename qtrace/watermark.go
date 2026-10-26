package qtrace

import (
	"fmt"
	"slices"
	"time"
)

// AllRecorders is the recorder a watermark record names to state it of
// every recorder of the input (a collector's: everything it ingested
// through a time).
const AllRecorders = "*"

// A Watermark is how far a recorder's steps are known collected: every
// step it recorded at or before Through is in the input (a collector
// that knows what it has ingested, an exporter that flushed). A log with
// no watermark is taken whole, as before watermarks: what its recorder
// recorded is all there, and the run's own end bounds what it shows.
type Watermark struct {
	Recorder string
	// Through is the watermark; zero for a recorder that stated none,
	// whose log is taken whole.
	Through time.Time
}

// String says what the watermark states.
func (w Watermark) String() string {
	if w.Through.IsZero() {
		return fmt.Sprintf("recorder %s states no watermark (its log taken whole)", w.Recorder)
	}
	return fmt.Sprintf("recorder %s is complete through %s", w.Recorder, w.Through.UTC().Format(time.RFC3339Nano))
}

// A Gap is a hole in a recorder's seqs: steps First..Last were lost (a
// dropped export, sampling), recorded between the kept steps either side
// of them, at After and Before. Which actor they were of is unknown, so
// a gap is every actor's of its recorder, as its issue is.
type Gap struct {
	Recorder    string
	First, Last uint64
	// After and Before are the times of the kept steps before and after
	// the hole: the lost steps were recorded between them.
	After, Before time.Time
}

// String says what the gap lost.
func (g Gap) String() string {
	if g.First == g.Last {
		return fmt.Sprintf("recorder %s lost seq %d", g.Recorder, g.First)
	}
	return fmt.Sprintf("recorder %s lost seqs %d..%d", g.Recorder, g.First, g.Last)
}

// MarkComplete states a watermark the log does not carry: steps with a
// watermark record added, saying recorder's steps (AllRecorders: every
// recorder's) through t are all in the input, which Reconstruct reads
// into Trace.Collected. A recorder's later watermark replaces an earlier
// one; one of AllRecorders and one of its own, the later of the two.
func MarkComplete(steps []Step, recorder string, t time.Time) []Step {
	return append(slices.Clip(steps), Step{Action: "complete", Recorder: recorder, Process: recorder, Time: t, CompleteThrough: t})
}

// CarryWatermark is the watermark record a search's order carries for
// an actor it re-sequences onto recorder (validate's Linearize and
// ResolvePending): the actor's CompleteThrough as recorded, taken as
// it is (the order's own dense seqs do not raise it), and with whole
// set when some recorder of the actor stated none, whose log the
// trace's own end bounds. The order's check then judges what the steps
// as recorded were judged.
func CarryWatermark(recorder, actor string, through time.Time, whole bool) Step {
	return Step{Action: "complete", Recorder: recorder, Process: recorder, Actor: actor, Time: through, CompleteThrough: through, carried: true, carriedWhole: whole}
}

// CollectedThrough is how far each recorder of steps that stated a
// watermark (its own, or AllRecorders') is known collected: the later of
// the watermark and, for a recorder whose steps carry seqs, its last
// step before any hole in them (a recorder's seqs are dense, so its
// step at time t with nothing missing below it says every step it
// recorded by t is in: a watermark that lags its own steps costs no
// verdict). Steps without seqs (converted spans) have the watermark
// alone. A recorder with no step is in no trace, and not here; one that
// stated no watermark is not here either: its log is taken whole.
// Reconstruct and a search's settles read watermarks so.
func CollectedThrough(steps []Step) map[string]time.Time {
	marks := map[string]time.Time{}
	carried := map[string]bool{}
	seqs := map[string][]uint64{}
	at := map[string]map[uint64]time.Time{}
	for _, s := range steps {
		switch {
		case !s.CompleteThrough.IsZero():
			if s.CompleteThrough.After(marks[s.Recorder]) { // a watermark only rises
				marks[s.Recorder] = s.CompleteThrough
			}
			carried[s.Recorder] = carried[s.Recorder] || s.carried
		case s.Lost != "":
		default:
			if at[s.Recorder] == nil {
				at[s.Recorder] = map[uint64]time.Time{}
			}
			if s.Seq != 0 {
				seqs[s.Recorder] = append(seqs[s.Recorder], s.Seq)
				at[s.Recorder][s.Seq] = s.Time
			}
		}
	}
	out := map[string]time.Time{}
	for rec := range at {
		w := marks[rec]
		if all := marks[AllRecorders]; all.After(w) && !carried[rec] {
			w = all
		}
		if w.IsZero() {
			continue
		}
		if q := seqs[rec]; len(q) > 0 && !carried[rec] {
			slices.Sort(q)
			last := q[0]
			for _, n := range q[1:] {
				if n > last+1 {
					break // a hole: what comes after it is not known complete
				}
				last = n
			}
			if t := at[rec][last]; t.After(w) {
				w = t
			}
		}
		out[rec] = w
	}
	return out
}

// CompleteThrough is how far the actor's steps are known collected: the
// earliest watermark of the recorders of its steps, as CollectedThrough
// raises it (zero: none stated one), and whether each of them stated one
// (else those that did not keep logs taken whole, which the trace's own
// end bounds).
func (t *Trace) CompleteThrough() (through time.Time, every bool) {
	every = len(t.Collected) > 0 && !t.carriedWhole
	for _, w := range t.Collected {
		if w.Through.IsZero() {
			every = false
			continue
		}
		if through.IsZero() || w.Through.Before(through) {
			through = w.Through
		}
	}
	return through, every && !through.IsZero()
}
