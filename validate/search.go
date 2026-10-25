package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	// intervals: no search ran, and the steps are a client's history (a
	// failing actor's report suggests Linearize)
	intervals bool
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
		// its ITF on: a conforming candidate's last state lets the complete
		// search merge prefixes of one state (qtrace.LinearizeStates)
		lc.Options.Dir, lc.Options.NoITF, lc.Options.VerdictsOnly = filepath.Join(c.Options.Dir, "linearize"), false, true
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
	s.Steps, s.intervals = steps, intervals(steps)
	return s, nil
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

// Mark puts what the search found in rep, the report of its Steps: its
// issues (with no search, for a failing client history, a hint to
// linearize), the issues of each actor's steps as recorded that no order
// removes (gaps, cycles), and no verdict for an actor whose search ran out
// of its budget (its conformance and failed invariants n/a: the order
// shown is the one that got furthest).
func (s *Search) Mark(rep *Report) {
	rep.Issues = append(rep.Issues, s.issues...)
	for _, a := range rep.Actors {
		if s.intervals && a.Conforms == Fail {
			a.Issues = append(a.Issues, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: "its steps carry invocation times, a client's history: " +
				"calls that overlapped return in another order than they took effect, which the recorded order checks; if they were recorded where they returned, " +
				"check in an order real time allows (qtest.Linearize(), gotogether validate -linearize)"})
		}
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
	type job struct {
		tr, part *qtrace.Trace
		o        []qtrace.Step
		issue    qtrace.Issue
		cut      bool
		panicked any
	}
	var jobs []*job
	for _, tr := range traces {
		for _, part := range partitions(tr, key) {
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
			j.o, j.issue, j.cut = order(jch, jfull, j.part, budget, key != "")
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
		for _, i := range tr.Issues {
			if i.Kind == "gap" || i.Kind == "cycle" {
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
	return out, issues, undecided, kept
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
	// candidates are judged by the Go evaluator while it can (quint for the
	// rest of the search once it refuses one), and quint confirms what the
	// search concludes
	ev := &evalUse{}
	if evalMode() == "go" {
		if ev.e = evaluatorFor(full); ev.e.err != nil {
			ev.off, ev.unavailable, ev.why = true, true, ev.e.err.Error()
		}
	} else {
		ev.off, ev.why = true, "GOTOGETHER_EVAL=quint"
	}
	checker := func(ch *Checker, invariants bool) func(orders [][]qtrace.Step) ([]int, []string) {
		// candidates in the history's instance and naming, as the memo and
		// the merge of prefixes by state assume
		within := *ch
		within.within = tr
		ch = &within
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
					return fs, states
				}
			}
			return quint(orders)
		}
	}
	check := checker(ch, false)
	if len(tr.Steps) == 0 {
		return nil, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no steps to order", a)}, false
	}
	if o, ok := qtrace.ForcedOrder(tr.Steps); ok {
		// one order: a search would check it alone, and the caller checks
		// the order it gets (Checker.Check of Search.Steps), which judges it
		// as this search would have; two quint processes saved (the probe
		// of every account makes many such partitions)
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: real time allows one order of its %d steps, checked as the history's", a, len(o))}, false
	}
	o, ok, tried, cut, proof := qtrace.LinearizeProof(tr.Steps, budget, check)
	o, ok, tried, cut = ev.decide(o, ok, tried, cut, proof, quintChecker(within(ch, tr), tr, a, false), a,
		func() ([]qtrace.Step, bool, int, bool) { return qtrace.LinearizeStates(tr.Steps, budget, check) })
	o, issue, cut := orderIssue(ch, full, tr, a, budget, partitioned, checker, ev, o, ok, tried, cut)
	return o, ev.note(issue), cut
}

// within is ch planning its candidates in tr's instance.
func within(ch *Checker, tr *qtrace.Trace) *Checker {
	c := *ch
	c.within = tr
	return &c
}

// decide is a search's conclusion as quint confirms it, when the Go
// evaluator judged its candidates: an order found is checked by quint;
// no order found, by its proof in one batch (quint rejects every prefix
// the search dropped as rejected, and gives each pair it merged one
// state), since no one order confirms that every order fails. A
// disagreement searches again with quint alone, and says so. A search cut
// short, or a proof whose states quint did not write, is decided by quint
// alone.
func (ev *evalUse) decide(o []qtrace.Step, ok bool, tried int, cut bool, proof *qtrace.Proof, quint func([][]qtrace.Step) ([]int, []string), a string,
	again func() ([]qtrace.Step, bool, int, bool)) ([]qtrace.Step, bool, int, bool) {
	if !ev.used {
		return o, ok, tried, cut // quint judged every candidate
	}
	ev.used = false
	if ok {
		if fq := first(quint([][]qtrace.Step{o})); fq >= 0 {
			ev.off, ev.disagreed = true, true
			ev.why = fmt.Sprintf("the Go evaluator and quint disagree on actor %s's order (quint: %s)", a, verdictOf(fq))
			searchedAgain.Add(1)
			return again()
		}
		return o, ok, tried, cut
	}
	if !cut && proof != nil {
		switch confirms(proof, quint) {
		case confirmed:
			return o, ok, tried, cut
		case contradicted:
			ev.off, ev.disagreed = true, true
			ev.why = fmt.Sprintf("quint does not confirm the Go evaluator's verdict that no order of actor %s's steps conforms", a)
			searchedAgain.Add(1)
			return again()
		}
	}
	off := ev.off
	ev.off = true
	searchedAgain.Add(1)
	o, ok, tried, cut = again()
	ev.off = off
	return o, ok, tried, cut
}

// A proof's confirmation by quint.
const (
	confirmed = iota
	contradicted
	unconfirmed // quint wrote no state for a merged pair
)

// confirms checks proof with quint, in one batch: every rejected prefix
// rejected, every merged pair accepted with one state. (A tampered
// evaluator's proof, the tests' tamperEval, is contradicted here.)
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
	refused bool   // it refused a candidate: the rest of the search was quint's
	// unavailable: the binding has no evaluator (its evaluation module
	// cannot carry a template, or did not compile)
	unavailable bool
	disagreed   bool // quint contradicted its conclusion
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
		state += "|process=" + order[len(order)-1].Process
	}
	return state
}

