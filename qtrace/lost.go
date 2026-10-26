package qtrace

import (
	"fmt"
	"slices"
	"time"
)

// Lost is steps a recorder lost: a prefix (its log starts after seq 1: a
// rotated or truncated log, a recorder that started mid-run) or a suffix
// (its writer stopped: a torn tail, a killed process's buffer). A lost
// prefix makes its first step's state unknown, which is not the model's
// init; a lost suffix leaves what it recorded after unknown, which the
// other recorders' later steps may depend on.
type Lost struct {
	Recorder string
	// Prefix: the steps up to After were lost; else those after it.
	Prefix bool
	// After is the last seq kept, for a lost suffix, or the first seq
	// kept less one, for a prefix (0: the log has no seqs, or says
	// nothing more)
	After uint64
	// At is when the recorder's step next to the loss was recorded: its
	// last kept, for a suffix (its lost steps came after), its first, for
	// a prefix. Zero if it recorded none.
	At time.Time
	// Before, if set, bounds a lost suffix: every lost step was recorded
	// before it (a crash's: the process was dead by then). A settle
	// recorded later than Before (by more than RealTimeSlack) follows
	// every lost step, and settles the run; an unbounded loss leaves every
	// settle unknown. A failure after the cut stays no verdict either way:
	// a later step may depend on a lost one.
	Before time.Time
}

// String says what was lost.
func (l Lost) String() string {
	switch {
	case l.Prefix && l.After > 0:
		return fmt.Sprintf("recorder %s lost seqs 1..%d", l.Recorder, l.After)
	case l.Prefix:
		return fmt.Sprintf("recorder %s lost its first steps", l.Recorder)
	case l.After > 0:
		return fmt.Sprintf("recorder %s lost its tail after seq %d", l.Recorder, l.After)
	}
	return fmt.Sprintf("recorder %s lost its tail", l.Recorder)
}

// Issue is how Reconstruct reports a lost prefix: an error, since the
// trace's first state is not init, which names the fix. A lost suffix is
// no issue of its own: validation says what it leaves unjudged.
func (l Lost) Issue() Issue {
	return Issue{"error", "lost-prefix", fmt.Sprintf("%s (a rotated or truncated log, or a recorder started mid-run): its first step's state is not the model's init. "+
		"Record a snapshot step at the head of each log, the whole state as a model action's parameters, and name that action in the binding (snapshot: restore); "+
		"or name a nullary action whose nondet picks admit the states a log may start in (resume: resumeAny)", l)}
}

// MarkLost declares a loss a log cannot show (a killed process's
// buffered steps, a log whose steps carry no seqs): steps with a loss
// record added, which Reconstruct reads as it reads a lost prefix's or a
// torn tail's (Trace.Lost). Its time is that of l.Recorder's step next to
// the loss: for a suffix the last with a seq up to l.After (the last
// recorded, with After 0), for a prefix its first.
func MarkLost(steps []Step, l Lost) []Step {
	rec := Step{Action: "lost", Recorder: l.Recorder, Process: l.Recorder, Lost: "suffix", LostAfter: l.After, LostBefore: l.Before}
	if l.Prefix {
		rec.Lost = "prefix"
	}
	// earlier orders one recorder's steps: by seq, then by time (a log
	// without seqs)
	earlier := func(a, b *Step) bool { return a.Seq < b.Seq || a.Seq == b.Seq && a.Time.Before(b.Time) }
	var near *Step
	for i := range steps {
		s := &steps[i]
		if s.Recorder != l.Recorder || s.Lost != "" || !s.CompleteThrough.IsZero() || !l.Prefix && l.After > 0 && s.Seq > l.After {
			continue
		}
		if near == nil || l.Prefix && earlier(s, near) || !l.Prefix && earlier(near, s) {
			near = s
		}
	}
	if near != nil {
		rec.Time, rec.Actor, rec.Process = near.Time, near.Actor, near.Process
	}
	return append(slices.Clip(steps), rec)
}
