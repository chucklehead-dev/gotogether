package qtrace

import (
	"container/heap"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"
)

// reconstructRef is Reconstruct as it was before it worked by index
// (I38), the reference TestReconstructAgainstReference compares it with:
// steps copied by value through every pass. Its one change: it ranges
// over the settles in sorted order, as Reconstruct now does (a map's
// order made a run's settle issues come in either order).
func reconstructRef(steps []Step) ([]*Trace, []Issue) {
	var issues []Issue

	// 1. Dedupe on (recorder, seq): OTLP exporters retry, collectors fan
	// out, and a copy is the same record twice. Two records of one
	// (recorder, seq) that differ are no copy: two logs naming one
	// recorder (two drivers' request logs both "ui"), whose second's steps
	// a dedupe would drop unseen, a failing run passing on the first's.
	// That is a defect of the input, an error issue of each actor of
	// either record, as a cycle is.
	type rk struct {
		rec string
		seq uint64
	}
	seen := map[rk]int{} // the index in uniq of the record kept
	dupIssues := map[string][]Issue{}
	uniq := steps[:0:0]
	for _, s := range steps {
		if s.Seq != 0 {
			k := rk{s.Recorder, s.Seq}
			if i, ok := seen[k]; ok {
				first := &uniq[i]
				if SameRecord(first, &s) {
					issues = append(issues, Issue{"info", "duplicate", fmt.Sprintf("recorder %s seq %d seen twice; kept the first", s.Recorder, s.Seq)})
					continue
				}
				is := Issue{"error", "duplicate", fmt.Sprintf("recorder %s seq %d is two different records (%s, then %s): two logs or exporters number their steps in one recorder; "+
					"give each log its own recorder (a request log's header names it), or drop the log that is not this run's", s.Recorder, s.Seq, describe(first), describe(&s))}
				dupIssues[first.Actor] = append(dupIssues[first.Actor], is)
				if s.Actor != first.Actor {
					dupIssues[s.Actor] = append(dupIssues[s.Actor], is)
				}
				continue
			}
			seen[k] = len(uniq)
		}
		uniq = append(uniq, s)
	}

	// 2. Gaps: seq is dense per recorder, so a hole is a lost step (sampling,
	// a dropped export, a crash before flush). We cannot tell which actor it
	// belonged to, so every actor of that recorder is affected. A log that
	// starts after seq 1 lost a prefix, not a step: its first state is not
	// init (Lost.Issue), which a snapshot step at its head can say. Loss
	// records (MarkLost, a torn tail) declare what a log cannot show.
	byRec := map[string][]uint64{}
	recActors := map[string]map[string]bool{}
	firstAt := map[string]time.Time{} // each recorder's first step's time
	firstSeq := map[string]uint64{}
	declared := map[string][]Step{} // loss records, by recorder
	marks := map[string]time.Time{} // watermarks, by recorder (AllRecorders: every one's)
	whole := map[string]bool{}      // recorders whose carried watermark says a log of the actor was whole
	seqAt := map[rk]time.Time{}     // each record's time, by its seq: when a gap's steps were lost
	for _, s := range uniq {
		if !s.CompleteThrough.IsZero() {
			if s.CompleteThrough.After(marks[s.Recorder]) { // a watermark only rises
				marks[s.Recorder] = s.CompleteThrough
			}
			whole[s.Recorder] = whole[s.Recorder] || s.carriedWhole
			continue
		}
		if s.Seq != 0 {
			seqAt[rk{s.Recorder, s.Seq}] = s.Time
		}
		if s.Lost != "" {
			declared[s.Recorder] = append(declared[s.Recorder], s)
			continue
		}
		byRec[s.Recorder] = append(byRec[s.Recorder], s.Seq)
		if recActors[s.Recorder] == nil {
			recActors[s.Recorder] = map[string]bool{}
		}
		recActors[s.Recorder][s.Actor] = true
		// the lowest seq, a step without one only if it has none (then the
		// earliest)
		if f, ok := firstSeq[s.Recorder]; !ok || s.Seq != 0 && (f == 0 || s.Seq < f) || s.Seq == f && s.Time.Before(firstAt[s.Recorder]) {
			firstSeq[s.Recorder], firstAt[s.Recorder] = s.Seq, s.Time
		}
	}
	gapActors := map[string][]string{}
	holesOf := map[string][]Gap{} // each actor's gaps
	lostActors := map[string][]Lost{}
	collected := map[string][]Watermark{} // each actor's recorders' watermarks
	// markOf is how far rec is known collected (CollectedThrough: its
	// watermark, raised by its own dense seqs), zero if it stated none
	through := collectedThroughRef(uniq)
	markOf := func(rec string) time.Time { return through[rec] }
	carriedWhole := map[string]bool{} // actors a carried watermark says had a whole log
	// some recorder lost its tail: no settle settles the run, but one
	// after every bounded loss's Before
	lostTail := false
	var lostBefore time.Time // the latest bound of the lost tails
	unbounded := false       // a lost tail with no bound
	recs := make([]string, 0, len(byRec)+len(declared))
	for rec := range byRec {
		recs = append(recs, rec)
	}
	for rec := range declared {
		if _, ok := byRec[rec]; !ok {
			recs = append(recs, rec)
		}
	}
	sort.Strings(recs)
	for _, rec := range recs {
		seqs := byRec[rec]
		slices.Sort(seqs)
		var missing []uint64
		var holes []Gap
		var first, next, prev uint64 // the first seq, the one expected next, the last seen
		for _, q := range seqs {
			if q == 0 {
				continue // a step without a seq: nothing to count
			}
			if first == 0 {
				first, next = q, q
			}
			if q > next {
				holes = append(holes, Gap{Recorder: rec, First: next, Last: q - 1, After: seqAt[rk{rec, prev}], Before: seqAt[rk{rec, q}]})
			}
			for ; next < q && len(missing) < 20; next++ {
				missing = append(missing, next)
			}
			next, prev = q+1, q
		}
		if len(missing) > 0 {
			msg := fmt.Sprintf("recorder %s is missing seq %v (of %d..%d): steps were lost or sampled out", rec, missing, first, seqs[len(seqs)-1])
			for a := range recActors[rec] {
				gapActors[a] = append(gapActors[a], msg)
				holesOf[a] = append(holesOf[a], holes...)
			}
		}
		var losses []Lost
		if first > 1 {
			losses = append(losses, Lost{Recorder: rec, Prefix: true, After: first - 1, At: firstAt[rec]})
		}
		for _, d := range declared[rec] {
			l := Lost{Recorder: rec, Prefix: d.Lost == "prefix", After: d.LostAfter, At: d.Time, Before: d.LostBefore}
			// one loss of each kind per recorder: the longest prefix, the
			// earliest cut
			merged := false
			for i := range losses {
				o := &losses[i]
				if o.Prefix != l.Prefix {
					continue
				}
				merged = true
				if l.Prefix && l.After > o.After || !l.Prefix && l.After < o.After {
					o.After = l.After
				}
				if l.Prefix && o.At.IsZero() || !l.Prefix && l.At.Before(o.At) {
					o.At = l.At
				}
				// the latest bound, and none if one is unbounded
				if !l.Prefix && (o.Before.IsZero() || l.Before.IsZero()) {
					o.Before = time.Time{}
				} else if !l.Prefix && l.Before.After(o.Before) {
					o.Before = l.Before
				}
			}
			if !merged {
				if l.Prefix && l.At.IsZero() {
					l.At = firstAt[rec]
				}
				losses = append(losses, l)
			}
		}
		sort.SliceStable(losses, func(i, j int) bool { return losses[i].Prefix && !losses[j].Prefix })
		actors := recActors[rec]
		if len(actors) == 0 {
			// a recorder whose every step was lost is the actors its loss
			// records name (a crash's, qnemesis: the incarnation was killed
			// before it recorded anything, after an effect another recorder
			// saw), else no actor's: whose steps they were is unknown; its
			// tail still settles nothing
			for _, d := range declared[rec] {
				if d.Actor != "" {
					if actors == nil {
						actors = map[string]bool{}
					}
					actors[d.Actor] = true
				}
			}
		}
		for _, l := range losses {
			if !l.Prefix {
				lostTail = true
				unbounded = unbounded || l.Before.IsZero()
				if l.Before.After(lostBefore) {
					lostBefore = l.Before
				}
			}
			for a := range actors {
				lostActors[a] = append(lostActors[a], l)
			}
		}
		if len(marks) > 0 { // (no watermark stated: every log whole, as before them)
			for a := range actors {
				collected[a] = append(collected[a], Watermark{Recorder: rec, Through: markOf(rec)})
				carriedWhole[a] = carriedWhole[a] || whole[rec]
			}
		}
	}

	// 3. Invocation and settle records are no steps: an invocation whose
	// completion names it (Call, same recorder) is done with, any other is
	// pending; a settle settles the run if its recorder recorded nothing
	// after it (seq), whatever its actor.
	completed := map[rk]bool{}
	invokedAt := map[rk]time.Time{}
	invokedRef := map[rk]string{}
	lastSettle, last := map[string]uint64{}, map[string]uint64{}
	for _, s := range uniq {
		if s.Call != 0 {
			completed[rk{s.Recorder, s.Call}] = true
		}
		if s.Invocation {
			invokedAt[rk{s.Recorder, s.Seq}] = s.Time
			if s.Ref != "" {
				invokedRef[rk{s.Recorder, s.Seq}] = s.Ref
			}
		}
		if s.Settled {
			lastSettle[s.Recorder] = max(lastSettle[s.Recorder], s.Seq)
		} else if s.Lost == "" && s.CompleteThrough.IsZero() {
			last[s.Recorder] = max(last[s.Recorder], s.Seq)
		}
	}
	// and no other recorder recorded a step later in time: a harness's
	// settle says nothing of servers still at work
	settleAt := map[string]time.Time{}
	for _, s := range uniq {
		if s.Settled && s.Seq == lastSettle[s.Recorder] {
			settleAt[s.Recorder] = s.Time
		}
	}
	// short is a recorder whose stated watermark is before t, by more than
	// the clocks' slack (a step within it of a settle does not void it
	// either): a step it recorded after t may not be collected yet
	short := func(t time.Time) (string, time.Time, bool) {
		for _, rec := range recs {
			if w := markOf(rec); !w.IsZero() && w.Before(t.Add(-RealTimeSlack)) {
				return rec, w, true
			}
		}
		return "", time.Time{}, false
	}
	settled, voided := false, false
	uncollected := ""                                          // why a settle a watermark does not reach settles nothing
	for _, rec := range slices.Sorted(maps.Keys(lastSettle)) { // (sorted: the original ranged over the map)
		seq := lastSettle[rec]
		if seq <= last[rec] {
			continue
		}
		var later *Step
		for i := range uniq {
			o := &uniq[i]
			if o.Recorder != rec && !o.Settled && o.Lost == "" && o.CompleteThrough.IsZero() && o.Time.After(settleAt[rec].Add(RealTimeSlack)) {
				later = o
				break
			}
		}
		if later != nil {
			issues = append(issues, Issue{"warn", "settle", fmt.Sprintf("recorder %s recorded %s (seq %d) after recorder %s's settle: the system was not quiet, and the settle settles nothing",
				later.Recorder, later.Action, later.Seq, rec)})
			continue
		}
		if lostTail && !unbounded && !settleAt[rec].After(lostBefore.Add(RealTimeSlack)) {
			voided = true // a lost step may follow it
			continue
		}
		if r, w, ok := short(settleAt[rec]); ok {
			uncollected = fmt.Sprintf("recorder %s's steps are complete only through %s (its watermark), before recorder %s's settle at %s: a step it recorded after the settle may not be collected yet, "+
				"and the settle settles nothing until every watermark reaches it (raise them once the collector has the steps: qobs.Recorder.CompleteThrough, gotogether validate -complete-through)",
				r, w.UTC().Format(time.RFC3339Nano), rec, settleAt[rec].UTC().Format(time.RFC3339Nano))
			continue
		}
		settled = true
	}
	if !settled && uncollected != "" {
		issues = append(issues, Issue{"warn", "settle", uncollected})
	}
	if !settled && voided {
		issues = append(issues, Issue{"warn", "settle", "a recorder lost its tail (Trace.Lost): what it recorded after, a settle or a step after one, is unknown, and the settle settles nothing"})
	}
	if settled && lostTail && unbounded {
		issues = append(issues, Issue{"warn", "settle", "a recorder lost its tail (Trace.Lost): what it recorded after, a settle or a step after one, is unknown, and the settle settles nothing"})
		settled = false
	}
	pending := map[string][]Step{}
	callIssues := map[string][]Issue{}
	steps = uniq[:0:0]
	for _, s := range uniq {
		switch {
		case s.Settled, s.Lost != "", !s.CompleteThrough.IsZero():
		case !s.Invocation:
			// a completion was invoked when its invocation record was
			// written: the recorder's own clock, the one its Time is on (a
			// simulation's true time), whatever "invoked" said
			if t, ok := invokedAt[rk{s.Recorder, s.Call}]; ok && s.Call != 0 {
				s.Invoked, s.fromRecord = t, true
				// the record's time is trusted as the call's start: one
				// after its completion, or a completion numbered before
				// its invocation, is an adapter's or a recorder's error
				switch {
				case s.Seq != 0 && s.Seq <= s.Call:
					callIssues[s.Actor] = append(callIssues[s.Actor], Issue{"error", "invocation", fmt.Sprintf("%s (seq %d) completes the invocation of seq %d, which is not before it: a completion follows its invocation record in its recorder's sequence (an adapter numbering records out of order?)", s.Action, s.Seq, s.Call)})
				case t.After(s.Time.Add(RealTimeSlack)):
					callIssues[s.Actor] = append(callIssues[s.Actor], Issue{"error", "invocation", fmt.Sprintf("%s (seq %d) completed at %s, before its invocation record (seq %d) at %s: an invocation's time is when the call began, by the clock of its completion (an adapter that timed it after the outcome?)", s.Action, s.Seq, s.Time.Format(time.RFC3339Nano), s.Call, t.Format(time.RFC3339Nano))})
				}
			}
			if s.Ref == "" && s.Call != 0 { // a driver may link only the call's start to its artifact
				s.Ref = invokedRef[rk{s.Recorder, s.Call}]
			}
			steps = append(steps, s)
		case !completed[rk{s.Recorder, s.Seq}]:
			pending[s.Actor] = append(pending[s.Actor], s)
		}
	}

	// 4. Partition by actor, then order each.
	byActor := map[string][]Step{}
	for _, s := range steps {
		byActor[s.Actor] = append(byActor[s.Actor], s)
	}
	for a := range pending {
		if byActor[a] == nil {
			byActor[a] = []Step{} // an actor whose every operation is pending
		}
	}
	for a := range dupIssues {
		if byActor[a] == nil {
			byActor[a] = []Step{} // an actor whose every record was dropped as a conflicting duplicate: its issue says so
		}
	}
	actors := slices.Sorted(maps.Keys(byActor))
	var out []*Trace
	for _, a := range actors {
		t := orderRef(a, byActor[a])
		t.Pending = pending[a]
		t.Settled = settled && len(t.Steps) > 0
		sort.SliceStable(t.Pending, func(i, j int) bool { return t.Pending[i].Time.Before(t.Pending[j].Time) })
		t.Lost = lostActors[a]
		t.Gaps = holesOf[a]
		t.Collected = collected[a]
		t.carriedWhole = carriedWhole[a]
		for _, l := range t.Lost {
			if l.Prefix {
				t.Issues = append(t.Issues, l.Issue())
			}
		}
		for _, m := range gapActors[a] {
			t.Issues = append(t.Issues, Issue{"error", "gap", m})
		}
		t.Issues = append(t.Issues, callIssues[a]...)
		t.Issues = append(t.Issues, dupIssues[a]...)
		out = append(out, t)
	}
	return out, issues
}

