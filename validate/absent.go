package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A Cause is why a step a run was expected to show is absent from it
// (Absence): telemetry that was lost, telemetry not collected yet, or a
// system that did not take the step. Only a Defect is a FAIL: a missing
// observation in a production trace is not read as the application's
// bug, nor the application's bug as missing telemetry.
type Cause string

const (
	// Dropped: a recorder that would hold the step lost steps there (a
	// gap in its seqs, a lost tail, a loss record): no verdict. The fix is
	// telemetry that keeps its steps.
	Dropped Cause = "dropped"
	// Uncollected: the step was due after what the recorders that would
	// hold it are known to have delivered (a watermark before the
	// window's end), or the run recorded no settle and ended before the
	// window did: no verdict. The fix is to wait for collection and state
	// it (a watermark past the window), or record a settle.
	Uncollected Cause = "uncollected"
	// Defect: those recorders are complete past where the step was due
	// (a settle, a watermark, the run's steps going on past it) and lost
	// nothing: the system did not take the step.
	Defect Cause = "defect"
)

// label is how a report prints the cause, with what it makes of the
// verdict.
func (c Cause) label() string {
	if c == Defect {
		return "absent (a defect)"
	}
	return string(c) + " (no verdict)"
}

// An Absence is a step a run was expected to show and does not, and why
// (Result.Absent). Which recorders would hold it: those of the steps of
// its actor (Trace.Collected's), since an actor is one instance of the
// model and any of its recorders may record the step that meets the
// obligation; a call's completion is on its invocation's recorder, one
// of them. A gap or a lost tail of one of them, while the step was due,
// makes it Dropped; else a watermark before the window's end, or no
// settle and a run that ended first, Uncollected; else it is a Defect.
type Absence struct {
	Actor string
	// Kind is "within" (a bounded response not met in time, or not
	// judged), "eventually" (an eventual val not judged, or false at the
	// settle) or "pending" (a call never completed).
	Kind string
	// Name is the obligation's (binding.Within.Name, EventuallyName) or,
	// for a call, its action.
	Name  string
	Cause Cause
	// Step is the step that opened a within (nil: the initial state) or
	// the call's invocation record.
	Step *qtrace.Step
	// Due is when a within's response was due (its trigger and the bound).
	Due time.Time
	// Why says what is known of it, and the fix.
	Why string
}

// String is the absence as a report's Absent section prints it.
func (a Absence) String() string {
	return fmt.Sprintf("actor %s: %s: %s: %s", a.Actor, a.Name, a.Cause.label(), a.Why)
}

// Absent are the absences of every actor of the run (Result.Absent).
func (r *Report) Absent() []Absence {
	var out []Absence
	for _, a := range r.Actors {
		out = append(out, a.Absent...)
	}
	return out
}

// dropFix is what a dropped step's diagnosis says to do.
const dropFix = "repair the dropped telemetry (a sink that loses no steps: qobs.FileSink with PerStep or Synced; an exporter that retries), or check a log that has them"

