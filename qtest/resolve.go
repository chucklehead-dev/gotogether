package qtest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// DefaultResolveBudget is how many resolutions ResolvePending checks per
// actor at most.
const DefaultResolveBudget = 200

// ResolvePending makes Record and Validate search what each call left
// pending (qobs.Invoke with no Done: the caller crashed or never heard
// back) may have done, as Jepsen treats an :info operation: either it had
// no effect, or it took effect after one of the steps recorded at or
// after its invocation and before the first settle after it (the system
// was quiet there), with no outcome or observation (its step is
// Ambiguous: no expect is checked after it). The actor conforms if some
// resolution does; the report says which, as an issue, and the run's
// Steps are the resolution checked. When none conforms, the one that got
// furthest is shown and the actor fails as it does. At most
// DefaultResolveBudget resolutions per actor, the fewest effects first; a
// search cut short is no verdict, as Linearize's. Pending calls stay
// pending (Completes still fails of them): their callers never knew.
//
// A resolution keeps the recorded order of the steps: with Linearize too
// it would search both, which it does not; the two are refused together.
func ResolvePending() Option {
	return option("ResolvePending", func(c *settings) { c.resolvePending = true })
}

// resolve replaces each actor's steps by a resolution of its pending
// calls the model accepts (checked with ch, conformance only), within
// budget per actor, re-sequenced onto one thread per actor. It returns the
// steps, an issue per actor with pending calls, the actors whose search
// was cut short, and the issues each actor's steps had as recorded that no
// resolution removes (gaps, cycles).
func resolve(t testing.TB, ch *validate.Checker, steps []qtrace.Step, budget int) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue) {
	t.Helper()
	traces, issues := qtrace.Reconstruct(steps)
	undecided, kept = map[string]bool{}, map[string][]qtrace.Issue{}
	for _, tr := range traces {
		for _, i := range tr.Issues {
			if i.Kind == "gap" || i.Kind == "cycle" {
				kept[tr.Actor] = append(kept[tr.Actor], i)
			}
		}
		chosen := tr.Steps
		if len(tr.Pending) > 0 {
			var issue qtrace.Issue
			var cut bool
			chosen, issue, cut = resolution(t, ch, tr, settlesOf(steps), budget)
			issues = append(issues, issue)
			if cut {
				undecided[tr.Actor] = true
			}
		}
		seq := 0
		add := func(s qtrace.Step) {
			seq++
			// one thread, in the resolution's order: its order keys and the
			// calls its completions name (paired already) would contradict it
			s.Recorder, s.Thread, s.Seq, s.Actor, s.Order = "resolved:"+tr.Actor, "resolved", uint64(seq), tr.Actor, nil
			if !s.Invocation {
				s.Call = 0
			}
			out = append(out, s)
		}
		for _, s := range chosen {
			add(s)
		}
		for _, p := range tr.Pending { // still pending: their callers never knew
			add(p)
		}
		if tr.Settled {
			add(qtrace.Step{Action: "settled", Settled: true})
		}
	}
	return out, issues, undecided, kept
}

// placement is where each pending call took effect: -1 none, else after
// that many of the actor's steps.
type placement []int

// resolutions enumerates the placements of pending calls, the fewest
// effects first, then the earliest, at most budget of them: a call may
// take effect only after the steps recorded before its invocation.
func resolutions(tr *qtrace.Trace, settles []qtrace.Step, budget int) (out []placement, cut bool) {
	k := len(tr.Pending)
	from := make([]int, k) // the earliest position each may take effect at
	to := make([]int, k)   // the latest
	for i, p := range tr.Pending {
		// after every step recorded before its invocation
		for j, s := range tr.Steps {
			if before(s, p) {
				from[i] = j + 1
			}
		}
		// before every step recorded after the first settle that followed
		// it: the system was quiet there, with nothing left in flight
		to[i] = len(tr.Steps)
		for _, st := range settles {
			if !before(p, st) {
				continue
			}
			to[i] = 0
			for j, s := range tr.Steps {
				if before(s, st) {
					to[i] = j + 1
				}
			}
			break
		}
		to[i] = max(to[i], from[i])
	}
	for effects := 0; effects <= k; effects++ {
		var rec func(i, left int, cur placement) bool
		rec = func(i, left int, cur placement) bool {
			if i == k {
				if left == 0 {
					if len(out) == budget {
						return false
					}
					out = append(out, append(placement(nil), cur...))
				}
				return true
			}
			if k-i > left && !rec(i+1, left, append(cur, -1)) {
				return false
			}
			if left > 0 {
				for pos := from[i]; pos <= to[i]; pos++ {
					if !rec(i+1, left-1, append(cur, pos)) {
						return false
					}
				}
			}
			return true
		}
		if !rec(0, effects, nil) {
			return out, true
		}
	}
	return out, false
}

