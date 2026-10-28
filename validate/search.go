package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// DefaultLinearizeBudget is how many orders a linearization checks per
// actor (per partition with a Key) at most, unless the options say
// otherwise: as many again for the complete search after the guided one.
const DefaultLinearizeBudget = qtrace.DefaultLinearizeBudget

// DefaultResolveBudget is how many resolutions of an actor's pending calls
// a search checks at most.
const DefaultResolveBudget = 200

// SearchOptions choose the search that orders a run's steps before they
// are checked: Linearize (real time's orders, searched for one the model
// accepts), by Key (each value's history alone), or ResolvePending (what
// each call left pending may have done). With both, a pending call may
// take effect anywhere real time allows in the order searched, or not at
// all (qtrace.Linearize's Pending: Jepsen's indeterminate calls).
type SearchOptions struct {
	Linearize bool
	Key       string // partition each actor's history by this argument
	Budget    int    // orders per actor or partition (0: DefaultLinearizeBudget)
	// ResolvePending searches the pending calls' effects, in recorded order.
	ResolvePending bool
	ResolveBudget  int // 0: DefaultResolveBudget
	// Skew is how far apart two recorders' clocks may be when real time
	// orders their steps (0: qtrace.RealTimeSlack, 1 ms): a verdict that
	// rests on such an order says so, and holds only within it.
	Skew time.Duration
}

// A search is a run's steps as a search ordered them, for Check, and what
// the search found, for the report (mark).
type search struct {
	// Steps are the order to check: each actor's (or partition's) on one
	// thread of its own, its pending calls and settle after it.
	Steps     []qtrace.Step
	issues    []qtrace.Issue
	undecided map[string]bool
	kept      map[string][]qtrace.Issue
	// intervals are the actors whose own steps are a client's history
	// (they carry invocation times or records), when no search ran: a
	// failing one's report suggests Linearize. Another actor's do not
	// make the hint apply to it (a store's commits beside its clients'
	// calls, each side its own binding's)
	intervals map[string]bool
	// claims are the orders the Go evaluator found that quint has not
	// confirmed, by actor (byCheck); again searches with quint alone
	claims map[string]*claim
	again  func() (*search, error)
	// lost are each actor's (or partition's) losses as recorded
	// (qtrace.Trace.Lost), which its re-sequenced order no longer shows:
	// mark judges the check's verdict by them (judgeLost)
	lost map[string][]qtrace.Lost
	// gaps and collected are each actor's (or partition's) gaps and
	// watermarks as recorded (qtrace.Trace.Gaps, Collected), which the
	// order no longer shows either: mark diagnoses its absent steps by
	// them (judgeAbsent). searched: a search ran (else the check judged
	// them on the steps as recorded)
	gaps      map[string][]qtrace.Gap
	collected map[string][]qtrace.Watermark
	// pending are each actor's (or partition's) pending calls as
	// recorded, with the recorder and seq the order re-sequences
	pending map[string][]qtrace.Step
	// recorded are each actor's (or partition's) steps as recorded, which
	// the order re-sequences: mark places a failure among its gaps by
	// them (judgeGaps)
	recorded map[string][]qtrace.Step
	searched bool
	binding  *binding.Binding
	// overlaps are, by actor (or partition), the steps a linearization
	// kept after the snapshot it starts from though real time lets them
	// precede it: their effects may be in the snapshot's state, which no
	// order can say, so a failure is no verdict (mark)
	overlaps map[string][]string
}

// A claim is what a search took from the Go evaluator alone: an order of
// an actor's (or partition's) steps that conforms, and with invariants
// keeps them, in the instance of tr; and the orders it rejected before
// it, where the search takes the first that conforms (a resolution: the
// run's invariants are judged on the one it takes, so quint's must be
// it).
type claim struct {
	ch         *Checker
	tr         *qtrace.Trace
	order      []qtrace.Step
	invariants bool
	rejected   [][]qtrace.Step
}

// confirm confirms, after the check of a byCheck search's Steps, the
// claims the Go evaluator made for the actors rep does not pass (an actor that passes every check confirmed its order there): each
// order is checked by quint (conformance, eventual vals, and invariants
// when the claim is that they hold), with the orders a resolution
// rejected before it, side by side. When quint disagrees with one, it
// returns the search again with quint alone, its Steps to check instead
// (and its report to mark), with a warning that says so; nil when every
// claim stands. rep may be marked already (an actor the search left
// undecided is confirmed whatever its marked verdicts). It confirms once:
// a second call returns nil. Judge calls it: a caller that did not would
// take the evaluator's orders unconfirmed.
func (s *search) confirm(rep *Report) (again *search, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(searchError)
			if !ok {
				panic(r)
			}
			again, err = nil, e
		}
	}()
	var todo []*claim
	var names []string
	for _, a := range rep.Actors {
		cl := s.claims[a.Actor]
		if cl == nil || !s.undecided[a.Actor] && passed(a) {
			continue
		}
		todo = append(todo, cl)
		names = append(names, nameOf(a.Actor))
	}
	s.claims = nil
	wrong := make([]bool, len(todo))
	var wg sync.WaitGroup
	panics := make([]any, len(todo))
	for i, cl := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { panics[i] = recover() }()
			fs, _ := quintChecker(cl.ch, cl.tr, names[i], cl.invariants)(append([][]qtrace.Step{cl.order}, cl.rejected...))
			wrong[i] = fs[0] >= 0
			for _, f := range fs[1:] {
				wrong[i] = wrong[i] || f < 0
			}
		}()
	}
	wg.Wait()
	var contradicted []string
	for i := range todo {
		if panics[i] != nil {
			panic(panics[i])
		}
		if wrong[i] {
			contradicted = append(contradicted, names[i])
		}
	}
	if len(contradicted) == 0 {
		return nil, nil
	}
	searchedAgain.Add(1)
	again, err = s.again()
	if err != nil {
		return nil, err
	}
	again.issues = append(again.issues, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("the Go evaluator's order of actor %s's steps does not conform for quint: "+
		"searched again with quint alone (GOTOGETHER_EVAL=quint checks with quint alone)", strings.Join(contradicted, ", "))})
	return again, nil
}

// passed reports whether a's check failed nothing: its conformance, an
// invariant or an eventual val. A claim speaks to its conformance and
// eventual vals, with invariants to them too, and a resolution's choice
// decides which order its invariants are judged on: any failure, and
// its claim is checked.
func passed(a *Result) bool {
	if a.Conforms == Fail {
		return false
	}
	for _, inv := range a.Invariants {
		if inv.Verdict == Fail {
			return false
		}
	}
	return true
}

