package qtrace

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// op is a register operation over [from, to] in units of 10 ms (each
// beyond RealTimeSlack), from a client recorder of its own.
func op(client string, seq uint64, action string, v int64, from, to int) Step {
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	s := Step{Action: action, Recorder: client, Seq: seq, Thread: client, Actor: "r", Process: "p",
		Invoked: at(from), Time: at(to), Args: map[string]any{}, Obs: map[string]any{}}
	if action == "write" {
		s.Args["v"] = v
	} else {
		s.Obs["v"] = v
	}
	return s
}

// register is the model: a read sees the last write. It returns, for each
// order, the index of its first read that does not, or -1.
func register(orders [][]Step) []int {
	out := make([]int, len(orders))
	for i, o := range orders {
		out[i] = -1
		var v int64
		for j, s := range o {
			if s.Action == "write" {
				v = s.Args["v"].(int64)
			} else if s.Obs["v"] != v {
				out[i] = j
				break
			}
		}
	}
	return out
}

// registerStates is register, with each accepted order's state: the
// value a read would see.
func registerStates(orders [][]Step) ([]int, []string) {
	fs := register(orders)
	st := make([]string, len(orders))
	for i, o := range orders {
		if fs[i] < 0 {
			var v int64
			for _, s := range o {
				if s.Action == "write" {
					v = s.Args["v"].(int64)
				}
			}
			st[i] = fmt.Sprint(v)
		}
	}
	return fs, st
}

// A read that overlaps a write and saw it completes first: its recording
// order is wrong, and a single move finds the order that is right.
func TestLinearizeOverlap(t *testing.T) {
	steps := []Step{op("a", 1, "write", 1, 0, 10), op("b", 1, "read", 1, 1, 5)}
	if f := register([][]Step{steps}); f[0] != -1 {
		t.Fatal("the steps as given should conform")
	}
	o, ok, tried, _ := Linearize(steps, 10, register)
	if !ok || o[0].Action != "write" || tried != 2 {
		t.Errorf("ok %v after %d orders: %v", ok, tried, o)
	}
	if o[0].Recorder != "linearized:r" || o[1].Seq != 2 || o[0].Thread != o[1].Thread {
		t.Errorf("not re-sequenced onto one thread: %+v", o)
	}
}

// A read invoked after a write completed that does not see it is stale:
// real time forbids every order that would explain it.
func TestLinearizeStale(t *testing.T) {
	steps := []Step{op("a", 1, "write", 1, 0, 2), op("b", 1, "read", 0, 3, 4), op("a", 2, "write", 2, 3, 8)}
	o, ok, _, cut := Linearize(steps, 10, register)
	if ok || cut {
		t.Errorf("a stale read linearized, or the search was cut short with room left: %v (cut %v)", o, cut)
	}
	// with room for the first order only, the search is cut short
	if _, ok, _, cut := Linearize(steps, 1, register); ok || !cut {
		t.Errorf("a budget of 1: ok %v cut %v, want a search cut short", ok, cut)
	}
}

// Fixes compose: each move gets further, and two overlapping pairs need
// two moves.
func TestLinearizeTwoWindows(t *testing.T) {
	steps := []Step{
		op("a", 1, "write", 1, 0, 10), op("b", 1, "read", 1, 1, 5),
		op("a", 2, "write", 2, 20, 30), op("b", 2, "read", 2, 21, 25),
	}
	if _, ok, tried, _ := Linearize(steps, 20, register); !ok || tried > 6 {
		t.Errorf("ok %v after %d orders", ok, tried)
	}
}

// An order key that puts a step before one that completed before it was
// invoked contradicts real time; a step whose invocation is unknown has
// no bound.
func TestRealTimeIssue(t *testing.T) {
	a, b := op("a", 1, "write", 1, 0, 2), op("b", 1, "write", 2, 3, 4)
	a.Order, b.Order = map[string]int64{"c": 2}, map[string]int64{"c": 1}
	traces, _ := Reconstruct([]Step{a, b})
	if is := traces[0].Issues; len(is) != 1 || is[0].Kind != "realtime" || !strings.Contains(is[0].Msg, "contradict real time") {
		t.Errorf("issues %v", is)
	}
	b.Invoked = time.Time{}
	if traces, _ := Reconstruct([]Step{a, b}); len(traces[0].Issues) != 0 {
		t.Errorf("an unknown invocation bounded the order: %v", traces[0].Issues)
	}
}

