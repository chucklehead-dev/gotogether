package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// This compiler deliberately holds the first attempt. Every later attempt emits
// valid IR, so an owner's cancellation must release its independent waiters and
// allow a successful retry, rather than memoizing a killed process.
func evaluatorContextFixture(t *testing.T) (*Checker, func(string) (*binding.EvalModule, error), string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("compiler fixture uses POSIX shell")
	}
	// This fixture tests compiler ownership and cancellation, not admission
	// against unrelated model checks in other test binaries. A successful retry
	// must have its own pool, just as the compiler process is local to this test.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "quint")
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 0.33.context-test; exit 0; fi
printf 'compile\n' >> "$0.attempts"
if mkdir "$0.first" 2>/dev/null; then
 : > "$0.started"
 while [ ! -f "$0.release" ]; do sleep 0.01; done
fi
printf '%s\n' '{"modules":[{"name":"m","declarations":[]}],"main":"m"}'
`
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	// Also release a broken implementation's child when the watchdog fails.
	t.Cleanup(func() { _ = os.WriteFile(bin+".release", nil, 0600) })
	if err := os.WriteFile(filepath.Join(dir, "m.qnt"), []byte("module m {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Checker{Binding: &binding.Binding{Dir: dir, Path: filepath.Join(dir, "b.yaml"), Spec: "m.qnt", Module: "m"}, Options: Options{Quint: bin}}
	make := func(string) (*binding.EvalModule, error) {
		return &binding.EvalModule{Name: "m", Init: "init", Text: "module m {}\n"}, nil
	}
	return c, make, bin
}

func awaitEvaluator(t *testing.T, operation string, done <-chan *evaluator) *evaluator {
	t.Helper()
	select {
	case e := <-done:
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("evaluator did not return: %s", operation)
		return nil
	}
}

func awaitEvaluatorFile(t *testing.T, path string) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("compiler did not start")
		case <-tick.C:
		}
	}
}

func TestEvaluatorContextOwnerWaiterAndRetry(t *testing.T) {
	t.Run("ordinary", testEvaluatorContextOwnerWaiterAndRetry)
	t.Run("unrelated-admission-occupied", func(t *testing.T) {
		switch runtime.GOOS {
		case "linux", "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		default:
			t.Skip("independent admission pools require Unix file locks")
		}
		// These independent processes retain the inherited pool after the compiler
		// fixture changes this process's cache directory. Neither may prevent its
		// canceled owner from releasing its live waiter to retry.
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		t.Setenv("HOME", t.TempDir())
		for range 2 {
			ctx, cancel := context.WithCancel(t.Context())
			cmd := quintcmd.Command(ctx, "sleep", "", "3600")
			if err := cmd.Start(); err != nil {
				cancel()
				t.Fatal(err)
			}
			t.Cleanup(func() { cancel(); _ = cmd.Wait() })
		}
		testEvaluatorContextOwnerWaiterAndRetry(t)
	})
}

func testEvaluatorContextOwnerWaiterAndRetry(t *testing.T) {
	t.Setenv("GOTOGETHER_CACHE", "off")
	c, makeModule, bin := evaluatorContextFixture(t)
	ownerCtx, cancelOwner := context.WithCancel(t.Context())
	defer cancelOwner()
	owner := make(chan *evaluator, 1)
	go func() { owner <- evaluatorOf(ownerCtx, c, makeModule) }()
	awaitEvaluatorFile(t, bin+".started")

	waiterCtx, cancelWaiter := context.WithCancel(t.Context())
	defer cancelWaiter()
	waiterReady := &cacheWaitContext{Context: waiterCtx, waiting: make(chan struct{})}
	waiter := make(chan *evaluator, 1)
	go func() { waiter <- evaluatorOf(waiterReady, c, makeModule) }()
	<-waiterReady.waiting
	cancelWaiter()
	if e := awaitEvaluator(t, "canceled waiter", waiter); !errors.Is(e.err, context.Canceled) {
		t.Fatalf("waiter error: %v", e.err)
	}
	select {
	case e := <-owner:
		t.Fatalf("canceling waiter stopped owner: %v", e.err)
	default:
	}

	liveReady := &cacheWaitContext{Context: t.Context(), waiting: make(chan struct{})}
	live := make(chan *evaluator, 1)
	go func() { live <- evaluatorOf(liveReady, c, makeModule) }()
	<-liveReady.waiting
	cancelOwner()
	if e := awaitEvaluator(t, "canceled owner", owner); !errors.Is(e.err, context.Canceled) {
		t.Fatalf("owner error: %v", e.err)
	}
	recovered := awaitEvaluator(t, "successful live retry", live)
	if recovered.err != nil || recovered.m == nil {
		t.Fatalf("live waiter did not retry: %v", recovered.err)
	}
	attempts, err := os.ReadFile(bin + ".attempts")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(attempts), "compile\n"); n != 2 {
		t.Fatalf("compiler attempts=%d, want canceled owner and successful retry", n)
	}

	// A canceled request cannot even use a warm cache; the live cache remains good.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if e := evaluatorOf(canceled, c, makeModule); !errors.Is(e.err, context.Canceled) {
		t.Fatalf("warm canceled request: %v", e.err)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if e := evaluatorOf(t.Context(), c, makeModule); e.err != nil || e.m != recovered.m {
		t.Fatalf("successful retry was not cached: %v", e.err)
	}
}

func TestCompileEvaluatorContextBeforeWork(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, release := context.WithDeadline(t.Context(), time.Time{})
	defer release()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
	}{{"canceled", canceled, context.Canceled}, {"expired", expired, context.DeadlineExceeded}} {
		t.Run(tc.name, func(t *testing.T) {
			// No checker or module may be needed to refuse an already ended request.
			if e := compileEval(tc.ctx, nil, nil, ""); !errors.Is(e.err, tc.err) {
				t.Fatalf("ended compile: %v", e.err)
			}
			if e := evaluatorOf(tc.ctx, nil, nil); !errors.Is(e.err, tc.err) {
				t.Fatalf("ended evaluator: %v", e.err)
			}
		})
	}
}