// judgeAbsent fills r.Absent from its verdicts and what its trace lost
// (lost: its losses as recorded, a search's history's; gaps: the holes
// in its recorders' seqs), and makes a bounded response's or an eventual
// val's failure that a gap may explain no verdict, as judgeLost makes
// one a lost tail may: a response, or the step that would make the val
// hold, may be among the steps lost. It runs after judgeLost, and again
// on a search's report (mark), so it rebuilds r.Absent each time.
//
// pendingGap is the gap that may hold a pending call's completion (nil:
// ownGap of gaps, the steps as recorded); a search's order names the
// call's original, whose recorder and seq it re-sequenced.
func judgeAbsent(b *binding.Binding, r *Result, lost []qtrace.Lost, gaps []qtrace.Gap, pendingGap func(qtrace.Step) (qtrace.Gap, bool)) {
	if pendingGap == nil {
		pendingGap = ownGap(gaps)
	}
	r.Gaps, r.Absent = gaps, nil
	var tails []qtrace.Lost
	for _, l := range lost {
		if !l.Prefix {
			tails = append(tails, l)
		}
	}
	slack := qtrace.RealTimeSlack
	// gapIn is a gap whose lost steps may have been recorded in [from, to]
	gapIn := func(from, to time.Time) (qtrace.Gap, bool) {
		for _, g := range gaps {
			if !g.Before.Before(from.Add(-slack)) && (to.IsZero() || !g.After.After(to.Add(slack))) {
				return g, true
			}
		}
		return qtrace.Gap{}, false
	}
	// tailBy is a lost tail cut before t (zero: at any time)
	tailBy := func(t time.Time) (qtrace.Lost, bool) {
		for _, l := range tails {
			if t.IsZero() || l.At.Before(t.Add(slack)) {
				return l, true
			}
		}
		return qtrace.Lost{}, false
	}
	through, every := r.collected()
	add := func(a Absence) { a.Actor = r.Actor; r.Absent = append(r.Absent, a) }
	invariant := func(name string) *InvariantResult {
		for i := range r.Invariants {
			if r.Invariants[i].Name == name {
				return &r.Invariants[i]
			}
		}
		return nil
	}
	// downgrade makes inv's failure no verdict: lost steps may explain it
	downgrade := func(inv *InvariantResult, why string) {
		inv.Verdict, r.noVerdict = NotEvaluated, "lost steps"
		r.Issues = append(r.Issues, qtrace.Issue{Severity: "warn", Kind: "gap", Msg: fmt.Sprintf("%s not evaluated: %s", inv.Name, why)})
	}
	if r.Conforms == Pass || r.noVerdict != "" {
		for _, o := range r.withins {
			a := Absence{Kind: "within", Name: o.name, Step: o.trigger, Due: o.due}
			inv := invariant(o.name)
			failed := o.how == "late" || o.how == "never"
			g, gapped := gapIn(o.at, o.due)
			l, cut := tailBy(o.due)
			switch {
			case failed && inv != nil && inv.Verdict == Fail && gapped:
				downgrade(inv, fmt.Sprintf("%s while its response was due (%s): the response may be among them", g, stamp(o.due)))
				a.Cause, a.Why = Dropped, fmt.Sprintf("%s while the response was due (by %s): it may be among them; %s", g, stamp(o.due), dropFix)
			case failed && inv != nil && inv.Verdict == NotEvaluated && r.noVerdict == "lost steps" && len(tails) > 0:
				l, _ := tailBy(time.Time{})
				if c, ok := tailBy(o.due); ok {
					l = c
				}
				a.Cause, a.Why = Dropped, fmt.Sprintf("%s before the response was due (by %s): it may be among the lost steps; %s", l, stamp(o.due), dropFix)
			case failed && inv != nil && inv.Verdict == Fail:
				a.Cause, a.Why = Defect, o.note+": the recorders are complete past the window and lost nothing there"
			case failed:
				continue // another verdict decided it (a search out of its budget, another admitted start)
			case gapped:
				a.Cause, a.Why = Dropped, fmt.Sprintf("%s while the response was due (by %s): it may be among them; %s", g, stamp(o.due), dropFix)
			case cut:
				a.Cause, a.Why = Dropped, fmt.Sprintf("%s before the response was due (by %s): it may be among the lost steps; %s", l, stamp(o.due), dropFix)
			case o.how == "uncovered":
				a.Cause, a.Why = Uncollected, o.note+"; "+raiseFix(o.due)
			default:
				a.Cause, a.Why = Uncollected, openWhy(o, through, every)
			}
			add(a)
		}
	}
	if r.Conforms == Pass || r.noVerdict != "" {
		for _, ev := range b.Eventually {
			name := EventuallyName(ev)
			a := Absence{Kind: "eventually", Name: name}
			inv := invariant(name)
			switch {
			case inv != nil && inv.Verdict == Pass:
				continue
			case inv != nil && inv.Verdict == Fail && len(gaps) > 0:
				downgrade(inv, fmt.Sprintf("%s: the step that makes it hold may be among them", gaps[0]))
				a.Cause, a.Why = Dropped, fmt.Sprintf("false where the run settled, but %s: the step that makes it hold may be among them; %s", gaps[0], dropFix)
			case inv != nil && inv.Verdict == Fail:
				a.Cause, a.Why = Defect, "false where the run settled, and its recorders lost nothing"
			case inv != nil && r.noVerdict == "lost steps":
				l, _ := tailBy(time.Time{})
				a.Cause, a.Why = Dropped, fmt.Sprintf("its failure may depend on lost steps (%s); %s", l, dropFix)
			case inv != nil:
				continue // another verdict decided it
			case len(tails) > 0:
				a.Cause, a.Why = Dropped, fmt.Sprintf("not judged: %s, which may hold the settle or the steps after it; %s", tails[0], dropFix)
			case !through.IsZero():
				a.Cause, a.Why = Uncollected, fmt.Sprintf("not judged: no settle that the recorders' watermarks reach (the earliest %s): the settle, or a step after it, may not be collected yet; "+
					"raise the watermarks past the settle once the collector has the steps (qobs.Recorder.CompleteThrough, gotogether validate -complete-through)", stamp(through))
			default:
				a.Cause, a.Why = Uncollected, "not judged: the trace's last record is no settle (or another recorder recorded after it): record qobs.Settled once the system is quiet and its telemetry collected (Sim.Settle in a simulation)"
			}
			add(a)
		}
	}
	for i := range r.Pending {
		p := &r.Pending[i]
		a := Absence{Kind: "pending", Name: p.Action, Step: p}
		g, gapped := pendingGap(*p)
		switch {
		case maybeInTail(r, *p):
			var l qtrace.Lost
			for _, t := range r.Lost {
				if !p.Time.After(t.At.Add(slack)) {
					l = t
					break
				}
			}
			a.Cause, a.Why = Dropped, fmt.Sprintf("call %s may have completed in a lost tail (%s); %s", p, l, dropFix)
		case gapped:
			a.Cause, a.Why = Dropped, fmt.Sprintf("call %s may have completed in steps lost after it (%s); %s", p, g, dropFix)
		case !r.Settled && !through.IsZero():
			a.Cause, a.Why = Uncollected, fmt.Sprintf("call %s's completion may not be collected yet: its recorders are complete only through %s and the run recorded no settle they reach; "+
				"validate again once the watermarks pass the run's settle (qobs.Recorder.CompleteThrough, gotogether validate -complete-through)", p, stamp(through))
		case r.Settled:
			a.Cause, a.Why = Defect, fmt.Sprintf("call %s (process %s) was never completed, though the run settled and its recorders lost nothing", p, p.Process)
		default:
			a.Cause, a.Why = Defect, fmt.Sprintf("call %s (process %s) was never completed in a log taken whole (no watermark stated) that ends with no settle: if the log is still being collected, "+
				"state how far it is (-complete-through) and it is uncollected; if the run was cut short, its caller never got the answer", p, p.Process)
		}
		add(a)
	}
}