// The complete search is cut by the budget alone: five concurrent
// operations no order explains are refuted given the budget to try every
// order (a beam of 64 prefixes once cut it whatever the budget), and no
// verdict with less.
func TestLinearizeCompleteSearchRefutes(t *testing.T) {
	var steps []Step
	for i := range 5 {
		steps = append(steps, op(string(rune('a'+i)), 1, "write", int64(i), 0, 10))
	}
	// proper prefixes conform, every full order fails at its last step
	none := func(orders [][]Step) []int {
		out := make([]int, len(orders))
		for i, o := range orders {
			out[i] = -1
			if len(o) == len(steps) {
				out[i] = len(o) - 1
			}
		}
		return out
	}
	checked := 0
	counting := func(orders [][]Step) []int { checked += len(orders); return none(orders) }
	if _, ok, tried, cut := Linearize(steps, 100000, counting); ok || cut {
		t.Errorf("with the budget to try every order: ok %v, cut %v after %d", ok, cut, tried)
	} else if checked >= tried {
		// the guided search's orders, each failing at its last step, accepted
		// every prefix of four steps they hold: the complete search's level
		// of four is decided without the checker
		t.Errorf("checked %d of %d orders tried: the memo of accepted prefixes decided none", checked, tried)
	}
	// cut short, the complete search is no verdict, whatever the guided
	// search's stop said
	if _, ok, _, cut := Linearize(steps, 50, none); ok || !cut {
		t.Errorf("with a budget of 50: ok %v, cut %v", ok, cut)
	}
}

// Linearize against brute force: random register histories of up to six
// operations on three clients, each client's operations in turn, whose
// verdict is decided by enumerating every order real time and the clients
// allow. Linearize, with the budget to finish, must find an order exactly
// when one exists, and the order it returns must be one of those. (The
// toolkit's own mutation testing found most of Linearize's code
// unconstrained by its unit tests.)
func TestLinearizeAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	saved := 0
	defer func() {
		if saved == 0 {
			t.Error("merging prefixes by state saved no order in any trial: the property never compared it")
		}
	}()
	for trial := range 400 {
		var steps []Step
		next := map[string]int{}
		written := []int64{0}
		n := 2 + rng.IntN(5)
		for i := range n {
			c := string(rune('a' + rng.IntN(3)))
			from := next[c] + rng.IntN(3)
			to := from + 1 + rng.IntN(4)
			next[c] = to + 1
			if rng.IntN(2) == 0 {
				v := int64(i + 1)
				written = append(written, v)
				steps = append(steps, op(c, uint64(len(steps)+1), "write", v, from, to))
			} else {
				steps = append(steps, op(c, uint64(len(steps)+1), "read", written[rng.IntN(len(written))], from, to))
			}
		}
		// each recorder's seqs from 1, in its order (op takes one seq a step)
		seqs := map[string]uint64{}
		for i := range steps {
			seqs[steps[i].Recorder]++
			steps[i].Seq = seqs[steps[i].Recorder]
		}
		want := bruteLinearizable(steps)
		got, ok, _, cut := Linearize(steps, 100000, register)
		if cut {
			t.Fatalf("trial %d: cut with a budget of 100000", trial)
		}
		if ok != want {
			t.Fatalf("trial %d: Linearize ok %v, brute force %v, for\n%v", trial, ok, want, steps)
		}
		if ok && (register([][]Step{got})[0] >= 0 || !allowed(steps, got)) {
			t.Fatalf("trial %d: the order returned is no valid order: %v", trial, got)
		}
		// with the register's state, the complete search merges prefixes:
		// the same verdict, in no more orders
		sgot, sok, stried, scut := LinearizeStates(steps, 100000, registerStates)
		_, _, ptried, _ := Linearize(steps, 100000, register)
		if scut || sok != want || sok && !allowed(steps, sgot) {
			t.Fatalf("trial %d: by state ok %v (cut %v), brute force %v", trial, sok, scut, want)
		}
		if stried > ptried {
			t.Fatalf("trial %d: by state %d orders, by prefix %d", trial, stried, ptried)
		}
		saved += ptried - stried
		// a small budget may cut the search, but a verdict it gives (no
		// order: the guided search stopped, or the complete one proved it)
		// must be right
		for _, budget := range []int{1, 3, 10} {
			_, ok, _, cut := Linearize(steps, budget, register)
			if !cut && ok != want {
				t.Fatalf("trial %d, budget %d: a verdict of ok %v, brute force %v, for\n%v", trial, budget, ok, want, steps)
			}
		}
	}
}

