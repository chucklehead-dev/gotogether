package validate

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// DefaultLinearizeBudget is how many orders a linearization checks per
// actor (per partition with a Key) at most, unless the options say
// otherwise: as many again for the complete search after the guided one.
const DefaultLinearizeBudget = 200

// DefaultResolveBudget is how many resolutions of an actor's pending calls
// a search checks at most.
const DefaultResolveBudget = 200

// SearchOptions choose the search that orders a run's steps before they
// are checked: Linearize (real time's orders, searched for one the model
// accepts), by Key (each value's history alone), or ResolvePending (what
// each call left pending may have done). Linearize and ResolvePending are
// not combined.
type SearchOptions struct {
	Linearize bool
	Key       string // partition each actor's history by this argument
	Budget    int    // orders per actor or partition (0: DefaultLinearizeBudget)
	// ResolvePending searches the pending calls' effects, in recorded order.
	ResolvePending bool
	ResolveBudget  int // 0: DefaultResolveBudget
}

// A Search is a run's steps as a search ordered them, for Check, and what
// the search found, for the report (Mark).
type Search struct {
	// Steps are the order to check: each actor's (or partition's) on one
	// thread of its own, its pending calls and settle after it.
	Steps     []qtrace.Step
	issues    []qtrace.Issue
	undecided map[string]bool
	kept      map[string][]qtrace.Issue
}

// Search orders steps by opt's search, checking candidates with c's
// binding and instance (verdicts only, under c's Dir). An error is the
// search's (a step without the Key's argument, a candidate quint could
// not check), never a verdict.
func (c *Checker) Search(steps []qtrace.Step, opt SearchOptions) (s *Search, err error) {
	if opt.Linearize && opt.ResolvePending {
		return nil, fmt.Errorf("ResolvePending with Linearize: a resolution keeps the recorded order, and the two searches are not combined; " +
			"record the steps where they take effect, or resolve the pending calls of a history in its recorded order")
	}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(searchError)
			if !ok {
				panic(r)
			}
			s, err = nil, e
		}
	}()
	// the search needs conformance only: without invariants each batch is
	// one quint process, not two
	lc, err := c.WithInvariants()
	if err != nil {
		return nil, err
	}
	s = &Search{}
	if opt.Linearize {
		lc.Options.Dir, lc.Options.NoITF, lc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "linearize"), true, true
		fc := *c // the invariants' search (an order that violates one): verdicts only too
		fc.Options.Dir, fc.Options.NoITF, fc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "linearize-invariants"), true, true
		budget := opt.Budget
		if budget == 0 {
			budget = DefaultLinearizeBudget
		}
		s.Steps, s.issues, s.undecided, s.kept = linearize(lc, &fc, steps, budget, opt.Key)
		return s, nil
	}
	if opt.ResolvePending {
		lc.Options.Dir, lc.Options.NoITF, lc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "resolve"), true, true
		budget := opt.ResolveBudget
		if budget == 0 {
			budget = DefaultResolveBudget
		}
		s.Steps, s.issues, s.undecided, s.kept = resolve(lc, steps, budget)
		return s, nil
	}
	s.Steps = steps
	return s, nil
}

// Mark puts what the search found in rep, the report of its Steps: its
// issues, the issues of each actor's steps as recorded that no order
// removes (gaps, cycles), and no verdict for an actor whose search ran out
// of its budget (its conformance and failed invariants n/a: the order
// shown is the one that got furthest).
func (s *Search) Mark(rep *Report) {
	rep.Issues = append(rep.Issues, s.issues...)
	for _, a := range rep.Actors {
		a.Issues = append(a.Issues, s.kept[a.Actor]...)
		if !s.undecided[a.Actor] {
			continue
		}
		// the search ran out of budget: the order shown is no verdict, for
		// its conformance nor for what it violated (an order that conformed
		// step by step but failed an eventual val was still a candidate the
		// search rejected)
		a.Conforms = NotEvaluated
		for i := range a.Invariants {
			if a.Invariants[i].Verdict == Fail {
				a.Invariants[i].Verdict, a.Invariants[i].Failure = NotEvaluated, nil
			}
		}
	}
}