// ownGap is the gap of gaps that may hold pending call p's completion:
// one on p's own recorder (a completion is recorded by its invocation's
// recorder) missing a seq after p's (a completion follows its
// invocation in its recorder's sequence). A gap's times would not do:
// a simulation records many steps at one instant.
func ownGap(gaps []qtrace.Gap) func(qtrace.Step) (qtrace.Gap, bool) {
	return func(p qtrace.Step) (qtrace.Gap, bool) {
		for _, g := range gaps {
			if g.Recorder == p.Recorder && g.Last > p.Seq {
				return g, true
			}
		}
		return qtrace.Gap{}, false
	}
}

// collected is how far r's steps are known collected (qtrace.Trace's
// CompleteThrough of its Collected).
func (r *Result) collected() (time.Time, bool) {
	t := qtrace.Trace{Collected: r.Collected}
	return t.CompleteThrough()
}

// raiseFix is what an uncollected window's diagnosis says to do.
func raiseFix(due time.Time) string {
	return fmt.Sprintf("wait for collection and state a watermark past %s (qobs.Recorder.CompleteThrough, gotogether validate -complete-through), or record a settle", stamp(due))
}

// openWhy is why a bounded response the trace ended before is not judged.
func openWhy(o obligation, through time.Time, every bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the response was due by %s, after the trace's end (%s), and the run recorded no settle", stamp(o.due), stamp(o.end))
	switch {
	case through.IsZero():
		b.WriteString(" nor a watermark past the window")
	case !through.Before(o.due) && !every:
		b.WriteString("; the watermarks reach past the window, but a recorder of the actor stated none, and its log ends before it")
	default:
		fmt.Fprintf(&b, "; its recorders are complete only through %s", stamp(through))
	}
	return b.String() + ": " + raiseFix(o.due)
}

// maybeInTail reports whether pending call p's completion may be in a
// lost tail of a's: it was invoked before the cut of some recorder of a's
// steps (a search re-sequences the caller's recorder: any lost tail is
// taken as its).
func maybeInTail(a *Result, p qtrace.Step) bool {
	for _, l := range a.Lost {
		if !p.Time.After(l.At.Add(qtrace.RealTimeSlack)) {
			return true
		}
	}
	return false
}

// PendingCause is why pending call p of r (one of r.Pending) never
// completed, as r.Absent says it: Dropped, Uncollected or Defect.
// qtest's Completes and Pending, and Finished, judge a call by it: one
// certainly pending is a Defect's.
func (r *Result) PendingCause(p qtrace.Step) Cause {
	for _, a := range r.Absent {
		if a.Kind == "pending" && a.Step != nil && a.Step.Recorder == p.Recorder && a.Step.Seq == p.Seq && a.Step.Time.Equal(p.Time) {
			return a.Cause
		}
	}
	// a result whose Absent was not judged (made by hand): its losses alone
	if maybeInTail(r, p) {
		return Dropped
	}
	return Defect
}
