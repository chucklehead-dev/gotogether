package qtrace

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"
)

// A mutant's detail names its step by recorder and seq (a converted
// span's by recorder alone), and a string value quoted.
func TestMutantDetails(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{(&Step{Action: "put", Recorder: "r", Seq: 3}).where(), "put (recorder r seq 3)"},
		{(&Step{Action: "put", Recorder: "r"}).where(), "put (recorder r)"},
		{fmtValue("x"), `"x"`},
		{fmtValue(int64(3)), "3"},
	} {
		if c.got != c.want {
			t.Errorf("%s, want %s", c.got, c.want)
		}
	}
}

// Mutations are made in one order, whatever order a map hands the
// recorders or a value's keys in: swaps by the place of their steps, and
// a map's drop takes out its first key, sorted.
func TestMutationsDeterministic(t *testing.T) {
	var log []Step
	for i, rec := range []string{"d", "c", "b", "a"} {
		for seq := uint64(1); seq <= 2; seq++ {
			log = append(log, Step{Action: fmt.Sprint("op", seq), Recorder: rec, Process: rec, Thread: rec, Actor: rec, Seq: seq, Time: time.Unix(int64(10*i)+int64(seq), 0)})
		}
	}
	val := Step{Action: "put", Recorder: "v", Process: "v", Seq: 1, Time: time.Unix(100, 0),
		Args: map[string]any{"m": map[string]any{"d": int64(4), "c": int64(3), "a": int64(1), "b": int64(2)}}}
	for range 10 {
		var at []int
		for _, m := range Mutations(log, MutateOps("swap")) {
			at = append(at, m.Index)
		}
		if !slices.Equal(at, []int{0, 2, 4, 6}) {
			t.Fatalf("swaps at %v, want [0 2 4 6]", at)
		}
		for _, m := range Mutations([]Step{val}, MutateOps("corrupt")) {
			if m.Change == "drop" {
				if got := slices.Sorted(maps.Keys(m.Steps[0].Args["m"].(map[string]any))); !slices.Equal(got, []string{"b", "c", "d"}) {
					t.Fatalf("drop kept %v, want [b c d]", got)
				}
			}
		}
	}
}

// A swap exchanges neighbours in a recorder's sequence, though the log
// has them out of order.
func TestSwapsInSeqOrder(t *testing.T) {
	step := func(seq uint64) Step {
		return Step{Action: fmt.Sprint("op", seq), Recorder: "r", Process: "r", Thread: "r", Actor: "A", Seq: seq, Time: time.Unix(int64(seq), 0)}
	}
	log := []Step{step(3), step(1), step(2)}
	ms := Mutations(log, MutateOps("swap"))
	if len(ms) != 2 {
		t.Fatalf("%d swaps, want 2: %v", len(ms), ms)
	}
	for _, m := range ms {
		var moved []uint64
		for i := range log {
			if m.Steps[i].Action != log[i].Action {
				moved = append(moved, log[i].Seq)
			}
		}
		if len(moved) != 2 || max(moved[0], moved[1])-min(moved[0], moved[1]) != 1 {
			t.Errorf("%s exchanged seqs %v, not neighbours", m, moved)
		}
	}
}

// A retime moves a step past the latest other actor's step in its
// window, in time, and places it just after that step, though the log
// is not in time order.
func TestRetimeOutOfTimeOrder(t *testing.T) {
	ms := func(n int) time.Time { return time.Unix(0, int64(n)*int64(time.Millisecond)) }
	step := func(actor string, seq uint64, at int) Step {
		return Step{Action: "x" + actor, Recorder: actor, Process: actor, Thread: actor, Actor: actor, Seq: seq, Time: ms(at)}
	}
	retimed := func(log []Step, i int) []Step {
		t.Helper()
		for _, m := range Mutations(log, MutateOps("retime")) {
			if m.Index == i {
				return m.Steps
			}
		}
		t.Fatalf("no retime of step %d", i)
		return nil
	}
	at := func(s Step, n int) Step { return with(s, func(s *Step) { s.Time = ms(n) }) }

	// A's first in 0..10ms, C's at 8ms and B's at 4ms in it: past C's
	a1, c1, b1, a2 := step("A", 1, 0), step("C", 1, 8), step("B", 1, 4), step("A", 2, 10)
	if got, want := retimed([]Step{a1, c1, b1, a2}, 0), []Step{c1, at(a1, 9), b1, a2}; !reflect.DeepEqual(got, want) {
		t.Errorf("past the latest:\n got %v\nwant %v", got, want)
	}
	// the step it moves past first in the log, the step itself third
	b1, c1 = step("B", 1, 5), step("C", 1, 20)
	if got, want := retimed([]Step{b1, c1, a1, a2}, 2), []Step{b1, with(a1, func(s *Step) { s.Time = ms(7).Add(500 * time.Microsecond) })}; !reflect.DeepEqual(got[:2], want) {
		t.Errorf("past the first in the log:\n got %v\nwant %v first", got, want)
	}
}