// search orders steps by opt's search, checking candidates with c's
// binding and instance (verdicts only, under c's Dir). With byCheck an
// order (or a resolution) the Go evaluator found is left to the check of
// Steps, which replays it with quint anyway, instead of a quint process
// of its own: confirm then checks the evaluator's orders of the actors
// that check fails with quint, and searches again with quint alone if
// one does not conform. An error is the search's (a step without the
// Key's argument, a candidate quint could not check), never a verdict.
func (c *Checker) search(steps []qtrace.Step, opt SearchOptions, byCheck bool) (s *search, err error) {
	if len(c.Binding.Hidden) > 0 && (opt.Linearize || opt.ResolvePending) {
		return nil, fmt.Errorf("a search (Linearize, ResolvePending) with hidden actions (%s): not combined, each candidate would be a search over the hidden actions' states; check the recorded order, or record the steps the search would order where they take effect",
			strings.Join(c.Binding.Hidden, ", "))
	}
	if c.Binding.Partition != "" && (opt.Linearize || opt.ResolvePending) {
		return nil, fmt.Errorf("a search (Linearize, ResolvePending) of a binding that partitions the log (partition: %s): not combined, a partition being a serial log's; "+
			"a client history is partitioned by the search's key (-linearize-key, qtest.LinearizeByKey)", c.Binding.Partition)
	}
	if opt.Linearize || opt.ResolvePending {
		traces, _ := reconstruct(c.Binding, steps)
		for _, tr := range traces {
			if c.resumes(tr) {
				return nil, fmt.Errorf("a search (Linearize, ResolvePending) of actor %s, which lost its head and resumes from the states %s admits: not combined, each candidate would be a search over those states; "+
					"record a snapshot step at each log's head (snapshot: restore), which a search keeps first", nameOf(tr.Actor), c.Binding.Resume)
			}
		}
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
	lc.mode.byCheck, lc.mode.skew = byCheck, opt.Skew
	s = &search{again: func() (*search, error) {
		q := *c
		q.mode.quintOnly = true
		return q.search(steps, opt, false)
	}}
	if opt.Linearize {
		// its ITF on: a conforming candidate's last state lets the complete
		// search merge prefixes of one state (qtrace.Linearize)
		lc.Options.Dir, lc.Options.NoITF, lc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "linearize"), false, true
		fc := *c // the invariants' search (an order that violates one): verdicts only too
		fc.mode.byCheck, fc.mode.skew = byCheck, opt.Skew
		fc.Options.Dir, fc.Options.NoITF, fc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "linearize-invariants"), true, true
		budget := opt.Budget
		if budget == 0 {
			budget = DefaultLinearizeBudget
		}
		s.Steps, s.issues, s.undecided, s.kept, s.claims = linearize(lc, &fc, steps, budget, opt.Key, opt.ResolvePending)
		s.lost, s.binding = lostOf(c.Binding, steps, opt.Key), c.Binding
		s.gaps, s.collected, s.pending, s.recorded, s.searched = recordedOf(c.Binding, steps, opt.Key)
		s.overlaps = overlapsOf(c.Binding, steps, opt.Key)
		return s, nil
	}
	if opt.ResolvePending {
		lc.Options.Dir, lc.Options.NoITF, lc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "resolve"), true, true
		budget := opt.ResolveBudget
		if budget == 0 {
			budget = DefaultResolveBudget
		}
		fc := *c // the invariants' search (a resolution that violates one): verdicts only too
		fc.mode.byCheck = byCheck
		fc.Options.Dir, fc.Options.NoITF, fc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "resolve-invariants"), true, true
		s.Steps, s.issues, s.undecided, s.kept, s.claims = resolve(lc, &fc, steps, budget)
		s.lost, s.binding = lostOf(c.Binding, steps, ""), c.Binding
		s.gaps, s.collected, s.pending, s.recorded, s.searched = recordedOf(c.Binding, steps, "")
		return s, nil
	}
	s.Steps, s.intervals = steps, intervalActors(c.Binding, steps)
	return s, nil
}

// lostOf is each actor's losses (qtrace.Trace.Lost), by the name a
// search's order gives it: with a key, every partition of the actor has
// them all (whose the lost steps were is unknown).
func lostOf(b *binding.Binding, steps []qtrace.Step, key string) map[string][]qtrace.Lost {
	traces, _ := reconstruct(b, steps)
	out := map[string][]qtrace.Lost{}
	for _, tr := range traces {
		if len(tr.Lost) == 0 {
			continue
		}
		for _, p := range partitions(tr, key, snapshotFirst(b, tr)) { // as the search split it, which it did
			out[p.Actor] = tr.Lost
		}
	}
	return out
}

// recordedOf is each actor's gaps, watermarks, pending calls and steps
// (qtrace.Trace.Gaps, Collected, Pending, Steps), by the name a search's
// order gives it, as lostOf its losses; and true, for search.searched.
func recordedOf(b *binding.Binding, steps []qtrace.Step, key string) (map[string][]qtrace.Gap, map[string][]qtrace.Watermark, map[string][]qtrace.Step, map[string][]qtrace.Step, bool) {
	traces, _ := reconstruct(b, steps)
	gaps, marks, pending, recorded := map[string][]qtrace.Gap{}, map[string][]qtrace.Watermark{}, map[string][]qtrace.Step{}, map[string][]qtrace.Step{}
	for _, tr := range traces {
		for _, p := range partitions(tr, key, snapshotFirst(b, tr)) {
			gaps[p.Actor], marks[p.Actor], pending[p.Actor], recorded[p.Actor] = tr.Gaps, tr.Collected, p.Pending, p.Steps
		}
	}
	return gaps, marks, pending, recorded, true
}

// pendingGap is the gap of the steps as recorded that may hold a pending
// call of actor's order (ownGap of its original: the call recorded at
// the same time, of the same action and arguments). One with no original
// found takes any gap of the actor's, as a lost tail is taken.
func (s *search) pendingGap(actor string) func(qtrace.Step) (qtrace.Gap, bool) {
	own := ownGap(s.gaps[actor])
	return func(p qtrace.Step) (qtrace.Gap, bool) {
		for _, o := range s.pending[actor] {
			if o.Action == p.Action && o.Time.Equal(p.Time) && o.String() == p.String() {
				return own(o)
			}
		}
		if g := s.gaps[actor]; len(g) > 0 {
			return g[0], true
		}
		return qtrace.Gap{}, false
	}
}

// carried is the watermark record a search's order carries for actor
// tr, on its own recorder rec, if its recorders stated any: tr's
// CompleteThrough as it is, whole if some recorder stated none
// (qtrace.CarryWatermark). The order's check then judges a window as the
// steps as recorded were judged: by the watermark, and with a whole log
// among them by the trace's own end too, which the order's invocation
// times keep. Without it the order's one recorder would be a log taken
// whole, and judge an uncollected window absent.
func carried(tr *qtrace.Trace, rec string) (qtrace.Step, bool) {
	through, every := tr.CompleteThrough()
	if through.IsZero() {
		return qtrace.Step{}, false
	}
	return qtrace.CarryWatermark(rec, tr.Actor, through, !every), true
}

