package validate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCacheLockCancellationLeavesOwnerLock(t *testing.T) {
	t.Setenv("GOTOGETHER_CACHE", t.TempDir())
	const key = "0123456789abcdef"
	lock := cachePath("typecheck", key) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	waiting := &cacheWaitContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		unlock, _, ok := cacheLock(waiting, "typecheck", key)
		unlock()
		if ok {
			t.Error("cancelled lock returned entry")
		}
		close(done)
	}()
	<-waiting.waiting
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled cache lock did not return")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("cancelled waiter removed owner's lock: %v", err)
	}
}

// Done is first called by cacheLock after encountering the owner's lock.
type cacheWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *cacheWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestQuintVersionCancellationCanRetry(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	bin := filepath.Join(t.TempDir(), "quint")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n: > \"$0.started\"\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan string, 1)
	go func() { done <- quintVersion(ctx, bin) }()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
started:
	for {
		select {
		case v := <-done:
			t.Fatalf("version returned %q before cancellation", v)
		case <-timeout.C:
			t.Fatal("version command did not start")
		case <-tick.C:
			if _, err := os.Stat(bin + ".started"); err == nil {
				break started
			}
		}
	}
	cancel()
	select {
	case v := <-done:
		if v != "" {
			t.Fatalf("cancelled version = %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("version cancellation did not return")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 0.33.retry\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if v := quintVersion(t.Context(), bin); v != "0.33.retry" {
		t.Fatalf("retry version = %q", v)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if v := quintVersion(t.Context(), bin); v != "0.33.retry" {
		t.Fatalf("cached version = %q", v)
	}
}
