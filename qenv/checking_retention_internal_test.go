package qenv

import (
	"context"
	"testing"
	"time"
)

// Inspect the retained backing storage rather than relying on nondeterministic
// garbage collection: finished tasks must leave neither live entries nor tail
// references in the scheduler's diagnostic roster.
func TestCheckingReleasesFinishedTaskReferences(t *testing.T) {
	s := New(Nominal{})
	s.Check = true
	defer s.Close()
	for range 3 {
		completed := 0
		for range 4 {
			s.Spawn("short", func(context.Context) { completed++ })
		}
		storage := s.checking
		if err := s.Run(time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.selfCheck()
		if completed != 4 || len(s.checking) != 0 {
			t.Fatalf("completed=%d diagnostic roster=%d", completed, len(s.checking))
		}
		for index, retained := range storage {
			if retained != nil {
				t.Fatalf("diagnostic backing slot %d retains a finished task", index)
			}
		}
	}
}
