package qenv_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Duplicate display names remain distinguishable by captured incarnation and
// creation index. A restarted older process must not sort ahead of a younger
// same-name process merely because it was created first.
func TestSettleDownIdentityOrderIncludesIncarnations(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	var processes []*qenv.Process
	for _, name := range []string{"z", "a", "a", "a", "a"} {
		processes = append(processes, s.Process(name, false, func(context.Context) {}))
	}
	for index, restarts := range []int{0, 2, 0, 1, 1} {
		for range restarts {
			processes[index].Crash()
			processes[index].Restart()
		}
	}
	for _, process := range processes {
		process.Crash()
	}
	first, ok := errors.AsType[*qenv.Unsettled](s.Settle(time.Second))
	if !ok {
		t.Fatal("settlement did not retain the required down process identities")
	}
	want := []qenv.ProcessIdentity{
		{Name: "a", Incarnation: 1, Index: 3},
		{Name: "a", Incarnation: 2, Index: 4},
		{Name: "a", Incarnation: 2, Index: 5},
		{Name: "a", Incarnation: 3, Index: 2},
		{Name: "z", Incarnation: 1, Index: 1},
	}
	if !slices.Equal(first.DownProcesses, want) {
		t.Fatalf("down identities = %+v, want name/incarnation/index order %+v", first.DownProcesses, want)
	}
	for _, process := range processes {
		process.Restart()
	}
	if !slices.Equal(first.DownProcesses, want) {
		t.Fatal("later restarts changed the captured identity snapshot")
	}
	for _, process := range processes {
		process.Crash()
	}
	second, ok := errors.AsType[*qenv.Unsettled](s.Settle(time.Second))
	if !ok {
		t.Fatal("second settlement lost down process identities")
	}
	newWant := slices.Clone(want)
	for index := range newWant {
		newWant[index].Incarnation++
	}
	if !slices.Equal(second.DownProcesses, newWant) || !slices.Equal(first.DownProcesses, want) {
		t.Fatalf("settlement snapshots were not independent: first=%+v second=%+v", first.DownProcesses, second.DownProcesses)
	}
}