// before reports whether a must precede b: the same client's earlier
// operation, or one that completed (beyond the slack) before b began.
func mustBefore(a, b Step) bool {
	if a.Recorder == b.Recorder {
		return a.Seq < b.Seq
	}
	return a.Time.Add(RealTimeSlack).Before(b.Invoked)
}

// allowed reports whether order (Linearize's, re-sequenced) is a
// permutation of steps that respects mustBefore, matching steps by their
// interval and action.
func allowed(steps, order []Step) bool {
	if len(order) != len(steps) {
		return false
	}
	pos := make([]int, len(steps))
	used := make([]bool, len(steps))
	for k, o := range order {
		found := false
		for i, s := range steps {
			if !used[i] && s.Action == o.Action && s.Invoked.Equal(o.Invoked) && s.Time.Equal(o.Time) && fmt.Sprint(s.Args, s.Obs) == fmt.Sprint(o.Args, o.Obs) {
				used[i], pos[i], found = true, k, true
				break
			}
		}
		if !found {
			return false
		}
	}
	for i := range steps {
		for j := range steps {
			if i != j && mustBefore(steps[i], steps[j]) && pos[i] > pos[j] {
				return false
			}
		}
	}
	return true
}

// bruteLinearizable enumerates every order mustBefore allows for one the
// register accepts.
func bruteLinearizable(steps []Step) bool { return bruteWith(steps, mustBefore) }

