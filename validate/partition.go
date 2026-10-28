package validate

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A serial log of independent entities (charges, accounts) is judged
// entity by entity when its binding names the argument that says whose
// each step is (binding.Partition): a log judged whole costs the sum of
// the model's states it passes through, which grow with the entities
// (an invariant over every charge, judged after every step of a day's
// log, is quadratic in it), and stops at its first failure. Each
// entity's steps are a trace of their own ("payments id=ch_1"), planned
// in the whole log's instance, judged in parallel and reported each, the
// passes by the Go evaluator, the failures by quint, as any actor's.
//
// That is the whole log's verdict only if the model factors by the
// entity, which is checked on the run's own evaluation, never assumed:
// the Go evaluator replays every entity's trace recording each entry of
// the state's maps it reads and writes (qeval.Footprint), and the run is
// refused (an error naming the step and the entry, never a verdict)
// when
//   - a step, an expect or a judged val accesses a map otherwise than by
//     entry (its keys, its size, an iteration, a comparison of it whole),
//     reads a scalar variable, or assigns a map anew: it depends on every
//     entity's state;
//   - a step accesses an entry another entity's step wrote before it, in
//     the log's order: it would have seen that entity's value;
//   - a judged val reads an entry another entity's step writes at all:
//     the whole log's val is judged after every step, that one's too.
//
// A judged val (an invariant, an eventual val) may take a forall over a
// state map's keys at a conjunctive position as each entry's judgment:
// such a val holds of the union of the entities' states exactly when it
// holds of each one's, the other conditions holding. Otherwise every
// entity's step reads and writes entries no other entity's touches, so
// the whole log's states are the union of the entities' at every step,
// and each step, expect and val is evaluated in the whole log as in its
// entity's trace, over the same entries' same values: the same verdict.
// A failing entity's steps after its failure are not evaluated, so the
// claim covers what was (a whole log stops at its first failure anyway).

// A partitioned trace is a whole trace's parts, each one entity's steps
// (splitTrace's), with their plans in the whole's instance.
type partitioned struct {
	whole *qtrace.Trace
	parts []*qtrace.Trace
	plans []*binding.Plan
	// at are each part's steps' indices in whole.Steps
	at [][]int
	// constants are the whole's instance, listed once for every part's
	// result (a day's instance lists every charge)
	constants []string
	// consts are its values, for the Go evaluator (nil: it has none)
	consts map[string]qeval.Value
}

// partitionedChecks reports whether c's checks judge a trace by its
// binding's partition: a recorded run's, not a search's candidates.
func (c *Checker) partitionedChecks() bool {
	return c.Binding.Partition != "" && c.mode.history == nil
}

