package qtrace

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// realTime reports the steps an order puts before a step that completed
// before they were invoked: real time says otherwise, so the order's hard
// constraints (threads, domain clocks) contradict it.
// Only steps with a known invocation (Invoked) are bounded by it.
func realTime(steps []Step) []Issue {
	var out []Issue
	// earliest[i]: the step from i on that completed first
	earliest := make([]int, len(steps)+1)
	earliest[len(steps)] = -1
	for i := len(steps) - 1; i >= 0; i-- {
		earliest[i] = i
		if j := earliest[i+1]; j >= 0 && steps[j].Time.Before(steps[i].Time) {
			earliest[i] = j
		}
	}
	for i := range steps {
		s := &steps[i]
		if !s.Invoked.IsZero() && s.Invoked.After(s.Time) {
			out = append(out, Issue{"warn", "realtime", fmt.Sprintf("%s (seq %d) was invoked after it completed: its invocation time is from another clock, or is not when it began "+
				"(record the call with qobs.Invoke and Call.Done, which take both times from the recorder, and pass no \"invoked\"; in a simulation, a time from Sim.Now)", s.Action, s.Seq)})
			continue
		}
		// an invocation time the recorder gave is its own clock's: an error
		// against another machine's would be the clocks' skew (the user's
		// "invoked" is a claim about real time, and is checked)
		if j := earliest[i+1]; j >= 0 && !s.Invoked.IsZero() && !s.fromRecord && steps[j].Time.Add(RealTimeSlack).Before(s.Invoked) {
			a := &steps[j]
			out = append(out, Issue{"error", "realtime", fmt.Sprintf("%s (seq %d) completed before %s (seq %d) was invoked, but the order puts it after: "+
				"the thread or order keys contradict real time (a step recorded before its effect, an order key from another clock)", a.Action, a.Seq, s.Action, s.Seq)})
			if len(out) >= 5 {
				break
			}
		}
	}
	return out
}

// RealTimeSlack is how much later than another's end a step's invocation
// must be for real time to order them. Recorded times are approximate:
// OpenTelemetry's SDK writes a span's end as its start's wall-clock time
// plus the monotonic time since, so one span's start and another's end
// come from different clocks, and spans of a correct service were seen
// to "end before" ones that took effect before them by up to a few
// microseconds (kvstore's, under CI's load). Steps closer than this are
// concurrent; machines whose clocks differ by more need their own order
// keys.
const RealTimeSlack = time.Millisecond

// moveWindow bounds how far a move takes a step: concurrent operations
// are near each other in recording order.
const moveWindow = 32

// reorderWindow bounds the reorderings of steps before the failing one:
// each pair is a candidate, so it is kept small.
const reorderWindow = 8