// searchError is a search's failure, carried out of its callbacks.
type searchError struct{ error }

// fail ends a search with an error (Search recovers it).
func fail(format string, a ...any) { panic(searchError{fmt.Errorf(format, a...)}) }

// linearize orders each actor's steps by the search, checking candidate
// orders with ch in batches, at most budget per actor. With key, each
// actor's history is partitioned by that argument's value first, and
// each partition searched alone, as its own actor ("acct key=a"): the
// orders of independent keys multiply, a partition's are its own.
// undecided are the actors whose search ran out of the budget. kept are
// the issues of each actor's steps as recorded that no order removes (a
// gap: steps were lost; a cycle: their constraints contradict each
// other), for its result: the order checked is re-sequenced onto one
// thread, where neither shows.
func linearize(ch, full *Checker, steps []qtrace.Step, budget int, key string) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue) {
	traces, issues := qtrace.Reconstruct(steps) // deduplicated, gaps found across actors
	undecided, kept = map[string]bool{}, map[string][]qtrace.Issue{}
	for _, tr := range traces {
		for _, part := range partitions(tr, key) {
			actor := part.Actor // as the report names it ("" for no actor)
			for _, i := range tr.Issues {
				if i.Kind == "gap" || i.Kind == "cycle" {
					kept[actor] = append(kept[actor], i)
				}
			}
			o, issue, cut := order(ch, full, part, budget, key != "")
			issues = append(issues, issue)
			if unbounded(part.Steps) {
				issues = append(issues, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: real time bounds none of its %d steps: "+
					"no step has an invocation (qobs.Invoke, then Call.Done) or an \"invoked\" time, so every order of its threads is allowed "+
					"(sequential consistency, not linearizability)", nameOf(actor), len(part.Steps))})
			}
			if cut {
				undecided[actor] = true
			}
			out = append(out, o...)
			// what is no step goes on after the order, on its recorder: the
			// calls never completed, then the settle, if the run recorded one
			rec := len(o)
			if len(o) > 0 {
				rec = int(o[len(o)-1].Seq)
			}
			after := func(s qtrace.Step) {
				rec++
				s.Recorder, s.Thread, s.Seq, s.Actor = "linearized:"+actor, "linearized", uint64(rec), actor
				out = append(out, s)
			}
			for _, p := range part.Pending {
				after(p)
			}
			if part.Settled {
				after(qtrace.Step{Action: "settled", Settled: true, Time: lastSettle(steps)})
			}
		}
	}
	return out, issues, undecided, kept
}

// partitions splits a trace by the value of argument key, in order of
// first appearance, each named "<actor> <key>=<value>" (key "": the
// trace whole). A step without the argument cannot be put in one: it
// fails the test, as partitioning would be unsound.
func partitions(tr *qtrace.Trace, key string) []*qtrace.Trace {
	if key == "" {
		return []*qtrace.Trace{tr}
	}
	var out []*qtrace.Trace
	by := map[string]*qtrace.Trace{}
	of := func(s qtrace.Step) *qtrace.Trace {
		v, ok := s.Args[key]
		if !ok {
			fail("LinearizeByKey(%q): step %s has no %s argument: partition only a history whose every step names one key "+
				"(a step over several keys, a scan, makes them dependent: use Linearize)", key, s.String(), key)
		}
		actor := tr.Actor
		if actor == "" {
			actor = "default" // as a step log's steps name it
		}
		name := fmt.Sprintf("%s %s=%v", actor, key, v)
		id := fmt.Sprintf("%T:%v", v, v) // 1 and "1" are two keys
		p := by[id]
		if p == nil {
			if slices.ContainsFunc(out, func(o *qtrace.Trace) bool { return o.Actor == name }) {
				name += fmt.Sprintf(" (%T)", v)
			}
			p = &qtrace.Trace{Actor: name, Settled: tr.Settled}
			by[id] = p
			out = append(out, p)
		}
		return p
	}
	for _, s := range tr.Steps {
		p := of(s)
		s.Actor = p.Actor
		p.Steps = append(p.Steps, s)
	}
	for _, s := range tr.Pending {
		p := of(s)
		s.Actor = p.Actor
		p.Pending = append(p.Pending, s)
	}
	return out
}

