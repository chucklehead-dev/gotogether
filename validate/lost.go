package validate

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// snapshotFirst is b.SnapshotFirst(tr).
func snapshotFirst(b *binding.Binding, tr *qtrace.Trace) bool { return b.SnapshotFirst(tr) }

// startIssues is b.StartIssues(tr).
func startIssues(b *binding.Binding, tr *qtrace.Trace) []qtrace.Issue { return b.StartIssues(tr) }

// judgeLost is r as its trace's lost tails leave it (tr.Lost, or a
// search's of the history it ordered): a failure at a step recorded at or
// after a cut (when the recorder lost its tail) may depend on the steps it
// lost, so it is no verdict, said so; a verdict before every cut stands,
// and says what was lost. The eventual vals of such a trace were not
// judged (Reconstruct leaves it unsettled).
func judgeLost(b *binding.Binding, r *Result, lost []qtrace.Lost) {
	var tails []qtrace.Lost
	for _, l := range lost {
		if !l.Prefix {
			tails = append(tails, l)
		}
	}
	if len(tails) == 0 {
		return
	}
	r.Lost = tails
	for i, is := range r.Issues {
		if is.Kind == "eventually" { // (a search's order shows no loss: its result said the generic why)
			r.Issues[i] = notJudged(b.Eventually, lost)
		}
	}
	// cutBy is the lost tail f's step may depend on, if any
	cutBy := func(f *Failure) (qtrace.Lost, bool) {
		if f == nil {
			return qtrace.Lost{}, false
		}
		s := f.Step
		for i := min(f.Index, len(r.Calls)-1); s == nil && i >= 0; i-- {
			s = r.Calls[i].Step // an inferred step (a restart): where the steps before it were
		}
		if s == nil {
			s = &qtrace.Step{} // the initial state's: before every cut, unless a window says otherwise
		}
		for _, l := range tails {
			// a lost step came after l.At on its recorder's clock: a step of
			// another recorder at that time, give or take the clocks' slack,
			// may follow it; a within's response lost before it was due
			// would have met it
			own := s.Recorder == l.Recorder && (s.Seq <= l.After || l.After == 0)
			if !own && !s.Time.Before(l.At.Add(-qtrace.RealTimeSlack)) || !f.window.IsZero() && l.At.Before(f.window) {
				return l, true
			}
		}
		return qtrace.Lost{}, false
	}
	at := func(f *Failure) string {
		switch s := f.Step; {
		case s == nil:
			return fmt.Sprintf("step %d", f.Index+1)
		case strings.HasPrefix(s.Recorder, "linearized:") || strings.HasPrefix(s.Recorder, "resolved:"):
			return fmt.Sprintf("step %d (%s)", f.Index+1, s.Action) // a search's order: its seqs are the order's
		default:
			return fmt.Sprintf("step %d (%s, seq %d of recorder %s)", f.Index+1, s.Action, s.Seq, s.Recorder)
		}
	}
	downgraded := false
	if r.Conforms == Fail {
		if l, ok := cutBy(r.Failure); ok {
			r.Conforms, r.noVerdict, downgraded = NotEvaluated, "lost steps", true
			r.Issues = append(r.Issues, qtrace.Issue{Severity: "warn", Kind: "lost-suffix", Msg: fmt.Sprintf(
				"conformance not evaluated: the failure at %s may depend on %s's lost steps after seq %d (%s): record so the steps survive (a qobs.Synced sink; a Buffered one loses its tail when its process dies), or check a log that has them",
				at(r.Failure), l.Recorder, l.After, l)})
			for i := range r.Invariants {
				if r.Invariants[i].Verdict == Fail {
					r.Invariants[i].Verdict, r.Invariants[i].Failure = NotEvaluated, nil
				}
			}
		}
	}
	for i := range r.Invariants {
		inv := &r.Invariants[i]
		if inv.Verdict != Fail {
			continue
		}
		if l, ok := cutBy(inv.Failure); ok {
			r.Issues = append(r.Issues, qtrace.Issue{Severity: "warn", Kind: "lost-suffix", Msg: fmt.Sprintf(
				"%s not evaluated: its violation at %s may depend on %s's lost steps after seq %d (%s)", inv.Name, at(inv.Failure), l.Recorder, l.After, l)})
			inv.Verdict, downgraded = NotEvaluated, true
			r.noVerdict = "lost steps"
		}
	}
	if downgraded {
		return
	}
	var said []string
	for _, l := range tails {
		said = append(said, l.String())
	}
	msg := strings.Join(said, "; ") // a verdict already none (a search's budget): the loss alone
	switch {
	case r.Conforms == Pass && passed(r):
		msg = "the recorded steps conform; " + msg
	case !passed(r):
		msg += "; the failure comes before it, and stands"
	}
	r.Issues = append(r.Issues, qtrace.Issue{Severity: "info", Kind: "lost-suffix", Msg: msg})
}

// notJudged is the issue of a trace whose eventual vals were not judged:
// it recorded no settle last, or (a lost tail) what came after is unknown.
func notJudged(eventually []string, lost []qtrace.Lost) qtrace.Issue {
	why := "the trace's last record is no settle (Sim.Settle, or qobs.Settled after a probe), or another recorder recorded after it"
	var tails []string
	for _, l := range lost {
		if !l.Prefix {
			tails = append(tails, l.String())
		}
	}
	if len(tails) > 0 { // a lost enqueue would leave work pending that no state shows
		why = strings.Join(tails, "; ") + ": what was recorded after, a settle or the work it leaves, is unknown"
	}
	return qtrace.Issue{Severity: "info", Kind: "eventually", Msg: fmt.Sprintf("eventual vals not judged (%s): %s", strings.Join(eventually, ", "), why)}
}
