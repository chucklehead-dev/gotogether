package qtrace

import (
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
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
// search short (cut: no verdict). A search that found none and was not
// cut checked every order, the complete search's proof.
func Linearize(steps []Step, budget int, check func(orders [][]Step) []int) (best []Step, ok bool, tried int, cut bool) {
	return LinearizeStates(steps, budget, func(orders [][]Step) ([]int, []string) { return check(orders), nil })
}

// LinearizeStates is Linearize with a check that also returns, for each
// order the model accepts, its state after the order (any string that is
// equal exactly when the states are; "" or a nil slice: unknown). The
// complete search then keeps one of the prefixes that placed the same
// steps and reached the same state (Lowe's memo for Wing and Gong's
// search): what follows them is the same, so a history whose operations
// overlap is searched in as many prefixes as states, not orders.
func LinearizeStates(steps []Step, budget int, check func(orders [][]Step) (failed []int, states []string)) (best []Step, ok bool, tried int, cut bool) {
	return linearize(steps, budget, check, !hooks.CompleteSearchOnly.Load(), nil)
}

// A Proof is what a complete search's "no order" rests on: every
// candidate it dropped as rejected, and every pair of accepted prefixes
// it merged as one state (the second dropped). An oracle other than the
// one that judged the search (quint, for the Go evaluator's) confirms the
// verdict by rejecting each of Rejected and giving each of Merged's pairs
// one state: a candidate wrongly accepted needs no confirmation, since
// its extensions were judged in turn.
type Proof struct {
	Rejected [][]Step
	Merged   [][2][]Step
}

// LinearizeProof is LinearizeStates, with the proof of a verdict of no
// order (nil for any other).
func LinearizeProof(steps []Step, budget int, check func(orders [][]Step) (failed []int, states []string)) (best []Step, ok bool, tried int, cut bool, proof *Proof) {
	return LinearizePending(steps, nil, nil, budget, check)
}

// LinearizePending is LinearizeProof with calls left pending (Jepsen's
// indeterminate calls: a caller that crashed or timed out never learned
// whether its call took effect), each of which may have taken effect
// once, anywhere real time allows, or not at all: after every step
// recorded before its invocation, and before every step recorded after a
// settle that followed it (the system was quiet there). An order is
// complete when every step is in it; a pending call in it is an assumed
// step (Ambiguous, no observation, in the process of the step before it),
// and one left out took no effect. The guided search orders the steps
// alone (no pending call took effect); the complete search places the
// pending calls too. check is told which orders are complete by their
// steps that are not assumed: as many as steps.
func LinearizePending(steps, pending, settles []Step, budget int, check func(orders [][]Step) (failed []int, states []string)) (best []Step, ok bool, tried int, cut bool, proof *Proof) {
	proof = &Proof{}
	best, ok, tried, cut = linearizeWith(steps, pending, settles, budget, check, !hooks.CompleteSearchOnly.Load(), proof)
	if ok || cut {
		proof = nil
	}
	return best, ok, tried, cut, proof
}

// constraints are steps' nodes, an order of them the hard edges allow (of
// indices into ns), the relation every order must keep, how an order of
// indices becomes steps on one recorder, and a cycle of the hard edges,
// broken.
func constraints(steps []Step) (ns []*node, at []int, mustPrecede func(a, b int) bool, render func([]int) []Step, issues []Issue) {
	ns = build(steps)
	at = kahn(ns, &issues)
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
	mustPrecede = func(a, b int) bool { return hard[[2]int{a, b}] || after(a, b) }
	actor := steps[0].Actor
	render = func(order []int) []Step {
		out := make([]Step, len(order))
		for i, k := range order {
			s := ns[k].s
			s.Recorder, s.Thread, s.Seq = "linearized:"+actor, "linearized", uint64(i+1)
			s.Call = 0 // paired already: on the new recorder its invocation's seq means another record
			out[i] = s
		}
		return out
	}
	return ns, at, mustPrecede, render, issues
}

// ForcedOrder is the one order of steps real time and their recorders
// allow, when there is one (each step must precede the next): a search
// would try it alone, so a caller that checks the order it returns needs
// no search. Its steps are on one recorder, as Linearize's.
func ForcedOrder(steps []Step) ([]Step, bool) {
	if len(steps) == 0 {
		return nil, true
	}
	_, at, mustPrecede, render, issues := constraints(steps)
	if len(issues) > 0 {
		return nil, false // a cycle, broken: the search reports it
	}
	for i := 1; i < len(at); i++ {
		if !mustPrecede(at[i-1], at[i]) {
			return nil, false
		}
	}
	return render(at), true
}

// linearize is LinearizeStates; without guided, the complete search alone
// (for tests of it).
func linearize(steps []Step, budget int, check func(orders [][]Step) ([]int, []string), guided bool, proof *Proof) (best []Step, ok bool, tried int, cut bool) {
	return linearizeWith(steps, nil, nil, budget, check, guided, proof)
}

// before reports whether a step was recorded before another took effect
// or was invoked: on one recorder by seq, else by time beyond the slack
// (ResolvePending's bounds on a pending call).
func before(a, b Step) bool {
	if a.Recorder == b.Recorder {
		return a.Seq < b.Seq
	}
	return a.Time.Add(RealTimeSlack).Before(b.Time)
}

// linearizeWith is LinearizePending: steps are nodes 0..n-1, pending calls
// n..n+m-1, which no order needs.
func linearizeWith(steps, pending, settles []Step, budget int, check func(orders [][]Step) ([]int, []string), guided bool, proof *Proof) (best []Step, ok bool, tried int, cut bool) {
	if len(steps) == 0 {
		return nil, true, 0, false // nothing observed: no call need have taken effect
	}
	ns, at, required, renderSteps, _ := constraints(steps)
	n := len(ns)
	// a pending call's bounds: the steps recorded before its invocation
	// precede it; it precedes the steps, and the pending calls, recorded
	// after a settle that followed it (the system was quiet there: it had
	// taken effect, if it did). Pending calls bound no step otherwise.
	bound := map[[2]int]bool{}
	for pi, p := range pending {
		i := n + pi
		for j := range n {
			if before(ns[j].s, p) {
				bound[[2]int{j, i}] = true
			}
		}
		for _, st := range settles {
			if !before(p, st) {
				continue
			}
			for j := range n {
				if before(st, ns[j].s) {
					bound[[2]int{i, j}] = true
				}
			}
			for qi, q := range pending {
				if qi != pi && before(st, q) {
					bound[[2]int{i, n + qi}] = true
				}
			}
		}
	}
	mustPrecede := func(a, b int) bool {
		if a < n && b < n {
			return required(a, b)
		}
		return bound[[2]int{a, b}]
	}
	actor := ""
	if n > 0 {
		actor = steps[0].Actor
	} else {
		actor = pending[0].Actor
	}
	render := func(order []int) []Step {
		var req []int
		for _, k := range order {
			if k < n {
				req = append(req, k)
			}
		}
		if len(req) == len(order) {
			return renderSteps(order)
		}
		out := make([]Step, len(order))
		for i, k := range order {
			var s Step
			if k < n {
				s = ns[k].s
			} else {
				s = pending[k-n]
				// in its own process ({{.process}} is its caller's): the
				// binding infers no restart around an assumed step
				s.Invocation, s.Ambiguous, s.Outcome, s.Obs, s.Order = false, true, "", nil, nil
			}
			s.Recorder, s.Thread, s.Seq, s.Actor = "linearized:"+actor, "linearized", uint64(i+1), actor
			s.Call = 0
			out[i] = s
		}
		return out
	}
	// complete: every step placed (pending calls left out took no effect);
	// check judges such an order whole, a settle's eventual vals included
	complete := func(order []int) bool {
		c := 0
		for _, k := range order {
			if k < n {
				c++
			}
		}
		return c == n
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
	// A complete order is judged whole: accepted, it is no prefix's verdict
	// (a prefix is not judged at a settle), and failing at its last step it
	// may fail a settle's eventual val, which a pending call placed after
	// it may make hold: its extensions are judged.
	accepted, rejected := map[string]bool{}, map[string]bool{}
	stateOf := map[string]string{} // an accepted order's state, when check knows it
	judge := func(orders [][]int) []int {
		out := make([]int, len(orders))
		var ask []int
		for i, o := range orders {
			out[i] = -2
			// (a complete order is judged before its extensions, in the
			// complete search's order: this and the marking below are guards)
			if accepted[key(o)] && !complete(o) {
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
		fs, states := check(rendered)
		for j, f := range fs {
			o := orders[ask[j]]
			if f < 0 && j < len(states) && states[j] != "" {
				stateOf[key(o)] = states[j]
			}
			out[ask[j]] = f
			upTo := len(o)
			if f >= 0 {
				upTo = f
				if !complete(o) || f < len(o)-1 {
					rejected[key(o[:f+1])] = true
				}
			}
			for l := 1; l <= upTo; l++ {
				if !complete(o[:l]) {
					accepted[key(o[:l])] = true
				}
			}
		}
		return out
	}
	seen := map[string]bool{key(at): true}
	cur := at
	f := judge([][]int{cur})[0]
	tried = 1
	for guided && f >= 0 && tried < budget {
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
			break // no move left
		}
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
		if bi < 0 {
			break // no move got further
		}
		cur, f = cands[bi], bf
	}
	if f < 0 {
		return render(cur), true, tried, false
	}
	// the guided search found none: search every order the constraints
	// allow, prefix by prefix, before calling it a bug
	// two accepted prefixes of the same steps and state have the same
	// futures: the complete search keeps one
	same := func(o []int) string {
		st := stateOf[key(o)]
		if st == "" {
			return ""
		}
		placed := slices.Sorted(slices.Values(o))
		return key(placed) + "|" + st
	}
	nodes := slices.Clone(at)
	for i := range pending {
		nodes = append(nodes, n+i)
	}
	if o, ok, n, cutShort := prefixes(n, nodes, mustPrecede, budget, render, judge, same, complete, proof); ok || n > 0 {
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
	// the complete search tried nothing (a budget under 1): whatever
	// stopped the guided search, no verdict
	return render(cur), false, tried, true
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
//
// nodes are the steps (0..n-1, in the hard edges' order) and the pending
// calls (n on), which an order may leave out: one is placed after the
// steps that must precede it and while none it must precede is placed. A
// complete order (every step placed) that fails at its last step is
// extended still: a pending call after it may make a settle's eventual
// val hold.
func prefixes(n int, nodes []int, mustPrecede func(a, b int) bool, budget int, render func([]int) []Step,
	judge func(orders [][]int) []int, same func([]int) string, complete func([]int) bool, proof *Proof) (best []Step, ok bool, tried int, cut bool) {
	total := len(nodes)
	level := [][]int{nil}
	recorded := map[string]bool{} // rejected prefixes in the proof (candidates the memo rejects share them)
	k := 0
	for ; k < total && tried < budget; k++ {
		var cands [][]int
		for _, p := range level {
			placed := make([]bool, total)
			for _, i := range p {
				placed[i] = true
			}
			for _, i := range nodes {
				if placed[i] {
					continue
				}
				// after the steps it must follow (a pending call need follow no
				// pending call: that one may have had no effect), and before
				// every node it must precede
				minimal := true
				for j := 0; j < total && minimal; j++ {
					if j < n && !placed[j] && j != i && mustPrecede(j, i) {
						minimal = false
					}
					if placed[j] && mustPrecede(i, j) {
						minimal = false
					}
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
		kept := map[string][]int{}
		for i, f := range fs {
			if f >= 0 {
				if p := cands[i][:min(f+1, len(cands[i]))]; proof != nil && !recorded[fmt.Sprint(p)] {
					recorded[fmt.Sprint(p)] = true
					proof.Rejected = append(proof.Rejected, render(p))
				}
				if complete(cands[i]) && f == len(cands[i])-1 && len(cands[i]) < total {
					next = append(next, cands[i]) // judged whole: a pending call after it may make it hold
				}
				continue
			}
			if complete(cands[i]) {
				return render(cands[i]), true, tried, false
			}
			if s := same(cands[i]); s != "" {
				if first, ok := kept[s]; ok {
					if proof != nil {
						proof.Merged = append(proof.Merged, [2][]Step{render(first), render(cands[i])})
					}
					continue // another prefix of these steps reached this state
				}
				kept[s] = cands[i]
			}
			next = append(next, cands[i])
		}
		if len(next) == 0 {
			return nil, false, tried, cut
		}
		level = next
	}
	if k == total {
		return nil, false, tried, cut // every order tried
	}
	return nil, false, tried, true
}
