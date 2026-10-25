package qtrace

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// orderOf renders traces as their actors, steps and pending calls in
// order, and the issues, for comparisons.
func orderOf(traces []*Trace, issues []Issue) string {
	var b strings.Builder
	for _, t := range traces {
		fmt.Fprintf(&b, "%s:", t.Actor)
		for _, s := range t.Steps {
			fmt.Fprintf(&b, " %s/%d", s.Recorder, s.Seq)
		}
		b.WriteString(" pending")
		for _, s := range t.Pending {
			fmt.Fprintf(&b, " %s/%d", s.Recorder, s.Seq)
		}
		fmt.Fprintf(&b, " issues %v\n", t.Issues)
	}
	fmt.Fprintf(&b, "issues %v", issues)
	return b.String()
}

// Reconstruct's traces are the same in whatever order the steps arrive
// (a collector's, a merge of files): its actors by name, each recorder's
// steps by seq however its clock ran, a thread's in program order, ties
// of time broken by recorder and seq, pending calls by time, a cycle
// broken at its earliest step. Each is a sort a map's order or the
// input's would otherwise decide.
func TestReconstructIgnoresInputOrder(t *testing.T) {
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	steps := []Step{
		// two recorders whose steps share their times: by recorder, then seq
		{Action: "w", Recorder: "b", Seq: 1, Actor: "x", Time: ms(10)},
		{Action: "w", Recorder: "a", Seq: 1, Actor: "x", Time: ms(10)},
		{Action: "w", Recorder: "a", Seq: 2, Actor: "x", Time: ms(10)},
		// a clock that ran backwards: seq order, not time's
		{Action: "w", Recorder: "c", Seq: 1, Actor: "x", Time: ms(30)},
		{Action: "w", Recorder: "c", Seq: 2, Actor: "x", Time: ms(20)},
		// a thread whose clock ran backwards: program order
		{Action: "w", Recorder: "d", Thread: "t", Seq: 1, Actor: "y", Time: ms(50)},
		{Action: "w", Recorder: "d", Thread: "t", Seq: 2, Actor: "y", Time: ms(40)},
		{Action: "w", Recorder: "d", Thread: "t", Seq: 3, Actor: "y", Time: ms(45)},
		// pending calls, invoked out of their seq order
		{Action: "p", Recorder: "e", Seq: 2, Actor: "y", Time: ms(5), Invocation: true},
		{Action: "p", Recorder: "e", Seq: 1, Actor: "y", Time: ms(8), Invocation: true},
		// an actor whose name sorts first
		{Action: "w", Recorder: "f", Seq: 1, Actor: "a", Time: ms(1)},
		// order keys that contradict their thread: a cycle
		{Action: "z", Recorder: "g", Thread: "u", Seq: 1, Actor: "z", Time: ms(1), Order: map[string]int64{"k": 2}},
		{Action: "z", Recorder: "g", Thread: "u", Seq: 2, Actor: "z", Time: ms(2), Order: map[string]int64{"k": 1}},
	}
	want := orderOf(Reconstruct(steps))
	for _, s := range []string{"a: f/1", "x: a/1 a/2 b/1 c/1 c/2", "y: d/1 d/2 d/3 pending e/2 e/1", "cycle"} {
		if !strings.Contains(want, s) {
			t.Errorf("want %q in\n%s", s, want)
		}
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for range 50 {
		in := slices.Clone(steps)
		rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
		if got := orderOf(Reconstruct(in)); got != want {
			t.Fatalf("shuffled, the traces differ:\n%s\nwant\n%s", got, want)
		}
	}
}

// A recorder's steps given out of their seq order have no gap: the gap
// check reads them in seq order.
func TestReconstructGapOfShuffledSeqs(t *testing.T) {
	steps := []Step{{Action: "w", Recorder: "r", Seq: 3, Actor: "x"}, {Action: "w", Recorder: "r", Seq: 1, Actor: "x"}, {Action: "w", Recorder: "r", Seq: 2, Actor: "x"}}
	traces, _ := Reconstruct(steps)
	for _, i := range traces[0].Issues {
		if i.Kind == "gap" {
			t.Errorf("a gap in seqs 3, 1, 2: %v", i)
		}
	}
}