// partition is trace t split by the binding's partition, its parts
// planned in t's instance, and the model's factoring by it checked on
// their evaluation (factors). An error is why the trace cannot be
// judged so: a step that names no entity, a plan's error, or the model
// not factoring.
func (c *Checker) partition(t *qtrace.Trace) (*partitioned, error) {
	key := c.Binding.Partition
	parts, at, missing := splitTrace(t, key, snapshotFirst(c.Binding, t))
	if missing != nil {
		return nil, fmt.Errorf("partition %s: step %s has no %s argument: every step of a partitioned log names its entity; record it with the step, or drop partition: from the binding to judge the log whole",
			key, missing.String(), key)
	}
	pt := &partitioned{whole: t, parts: parts, at: at}
	planner := c.Binding.Planner(c.Model, t)
	for _, p := range parts {
		// the whole's losses, gaps and issues are every entity's: whose the
		// lost steps were is unknown
		p.Lost, p.Gaps, p.Collected, p.Issues = t.Lost, t.Gaps, t.Collected, t.Issues
		p.Processes = t.Processes
		plan, err := planner.Plan(p)
		if err != nil {
			return nil, fmt.Errorf("actor %s: %w", p.Actor, err)
		}
		pt.plans = append(pt.plans, plan)
	}
	read, err := c.factors(pt)
	if err != nil {
		return nil, err
	}
	if len(pt.plans) == 0 {
		return pt, nil
	}
	// The parts are replayed in one instance, the whole's, but for the
	// constants no evaluation of any part read (its every step, expect,
	// invariant in every state and eventual val, as quint's detail replay
	// judges them): those take the first part's own value, which no
	// verdict depends on. A day's IDS (every charge) made each quint
	// replay of a failing part typecheck a set of thousands (a replay of
	// two failing parts in a 10,000-step log: 7 minutes).
	own, err := c.Binding.Plan(c.Model, pt.parts[0])
	if err != nil {
		return nil, fmt.Errorf("actor %s: %w", pt.parts[0].Actor, err)
	}
	var unread []string
	for k := range pt.plans[0].Constants {
		if read[k] {
			continue
		}
		if v, ok := own.Constants[k]; ok && v != pt.plans[0].Constants[k] {
			unread = append(unread, k)
			for _, p := range pt.plans {
				p.Constants[k] = v
			}
		}
	}
	slices.Sort(unread)
	pt.constants = pt.plans[0].ConstantList()
	if len(unread) > 0 {
		pt.constants = append(pt.constants, fmt.Sprintf("(%s read by no step or val of this run: every partition replayed with the first one's)", strings.Join(unread, ", ")))
	}
	return pt, nil
}

// A use is one part's accesses of one entry: its first write and last
// access by a step (positions in the whole log's order: 2i for step i,
// -1 none), and whether a judged val read it.
type use struct {
	firstWrite, lastStep int
	judged               bool
}

// factors checks that the model factors by the binding's partition over
// pt's parts (the comment above says what that is): each part replayed
// by the Go evaluator, as a check replays it, with a footprint; nil, or
// the error that names the first access that breaks it, and the
// binding's constants any evaluation read. The evaluator is needed: a
// binding it has no module for, or a part it refuses, is an error, since
// nothing else checks it.
func (c *Checker) factors(pt *partitioned) (map[string]bool, error) {
	key := c.Binding.Partition
	if len(pt.parts) == 0 {
		return nil, nil
	}
	e := evaluatorFor(c)
	if e.err != nil {
		return nil, fmt.Errorf("partition %s: the Go evaluator checks that the model factors by %s, and has no evaluation module for this binding (%v): drop partition: to judge the log whole", key, key, e.err)
	}
	e = e.replaying()
	consts, err := e.constants(pt.plans[0])
	if err != nil {
		return nil, fmt.Errorf("partition %s: the instance: %w", key, err)
	}
	pt.consts = consts
	uses := make([]map[qeval.Entry]*use, len(pt.parts))
	reads := make([]map[string]bool, len(pt.parts)) // the evaluation module's constants each part read
	wholes := make([]string, len(pt.parts))         // the first access of each part's that was not to one entry, described
	errs := make([]error, len(pt.parts))
	forEach(len(pt.parts), func(i int) { uses[i], reads[i], wholes[i], errs[i] = c.footprint(e, pt, i) })
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("partition %s: actor %s: %w", key, pt.parts[i].Actor, err)
		}
	}
	for i, w := range wholes {
		if w != "" {
			return nil, fmt.Errorf("partition %s: the model does not factor by %s: actor %s, %s: it depends on every entity's state, not its own, so "+
				"each entity's verdict would not be the whole log's. Drop partition: from the binding to judge the log whole, or restate the model "+
				"so that a step reads and writes entries keyed by its entity (an index map in place of a scan: examples/migrate-payments' byKey)",
				key, key, pt.parts[i].Actor, w)
		}
	}
	if err := conflicts(pt, key, uses); err != nil {
		return nil, err
	}
	read := map[string]bool{} // by the binding's (the model's) names
	for k, name := range e.em.Consts {
		for _, r := range reads {
			if r[name] {
				read[k] = true
				break
			}
		}
	}
	return read, nil
}

