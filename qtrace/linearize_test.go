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
func bruteLinearizable(steps []Step) bool {
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