// Linearize searches the orders of one actor's steps that their
// constraints allow for one that check accepts: program order, domain
// clocks, and real time (a step whose operation completed before another
// was invoked precedes it). It is for steps recorded where an operation
// was invoked and returned (a client's), not where it took effect: the
// recording order of concurrent operations is then not their order, and
// checking it rejects correct code.
//
// check gets candidate orders, each re-sequenced onto one thread of one
// recorder ("linearized:<actor>") so that Reconstruct keeps it, and
// returns for each the index of its first step the model rejects, or -1.
// Re-sequenced, an order hides its steps' own issues (a gap, a cycle):
// they are Reconstruct's, and the caller reports them with the verdict.
// The search starts from Reconstruct's order and, while an order fails,
// tries every single move the constraints allow around the failing step
// (it, earlier; a later step, before it; a step before it, earlier),
// keeping the move that gets
// furthest, until one conforms, no move gets further, or budget orders
// were checked. When it finds none, a complete search follows (every
// order real time allows, prefix by prefix, rejected prefixes pruned):
// finding none there, within budget, is a proof.
//
// It returns the order that conformed, or that got furthest, whether one
// conformed, how many orders it checked, and whether the budget cut the
// search short (cut: it stopped with moves left that might have got
// further). A search that found none and was not cut stopped because no
// move got further: the stronger evidence.
func Linearize(steps []Step, budget int, check func(orders [][]Step) []int) (best []Step, ok bool, tried int, cut bool) {
	if len(steps) == 0 {
		return nil, true, 0, false
	}
	ns := build(steps)
	var issues []Issue
	at := kahn(ns, &issues) // an order is of indices into ns
	hard := map[[2]int]bool{}
	for i, n := range ns {
		for _, j := range n.succ {
			hard[[2]int{i, j}] = true
		}
	}
	// mustPrecede: a hard edge, or a completed before b was invoked: on one
	// recorder, exactly, when a was recorded before b's invocation record
	// (b.Call); else by time, give or take the slack (a step whose
	// invocation is unknown has no real-time bound)
	after := func(a, b int) bool {
		sa, sb := &ns[a].s, &ns[b].s
		if sa.Recorder == sb.Recorder && sb.Call != 0 && sa.Seq != 0 {
			return sa.Seq < sb.Call
		}
		if (sb.fromRecord || sa.fromRecord) && (len(sa.Order) > 0 || len(sb.Order) > 0) {
			return false // machines that order themselves by domain clocks: their own clocks compare with no other's
		}
		inv := sb.Invoked
		return !inv.IsZero() && sa.Time.Add(RealTimeSlack).Before(inv)
	}
	mustPrecede := func(a, b int) bool { return hard[[2]int{a, b}] || after(a, b) }
	actor := steps[0].Actor
	render := func(order []int) []Step {
		out := make([]Step, len(order))
		for i, k := range order {
			s := ns[k].s
			s.Recorder, s.Thread, s.Seq = "linearized:"+actor, "linearized", uint64(i+1)
			s.Call = 0 // paired already: on the new recorder its invocation's seq means another record
			out[i] = s
		}
		return out
	}
	key := func(order []int) string {
		var b strings.Builder
		for _, k := range order {
			b.WriteString(strconv.Itoa(k))
			b.WriteByte(',')
		}
		return b.String()
	}
	// judge checks orders of indices, memoized by prefix: an order that
	// fails at step f has its prefixes up to f accepted and the one through
	// f rejected (an extension of it fails there too); one that conforms,
	// every prefix accepted. A candidate the memo decides costs no quint.
	accepted, rejected := map[string]bool{}, map[string]bool{}
	judge := func(orders [][]int) []int {
		out := make([]int, len(orders))
		var ask []int
		for i, o := range orders {
			out[i] = -2
			if accepted[key(o)] {
				out[i] = -1
				continue
			}
			for l := 1; l <= len(o); l++ {
				if rejected[key(o[:l])] {
					out[i] = l - 1
					break
				}
			}
			if out[i] == -2 {
				ask = append(ask, i)
			}
		}
		if len(ask) == 0 {
			return out
		}
		rendered := make([][]Step, len(ask))
		for j, i := range ask {
			rendered[j] = render(orders[i])
		}
		for j, f := range check(rendered) {
			o := orders[ask[j]]
			out[ask[j]] = f
			upTo := len(o)
			if f >= 0 {
				upTo = f
				rejected[key(o[:f+1])] = true
			}
			for l := 1; l <= upTo; l++ {
				accepted[key(o[:l])] = true
			}
		}
		return out
	}
	seen := map[string]bool{key(at): true}
	cur := at
	f := judge([][]int{cur})[0]
	tried = 1
	stopped := false // no move left, or none got further
	for f >= 0 && tried < budget {
		var cands [][]int
		add := func(c []int) {
			if k := key(c); !seen[k] {
				seen[k] = true
				cands = append(cands, c)
			}
		}
		s := cur[f]
		// the failing step, earlier: across steps that need not precede it
		for q := f - 1; q >= 0 && q >= f-moveWindow && !mustPrecede(cur[q], s); q-- {
			c := slices.Clone(cur)
			copy(c[q+1:f+1], cur[q:f])
			c[q] = s
			add(c)
		}
		// a later step, just before the failing one: across steps that need
		// not precede it
		for j := f + 1; j < len(cur) && j <= f+moveWindow; j++ {
			y := cur[j]
			movable := true
			for _, x := range cur[f:j] {
				if mustPrecede(x, y) {
					movable = false
					break
				}
			}
			if !movable {
				continue // the order is by completion: a step further on may have begun earlier
			}
			c := slices.Clone(cur)
			copy(c[f+1:j+1], cur[f:j])
			c[f] = y
			add(c)
		}
		// an earlier step, earlier still: the failing step may need a step
		// before it reordered with another (two writes, of which a read saw
		// the one recorded first), which no move of the failing step makes
		for q := f - 1; q >= 0 && q >= f-reorderWindow; q-- {
			y := cur[q]
			for p := q - 1; p >= 0 && p >= q-reorderWindow && !mustPrecede(cur[p], y); p-- {
				c := slices.Clone(cur)
				copy(c[p+1:q+1], cur[p:q])
				c[p] = y
				add(c)
			}
		}
		if len(cands) == 0 {
			stopped = true
			break
		}
		all := len(cands)
		if room := budget - tried; len(cands) > room {
			cands = cands[:room]
		}
		fs := judge(cands)
		tried += len(cands)
		bi, bf := -1, f
		for i, fi := range fs {
			if fi < 0 {
				return render(cands[i]), true, tried, false
			}
			if fi > bf {
				bi, bf = i, fi
			}
		}
		if bi < 0 && len(cands) == all {
			stopped = true
			break // no move gets further
		}
		if bi < 0 {
			break // none of the moves the budget left room for got further
		}
		cur, f = cands[bi], bf
	}
	if f < 0 {
		return render(cur), true, tried, false
	}
	// the guided search found none: search every order the constraints
	// allow, prefix by prefix, before calling it a bug
	if o, ok, n, cutShort := prefixes(ns, at, mustPrecede, budget, render, judge); ok || n > 0 {
		tried += n
		if ok {
			return o, true, tried, false
		}
		// cut short, it is no verdict: the guided search's stop (no move
		// got further) is no proof, and a brute-force comparison found it
		// wrong (TestLinearizeAgainstBruteForce); uncut, it is a proof,
		// however the guided search ended
		if cutShort {
			return render(cur), false, tried, true
		}
		return render(cur), false, tried, false
	}
	return render(cur), false, tried, !stopped
}