// footprint replays part i of pt with a footprint: each entry it
// accessed, and its first access that was not to one entry (described,
// with where), or the evaluator's refusal. A part the model rejects, or
// that fails at run time, ends there: its later steps are not evaluated,
// and quint judges its failure.
func (c *Checker) footprint(e *evaluator, pt *partitioned, i int) (map[qeval.Entry]*use, map[string]bool, string, error) {
	part, plan := pt.parts[i], pt.plans[i]
	index := map[*qtrace.Step]int{}
	for j := range part.Steps {
		index[&part.Steps[j]] = pt.at[i][j]
	}
	// a position's time in the whole log's order: step g at 2g, the vals
	// judged after it at 2g+1, the start's at -1
	stepOf := func(call int) int { return index[plan.Calls[call].Step] }
	fp := &qeval.Footprint{}
	te := *e
	te.m, te.consts, te.judgeAll = e.m.Tracking(fp), pt.consts, true
	uses := map[qeval.Entry]*use{}
	get := func(en qeval.Entry) *use {
		u := uses[en]
		if u == nil {
			u = &use{firstWrite: -1, lastStep: -1}
			uses[en] = u
		}
		return u
	}
	time, judging, where := -1, false, "the start"
	whole := ""
	harvest := func() {
		for _, en := range fp.Reads {
			if u := get(en); judging {
				u.judged = true
			} else {
				u.lastStep = max(u.lastStep, time)
			}
		}
		for _, en := range fp.Writes {
			u := get(en)
			if u.firstWrite < 0 || time < u.firstWrite {
				u.firstWrite = time
			}
			u.lastStep = max(u.lastStep, time)
		}
		fp.Reset()
		if fp.Whole != "" && whole == "" {
			whole = where + ": " + fp.Whole
		}
	}
	te.phase = func(pos int, judged bool) {
		harvest()
		judging = judged
		fp.Judging = judged
		switch {
		case pos == 0:
			time, where = -1, "the vals judged in the start's state"
		case judged:
			g := stepOf(pos - 1)
			time, where = 2*g+1, fmt.Sprintf("the vals judged after %s", describeStep(pt.whole, g))
		default:
			g := stepOf(pos - 1)
			time, where = 2*g, describeStep(pt.whole, g)
		}
	}
	_, err := te.walk(c, pt.consts, plan, 0, nil, len(c.Binding.Invariants) > 0, part.Settled, nil)
	harvest()
	var u *qeval.Unsupported
	if errors.As(err, &u) {
		return nil, nil, "", fmt.Errorf("the Go evaluator, which checks that the model factors, refuses it (%v): drop partition: to judge the log whole", err)
	}
	// a model's run-time error (a map's missing key) is quint's to
	// report: the part ends there, as a rejected step ends it
	return uses, fp.Consts, whole, nil
}

// describeStep is step g of the whole trace, as an error names it.
func describeStep(whole *qtrace.Trace, g int) string {
	s := whole.Steps[g]
	src := ""
	if s.Source != "" {
		src = ", " + s.Source
	}
	return fmt.Sprintf("step %d (%s, seq %d%s)", g+1, s.Action, s.Seq, src)
}

