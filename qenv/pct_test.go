package qenv_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A bug that needs one task to run 100 steps before another's one step:
// a uniformly random schedule never finds it (2^-100 a run), PCT finds it
// in about half the runs (B's priority above A's), and a PCT run's
// recorded choices replay it without PCT.
func TestPCTFindsADeepOrdering(t *testing.T) {
	run := func(c qenv.Chooser) (bug bool) {
		s := qenv.New(c)
		defer s.Close()
		done := false
		s.Spawn("a", func(ctx context.Context) { qenv.Yield(ctx); done = true })
		s.Spawn("b", func(ctx context.Context) {
			for range 100 {
				qenv.Yield(ctx)
			}
			bug = !done
		})
		if err := s.Run(0); err != nil {
			t.Fatal(err)
		}
		return bug
	}
	random, pct := 0, 0
	var found []qenv.Choice
	for seed := uint64(1); seed <= 100; seed++ {
		if run(qenv.NewSeeded(seed)) {
			random++
		}
		log := &qenv.ChoiceLog{C: qenv.NewPCT(qenv.NewSeeded(seed), seed, 1, 200)}
		if run(log) {
			pct++
			found = log.Choices
		}
	}
	if random > 0 || pct < 25 {
		t.Fatalf("found by %d random schedules and %d PCT ones of 100: want none and about half", random, pct)
	}
	if !run(qenv.Replay(found)) {
		t.Error("a PCT run's choices do not replay its bug")
	}
}

// A bug of depth 2: b must run between a's two steps, a's first step
// first. Strict priorities (depth 1) never make it: one task runs to its
// end before the other starts. A change point does, when Steps is about
// the run's length. If change points did nothing, depth 2 would be
// depth 1.
func TestPCTDepthTwo(t *testing.T) {
	found := func(depth int) int {
		n := 0
		for seed := uint64(1); seed <= 200; seed++ {
			s := qenv.New(qenv.NewPCT(qenv.NewSeeded(seed), seed, depth, 60))
			x, bug := 0, false
			s.Spawn("a", func(ctx context.Context) {
				x = 1
				for range 30 {
					qenv.Yield(ctx)
				}
				x = 2
			})
			s.Spawn("b", func(ctx context.Context) {
				for range 30 {
					qenv.Yield(ctx)
				}
				bug = x == 1
			})
			if err := s.Run(0); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if bug {
				n++
			}
		}
		return n
	}
	if d1, d2 := found(1), found(2); d1 != 0 || d2 < 20 {
		t.Errorf("found in %d runs at depth 1 and %d at depth 2, of 200: want none, and about a quarter", d1, d2)
	}
}

// A PCT run where one task is runnable in the middle (the other asleep)
// replays from its recorded choices: a step with one runnable task is no
// choice, and none is recorded for it.
func TestPCTReplaysWithOneRunnable(t *testing.T) {
	run := func(c qenv.Chooser) []string {
		s := qenv.New(c)
		defer s.Close()
		var order []string
		s.Spawn("sleeper", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			order = append(order, "sleeper")
			qenv.Yield(ctx)
			order = append(order, "sleeper")
		})
		s.Spawn("worker", func(ctx context.Context) {
			for range 3 {
				qenv.Yield(ctx)
				order = append(order, "worker")
			}
			qenv.Sleep(ctx, 2*time.Second)
			order = append(order, "worker")
		})
		if err := s.Run(0); err != nil {
			t.Fatal(err)
		}
		return order
	}
	for seed := uint64(1); seed <= 20; seed++ {
		log := &qenv.ChoiceLog{C: qenv.NewPCT(qenv.NewSeeded(seed), seed, 2, 10)}
		want := run(log)
		rp := qenv.Replay(log.Choices)
		if got := run(rp); !slices.Equal(got, want) || rp.Err() != nil {
			t.Fatalf("seed %d: replayed %v (%v), ran %v", seed, got, rp.Err(), want)
		}
	}
}

// The native runtime schedules its own goroutines: PCT would pick none,
// so it is refused.
func TestPCTRefusedNatively(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "PCT") {
			t.Errorf("NewNative with PCT: %v", r)
		}
	}()
	qenv.NewNative(qenv.NewPCT(qenv.NewSeeded(1), 1, 2, 10), nil)
}
