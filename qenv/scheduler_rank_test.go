package qenv

import (
	"slices"
	"testing"
)

// Runnable IDs and every selected rank match the independent creation-order
// enumeration, including empty, full and interleaved queue/candidate streams.
func TestPickRanksAgainstMergedIDs(t *testing.T) {
	const tasks = 7
	states := 1
	for range tasks {
		states *= 3 // each ID is absent, queued, or predicate-ready
	}
	for state := range states {
		s := &Sim{}
		var candidates []*task
		var want []int
		remaining := state
		for id := 1; id <= tasks; id++ {
			task := &task{id: id, queued: remaining%3 == 1}
			s.tasks = append(s.tasks, task)
			if remaining%3 != 0 {
				want = append(want, id)
			}
			if remaining%3 == 2 {
				candidates = append(candidates, task)
			}
			remaining /= 3
		}
		s.runq.grow(tasks, s)
		if got := s.runnableIDs(len(want), candidates); !slices.Equal(got, want) {
			t.Fatalf("state %d: runnable IDs %v, want %v", state, got, want)
		}
		for rank, id := range want {
			if got := s.pick(rank, candidates).id; got != id {
				t.Fatalf("state %d rank %d: task %d, want merged IDs %v", state, rank, got, want)
			}
		}
	}
}