// order finds an order of one actor's steps the model accepts, within
// budget, and says how it went: an issue, and whether the search was cut
// short (no verdict).
func order(ch, full *Checker, tr *qtrace.Trace, budget int, partitioned bool) ([]qtrace.Step, qtrace.Issue, bool) {
	a := tr.Actor // as messages name it
	if a == "" {
		a = "default"
	}
	checker := func(ch *Checker, invariants bool) func(orders [][]qtrace.Step) []int {
		return func(orders [][]qtrace.Step) []int {
			reps, errs := ch.CheckBatch(context.Background(), settledAs(tr, orders), 0)
			fs := make([]int, len(orders))
			for i := range orders {
				if errs[i] != nil {
					fail("linearizing actor %s: %v", a, errs[i])
				}
				fs[i] = failedAt(reps[i], len(orders[i]))
				if invariants && fs[i] < 0 {
					fs[i] = violatedAt(reps[i], len(orders[i]))
				}
			}
			return fs
		}
	}
	check := checker(ch, false)
	if len(tr.Steps) == 0 {
		return nil, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no steps to order", a)}, false
	}
	o, ok, tried, cut := qtrace.Linearize(tr.Steps, budget, check)
	switch {
	case ok && len(full.Binding.Invariants) > 0 && checker(full, true)([][]qtrace.Step{o})[0] >= 0:
		// the order found violates an invariant: another that conforms may
		// not, and a run must not look violating by the order picked
		o2, ok2, tried2, cut2 := qtrace.Linearize(tr.Steps, budget, checker(full, true))
		switch {
		case ok2:
			return o2, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms with every invariant holding (%d orders tried, %d with the invariants)", a, len(o2), tried, tried2)}, false
		case cut2:
			// another order may keep them: no verdict for the invariants
			return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried), and the search for one in which every invariant holds ran out of its budget (%d orders): "+
				"no verdict for the invariants. Raise the budget (qtest.LinearizeBudget(%d))", a, len(o), tried, tried2, 4*budget)}, true
		}
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried); none of the orders real time allows keeps every invariant (%d searched)", a, len(o), tried, tried2)}, false
	case ok:
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried)", a, len(o), tried)}, false
	case cut:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps conformed in the %d orders of the budget, and the search had moves left: "+
			"no verdict (conformance n/a). Raise the budget (qtest.LinearizeBudget(%d)), %sor record its steps where they take effect", a, len(o), tried, 4*budget, partitionHint(partitioned))}, true
	default:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps that real time allows conformed in %d orders tried; "+
			"the order shown got furthest. A step the model rejects in every order is a bug (a stale read, a lost write), unless the invocation times are wrong", a, len(o), tried)}, false
	}
}

// failedAt is the index of the first step of a one-actor order the model
// rejects, or -1 when it conforms (an invariant it violates is not the
// order's fault). An eventual val false at the end is the order's: the
// last write a probe saw last may be the one an order put first, and
// another order of the same steps ends as the probe saw, so it fails at
// its last step and the search goes on.
func failedAt(rep *Report, n int) int {
	for _, a := range rep.Actors {
		if a.Conforms == Pass {
			for _, inv := range a.Invariants {
				if inv.Verdict == Fail && inv.Eventually {
					return n - 1
				}
			}
			continue
		}
		if a.Failure != nil && a.Failure.Step != nil && a.Failure.Step.Seq >= 1 {
			return int(a.Failure.Step.Seq) - 1 // the order's position: re-sequenced from 1
		}
		return n - 1
	}
	return -1
}

// unbounded reports steps of which none has a real-time bound: no
// invocation record (Call) and no invocation time.
func unbounded(steps []qtrace.Step) bool {
	if len(steps) < 2 {
		return false
	}
	for _, s := range steps {
		if s.Call != 0 || !s.Invoked.IsZero() {
			return false
		}
	}
	return true
}