// overlapsOf are, by the name a linearization gives each actor (or
// partition) that starts from a snapshot step, the steps real time lets
// precede it: invoked before it completed (by their invocation record on
// its recorder, else by time, give or take the slack). A step whose
// invocation is unknown is taken to follow it, as recorded.
func overlapsOf(b *binding.Binding, steps []qtrace.Step, key string) map[string][]string {
	traces, _ := reconstruct(b, steps)
	out := map[string][]string{}
	for _, tr := range traces {
		if !snapshotFirst(b, tr) {
			continue
		}
		for _, p := range partitions(tr, key, true) {
			snap := p.Steps[0]
			for _, s := range p.Steps[1:] {
				var follows bool
				switch {
				case s.Recorder == snap.Recorder && s.Call != 0 && snap.Seq != 0:
					follows = snap.Seq < s.Call
				case !s.Invoked.IsZero():
					follows = snap.Time.Add(qtrace.RealTimeSlack).Before(s.Invoked)
				default:
					follows = true
				}
				if !follows {
					out[p.Actor] = append(out[p.Actor], fmt.Sprintf("%s (seq %d of recorder %s)", s.String(), s.Seq, s.Recorder))
				}
			}
		}
	}
	return out
}

// judgeOverlaps makes a's failure no verdict when steps overlapped the
// snapshot its order starts from (overlapsOf): each may have taken
// effect before the snapshot was taken, its effect in the state it
// recorded, and replayed after it, applied twice. An order that conforms
// with them after it stands: it is one real time allows.
func judgeOverlaps(a *Result, overlaps []string) {
	if len(overlaps) == 0 || passed(a) {
		return
	}
	a.noVerdict = "snapshot overlap"
	if a.Conforms == Fail {
		a.Conforms = NotEvaluated
	}
	for i := range a.Invariants {
		if a.Invariants[i].Verdict == Fail {
			a.Invariants[i].Verdict = NotEvaluated
		}
	}
	a.Issues = append(a.Issues, qtrace.Issue{Severity: "warn", Kind: "snapshot", Msg: fmt.Sprintf(
		"no verdict: %s overlapped the snapshot in real time, so its effect may be in the state the snapshot recorded, and replayed after it, twice; "+
			"record the snapshot when no operation is in flight (under the lock that rotates the log)", strings.Join(overlaps, ", "))})
}

// intervalActors are the actors of steps that b checks whose own steps
// (or pending calls) carry invocation times or records with no domain
// clock: a client's history, in the order its calls returned. A step a
// domain clock orders (order.*: a store's or a processor's, placed where
// it took effect) is checked in that order, whatever its interval, so
// no order real time allows is the hint's to suggest.
func intervalActors(b *binding.Binding, steps []qtrace.Step) map[string]bool {
	out := map[string]bool{}
	traces, _ := reconstruct(b, steps)
	for _, t := range traces {
		var free []qtrace.Step
		for _, s := range append(slices.Clip(t.Steps), t.Pending...) {
			if len(s.Order) == 0 {
				free = append(free, s)
			}
		}
		if intervals(free) {
			out[t.Actor] = true
		}
	}
	return out
}

// intervals reports whether steps carry invocation times or records: a
// client's history, recorded where calls returned.
func intervals(steps []qtrace.Step) bool {
	for _, s := range steps {
		if s.Invocation || s.Call != 0 || !s.Invoked.IsZero() {
			return true
		}
	}
	return false
}

// mark puts what the search found in rep, the report of its Steps: its
// issues (with no search, for a failing client history, a hint to
// linearize), the issues of each actor's steps as recorded that no order
// removes (gaps, cycles), and no verdict for an actor whose search ran out
// of its budget (its conformance and failed invariants n/a: the order
// shown is the one that got furthest).
func (s *search) mark(rep *Report) {
	rep.Issues = append(rep.Issues, s.issues...)
	for _, a := range rep.Actors {
		if s.intervals[a.Actor] && a.Conforms == Fail {
			a.Issues = append(a.Issues, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: "its steps carry invocation times, a client's history: " +
				"calls that overlapped return in another order than they took effect, which the recorded order checks; if they were recorded where they returned, " +
				"check in an order real time allows (qtest.Linearize(), gotogether validate -linearize)"})
		}
		a.Issues = append(a.Issues, s.kept[a.Actor]...)
		if s.undecided[a.Actor] {
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
		// what a lost tail or a step overlapping the snapshot leaves
		// unjudged, of what the budget did not
		judgeLost(s.binding, a, s.lost[a.Actor])
		judgeOverlaps(a, s.overlaps[a.Actor])
		if s.searched {
			a.Collected = s.collected[a.Actor]
			judgeAbsent(s.binding, a, s.lost[a.Actor], s.gaps[a.Actor], s.pendingGap(a.Actor), s.recorded[a.Actor])
		}
	}
}

// searchError is a search's failure, carried out of its callbacks.
type searchError struct{ error }

// fail ends a search with an error (search recovers it).
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
func linearize(ch, full *Checker, steps []qtrace.Step, budget int, key string, resolving bool) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue, claims map[string]*claim) {
	traces, issues := reconstruct(ch.Binding, steps) // deduplicated, gaps found across actors
	undecided, kept, claims = map[string]bool{}, map[string][]qtrace.Issue{}, map[string]*claim{}
	type job struct {
		tr, part *qtrace.Trace
		o        []qtrace.Step
		issue    qtrace.Issue
		cut      bool
		claim    *claim
		panicked any
	}
	var jobs []*job
	for _, tr := range traces {
		for _, part := range partitions(tr, key, snapshotFirst(ch.Binding, tr)) {
			jobs = append(jobs, &job{tr: tr, part: part})
		}
	}
	// the searches side by side, a directory each: the partitions of a
	// history are independent, and each search is quint processes one
	// after another (a history of two keys took three times one search of
	// both); their results in order
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, j := range jobs {
		jch, jfull := ch, full
		if len(jobs) > 1 {
			jch, jfull = inDir(ch, i), inDir(full, i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() {
				<-sem
				if r := recover(); r != nil {
					j.panicked = r // a searchError, re-raised on the caller's goroutine
				}
			}()
			var pending, settles []qtrace.Step
			if resolving {
				pending, settles = j.part.Pending, settlesOf(steps)
			}
			j.o, j.issue, j.cut, j.claim = order(jch, jfull, j.part, pending, settles, budget, key != "")
		}()
	}
	wg.Wait()
	for _, j := range jobs {
		if j.panicked != nil {
			panic(j.panicked)
		}
	}
	for _, j := range jobs {
		tr, part, o := j.tr, j.part, j.o
		actor := part.Actor // as the report names it ("" for no actor)
		for _, i := range startIssues(ch.Binding, tr) {
			if i.Kind == "gap" || i.Kind == "lost-prefix" || i.Kind == "cycle" {
				kept[actor] = append(kept[actor], i)
			}
		}
		issues = append(issues, j.issue)
		if unbounded(part.Steps) {
			issues = append(issues, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: real time bounds none of its %d steps: "+
				"no step has an invocation (qobs.Invoke, then Call.Done) or an \"invoked\" time, so every order of its threads is allowed "+
				"(sequential consistency, not linearizability)", nameOf(actor), len(part.Steps))})
		}
		if j.cut {
			undecided[actor] = true
		}
		if j.claim != nil {
			claims[actor] = j.claim
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
		if w, ok := carried(tr, "linearized:"+actor); ok {
			w.Actor = actor
			out = append(out, w)
		}
	}
	return out, issues, undecided, kept, claims
}