type nodeRef struct {
	s     Step
	key   time.Time // recording time, made monotone per recorder
	idx   int
	succ  []int
	npred int
}

type pqRef []*nodeRef

func (q pqRef) Len() int { return len(q) }
func (q pqRef) Less(i, j int) bool {
	a, b := q[i], q[j]
	if !a.key.Equal(b.key) {
		return a.key.Before(b.key)
	}
	if a.s.Recorder != b.s.Recorder {
		return a.s.Recorder < b.s.Recorder
	}
	return a.s.Seq < b.s.Seq
}
func (q pqRef) Swap(i, j int)             { q[i], q[j] = q[j], q[i] }
func (q *pqRef) Push(x any)               { *q = append(*q, x.(*nodeRef)) }
func (q *pqRef) Pop() any                 { o := *q; n := o[len(o)-1]; *q = o[:len(o)-1]; return n }
func edgeRef(ns []*nodeRef, from, to int) { ns[from].succ = append(ns[from].succ, to); ns[to].npred++ }

func orderRef(actor string, steps []Step) *Trace {
	t := &Trace{Actor: actor}
	ns := buildRef(steps)
	for _, i := range kahnRef(ns, &t.Issues) {
		t.Steps = append(t.Steps, ns[i].s)
	}
	t.Issues = append(t.Issues, realTime(t.Steps)...)
	// Incarnations: in order of first appearance; interleaving means two
	// incarnations of one actor were alive at once.
	seenP := map[string]bool{}  // processes that ran: incarnations
	listed := map[string]bool{} // processes numbered (t.Processes)
	list := func(p string) {
		if !listed[p] {
			listed[p] = true
			t.Processes = append(t.Processes, p)
		}
	}
	cur := ""
	for _, s := range t.Steps {
		if s.Process == cur {
			continue
		}
		if s.Ambiguous {
			// a pending call's effect a search placed, in its caller's
			// process, which did not run there: numbered, no incarnation
			list(s.Process)
			continue
		}
		if seenP[s.Process] {
			// an error, not a warning: replayed, the old incarnation's
			// step is a third boot of the process, and the run passed
			t.Issues = append(t.Issues, Issue{"error", "overlap", fmt.Sprintf("process %s emitted %s after process %s had started: two incarnations of %s overlap "+
				"(a split brain, or the recorders' clocks disagree), and the model has one at a time. Fence the old incarnation, or order the steps with a domain clock", s.Process, s.Action, cur, actor)})
		} else {
			seenP[s.Process] = true
			list(s.Process)
		}
		cur = s.Process
	}
	return t
}