// nameOf is an actor as messages name it.
func nameOf(actor string) string {
	if actor == "" {
		return "default"
	}
	return actor
}

// violatedAt is the index of the step of a conforming order after which
// an invariant first fails, or -1 when every one holds.
func violatedAt(rep *Report, n int) int {
	at := -1
	for _, a := range rep.Actors {
		for _, inv := range a.Invariants {
			if inv.Verdict != Fail || inv.Eventually {
				continue
			}
			i := n - 1
			if inv.Failure != nil && inv.Failure.Step != nil && inv.Failure.Step.Seq >= 1 {
				i = int(inv.Failure.Step.Seq) - 1
			}
			if at < 0 || i < at {
				at = i
			}
		}
	}
	return at
}

// partitionHint suggests LinearizeByKey to a search not partitioned.
func partitionHint(partitioned bool) string {
	if partitioned {
		return ""
	}
	return "partition independent keys (qtest.LinearizeByKey), "
}

// settledAs gives each candidate order of all the trace's steps the run's
// settle, if it recorded one, after its last step: without it a candidate's eventual vals are not
// judged, and the search would take an order the probe contradicts.
func settledAs(tr *qtrace.Trace, orders [][]qtrace.Step) [][]qtrace.Step {
	if !tr.Settled {
		return orders
	}
	out := make([][]qtrace.Step, len(orders))
	for i, o := range orders {
		if len(o) < len(tr.Steps) { // a prefix of an order (Linearize's complete search): the run has not ended
			out[i] = o
			continue
		}
		s := qtrace.Step{Action: "settled", Settled: true, Actor: tr.Actor, Seq: uint64(len(o) + 1)}
		if len(o) > 0 {
			s.Recorder, s.Process = o[len(o)-1].Recorder, o[len(o)-1].Process
		}
		out[i] = append(slices.Clip(o), s)
	}
	return out
}

// resolve replaces each actor's steps by a resolution of its pending
// calls the model accepts (checked with ch, conformance only), within
// budget per actor, re-sequenced onto one thread per actor. It returns the
// steps, an issue per actor with pending calls, the actors whose search
// was cut short, and the issues each actor's steps had as recorded that no
// resolution removes (gaps, cycles).
func resolve(ch *Checker, steps []qtrace.Step, budget int) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue) {
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
			chosen, issue, cut = resolution(ch, tr, settlesOf(steps), budget)
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
			add(qtrace.Step{Action: "settled", Settled: true, Time: lastSettle(steps)})
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
				// at the settle's instant, on another recorder: before it
				if before(s, st) || s.Recorder != st.Recorder && s.Time.Equal(st.Time) {
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

// lastSettle is the time of steps' last settle record: a settle a search
// re-sequences keeps it, or the other partitions' steps would all be
// later than it, and it would settle nothing.
func lastSettle(steps []qtrace.Step) time.Time {
	var t time.Time
	for _, s := range steps {
		if s.Settled && s.Time.After(t) {
			t = s.Time
		}
	}
	return t
}

// settlesOf are the settle records of steps at which the system was
// quiet, earliest first: as Reconstruct takes a settle, one no other
// recorder recorded a step after (a settle that settles nothing bounds
// nothing).
func settlesOf(steps []qtrace.Step) []qtrace.Step {
	var out []qtrace.Step
	for _, s := range steps {
		if !s.Settled {
			continue
		}
		quiet := true
		for _, o := range steps {
			if o.Recorder != s.Recorder && !o.Settled && o.Time.After(s.Time.Add(qtrace.RealTimeSlack)) {
				quiet = false
				break
			}
		}
		if quiet {
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
func resolution(ch *Checker, tr *qtrace.Trace, settles []qtrace.Step, budget int) ([]qtrace.Step, qtrace.Issue, bool) {
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
		fail("resolving actor %s's pending calls: no resolution can be replayed: %v", a, firstErr)
	}
	if cut {
		return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conformed in the %d of the budget: "+
			"no verdict (conformance n/a); the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best]))}, true
	}
	return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conforms (%d tried): "+
		"whatever they did, the run is no behaviour of the model; the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best]))}, false
}