// inDir is ch writing its modules in a subdirectory of its own for the
// i-th of searches side by side (none if ch has no directory: each batch
// makes a temporary one).
func inDir(ch *Checker, i int) *Checker {
	if ch.Options.Dir == "" {
		return ch
	}
	c := *ch
	c.Options.Dir = filepath.Join(ch.Options.Dir, fmt.Sprintf("part%d", i))
	return &c
}

// partitions splits a trace by the value of argument key, in order of
// first appearance, each named "<actor> <key>=<value>" (key "": the
// trace whole). A step without the argument cannot be put in one: it
// fails the test, as partitioning would be unsound. With snapshot, the
// trace's first step is a snapshot of the whole state, every key's: it
// starts every partition.
func partitions(tr *qtrace.Trace, key string, snapshot bool) []*qtrace.Trace {
	if key == "" {
		return []*qtrace.Trace{tr}
	}
	out, _, missing := splitTrace(tr, key, snapshot)
	if missing != nil {
		fail("LinearizeByKey(%q): step %s has no %s argument: partition only a history whose every step names one key "+
			"(a step over several keys, a scan, makes them dependent: use Linearize)", key, missing.String(), key)
	}
	return out
}

// splitTrace is tr split by the value of argument key, as partitions
// says, and each part's steps' indices in tr.Steps (the snapshot's, 0,
// first in each); missing is a step (or a pending call, index -1) with
// no such argument, and nothing else then.
func splitTrace(tr *qtrace.Trace, key string, snapshot bool) (out []*qtrace.Trace, at [][]int, missing *qtrace.Step) {
	by := map[string]int{}
	names := map[string]bool{} // the parts' names: a lookup, not a scan of the parts (a day's log has a part per charge)
	of := func(s qtrace.Step) int {
		v, ok := s.Args[key]
		if !ok {
			return -1
		}
		actor := tr.Actor
		if actor == "" {
			actor = "default" // as a step log's steps name it
		}
		name := fmt.Sprintf("%s %s=%v", actor, key, v)
		id := fmt.Sprintf("%T:%v", v, v) // 1 and "1" are two keys
		p, ok := by[id]
		if !ok {
			if names[name] {
				name += fmt.Sprintf(" (%T)", v)
			}
			names[name] = true
			p = len(out)
			by[id] = p
			out = append(out, &qtrace.Trace{Actor: name, Settled: tr.Settled})
			at = append(at, nil)
		}
		return p
	}
	first := 0
	if snapshot {
		first = 1
	}
	for i := first; i < len(tr.Steps); i++ {
		s := tr.Steps[i]
		p := of(s)
		if p < 0 {
			return nil, nil, &tr.Steps[i]
		}
		s.Actor = out[p].Actor
		out[p].Steps = append(out[p].Steps, s)
		at[p] = append(at[p], i)
	}
	for i := range tr.Pending {
		s := tr.Pending[i]
		p := of(s)
		if p < 0 {
			return nil, nil, &tr.Pending[i]
		}
		s.Actor = out[p].Actor
		out[p].Pending = append(out[p].Pending, s)
	}
	if snapshot {
		for p, part := range out {
			s := tr.Steps[0]
			s.Actor = part.Actor
			part.Steps = append([]qtrace.Step{s}, part.Steps...)
			at[p] = append([]int{0}, at[p]...)
		}
	}
	return out, at, nil
}

// order finds an order of one actor's steps the model accepts, within
// budget, and says how it went: an issue, and whether the search was cut
// short (no verdict).
func order(ch, full *Checker, tr *qtrace.Trace, pending, settles []qtrace.Step, budget int, partitioned bool) ([]qtrace.Step, qtrace.Issue, bool, *claim) {
	a := tr.Actor // as messages name it
	if a == "" {
		a = "default"
	}
	// candidates are judged by the Go evaluator while it can (quint for the
	// rest of the search once it refuses one), and quint confirms what the
	// search concludes
	ev := newEvalUse(ch, full)
	checker := func(ch *Checker, invariants bool) func(orders [][]qtrace.Step) ([]int, []string) {
		// candidates in the history's instance and naming, as the memo and
		// the merge of prefixes by state assume
		hc := *ch
		hc.mode.history = tr
		hc.memo = newStateMemo() // the candidates' states, for their extensions
		hc.planner = ch.Binding.Planner(ch.Model, tr)
		ch = &hc
		quint := quintChecker(ch, tr, a, invariants)
		return func(orders [][]qtrace.Step) ([]int, []string) {
			if !ev.off {
				fs, states := make([]int, len(orders)), make([]string, len(orders))
				judged := true
				for i, o := range settledAs(tr, orders) {
					f, st, err := ev.e.judge(ch, tr, o, len(orders[i]), invariants)
					if err != nil {
						ev.off, ev.refused, ev.why, judged = true, true, err.Error(), false
						break
					}
					fs[i] = f
					if f < 0 {
						states[i] = withProcess(ch, st, orders[i])
					}
				}
				if judged {
					ev.used = true
					if ev.shadow {
						shadowCheck(a, orders, fs, quint)
					}
					return fs, states
				}
			}
			return quint(orders)
		}
	}
	check := checker(ch, false)
	if len(tr.Steps) == 0 {
		return nil, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no steps to order", a)}, false, nil
	}
	// the search, with the pending calls it places (ResolvePending)
	lin := func(check func([][]qtrace.Step) ([]int, []string)) (found, *qtrace.Proof) {
		// a candidate quint could not check ends the search with its error
		// (fail's searchError), which qtrace returns: raised again here, for
		// search's recover
		l, err := qtrace.Linearize(tr.Steps, qtrace.LinearizeOptions{Budget: budget, Pending: pending, Settles: settles, First: snapshotFirst(ch.Binding, tr), Skew: ch.mode.skew},
			func(orders [][]qtrace.Step) (fs []int, states []string, err error) {
				defer func() {
					if r := recover(); r != nil {
						e, ok := r.(searchError)
						if !ok {
							panic(r)
						}
						err = e
					}
				}()
				fs, states = check(orders)
				return fs, states, nil
			})
		if err != nil {
			panic(err.(searchError))
		}
		return found{l.Order, l.Verdict == qtrace.Linearizable, l.Tried, l.Verdict == qtrace.Undecided, l.Clocks}, l.Proof
	}
	again := func(check func([][]qtrace.Step) ([]int, []string)) func() found {
		return func() found {
			f, _ := lin(check)
			return f
		}
	}
	skew := ch.mode.skew
	if skew <= 0 {
		skew = qtrace.RealTimeSlack
	}
	if o, ok := qtrace.ForcedOrderWithin(tr.Steps, skew); ok && len(pending) == 0 && (!snapshotFirst(ch.Binding, tr) || o[0].Action == ch.Binding.Snapshot) {
		// one order: a search would check it alone, and the caller checks
		// the order it gets (Checker.Check of search.Steps), which judges it
		// as this search would have; two quint processes saved (the probe
		// of every account makes many such partitions)
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: real time allows one order of its %d steps, checked as the history's", a, len(o)) +
			forcedClockNote(qtrace.ClockOrders(tr.Steps, skew))}, false, nil
	}
	f, proof := lin(check)
	f = ev.decide(f, proof, quintChecker(inHistory(ch, tr), tr, a, false), a, again(check))
	cl := ev.claim(inHistory(ch, tr), tr, f.order, false)
	o, issue, cut, cl := orderIssue(ch, full, tr, a, budget, partitioned, checker, ev, lin, again, f, cl)
	if len(pending) > 0 {
		issue.Msg += "; " + effects(o, pending)
	}
	return o, ev.note(issue), cut, cl
}

