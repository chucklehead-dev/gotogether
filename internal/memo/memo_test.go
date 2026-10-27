package memo

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
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
