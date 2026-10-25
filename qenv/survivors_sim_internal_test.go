package qenv

import (
	"container/heap"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// live counts the simulations open: taskOf looks a goroutine up only
// while one is, so a task's context.Background() finds its simulation.
// New counts one more and Close one less; a simulation not counted would
// leave the first simulation of a process unable to find its tasks.
func TestLiveCountsOpenSimulations(t *testing.T) {
	before := live.Load()
	s := New(Nominal{})
	if got := live.Load(); got != before+1 {
		t.Errorf("after New: %d open, want %d", got, before+1)
	}
	s.Close()
	if got := live.Load(); got != before {
		t.Errorf("after Close: %d open, want %d", got, before)
	}
}

// Check makes the scheduler verify its invariants, and each broken one
// panics naming it: a scheduler bug is caught at the step it happens,
// not as a wrong run later. Each case breaks one invariant by hand (and
// mends it, so the simulation closes).
func TestSelfCheckNamesEachBrokenInvariant(t *testing.T) {
	for _, c := range []struct {
		want    string
		corrupt func(s *Sim, q *task) (mend func())
	}{
		{"is queued but finished", func(s *Sim, q *task) func() {
			q.done = true
			return func() { q.done = false }
		}},
		{"is both queued and waiting", func(s *Sim, q *task) func() {
			q.waiting = true
			return func() { q.waiting = false }
		}},
		{"run queue holds 1 tasks, 0 are flagged queued", func(s *Sim, q *task) func() {
			q.queued = false
			return func() { q.queued = true }
		}},
		{"is held for its stalled process and also queued", func(s *Sim, q *task) func() {
			q.held = true
			s.held = append(s.held, q)
			return func() { q.held, s.held = false, nil }
		}},
		{"waits until 1s with no live timer", func(s *Sim, q *task) func() {
			s.unqueue(q)
			q.waiting, q.deadline = true, s.now.Add(time.Second)
			return func() { q.waiting, q.deadline = false, time.Time{}; s.enqueue(q) }
		}},
		{"is past its deadline", func(s *Sim, q *task) func() {
			s.unqueue(q)
			q.waiting, q.deadline = true, s.now.Add(-time.Second)
			heap.Push(&s.timers, timer{at: q.deadline, id: q.id, t: q, gen: q.gen})
			return func() { q.waiting, q.deadline, s.timers = false, time.Time{}, nil; s.enqueue(q) }
		}},
		{"time went backwards", func(s *Sim, q *task) func() {
			s.lastNow = s.now.Add(time.Second)
			return func() { s.lastNow = time.Time{} }
		}},
	} {
		s := New(Nominal{})
		s.Spawn("q", func(context.Context) {})
		q := s.tasks[0]
		mend := c.corrupt(s, q)
		got := fmt.Sprint(svCatch(s.selfCheck))
		mend()
		if !strings.Contains(got, "scheduler invariant: ") || !strings.Contains(got, c.want) {
			t.Errorf("%s: selfCheck panicked with %q", c.want, got)
		}
		s.selfCheck() // mended
		s.Close()
	}
}

// Run checks the invariants at every step when Check is on, and only
// then: a broken one panics with Check, and goes unchecked without.
func TestRunChecksOnlyWithCheck(t *testing.T) {
	for _, check := range []bool{true, false} {
		s := New(Nominal{})
		s.Check = check
		s.Spawn("q", func(context.Context) {})
		s.lastNow = s.now.Add(time.Second) // time went backwards
		got := svCatch(func() { _ = s.Run(time.Second) })
		s.lastNow = time.Time{}
		if caught := strings.Contains(fmt.Sprint(got), "time went backwards"); caught != check {
			t.Errorf("Check %v: Run panicked with %v", check, got)
		}
		s.Close()
	}
}

// svCatch runs f and returns what it panicked with (nil if it did not).
func svCatch(f func()) (r any) {
	defer func() { r = recover() }()
	f()
	return nil
}