// newEvalUse is how a search with ch (and full, its invariants') uses
// the Go evaluator: not at all under GOTOGETHER_EVAL=quint or when it
// searches again with quint alone.
func newEvalUse(ch, full *Checker) *evalUse {
	ev := &evalUse{byCheck: ch.mode.byCheck}
	switch {
	case ch.mode.quintOnly:
		ev.off, ev.why = true, "a search again with quint alone"
	case ch.Options.evalMode() == "quint":
		ev.off, ev.why = true, "GOTOGETHER_EVAL=quint"
	default:
		ev.shadow = ch.Options.evalMode() == "shadow"
		if ev.e = evaluatorFor(full); ev.e.err != nil {
			ev.off, ev.unavailable, ev.why = true, true, ev.e.err.Error()
		}
	}
	return ev
}

// claim is the order decide left to the caller's check (byCheck),
// as a claim in ch's instance; nil if quint confirmed it or there is none.
func (ev *evalUse) claim(ch *Checker, tr *qtrace.Trace, o []qtrace.Step, invariants bool) *claim {
	if !ev.pending {
		return nil
	}
	ev.pending = false
	return &claim{ch: ch, tr: tr, order: o, invariants: invariants}
}

// inHistory is ch planning its candidates in tr's instance.
func inHistory(ch *Checker, tr *qtrace.Trace) *Checker {
	c := *ch
	c.mode.history = tr
	return &c
}

// found is a search's outcome: the order it got furthest with, whether
// it conforms, how many orders were tried, and whether the budget cut
// the search short.
type found struct {
	order  []qtrace.Step
	ok     bool
	tried  int
	cut    bool
	clocks qtrace.ClockAssumption // the orders real time took from different recorders' clocks
}

// decide is a search's conclusion as quint confirms it, when the Go
// evaluator judged its candidates: an order found is checked by quint;
// no order found, by its proof in one batch (quint rejects every prefix
// the search dropped as rejected, and gives each pair it merged one
// state), since no one order confirms that every order fails. A
// disagreement searches again with quint alone, and says so. A search cut
// short, or a proof whose states quint did not write, is decided by quint
// alone.
func (ev *evalUse) decide(f found, proof *qtrace.Proof, quint func([][]qtrace.Step) ([]int, []string), a string, again func() found) found {
	o, ok, cut := f.order, f.ok, f.cut
	if !ev.used {
		return f // quint judged every candidate
	}
	ev.used = false
	if ok {
		if ev.byCheck {
			ev.pending = true // the caller's check replays it with quint (search.confirm)
			return f
		}
		if fq := first(quint([][]qtrace.Step{o})); fq >= 0 {
			ev.off, ev.disagreed = true, true
			ev.why = fmt.Sprintf("the Go evaluator and quint disagree on actor %s's order (quint: %s)", a, verdictOf(fq))
			searchedAgain.Add(1)
			return again()
		}
		return f
	}
	if !cut && proof != nil {
		switch confirms(proof, quint) {
		case confirmed:
			return f
		case contradicted:
			ev.off, ev.disagreed = true, true
			ev.why = fmt.Sprintf("quint does not confirm the Go evaluator's verdict that no order of actor %s's steps conforms", a)
			searchedAgain.Add(1)
			return again()
		}
	}
	if cut {
		// no verdict either way: quint's search of the same budget, from the
		// same verdicts, would be cut where this one was, and one that
		// differs (an order the evaluator wrongly rejected) found at best
		// what a larger budget finds; searching again cost minutes for
		// nothing (the shadow mode checks the evaluator's verdicts)
		ev.cut = true
		return f
	}
	off := ev.off
	ev.off = true
	searchedAgain.Add(1)
	f = again()
	ev.off = off
	return f
}

// A proof's confirmation by quint.
const (
	confirmed = iota
	contradicted
	unconfirmed // quint wrote no state for a merged pair
)

// confirms checks proof with quint, in one batch: every rejected prefix
// rejected, every merged pair accepted with one state. (A tampered
// evaluator's proof, the tests' hooks.TamperEval, is contradicted here.)
func confirms(proof *qtrace.Proof, quint func([][]qtrace.Step) ([]int, []string)) int {
	orders := slices.Clone(proof.Rejected)
	for _, m := range proof.Merged {
		orders = append(orders, m[0], m[1])
	}
	if len(orders) == 0 {
		return contradicted // a verdict of no order rests on some rejection
	}
	fs, states := quint(orders)
	for i := range proof.Rejected {
		if fs[i] < 0 {
			return contradicted
		}
	}
	verdict := confirmed
	for j := range proof.Merged {
		x, y := len(proof.Rejected)+2*j, len(proof.Rejected)+2*j+1
		switch {
		case fs[x] >= 0 || fs[y] >= 0:
			return contradicted
		case states[x] == "" || states[y] == "":
			verdict = unconfirmed
		case states[x] != states[y]:
			return contradicted
		}
	}
	return verdict
}

// evalUse is how a search used the Go evaluator.
type evalUse struct {
	e       *evaluator
	off     bool   // quint checks the candidates (no evaluator, or it refused one)
	why     string // why it is off
	used    bool   // it judged candidates of the search being decided
	byCheck bool   // an order it finds is the caller's check to confirm
	shadow  bool   // quint checks every batch it judges too (GOTOGETHER_EVAL=shadow)
	pending bool   // decide left the order it found to the caller's check
	refused bool   // it refused a candidate: the rest of the search was quint's
	// unavailable: the binding has no evaluator (its evaluation module
	// cannot carry a template, or did not compile)
	unavailable bool
	disagreed   bool // quint contradicted its conclusion
	cut         bool // it judged a search cut short: no verdict, not searched again
}

// note is issue with how its candidates were checked, when quint checked
// some for a reason worth saying.
func (ev *evalUse) note(issue qtrace.Issue) qtrace.Issue {
	switch {
	case ev.disagreed:
		issue.Severity = "warn"
		issue.Msg += "; " + ev.why + ": searched again with quint (GOTOGETHER_EVAL=quint checks with quint alone)"
	case ev.unavailable:
		issue.Msg += "; candidates checked by quint, the binding having no Go evaluator (" + ev.why + ")"
	case ev.refused:
		issue.Msg += "; candidates after the evaluator refused one checked by quint (" + ev.why + ")"
	case ev.cut:
		issue.Msg += "; its candidates judged by the Go evaluator (GOTOGETHER_EVAL=quint searches with quint alone)"
	}
	return issue
}