// bruteWith enumerates every order prec allows for one the register
// accepts.
func bruteWith(steps []Step, mustBefore func(a, b Step) bool) bool {
	n := len(steps)
	order := make([]Step, 0, n)
	used := make([]bool, n)
	var rec func() bool
	rec = func() bool {
		if len(order) == n {
			return register([][]Step{order})[0] < 0
		}
		for i := range steps {
			if used[i] {
				continue
			}
			ok := true
			for j := range steps {
				if !used[j] && j != i && mustBefore(steps[j], steps[i]) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			used[i] = true
			order = append(order, steps[i])
			if rec() {
				return true
			}
			order, used[i] = order[:len(order)-1], false
		}
		return false
	}
	return rec()
}

// orderBy reports whether order (Linearize's) is a permutation of steps
// that respects prec, matching steps by their Source.
func orderBy(steps, order []Step, prec func(a, b Step) bool) bool {
	pos := map[string]int{}
	for k, o := range order {
		pos[o.Source] = k
	}
	if len(pos) != len(steps) || len(order) != len(steps) {
		return false
	}
	for _, a := range steps {
		for _, b := range steps {
			if _, ok := pos[a.Source]; !ok {
				return false
			}
			if a.Source != b.Source && prec(a, b) && pos[a.Source] > pos[b.Source] {
				return false
			}
		}
	}
	return true
}

// compareBrute runs Linearize on steps, from Reconstruct, against the
// brute force under prec, the precedence written from its definition.
func compareBrute(t *testing.T, trial int, steps []Step, prec func(a, b Step) bool) bool {
	t.Helper()
	traces, _ := Reconstruct(steps)
	if len(traces) != 1 {
		t.Fatalf("trial %d: %d traces", trial, len(traces))
	}
	tr := traces[0].Steps
	want := bruteWith(tr, prec)
	got, ok, _, cut := Linearize(tr, 100000, register)
	if cut {
		t.Fatalf("trial %d: cut with a budget of 100000", trial)
	}
	if ok != want {
		var b strings.Builder
		for _, s := range tr {
			fmt.Fprintf(&b, "\n  %s %v%v [%d,%d] order %v", s.Source, s.Args, s.Obs, s.Invoked.UnixMilli(), s.Time.UnixMilli(), s.Order)
		}
		fmt.Fprintf(&b, "\nLinearize's order:")
		for _, s := range got {
			fmt.Fprintf(&b, " %s", s.Source)
		}
		t.Fatalf("trial %d: Linearize ok %v, brute force %v, for%s", trial, ok, want, b.String())
	}
	if ok && (register([][]Step{got})[0] >= 0 || !orderBy(tr, got, prec)) {
		t.Fatalf("trial %d: the order returned is no valid order: %v", trial, got)
	}
	return want
}

// One recorder's concurrent calls (each on a thread of its own, its
// invocation recorded by qobs.Invoke), at one virtual instant, as a
// simulation's calls take no time: a call completed before another's
// invocation record precedes it, and nothing else orders them. Linearize
// agrees with every order that rule allows.
func TestLinearizeAgainstBruteForceRecords(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	at := time.Unix(0, 0)
	yes, no := 0, 0
	for trial := range 400 {
		k := 2 + rng.IntN(4)
		// the events: each call's invocation, then its completion, interleaved
		type event struct {
			call int
			done bool
		}
		var events []event
		open := []int{}
		next := 0
		for next < k || len(open) > 0 {
			if next < k && (len(open) == 0 || rng.IntN(2) == 0) {
				events = append(events, event{next, false})
				open = append(open, next)
				next++
			} else {
				i := rng.IntN(len(open))
				events = append(events, event{open[i], true})
				open = append(open[:i], open[i+1:]...)
			}
		}
		written := []int64{0}
		inv := map[int]uint64{}
		var steps []Step
		for seq, e := range events {
			s := Step{Recorder: "r", Seq: uint64(seq + 1), Thread: fmt.Sprint("t", e.call), Actor: "x", Process: "p", Time: at,
				Args: map[string]any{}, Obs: map[string]any{}, Source: fmt.Sprint("call", e.call)}
			if !e.done {
				s.Action, s.Invocation = "invoke", true
				inv[e.call] = s.Seq
			} else {
				s.Call = inv[e.call]
				if rng.IntN(2) == 0 {
					s.Action, s.Args["v"] = "write", int64(e.call+1)
					written = append(written, int64(e.call+1))
				} else {
					s.Action, s.Obs["v"] = "read", written[rng.IntN(len(written))]
				}
			}
			steps = append(steps, s)
		}
		prec := func(a, b Step) bool { return a.Seq < b.Call } // a recorded before b's invocation
		if compareBrute(t, trial, steps, prec) {
			yes++
		} else {
			no++
		}
	}
	if yes == 0 || no == 0 {
		t.Errorf("%d linearizable, %d not: the property needs both", yes, no)
	}
}

// Nodes that order their steps by a domain clock (order.lamport), each
// step also with its interval: a step precedes another of a larger clock
// value, or one invoked after it completed. Linearize agrees with every
// order those allow.
func TestLinearizeAgainstBruteForceDomainClock(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	yes, no, contradictory := 0, 0, 0
	for trial := range 400 {
		var steps []Step
		written := []int64{0}
		n := 2 + rng.IntN(5)
		for i := range n {
			from := rng.IntN(6)
			s := op(fmt.Sprint("n", i), 1, "read", 0, from, from+1+rng.IntN(4))
			s.Source = fmt.Sprint("s", i)
			if rng.IntN(2) == 0 {
				s.Action, s.Args["v"], s.Obs = "write", int64(i+1), map[string]any{}
				written = append(written, int64(i+1))
			} else {
				s.Obs["v"] = written[rng.IntN(len(written))]
			}
			s.Order = map[string]int64{"lamport": int64(rng.IntN(3))}
			steps = append(steps, s)
		}
		prec := func(a, b Step) bool {
			return a.Order["lamport"] < b.Order["lamport"] || a.Time.Add(RealTimeSlack).Before(b.Invoked)
		}
		// clocks that contradict real time are bad data, which Linearize
		// reports (a realtime issue) rather than judges: a domain clock
		// that respects causality respects real time
		if cyclic(steps, prec) {
			contradictory++
			continue
		}
		if compareBrute(t, trial, steps, prec) {
			yes++
		} else {
			no++
		}
	}
	if yes == 0 || no == 0 {
		t.Errorf("%d linearizable, %d not: the property needs both", yes, no)
	}
	t.Logf("%d linearizable, %d not, %d contradictory (skipped)", yes, no, contradictory)
}

// cyclic reports whether prec orders steps in a cycle.
func cyclic(steps []Step, prec func(a, b Step) bool) bool {
	state := make([]int, len(steps)) // 0 new, 1 on the path, 2 done
	var visit func(i int) bool
	visit = func(i int) bool {
		state[i] = 1
		for j := range steps {
			if j != i && prec(steps[i], steps[j]) {
				if state[j] == 1 || state[j] == 0 && visit(j) {
					return true
				}
			}
		}
		state[i] = 2
		return false
	}
	for i := range steps {
		if state[i] == 0 && visit(i) {
			return true
		}
	}
	return false
}

// The complete search merges two prefixes of the same steps only when
// check says their states are equal: with every order's state its own,
// it tries as many orders as with no states; with one state for all, it
// tries fewer. (Merging by the steps alone would merge the first too.)
func TestLinearizeStatesMergesOnlyEqualStates(t *testing.T) {
	// three concurrent writes, then a read no order explains: the complete
	// search must try every order of the writes
	steps := []Step{op("a", 1, "write", 1, 0, 10), op("b", 1, "write", 2, 0, 10), op("c", 1, "write", 3, 0, 10), op("d", 1, "read", 9, 20, 30)}
	with := func(state func(o []Step) string) int {
		_, ok, tried, cut := linearize(steps, 10000, func(orders [][]Step) ([]int, []string) {
			fs := register(orders)
			st := make([]string, len(orders))
			for i, o := range orders {
				st[i] = state(o)
			}
			return fs, st
		}, false, nil)
		if ok || cut {
			t.Fatalf("ok %v, cut %v: no order reads 9", ok, cut)
		}
		return tried
	}
	none := with(func([]Step) string { return "" })
	distinct := with(func(o []Step) string {
		var b strings.Builder
		for _, s := range o {
			fmt.Fprint(&b, s.Args["v"], ",")
		}
		return b.String()
	})
	equal := with(func([]Step) string { return "one" })
	if distinct != none || equal >= none {
		t.Errorf("orders tried: %d with no states, %d with distinct ones, %d with one: want the first two equal, the last fewer", none, distinct, equal)
	}
}

// ForcedOrder says there is one order exactly when the orders mustBefore
// allows are one, and returns it; on the brute force's random histories,
// both answers come up.
func TestForcedOrderAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	seen := map[bool]int{}
	for trial := range 400 {
		var steps []Step
		next := map[string]int{}
		n := 1 + rng.IntN(5)
		for i := range n {
			c := string(rune('a' + rng.IntN(3)))
			from := next[c] + rng.IntN(3)
			to := from + 1 + rng.IntN(4)
			next[c] = to + 1
			steps = append(steps, op(c, uint64(i+1), "write", int64(i+1), from, to))
		}
		seqs := map[string]uint64{}
		for i := range steps {
			seqs[steps[i].Recorder]++
			steps[i].Seq = seqs[steps[i].Recorder]
		}
		orders := countOrders(steps, mustBefore, 2)
		got, ok := ForcedOrder(steps)
		if ok != (orders == 1) {
			t.Fatalf("trial %d: ForcedOrder %v, %d orders allowed, for\n%v", trial, ok, orders, steps)
		}
		if ok && !allowed(steps, got) {
			t.Fatalf("trial %d: the forced order is not allowed: %v", trial, got)
		}
		seen[ok]++
	}
	if seen[true] == 0 || seen[false] == 0 {
		t.Errorf("forced %d, not %d: the histories never tried both", seen[true], seen[false])
	}
}

