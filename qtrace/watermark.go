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

// CompleteThrough is how far the actor's steps are known collected: the
// earliest watermark of the recorders of its steps (zero: none stated
// one), and whether each of them stated one (else those that did not
// keep logs taken whole, which the trace's own end bounds).
func (t *Trace) CompleteThrough() (through time.Time, every bool) {
	every = len(t.Collected) > 0
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