func verdictOf(f int) string {
	if f < 0 {
		return "it conforms"
	}
	return fmt.Sprintf("it fails at step %d", f+1)
}

// withProcess is a conforming candidate's state, with what of its
// processes its extensions depend on beyond the model's state: the order
// they first appear in, which numbers them ({{.process}}, procs: two
// prefixes of one state and other numberings render the next step
// otherwise), and, when the binding infers restarts, the last one (the
// restart it inserts next depends on it).
func withProcess(ch *Checker, state string, order []qtrace.Step) string {
	if state == "" || len(order) == 0 {
		return state
	}
	var procs []string
	for _, s := range order {
		if !slices.Contains(procs, s.Process) {
			procs = append(procs, s.Process)
		}
	}
	state += "|processes=" + strings.Join(procs, ",")
	if ch.Binding.Restart != "" {
		// the last observed step's: a placed pending call infers no restart
		for i := len(order) - 1; i >= 0; i-- {
			if !order[i].Ambiguous {
				state += "|process=" + order[i].Process
				break
			}
		}
	}
	return state
}

// quintChecker checks candidate orders of tr's steps with quint, in
// batches: each one's failure index (with invariants, an invariant's
// violation's too), and a conforming one's last state.
func quintChecker(ch *Checker, tr *qtrace.Trace, a string, invariants bool) func(orders [][]qtrace.Step) ([]int, []string) {
	return func(orders [][]qtrace.Step) ([]int, []string) {
		reps, errs := ch.CheckBatch(ch.mode.context(), settledAs(tr, orders), 0)
		fs, states := make([]int, len(orders)), make([]string, len(orders))
		for i := range orders {
			if errs[i] != nil {
				fail("linearizing actor %s: %v", a, errs[i])
			}
			fs[i] = failedAt(reps[i], len(orders[i]))
			if invariants && fs[i] < 0 {
				fs[i] = violatedAt(reps[i], len(orders[i]))
			}
			if fs[i] < 0 {
				states[i] = withProcess(ch, lastState(reps[i]), orders[i])
			}
		}
		return fs, states
	}
}

// orderIssue is a search's outcome, as order reports it: the order, the
// issue, and whether the search was cut short; with invariants, a second
// search for an order that keeps them.
func orderIssue(ch, full *Checker, tr *qtrace.Trace, a string, budget int, partitioned bool,
	checker func(ch *Checker, invariants bool) func(orders [][]qtrace.Step) ([]int, []string), ev *evalUse,
	lin func(func([][]qtrace.Step) ([]int, []string)) (found, *qtrace.Proof),
	again func(func([][]qtrace.Step) ([]int, []string)) func() found,
	f found, cl *claim) ([]qtrace.Step, qtrace.Issue, bool, *claim) {
	o, ok, tried, cut := f.order, f.ok, f.tried, f.cut
	// with ITF (the invariants' searches write none): a proof's merged
	// prefixes are confirmed by their states
	inst := inHistory(full, tr)
	inst.Options.NoITF = false
	quintInv := quintChecker(inst, tr, a, true)
	violates := false
	if ok && len(full.Binding.Invariants) > 0 {
		if violates = first(quintInv([][]qtrace.Step{o})) >= 0; !violates {
			cl = nil // quint checked the order, conformance and invariants both
		}
	}
	switch {
	case violates:
		// the order found violates an invariant (quint says, one check): another that conforms may
		// not, and a run must not look violating by the order picked
		inv := checker(full, true)
		ev.used = false
		f2, proof2 := lin(inv)
		f2 = ev.decide(f2, proof2, quintInv, a, again(inv))
		o2, ok2, tried2, cut2 := f2.order, f2.ok, f2.tried, f2.cut
		switch {
		case ok2:
			return o2, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms with every invariant holding (%d orders tried, %d with the invariants)", a, len(o2), tried, tried2)}, false,
				ev.claim(inst, tr, o2, true)
		case cut2:
			// another order may keep them: no verdict for the invariants
			return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried), and the search for one in which every invariant holds ran out of its budget (%d orders): "+
				"no verdict for the invariants. Raise the budget (qtest.LinearizeBudget(%d))", a, len(o), tried, tried2, 4*budget)}, true, cl
		}
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried); none of the orders real time allows keeps every invariant (%d searched)", a, len(o), tried, tried2) + clockNote(f2.clocks, true)}, false, cl
	case ok:
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried)", a, len(o), tried) + clockNote(f.clocks, false)}, false, cl
	case cut:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps conformed in the %d orders of the budget, and the search had moves left: "+
			"no verdict (conformance n/a). Raise the budget (qtest.LinearizeBudget(%d)), %sor record its steps where they take effect", a, len(o), tried, 4*budget, partitionHint(partitioned))}, true, nil
	default:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps that real time allows conformed in %d orders tried; "+
			"the order shown got furthest. A step the model rejects in every order is a bug (a stale read, a lost write), unless the invocation times are wrong", a, len(o), tried) + clockNote(f.clocks, true)}, false, nil
	}
}

// clockNote is what a search's verdict says of the orders real time took
// from different recorders' clocks (review 11, F5): none, nothing; a
// verdict of no order (negative) rests on them, true only if the clocks
// are within the skew; a conforming order stands under any skew (a larger
// one only allows more orders).
func clockNote(c qtrace.ClockAssumption, negative bool) string {
	switch {
	case c.Orders == 0:
		return ""
	case negative:
		return fmt.Sprintf("; the verdict rests on %s, and holds only if they are: with clocks %s apart or more, the nearest of those orders may not hold", c, c.Closest)
	}
	return fmt.Sprintf("; %s (the order found conforms under any skew)", c)
}

// forcedClockNote is clockNote for the one order real time allows: the
// check's verdict on it, whatever it is, rests on the orders by clocks
// that left no other.
func forcedClockNote(c qtrace.ClockAssumption) string {
	if c.Orders == 0 {
		return ""
	}
	return fmt.Sprintf("; that one order rests on %s: a failure holds only if they are, with clocks %s apart or more another order may conform", c, c.Closest)
}

// first is a check's verdict of its one order.
func first(fs []int, _ []string) int { return fs[0] }

