package qobs_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// TestMemorySinkKeepsEveryStepInOrder records runs whose lengths fall on
// and around the sink's chunk boundaries and checks each step comes back
// once, whole and in its place, in a copy the caller owns.
func TestMemorySinkKeepsEveryStepInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, n := range []int{0, 1, 255, 256, 257, 511, 512, 513, 4096, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			t.Parallel()
			m := &qobs.MemorySink{}
			for i := range n {
				m.Record(ctx, &qtrace.Step{Action: "step", Seq: uint64(i), Args: map[string]any{"i": int64(i)}})
			}
			got := m.Steps()
			if n == 0 {
				if got != nil {
					t.Fatalf("an empty sink's Steps is %v, want nil", got)
				}
				return
			}
			if len(got) != n {
				t.Fatalf("%d steps back of %d recorded", len(got), n)
			}
			for i, s := range got {
				if s.Seq != uint64(i) || s.Args["i"] != int64(i) {
					t.Fatalf("step %d is seq %d (i=%v): out of order", i, s.Seq, s.Args["i"])
				}
			}
			// the copy is the caller's: changing it changes no later copy
			got[n-1].Action, got[0].Action = "changed", "changed"
			again := m.Steps()
			if again[0].Action != "step" || again[n-1].Action != "step" {
				t.Fatalf("a change to Steps' result reached the sink: %q, %q", again[0].Action, again[n-1].Action)
			}
			// a step after Steps goes after the others, and Reset empties
			m.Record(ctx, &qtrace.Step{Action: "last", Seq: uint64(n)})
			if after := m.Steps(); len(after) != n+1 || after[n].Action != "last" || after[n-1].Seq != uint64(n-1) {
				t.Fatalf("after Steps, a step recorded is not last of %d", len(after))
			}
			m.Reset()
			if s := m.Steps(); s != nil {
				t.Fatalf("after Reset, %d steps", len(s))
			}
			m.Record(ctx, &qtrace.Step{Action: "again"})
			if s := m.Steps(); len(s) != 1 || s[0].Action != "again" {
				t.Fatalf("after Reset and a step, %v", s)
			}
		})
	}
}

// TestMemorySinkConcurrentWriters records from several goroutines at
// once, reading while they write: no step is lost or duplicated, and each
// writer's steps keep the order it recorded them in (run with -race).
func TestMemorySinkConcurrentWriters(t *testing.T) {
	t.Parallel()
	const writers, each = 8, 1500 // 12000 steps: past several chunks
	ctx := context.Background()
	m := &qobs.MemorySink{}
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				m.Record(ctx, &qtrace.Step{Action: "step", Thread: fmt.Sprint(w), Seq: uint64(i)})
			}
		})
	}
	var stop atomic.Bool
	var reader sync.WaitGroup
	reader.Go(func() { // a reader's copies are consistent prefixes per writer
		for !stop.Load() {
			if err := perWriterOrder(m.Steps(), false); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Wait()
	stop.Store(true)
	reader.Wait()
	got := m.Steps()
	if len(got) != writers*each {
		t.Fatalf("%d steps back of %d recorded", len(got), writers*each)
	}
	if err := perWriterOrder(got, true); err != nil {
		t.Fatal(err)
	}
}

// perWriterOrder checks each writer's steps are 0, 1, 2, ... in order
// (all of them, if whole).
func perWriterOrder(steps []qtrace.Step, whole bool) error {
	next := map[string]uint64{}
	for _, s := range steps {
		if s.Seq != next[s.Thread] {
			return fmt.Errorf("writer %s's step %d came where its step %d belongs", s.Thread, s.Seq, next[s.Thread])
		}
		next[s.Thread]++
	}
	if whole {
		for w, n := range next {
			if n != 1500 {
				return fmt.Errorf("writer %s: %d steps", w, n)
			}
		}
	}
	return nil
}

// BenchmarkMemorySinkRun is one run's recording into a fresh MemorySink,
// as an exploration's runs each have, of n steps, read back once at the
// end: per run, the sink's allocations for a short run and a long one.
func BenchmarkMemorySinkRun(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				m := &qobs.MemorySink{}
				r := qobs.NewRecorder(m)
				for i := range n {
					r.Record(ctx, "step", nil, "i", i)
				}
				if len(m.Steps()) != n {
					b.Fatal("steps lost")
				}
			}
		})
	}
}
