package qtrace

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// An order's memo key is one per order: without its separators [1, 12]
// and [11, 2] were one key, and a candidate took another's verdict.
func TestOrderKeyIsInjective(t *testing.T) {
	if orderKey([]int{1, 12}) == orderKey([]int{11, 2}) {
		t.Error("[1, 12] and [11, 2] share a key")
	}
	if orderKey([]int{1}) == orderKey([]int{1, 0}) || orderKey(nil) == orderKey([]int{0}) {
		t.Error("an order and its extension share a key")
	}
}

// The memo is what makes a search's candidates cheap: Linearize never
// asks its check about an order it asked about before, nor about one that
// extends a prefix the check rejected (an extension fails there too), nor
// about an incomplete order an answer accepted (a prefix of an order that
// failed later). A
// memo that forgot either still found the same verdicts, so only this
// says it works. Random register histories, as the brute-force test's.
func TestLinearizeAsksNothingTwice(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	repeats := 0
	for trial := range 300 {
		var steps []Step
		next := map[string]int{}
		written := []int64{0}
		for i := range 2 + rng.IntN(5) {
			c := string(rune('a' + rng.IntN(3)))
			from := next[c] + rng.IntN(3)
			to := from + 1 + rng.IntN(4)
			next[c] = to + 1
			var s Step
			if rng.IntN(2) == 0 {
				v := int64(i + 1)
				written = append(written, v)
				s = op(c, 0, "write", v, from, to)
			} else {
				s = op(c, 0, "read", written[rng.IntN(len(written))], from, to)
			}
			s.Args["id"] = i // the step's identity in a rendered order
			steps = append(steps, s)
		}
		seqs := map[string]uint64{}
		for i := range steps {
			seqs[steps[i].Recorder]++
			steps[i].Seq = seqs[steps[i].Recorder]
		}
		id := func(o []Step) string {
			var b strings.Builder
			for _, s := range o {
				fmt.Fprintf(&b, "%v,", s.Args["id"])
			}
			return b.String()
		}
		asked, rejected, accepted := map[string]bool{}, map[string]bool{}, map[string]bool{}
		check := func(orders [][]Step) ([]int, []string) {
			fs, st := registerStates(orders)
			for i, o := range orders {
				k := id(o)
				if asked[k] {
					repeats++
					t.Errorf("trial %d: order %s asked twice", trial, k)
				}
				for l := 1; l < len(o); l++ {
					if rejected[id(o[:l])] {
						t.Errorf("trial %d: order %s asked, though its prefix %s was rejected", trial, k, id(o[:l]))
					}
				}
				if accepted[k] && len(o) < len(steps) {
					t.Errorf("trial %d: order %s asked, though an earlier answer accepted it", trial, k)
				}
				asked[k] = true
				upTo := len(o)
				if fs[i] >= 0 {
					upTo = fs[i]
					if fs[i] < len(o)-1 {
						rejected[id(o[:fs[i]+1])] = true
					}
				}
				for l := 1; l <= upTo; l++ {
					accepted[id(o[:l])] = true
				}
			}
			return fs, st
		}
		linearizeStates(steps, 100000, check)
		if t.Failed() {
			return
		}
	}
}

// The guided search's moves fix an order in one step each: the failing
// step moved to the very front (a read of the initial value recorded
// after a write it overlapped), and an earlier step moved to the front (a
// read after two overlapping writes saw the one recorded first, and real
// time keeps the read where it is). Without the move the complete search
// found the order, after more candidates.
func TestLinearizeMovesToTheFront(t *testing.T) {
	for _, x := range []struct {
		name  string
		steps []Step
	}{
		{"the failing step first", []Step{op("a", 1, "write", 1, 0, 4), op("b", 1, "read", 0, 1, 6)}},
		// recorded by completion: the write of 2, the write of 1, the read
		{"an earlier step first", []Step{op("a", 1, "write", 1, 0, 10), op("b", 1, "write", 2, 1, 5), op("c", 1, "read", 2, 12, 14)}},
	} {
		// tried 2: the recorded order failed, and the first move conformed
		o, ok, tried, _ := linearizeBasic(x.steps, 100, register)
		if !ok || tried != 2 {
			t.Errorf("%s: ok %v after %d orders, want the first move's: %v", x.name, ok, tried, o)
		}
	}
}

// An order that contradicts real time is reported at most five times: a
// thread whose clock ran backwards contradicts it at every step, and the
// report said so once each, without bound, or (miscounted) once.
func TestRealTimeIssuesCapped(t *testing.T) {
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	var steps []Step
	for k := 1; k <= 8; k++ {
		// each invoked after the next one (in program order) completed
		steps = append(steps, Step{Action: "tick", Recorder: "r", Thread: "t", Seq: uint64(k), Actor: "x",
			Invoked: at(100 - 10*k), Time: at(101 - 10*k)})
	}
	traces, _ := Reconstruct(steps)
	n := 0
	for _, i := range traces[0].Issues {
		if i.Kind == "realtime" && i.Severity == "error" {
			n++
		}
	}
	if n != 5 {
		t.Errorf("%d real-time errors, want 5 (capped): %v", n, traces[0].Issues)
	}
}
