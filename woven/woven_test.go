package woven_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/woven"
)

// A waiting writer blocks new readers, as with sync.RWMutex: a reader that
// arrives while a writer waits gets the lock after the writer.
func TestWriterPreference(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var mu sync.RWMutex
	var order []string
	s.Spawn("reader1", func(ctx context.Context) {
		woven.RLock(&mu)
		qenv.Sleep(ctx, 10*time.Millisecond)
		woven.RUnlock(&mu)
	})
	s.Spawn("writer", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		woven.RWLock(&mu)
		order = append(order, "writer")
		woven.RWUnlock(&mu)
	})
	s.Spawn("reader2", func(ctx context.Context) {
		qenv.Sleep(ctx, 2*time.Millisecond)
		woven.RLock(&mu)
		order = append(order, "reader2")
		woven.RUnlock(&mu)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "writer" {
		t.Fatalf("order %v, want the writer before the reader that came while it waited", order)
	}
}