// prefixes searches the orders the constraints allow level by level (Wing
// and Gong's search, a batch of candidates a level): each prefix the model
// accepts is extended by every step all of whose required predecessors it
// already holds, and a rejected prefix is dropped with every order it
// begins. A full order must conform as a whole (an eventual val included).
// Unless budget cut it, finding none is a proof: no order real time
// allows is a behaviour of the model. Every accepted prefix is kept: a
// beam of them made five concurrent operations no verdict whatever the
// budget, and the budget is what a user can raise.
func prefixes(ns []*node, base []int, mustPrecede func(a, b int) bool, budget int, render func([]int) []Step,
	judge func(orders [][]int) []int) (best []Step, ok bool, tried int, cut bool) {
	n := len(ns)
	level := [][]int{nil}
	for k := 0; k < n && tried < budget; k++ {
		var cands [][]int
		for _, p := range level {
			placed := make([]bool, n)
			for _, i := range p {
				placed[i] = true
			}
			for _, i := range base {
				if placed[i] {
					continue
				}
				minimal := true
				for j := 0; j < n && minimal; j++ {
					minimal = placed[j] || j == i || !mustPrecede(j, i)
				}
				if minimal {
					cands = append(cands, append(slices.Clip(p), i))
				}
			}
		}
		if room := budget - tried; len(cands) > room {
			cands, cut = cands[:room], true
		}
		fs := judge(cands)
		tried += len(cands)
		var next [][]int
		for i, f := range fs {
			if f < 0 {
				if k == n-1 {
					return render(cands[i]), true, tried, false
				}
				next = append(next, cands[i])
			}
		}
		if len(next) == 0 {
			return nil, false, tried, cut
		}
		level = next
	}
	return nil, false, tried, true
}