// conflicts is the error of the first entry (in the log's order) that
// two parts share so that one's verdict could depend on the other's
// steps: written by both; accessed by a step after the other's wrote it;
// or read by a judged val of one and written by the other. nil if none.
func conflicts(pt *partitioned, key string, uses []map[qeval.Entry]*use) error {
	type writer struct {
		part, at int
	}
	writers := map[qeval.Entry][]writer{}
	for p, us := range uses {
		for en, u := range us {
			if u.firstWrite >= 0 {
				writers[en] = append(writers[en], writer{p, u.firstWrite})
			}
		}
	}
	var found []string
	best := -2
	note := func(at int, msg string) {
		if best == -2 || at < best {
			best, found = at, []string{msg}
		}
	}
	entry := func(en qeval.Entry) string { return fmt.Sprintf("%s[%s]", en.Var, en.Key) }
	at := func(t int) string {
		if t < 0 {
			return "the start"
		}
		return describeStep(pt.whole, t/2)
	}
	for p, us := range uses {
		for en, u := range us {
			for _, w := range writers[en] {
				if w.part == p {
					continue
				}
				other := pt.parts[w.part].Actor
				switch {
				case u.firstWrite >= 0:
					t := max(u.firstWrite, w.at)
					note(t, fmt.Sprintf("actors %s and %s both write %s (%s and %s)", pt.parts[p].Actor, other, entry(en), at(u.firstWrite), at(w.at)))
				case u.lastStep > w.at:
					note(u.lastStep, fmt.Sprintf("%s of actor %s reads %s, which %s of actor %s wrote before it", at(u.lastStep), pt.parts[p].Actor, entry(en), at(w.at), other))
				case u.judged:
					note(w.at, fmt.Sprintf("a judged val (an invariant, an eventual val) of actor %s reads %s, which %s of actor %s writes", pt.parts[p].Actor, entry(en), at(w.at), other))
				}
			}
		}
	}
	if found == nil {
		return nil
	}
	return fmt.Errorf("partition %s: the model does not factor by %s: %s: an entry two entities share, so one's verdict would not be the whole log's. "+
		"Drop partition: from the binding to judge the log whole, or restate the model so that each entity's steps touch entries keyed by it alone",
		key, key, strings.Join(found, "; "))
}

// partitionResults are the results of pt's parts, newResult's each,
// sharing the whole's instance list, and marked with their partition.
func (c *Checker) partitionResults(pt *partitioned) []*Result {
	out := make([]*Result, len(pt.parts))
	for i, p := range pt.parts {
		out[i] = newResultWith(c.Binding, p, pt.plans[i], pt.constants)
		out[i].Partition = strings.TrimPrefix(p.Actor, nameOf(pt.whole.Actor)+" ")
	}
	return out
}

// partitionOf is r's whole actor, when r is a partition's ("" else).
func partitionOf(r *Result) string {
	if r.Partition == "" {
		return ""
	}
	return strings.TrimSuffix(r.Actor, " "+r.Partition)
}

// foldPartitions is the line that says r's passing partitions, by their
// whole actor, in the order they first appear, and the results left to
// print (every other); nil lines when none is a partition's.
func foldPartitions(rs []*Result) (lines []string, rest []*Result) {
	type fold struct {
		n, pass, steps int
		key            string
		issues         []string // the passing partitions' issues (their whole's), once each
	}
	folds := map[string]*fold{}
	var order []string
	for _, r := range rs {
		w := partitionOf(r)
		if w == "" {
			rest = append(rest, r)
			continue
		}
		f := folds[w]
		if f == nil {
			f = &fold{key: strings.SplitN(r.Partition, "=", 2)[0]}
			folds[w] = f
			order = append(order, w)
		}
		f.n++
		if r.OK() && r.Hot == nil {
			f.pass++
			f.steps += r.Steps
			for _, i := range r.Issues {
				if s := i.String(); !slices.Contains(f.issues, s) {
					f.issues = append(f.issues, s)
				}
			}
			continue
		}
		rest = append(rest, r)
	}
	for _, w := range order {
		f := folds[w]
		lines = append(lines, fmt.Sprintf("actor %s, partitioned by %s: %d of %d partitions pass (%d steps), each judged alone; the model checked to factor by %s on this run",
			w, f.key, f.pass, f.n, f.steps, f.key))
		for _, i := range f.issues {
			lines = append(lines, "  issue: "+i)
		}
	}
	return lines, slices.Clip(rest)
}

// forEach calls f(i) for each i < n on GOMAXPROCS workers, each taking
// the next i: a goroutine per item grew a fresh stack for each of a
// day's partitions (a fifth of their judging's CPU).
func forEach(n int, f func(i int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(n, runtime.GOMAXPROCS(0)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				f(i)
			}
		}()
	}
	wg.Wait()
}