// quintChecker checks candidate orders of tr's steps with quint, in
// batches: each one's failure index (with invariants, an invariant's
// violation's too), and a conforming one's last state.
func quintChecker(ch *Checker, tr *qtrace.Trace, a string, invariants bool) func(orders [][]qtrace.Step) ([]int, []string) {
	return func(orders [][]qtrace.Step) ([]int, []string) {
		reps, errs := ch.CheckBatch(context.Background(), settledAs(tr, orders), 0)
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
	o []qtrace.Step, ok bool, tried int, cut bool) ([]qtrace.Step, qtrace.Issue, bool) {
	// with ITF (the invariants' searches write none): a proof's merged
	// prefixes are confirmed by their states
	inst := within(full, tr)
	inst.Options.NoITF = false
	quintInv := quintChecker(inst, tr, a, true)
	switch {
	case ok && len(full.Binding.Invariants) > 0 && first(quintInv([][]qtrace.Step{o})) >= 0:
		// the order found violates an invariant (quint says, one check): another that conforms may
		// not, and a run must not look violating by the order picked
		inv := checker(full, true)
		ev.used = false
		o2, ok2, tried2, cut2, proof2 := qtrace.LinearizeProof(tr.Steps, budget, inv)
		o2, ok2, tried2, cut2 = ev.decide(o2, ok2, tried2, cut2, proof2, quintInv, a,
			func() ([]qtrace.Step, bool, int, bool) { return qtrace.LinearizeStates(tr.Steps, budget, inv) })
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
// crossed are the calls whose bounds cross: invoked after a step recorded
// after a settle that followed them, which only clocks that disagree
// make; they take no effect.
func resolutions(tr *qtrace.Trace, settles []qtrace.Step, budget int) (out []placement, cut bool, crossed []int) {
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
	within := *ch // its candidates in the history's instance (binding.PlanWithin)
	within.within = tr
	ch = &within
	pls, cut, crossed := resolutions(tr, settles, budget)
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
				a, len(tr.Pending), i+1, describe(tr, pls[i])) + note}, false
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
			"no verdict (conformance n/a); the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best])) + note}, true
	}
	return apply(tr, pls[best]), qtrace.Issue{Severity: "warn", Kind: "resolve", Msg: fmt.Sprintf("actor %s: no resolution of its %d pending calls conforms (%d tried): "+
		"whatever they did, the run is no behaviour of the model; the one shown got furthest: %s", a, len(tr.Pending), len(pls), describe(tr, pls[best])) + note}, false
}