// build makes the happens-before graph of one actor's steps: a node per
// step, keyed by its recording time made monotone per recorder, with the
// hard edges of program order and domain clocks.
func buildRef(steps []Step) []*nodeRef {
	ns := make([]*nodeRef, len(steps))
	for i, s := range steps {
		ns[i] = &nodeRef{s: s, key: s.Time, idx: i}
	}
	// Monotone keys per recorder, in seq order: wall clocks step backwards.
	byRec := map[string][]*nodeRef{}
	for _, n := range ns {
		byRec[n.s.Recorder] = append(byRec[n.s.Recorder], n)
	}
	for _, l := range byRec {
		sort.Slice(l, func(i, j int) bool { return l[i].s.Seq < l[j].s.Seq })
		for i := 1; i < len(l); i++ {
			if l[i].key.Before(l[i-1].key) {
				l[i].key = l[i-1].key
			}
		}
	}
	// Program order per thread.
	threads := map[string][]*nodeRef{}
	for _, n := range ns {
		if n.s.Thread != "" {
			k := n.s.Recorder + "\x00" + n.s.Thread
			threads[k] = append(threads[k], n)
		}
	}
	for _, l := range threads {
		sort.Slice(l, func(i, j int) bool { return l[i].s.Seq < l[j].s.Seq })
		for i := 1; i < len(l); i++ {
			edgeRef(ns, l[i-1].idx, l[i].idx)
		}
	}
	// Domain clocks per (process, domain).
	domains := map[string][]*nodeRef{}
	for _, n := range ns {
		for d := range n.s.Order {
			k := n.s.Process + "\x00" + d
			domains[k] = append(domains[k], n)
		}
	}
	for k, l := range domains {
		d := k[indexNul(k)+1:]
		sort.SliceStable(l, func(i, j int) bool { return l[i].s.Order[d] < l[j].s.Order[d] })
		// Steps with equal clock values are unordered with each other; every
		// step of one value comes before every step of the next. (Linking
		// neighbours only would leave a(3) unordered with c(4) in a(3) b(3) c(4).)
		var prev, cur []*nodeRef
		for i, n := range l {
			if i > 0 && n.s.Order[d] != l[i-1].s.Order[d] {
				prev, cur = cur, nil
			}
			for _, p := range prev {
				edgeRef(ns, p.idx, n.idx)
			}
			cur = append(cur, n)
		}
	}
	return ns
}