// countOrders counts the orders of steps prec allows, up to limit.
func countOrders(steps []Step, prec func(a, b Step) bool, limit int) int {
	n, count := len(steps), 0
	used := make([]bool, n)
	var rec func(placed int)
	rec = func(placed int) {
		if count >= limit {
			return
		}
		if placed == n {
			count++
			return
		}
		for i := range steps {
			if used[i] {
				continue
			}
			ok := true
			for j := range steps {
				if !used[j] && j != i && prec(steps[j], steps[i]) {
					ok = false
					break
				}
			}
			if ok {
				used[i] = true
				rec(placed + 1)
				used[i] = false
			}
		}
	}
	rec(0)
	return count
}

// A verdict of no order, from a wrong oracle, is confirmed by its proof
// only if it is right: a history some order linearizes (brute force says)
// whose proof the true oracle accepts would be a wrong verdict confirmed.
// The wrong oracles reject a conforming candidate, or merge prefixes of
// different states; a right one's proof always checks out.
func TestLinearizeProofAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	caught := map[int]int{}
	confirms := func(p *Proof) bool {
		var orders [][]Step
		orders = append(orders, p.Rejected...)
		for _, m := range p.Merged {
			orders = append(orders, m[0], m[1])
		}
		fs, st := registerStates(orders)
		for i := range p.Rejected {
			if fs[i] < 0 {
				return false
			}
		}
		for j := range p.Merged {
			a, b := len(p.Rejected)+2*j, len(p.Rejected)+2*j+1
			if fs[a] >= 0 || fs[b] >= 0 || st[a] != st[b] {
				return false
			}
		}
		return true
	}
	for trial := range 600 {
		var steps []Step
		next := map[string]int{}
		written := []int64{0}
		for i := range 2 + rng.IntN(5) {
			c := string(rune('a' + rng.IntN(3)))
			from := next[c] + rng.IntN(3)
			to := from + 1 + rng.IntN(4)
			next[c] = to + 1
			if rng.IntN(2) == 0 {
				v := int64(i + 1)
				written = append(written, v)
				steps = append(steps, op(c, uint64(len(steps)+1), "write", v, from, to))
			} else {
				steps = append(steps, op(c, uint64(len(steps)+1), "read", written[rng.IntN(len(written))], from, to))
			}
		}
		seqs := map[string]uint64{}
		for i := range steps {
			seqs[steps[i].Recorder]++
			steps[i].Seq = seqs[steps[i].Recorder]
		}
		want := bruteLinearizable(steps)
		_, ok, _, cut, proof := LinearizeProof(steps, 100000, registerStates)
		if ok != want || cut || !ok && (proof == nil || !confirms(proof)) {
			t.Fatalf("trial %d: the true oracle's verdict ok %v (cut %v), brute force %v, or its proof does not check out", trial, ok, cut, want)
		}
		wrong := rng.Uint64()
		oracles := []func([][]Step) ([]int, []string){
			func(orders [][]Step) ([]int, []string) { // rejects some conforming candidates
				fs, st := registerStates(orders)
				for i, o := range orders {
					if fs[i] < 0 && len(o) > 0 && (wrong>>uint(len(o)%64))&1 == 1 {
						fs[i], st[i] = len(o)-1, ""
					}
				}
				return fs, st
			},
			func(orders [][]Step) ([]int, []string) { // one state for all
				fs, st := registerStates(orders)
				for i := range st {
					if fs[i] < 0 {
						st[i] = "s"
					}
				}
				return fs, st
			},
		}
		for k, oracle := range oracles {
			// the complete search alone: the guided one finds most orders
			// first, and its verdict of no order is the complete search's
			proof := &Proof{}
			_, ok, _, cut := linearize(steps, 100000, oracle, false, proof)
			if ok || cut || !want {
				continue
			}
			if confirms(proof) {
				t.Fatalf("trial %d, oracle %d: a wrong verdict of no order confirmed by its proof, for\n%v", trial, k, steps)
			}
			caught[k]++
		}
	}
	if caught[0] == 0 || caught[1] == 0 {
		t.Errorf("wrong verdicts caught, by oracle: %v: an oracle that gave none never tested a proof", caught)
	}
	t.Logf("wrong verdicts caught by their proofs, by oracle: %v", caught)
}
