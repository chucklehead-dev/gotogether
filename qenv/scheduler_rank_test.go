package qenv

import "testing"

// Every rank in the merged queued/predicate-ready tasks selects the same task
// as sorting all runnable IDs, including candidates on either side of queues.
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
		for rank, id := range want {
			if got := s.pick(rank, candidates).id; got != id {
				t.Fatalf("state %d rank %d: task %d, want merged IDs %v", state, rank, got, want)
			}
		}
	}
}