// kahn sorts the graph topologically, preferring recording order, and
// breaks a cycle (reported in issues) at its earliest step. It returns the
// nodes' indices in order.
func kahnRef(ns []*nodeRef, issues *[]Issue) []int {
	var out []int
	q := &pqRef{}
	for _, n := range ns {
		if n.npred == 0 {
			heap.Push(q, n)
		}
	}
	done := make([]bool, len(ns))
	for len(out) < len(ns) {
		if q.Len() == 0 {
			// A cycle: the hard constraints contradict each other, which
			// means the instrumentation's thread or order keys are wrong.
			// Break it at the earliest remaining step and report.
			var rest []*nodeRef
			for _, n := range ns {
				if !done[n.idx] {
					rest = append(rest, n)
				}
			}
			sort.Slice(rest, func(i, j int) bool { return (pqRef{rest[i], rest[j]}).Less(0, 1) })
			*issues = append(*issues, Issue{"error", "cycle", fmt.Sprintf("ordering constraints form a cycle at %s (seq %d); order keys contradict program order", rest[0].s.Action, rest[0].s.Seq)})
			rest[0].npred = 0
			heap.Push(q, rest[0])
		}
		n := heap.Pop(q).(*nodeRef)
		if done[n.idx] {
			continue
		}
		done[n.idx] = true
		out = append(out, n.idx)
		for _, s := range n.succ {
			ns[s].npred--
			if ns[s].npred == 0 && !done[s] {
				heap.Push(q, ns[s])
			}
		}
	}
	return out
}

// collectedThroughRef is CollectedThrough as it was (a map of every seq's time).
func collectedThroughRef(steps []Step) map[string]time.Time {
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

func indexNul(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}
