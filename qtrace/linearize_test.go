package qtrace

import (
	"strings"
	"testing"
	"time"
)

// op is a register operation over [from, to] in milliseconds, from a
// client recorder of its own.
func op(client string, seq uint64, action string, v int64, from, to int) Step {
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
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
	o, ok, tried := Linearize(steps, 10, register)
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
	o, ok, _ := Linearize(steps, 10, register)
	if ok {
		t.Errorf("a stale read linearized: %v", o)
	}
}

// Fixes compose: each move gets further, and two overlapping pairs need
// two moves.
func TestLinearizeTwoWindows(t *testing.T) {
	steps := []Step{
		op("a", 1, "write", 1, 0, 10), op("b", 1, "read", 1, 1, 5),
		op("a", 2, "write", 2, 20, 30), op("b", 2, "read", 2, 21, 25),
	}
	if _, ok, tried := Linearize(steps, 20, register); !ok || tried > 6 {
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
