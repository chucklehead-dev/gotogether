package memo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Callers asking for one key at once share one computation.
func TestOnceComputesOncePerKey(t *testing.T) {
	var m Map[string, int]
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if v := m.Once("k", func() int { calls.Add(1); return 7 }); v != 7 {
				t.Errorf("Once = %d, want 7", v)
			}
		})
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("computed %d times, want 1", n)
	}
	if v := m.Once("other", func() int { return 8 }); v != 8 {
		t.Fatalf("another key: %d, want 8", v)
	}
}

// Do keeps a value, and computes again after an error.
func TestDoRetriesAfterAnError(t *testing.T) {
	var m Map[int, string]
	bad := errors.New("bad")
	if _, err := m.Do(1, func() (string, error) { return "", bad }); err != bad {
		t.Fatalf("first Do: %v, want bad", err)
	}
	v, err := m.Do(1, func() (string, error) { return "ok", nil })
	if err != nil || v != "ok" {
		t.Fatalf("Do after an error: %q, %v; want ok", v, err)
	}
	v, err = m.Do(1, func() (string, error) { return "again", nil })
	if err != nil || v != "ok" {
		t.Fatalf("Do after a value: %q, %v; want the kept ok", v, err)
	}
}

func TestDoContextWaiterCancellation(t *testing.T) {
	var m Map[string, int]
	started, release := make(chan struct{}), make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		_, err := m.DoContext(t.Context(), "k", func() (int, error) { close(started); <-release; return 7, nil })
		owner <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	waiting := &waitContext{Context: ctx, waiting: make(chan struct{})}
	waiter := make(chan error, 1)
	go func() {
		_, err := m.DoContext(waiting, "k", func() (int, error) { t.Error("waiter computed while owner active"); return 0, nil })
		waiter <- err
	}()
	<-waiting.waiting
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter remained blocked on owner")
	}
	close(release)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if v, err := m.DoContext(t.Context(), "k", func() (int, error) { t.Error("lost cached value"); return 0, nil }); err != nil || v != 7 {
		t.Fatalf("cached = %d,%v", v, err)
	}
}

func TestDoContextRetriesCancelledOwner(t *testing.T) {
	var m Map[string, int]
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		_, err := m.DoContext(ctx, "k", func() (int, error) { close(started); <-ctx.Done(); return 0, errors.New("process killed") })
		owner <- err
	}()
	<-started
	// Obtain the owner's entry before cancellation to exercise a live waiter
	// on that computation, rather than just a fresh call after eviction.
	e := m.entry("k")
	waiting := &waitContext{Context: t.Context(), waiting: make(chan struct{})}
	waiter := make(chan int, 1)
	go func() {
		v, err := m.DoContext(waiting, "k", func() (int, error) { return 9, nil })
		if err != nil {
			t.Error(err)
		}
		waiter <- v
	}()
	<-waiting.waiting
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner: %v", err)
	}
	<-e.done
	if v := <-waiter; v != 9 {
		t.Fatalf("waiter = %d", v)
	}
}

// waitContext signals when DoContext has entered the waiter's select.
type waitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