// before reports whether a was recorded before b: on one recorder, a lower
// seq; across recorders, an earlier time.
func before(a, b qtrace.Step) bool {
	if a.Recorder == b.Recorder {
		return a.Seq < b.Seq
	}
	return a.Time.Before(b.Time)
}

// settlesOf are the settle records of steps, earliest first.
func settlesOf(steps []qtrace.Step) []qtrace.Step {
	var out []qtrace.Step
	for _, s := range steps {
		if s.Settled {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b qtrace.Step) int { return a.Time.Compare(b.Time) })
	return out
}

// apply is the actor's steps with the pending calls placed.
func apply(tr *qtrace.Trace, pl placement) []qtrace.Step {
	var out []qtrace.Step
	for pos := 0; pos <= len(tr.Steps); pos++ {
		for i, p := range tr.Pending {
			if pl[i] == pos {
				s := p
				s.Invocation, s.Ambiguous, s.Outcome, s.Obs, s.Order = false, true, "", nil, nil
				// in the process of the step it follows (the first's, at the
				// start): its effect landed there, and a process of its own
				// between them would read as two restarts
				if pos > 0 {
					s.Process = tr.Steps[pos-1].Process
				} else if len(tr.Steps) > 0 {
					s.Process = tr.Steps[0].Process
				}
				out = append(out, s)
			}
		}
		if pos < len(tr.Steps) {
			out = append(out, tr.Steps[pos])
		}
	}
	return out
}

// describe says what a placement assumes, for its issue.
func describe(tr *qtrace.Trace, pl placement) string {
	var parts []string
	for i, p := range tr.Pending {
		switch pos := pl[i]; {
		case pos < 0:
			parts = append(parts, fmt.Sprintf("%s had no effect", p.String()))
		case pos == 0:
			parts = append(parts, fmt.Sprintf("%s took effect first", p.String()))
		default:
			parts = append(parts, fmt.Sprintf("%s took effect after step %d (%s)", p.String(), pos, tr.Steps[pos-1].String()))
		}
	}
	return strings.Join(parts, "; ")
}

// resolution searches the placements of one actor's pending calls for one
// the model accepts.
func resolution(t testing.TB, ch *validate.Checker, tr *qtrace.Trace, settles []qtrace.Step, budget int) ([]qtrace.Step, qtrace.Issue, bool) {
	t.Helper()
	a := tr.Actor
	if a == "" {
		a = "default"
	}
	pls, cut := resolutions(tr, settles, budget)
	orders := make([][]qtrace.Step, len(pls))
	for i, pl := range pls {
		// each candidate on one thread of its own, as the run's steps will be
		for j, s := range apply(tr, pl) {
			s.Recorder, s.Thread, s.Seq = "resolving:"+tr.Actor, "resolving", uint64(j+1)
			orders[i] = append(orders[i], s)
		}
	}
	reps, errs := ch.CheckBatch(context.Background(), settledAs(tr, orders), 0)
	best, bestAt := 0, -2
	var firstErr error
	for i := range orders {
		if errs[i] != nil {
			// a resolution the binding cannot replay (a parameter read from an
			// observation the assumed step lacks): not one to take
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		at := failedAt(reps[i], len(orders[i]))
		if at < 0 {
			return apply(tr, pls[i]), qtrace.Issue{Severity: "info", Kind: "resolve", Msg: fmt.Sprintf("actor %s: %d pending calls, resolved (%d resolutions tried): %s",
				a, len(tr.Pending), i+1, describe(tr, pls[i]))}, false
		}
		if at > bestAt {
			best, bestAt = i, at
		}
	}
	if bestAt == -2 && firstErr != nil {
		t.Fatalf("gotogether: resolving actor %s's pending calls: no resolution can be replayed: %v", a, firstErr)
	}
	if cut {
		return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conformed in the %d of the budget: "+
			"no verdict (conformance n/a); the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best]))}, true
	}
	return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conforms (%d tried): "+
		"whatever they did, the run is no behaviour of the model; the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best]))}, false
}
