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
			out = append(out, Issue{"warn", "realtime", fmt.Sprintf("%s (seq %d) was invoked after it completed: its invocation time is from another clock, or is not when it began", s.Action, s.Seq)})
			continue
		}
		if j := earliest[i+1]; j >= 0 && !s.Invoked.IsZero() && steps[j].Time.Add(RealTimeSlack).Before(s.Invoked) {
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
// The search starts from Reconstruct's order and, while an order fails,
// tries every single move the constraints allow around the failing step
// (it, earlier; a later step, before it), keeping the move that gets
// furthest, until one conforms, no move gets further, or budget orders
// were checked. It is a guided search, not an exhaustive one: finding
// none is strong evidence of a bug, not a proof.
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
	// mustPrecede: a hard edge, or a completed before b was invoked (a step
	// whose invocation is unknown has no real-time bound)
	after := func(a, b int) bool {
		inv := ns[b].s.Invoked
		return !inv.IsZero() && ns[a].s.Time.Add(RealTimeSlack).Before(inv)
	}
	mustPrecede := func(a, b int) bool { return hard[[2]int{a, b}] || after(a, b) }
	actor := steps[0].Actor
	render := func(order []int) []Step {
		out := make([]Step, len(order))
		for i, k := range order {
			s := ns[k].s
			s.Recorder, s.Thread, s.Seq = "linearized:"+actor, "linearized", uint64(i+1)
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
	seen := map[string]bool{key(at): true}
	cur := at
	f := check([][]Step{render(cur)})[0]
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
		if len(cands) == 0 {
			stopped = true
			break
		}
		all := len(cands)
		if room := budget - tried; len(cands) > room {
			cands = cands[:room]
		}
		orders := make([][]Step, len(cands))
		for i, c := range cands {
			orders[i] = render(c)
		}
		fs := check(orders)
		tried += len(cands)
		bi, bf := -1, f
		for i, fi := range fs {
			if fi < 0 {
				return orders[i], true, tried, false
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
	return render(cur), f < 0, tried, f >= 0 && !stopped
}
