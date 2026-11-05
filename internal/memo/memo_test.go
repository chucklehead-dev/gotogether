package memo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	synctest.Test(t, func(t *testing.T) {
		var m Map[string, int]
		started, release := make(chan struct{}), make(chan struct{})
		releaseOwner := sync.OnceFunc(func() { close(release) })
		defer releaseOwner()
		owner := make(chan error, 1)
		go func() {
			_, err := m.DoContext(t.Context(), "k", func() (int, error) { close(started); <-release; return 7, nil })
			owner <- err
		}()
		<-started
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		waiter := make(chan error, 1)
		go func() {
			_, err := m.DoContext(ctx, "k", func() (int, error) { t.Error("waiter computed while owner active"); return 0, nil })
			waiter <- err
		}()
		// Both callers are durably blocked: the owner on release, the waiter
		// on the owner's result or its own real context cancellation.
		synctest.Wait()
		select {
		case err := <-waiter:
			t.Fatalf("waiter returned before cancellation: %v", err)
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case err := <-waiter:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("waiter: %v", err)
			}
		default:
			t.Fatal("cancelled waiter remained blocked on owner")
		}
		select {
		case err := <-owner:
			t.Fatalf("waiter cancellation released owner: %v", err)
		default:
		}
		releaseOwner()
		synctest.Wait()
		if err := <-owner; err != nil {
			t.Fatal(err)
		}
		if v, err := m.DoContext(t.Context(), "k", func() (int, error) { t.Error("lost cached value"); return 0, nil }); err != nil || v != 7 {
			t.Fatalf("cached = %d,%v", v, err)
		}
	})
}

func TestDoContextRetriesCancelledOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var m Map[string, int]
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		owner := make(chan error, 1)
		go func() {
			_, err := m.DoContext(ctx, "k", func() (int, error) { close(started); <-ctx.Done(); return 0, errors.New("process killed") })
			owner <- err
		}()
		<-started
		var retries int
		waiter := make(chan struct {
			value int
			err   error
		}, 1)
		go func() {
			v, err := m.DoContext(t.Context(), "k", func() (int, error) { retries++; return 9, nil })
			waiter <- struct {
				value int
				err   error
			}{v, err}
		}()
		// This is a live waiter on the owner's computation, not a fresh call
		// after cancellation evicted that entry.
		synctest.Wait()
		select {
		case result := <-waiter:
			t.Fatalf("waiter returned before owner cancellation: %+v", result)
		default:
		}
		cancel()
		synctest.Wait()
		if err := <-owner; !errors.Is(err, context.Canceled) {
			t.Fatalf("owner: %v", err)
		}
		result := <-waiter
		if result.err != nil || result.value != 9 || retries != 1 {
			t.Fatalf("waiter = %d,%v, computed %d times; want 9,nil once", result.value, result.err, retries)
		}
		if v, err := m.DoContext(t.Context(), "k", func() (int, error) { t.Error("lost retried value"); return 0, nil }); err != nil || v != 9 {
			t.Fatalf("cached retry = %d,%v", v, err)
		}
	})
}
