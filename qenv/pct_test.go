package qenv_test

import (
	"context"
	"testing"

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

// With a change point, PCT finds a bug of depth 2: b must run between a's
// two steps, a's first step first.
func TestPCTDepthTwo(t *testing.T) {
	found := 0
	for seed := uint64(1); seed <= 200; seed++ {
		s := qenv.New(qenv.NewPCT(qenv.NewSeeded(seed), seed, 2, 60))
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
			found++
		}
	}
	if found == 0 {
		t.Error("PCT of depth 2 never found the ordering in 200 runs")
	}
	t.Logf("found in %d of 200 runs", found)
}
