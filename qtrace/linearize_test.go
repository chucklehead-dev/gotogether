package qtrace

import (
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
// order, and no verdict without it. A beam of 64 prefixes once made them
// no verdict whatever the budget.
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
	if _, ok, _, cut := Linearize(steps, 50, none); ok || !cut {
		t.Errorf("with a budget of 50: ok %v, cut %v", ok, cut)
	}
}