// lastState is the model's state after a conforming replay, as a string
// equal exactly when the states are (its variables, sorted, as JSON; the
// mbt:: ones, which name the step taken, left out), or "" when the
// report has no ITF: two prefixes of one actor's steps with one state
// have the same futures.
func lastState(rep *Report) string {
	if rep == nil || len(rep.Actors) != 1 || rep.Actors[0].ITF == "" {
		return ""
	}
	data, err := os.ReadFile(rep.Actors[0].ITF)
	if err != nil {
		return ""
	}
	var trace struct {
		States []map[string]json.RawMessage `json:"states"`
	}
	if json.Unmarshal(data, &trace) != nil || len(trace.States) == 0 {
		return ""
	}
	last := trace.States[len(trace.States)-1]
	for k := range last {
		if strings.HasPrefix(k, "mbt::") || strings.HasPrefix(k, "#") {
			delete(last, k)
		}
	}
	out, err := json.Marshal(last)
	if err != nil {
		return ""
	}
	return string(out)
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

// recorded counts an order's steps that are tr's (all but the effects of
// tr's pending calls a search placed): an order is complete when it has
// them all. A step recorded as assumed (a saved resolution, checked
// again) is one of tr's.
func recorded(tr *qtrace.Trace, o []qtrace.Step) int {
	n := 0
	for _, s := range o {
		if !s.Ambiguous || !slices.ContainsFunc(tr.Pending, func(p qtrace.Step) bool {
			return p.Action == s.Action && p.Time.Equal(s.Time) && fmt.Sprint(p.Args) == fmt.Sprint(s.Args)
		}) {
			n++
		}
	}
	return n
}

// effects says which of pending a linearization's order assumes took
// effect, and where; the others took none.
func effects(o []qtrace.Step, pending []qtrace.Step) string {
	var took, none []string
	for _, p := range pending {
		at := -1
		for i, s := range o {
			if s.Ambiguous && s.Action == p.Action && s.Time.Equal(p.Time) && fmt.Sprint(s.Args) == fmt.Sprint(p.Args) {
				at = i
				break
			}
		}
		if at < 0 {
			none = append(none, p.String())
		} else {
			took = append(took, fmt.Sprintf("%s at step %d", p.String(), at+1))
		}
	}
	var parts []string
	if len(took) > 0 {
		parts = append(parts, "pending calls taking effect: "+strings.Join(took, ", "))
	}
	if len(none) > 0 {
		parts = append(parts, "pending calls with no effect: "+strings.Join(none, ", "))
	}
	return strings.Join(parts, "; ")
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
		if recorded(tr, o) < len(tr.Steps) { // a prefix of an order (Linearize's complete search): the run has not ended
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
// budget per actor, re-sequenced onto one thread per actor; one that
// violates an invariant gives way to a later one that keeps them all
// (checked with full, ch with the run's invariants). It returns the
// steps, an issue per actor with pending calls, the actors whose search
// was cut short, and the issues each actor's steps had as recorded that no
// resolution removes (gaps, cycles).
func resolve(ch, full *Checker, steps []qtrace.Step, budget int) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue, claims map[string]*claim) {
	traces, issues := reconstruct(ch.Binding, steps)
	undecided, kept, claims = map[string]bool{}, map[string][]qtrace.Issue{}, map[string]*claim{}
	for _, tr := range traces {
		for _, i := range startIssues(ch.Binding, tr) {
			if i.Kind == "gap" || i.Kind == "lost-prefix" || i.Kind == "cycle" {
				kept[tr.Actor] = append(kept[tr.Actor], i)
			}
		}
		chosen := tr.Steps
		if len(tr.Pending) > 0 {
			var issue qtrace.Issue
			var cut bool
			var cl *claim
			chosen, issue, cut, cl = resolution(ch, full, tr, settlesOf(steps), budget)
			issues = append(issues, issue)
			if cl != nil {
				claims[tr.Actor] = cl
			}
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
		if w, ok := carried(tr, "resolved:"+tr.Actor); ok {
			out = append(out, w)
		}
	}
	return out, issues, undecided, kept, claims
}

// placement is where each pending call took effect: -1 none, else after
// that many of the actor's steps.
type placement []int

// resolutions enumerates the placements of pending calls, the fewest
// effects first, then the earliest, at most budget of them: a call may
// take effect only after the steps recorded before its invocation.
// crossed are the calls whose bounds cross: invoked after a step recorded
// after a settle that followed them, which only clocks that disagree
// make; they take no effect. No call takes effect before position start
// (1 after a snapshot step: the state the trace starts from).
func resolutions(tr *qtrace.Trace, settles []qtrace.Step, budget, start int) (out []placement, cut bool, crossed []int) {
	k := len(tr.Pending)
	from := make([]int, k) // the earliest position each may take effect at
	to := make([]int, k)   // the latest
	for i, p := range tr.Pending {
		from[i] = start
		// after every step recorded before its invocation
		for j, s := range tr.Steps {
			if before(s, p) {
				from[i] = max(from[i], j+1)
			}
		}
		// before every step recorded after a settle that followed it: the
		// system was quiet there, with nothing left in flight. The first
		// such step bounds it, not the last before the settle (a step
		// concurrent with the settle may follow one recorded after it);
		// a call whose bounds cross took no effect.
		to[i] = len(tr.Steps)
		for _, st := range settles {
			if !before(p, st) {
				continue
			}
			for j, s := range tr.Steps {
				if j < to[i] && before(st, s) {
					to[i] = j
					break
				}
			}
		}
	}
	for i := range k {
		if to[i] < from[i] {
			crossed = append(crossed, i)
		}
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
			return out, true, crossed
		}
	}
	return out, false, crossed
}

// before reports whether a was surely recorded before b: on one recorder,
// a lower seq; across recorders, earlier by more than RealTimeSlack (as
// Reconstruct and Linearize take machines' clocks), else concurrent.
func before(a, b qtrace.Step) bool {
	if a.Recorder == b.Recorder {
		return a.Seq < b.Seq
	}
	return a.Time.Add(qtrace.RealTimeSlack).Before(b.Time)
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
	// a lost tail may have come after any settle, none quiet, unless it
	// is bounded in time (a crash's): then a settle after the bound is (as
	// Reconstruct takes it)
	var bound time.Time
	for _, s := range steps {
		if s.Lost != "suffix" {
			continue
		}
		if s.LostBefore.IsZero() {
			return nil
		}
		if s.LostBefore.After(bound) {
			bound = s.LostBefore
		}
	}
	// and one a stated watermark does not reach (give or take the
	// clocks' slack) settles nothing: a step after it may not be collected
	// yet (as Reconstruct takes it, by qtrace.CollectedThrough)
	var short time.Time // the earliest watermark of a recorder
	for _, w := range qtrace.CollectedThrough(steps) {
		if short.IsZero() || w.Before(short) {
			short = w
		}
	}
	var out []qtrace.Step
	for _, s := range steps {
		if !s.Settled || !bound.IsZero() && !s.Time.After(bound.Add(qtrace.RealTimeSlack)) || !short.IsZero() && short.Before(s.Time.Add(-qtrace.RealTimeSlack)) {
			continue
		}
		quiet := true
		for _, o := range steps {
			if o.Recorder != s.Recorder && !o.Settled && o.CompleteThrough.IsZero() && o.Time.After(s.Time.Add(qtrace.RealTimeSlack)) {
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
				// in its own process ({{.process}} is its caller's), which
				// infers no restart: the binding skips an assumed step's
				s.Invocation, s.Ambiguous, s.Outcome, s.Obs, s.Order = false, true, "", nil, nil
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
// the model accepts. The first that conforms (the fewest effects, then
// the earliest) is taken, unless it violates one of the run's invariants
// (full's) and a later one conforms with every invariant holding: a run
// must not look violating by the placement picked, as an order of
// Linearize's must not (orderIssue). Judged on the first alone, a pending
// call's "no effect" violated an invariant its effect keeps, and
// Violates held where Linearize found the placement that keeps it
// (review 11, F3).
func resolution(ch, full *Checker, tr *qtrace.Trace, settles []qtrace.Step, budget int) ([]qtrace.Step, qtrace.Issue, bool, *claim) {
	a := tr.Actor
	if a == "" {
		a = "default"
	}
	hc := *ch // its candidates in the history's instance (binding.Planner)
	hc.mode.history = tr
	ch = &hc
	start := 0
	if snapshotFirst(ch.Binding, tr) {
		start = 1
	}
	pls, cut, crossed := resolutions(tr, settles, budget, start)
	// a call whose bounds cross is resolved as no effect: say so, a clock
	// problem otherwise silent
	var note string
	for _, i := range crossed {
		note += fmt.Sprintf("; %s was invoked after a step recorded after a settle that followed it (clocks that disagree?): it can have had no effect", tr.Pending[i].String())
	}
	orders := make([][]qtrace.Step, len(pls))
	for i, pl := range pls {
		// each candidate on one thread of its own, as the run's steps will be
		for j, s := range apply(tr, pl) {
			s.Recorder, s.Thread, s.Seq = "resolving:"+tr.Actor, "resolving", uint64(j+1)
			orders[i] = append(orders[i], s)
		}
	}
	// found is resolution i, conforming, as the search's outcome: with a
	// later one if i violates an invariant and that one keeps them
	found := func(i int, sev, extra string, cl *claim) ([]qtrace.Step, qtrace.Issue, bool, *claim) {
		j, kept, undecided := keepInvariants(full, tr, a, orders, i, cut)
		if j != i && cl != nil {
			cl.order = orders[j] // quint judged it, and the rejections before i still stand
		}
		if undecided {
			sev = "warn"
		}
		return apply(tr, pls[j]), qtrace.Issue{Severity: sev, Kind: "resolve", Msg: fmt.Sprintf("actor %s: %d pending calls, resolved (%d resolutions tried): %s",
			a, len(tr.Pending), j+1, describe(tr, pls[j])) + kept + note + extra}, undecided, cl
	}
	// the first the Go evaluator accepts, confirmed by quint with the ones
	// it rejected before it (or by the caller's check); none, or a
	// refusal, and quint checks them all, as a verdict of none needs every
	// one rejected
	disagreed := ""
	if i, ok := resolveEval(ch).first(ch, tr, a, orders); ok {
		if ch.mode.byCheck {
			return found(i, "info", "", &claim{ch: ch, tr: tr, order: orders[i], rejected: orders[:i]})
		}
		// quint confirms it, and the evaluator's rejections before it: the
		// run's invariants are judged on the one taken (one batch)
		fs, _ := quintChecker(ch, tr, a, false)(orders[:i+1])
		agrees := fs[i] < 0
		for _, f := range fs[:i] {
			agrees = agrees && f >= 0
		}
		if agrees {
			return found(i, "info", "", nil)
		}
		// quint rejects it: its batch decides, as without the evaluator,
		// and the issue says so, as the order's search does (it once fell
		// back in silence, an evaluator bug unseen outside shadow mode)
		searchedAgain.Add(1)
		disagreed = fmt.Sprintf("; the Go evaluator and quint disagree on actor %s's resolutions: decided by quint (GOTOGETHER_EVAL=quint checks with quint alone)", a)
	}
	reps, errs := ch.CheckBatch(ch.mode.context(), settledAs(tr, orders), 0)
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
			sev := "info"
			if disagreed != "" {
				sev = "warn"
			}
			return found(i, sev, disagreed, nil)
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
			"no verdict (conformance n/a); the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best])) + note + disagreed}, true, nil
	}
	return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conforms (%d tried): "+
		"whatever they did, the run is no behaviour of the model; the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best])) + note + disagreed}, false, nil
}

// keepInvariants is the resolution to take of orders, i the first that
// conforms: i if it keeps every invariant of full's (the Go evaluator's
// word, else quint's), else the first after it that conforms with every
// invariant holding (quint's), what the issue adds, and undecided when
// none does and the budget cut the placements short (another may keep
// them: no verdict, as a cut search's). None, with every placement
// tried, and i is taken: the run violates whatever its calls did.
func keepInvariants(full *Checker, tr *qtrace.Trace, a string, orders [][]qtrace.Step, i int, cut bool) (int, string, bool) {
	if full == nil || len(full.Binding.Invariants) == 0 {
		return i, "", false
	}
	inst := inHistory(full, tr)
	if ev := newEvalUse(inst, inst); !ev.off {
		if f, _, err := ev.e.judge(inst, tr, settledAs(tr, orders[i:i+1])[0], len(orders[i]), true); err == nil && f < 0 {
			return i, "", false
		}
	}
	quintInv := quintChecker(inst, tr, a, true)
	if first(quintInv(orders[i:i+1])) < 0 {
		return i, "", false
	}
	if rest := orders[i+1:]; len(rest) > 0 {
		fs, _ := quintInv(rest)
		for k, f := range fs {
			if f < 0 {
				return i + 1 + k, fmt.Sprintf(", with every invariant holding (resolution %d, the first that conforms, violates one)", i+1), false
			}
		}
	}
	if cut {
		return i, fmt.Sprintf("; it violates an invariant, and no other resolution in the budget (%d) conforms with every invariant holding: no verdict for the invariants. "+
			"Raise the budget (validate.SearchOptions.ResolveBudget), or record where the pending calls took effect", len(orders)), true
	}
	return i, fmt.Sprintf("; it violates an invariant, and so does every other resolution that conforms (%d tried)", len(orders)), false
}

// resolveEval is how a resolution uses the Go evaluator.
func resolveEval(ch *Checker) *evalUse { return newEvalUse(ch, ch) }

// first is the index of the first of orders the Go evaluator accepts;
// false if it accepts none or refuses one before it.
func (ev *evalUse) first(ch *Checker, tr *qtrace.Trace, a string, orders [][]qtrace.Step) (int, bool) {
	if ev.off {
		return 0, false
	}
	var fs []int
	for i, o := range settledAs(tr, orders) {
		f, _, err := ev.e.judge(ch, tr, o, len(orders[i]), false)
		if err != nil {
			return 0, false
		}
		fs = append(fs, f)
		if f < 0 {
			break
		}
	}
	if ev.shadow {
		shadowCheck(a, orders[:len(fs)], fs, quintChecker(ch, tr, a, false))
	}
	if i := len(fs) - 1; i >= 0 && fs[i] < 0 {
		return i, true
	}
	return 0, false
}
