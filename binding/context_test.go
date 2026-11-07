package binding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/itf"
)

const contextQuintVersion = "if [ \"$1\" = --version ]; then\nprintf '0.33.1\\n'\n: > \"$0.versioned\"\nexit 0\nfi\n"

func contextQuint(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses a POSIX shell")
	}
	// Fake-only commands get their own admission pool: unrelated native suites
	// must not consume the startup deadline used to prove running cancellation.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "quint")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+contextQuintVersion+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadModelCancelledBeforeLaunch(t *testing.T) {
	quint := contextQuint(t, ": > \"$0.started\"\nexit 1\n")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, expire := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer expire()
	for _, c := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", cancelled, context.Canceled},
		{"expired", expired, context.DeadlineExceeded},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadModel(c.ctx, quint, "testdata/typed.qnt", "typed")
			if !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
	for _, marker := range []string{".started", ".versioned"} {
		if _, err := os.Stat(quint + marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled load launched Quint (%s): %v", marker, err)
		}
	}
}

// The ambiguous-number recovery path must expose cancellation to its caller,
// leave the reader retryable, and release subprocess admission when it stops.
func TestTraceReaderCancelledTypecheckCanRetry(t *testing.T) {
	quint := contextQuint(t, "[ \"$1\" = typecheck ] || exit 99\nprintf '%s\\n' \"$1\" > \"$0.started\"\nsleep 30\n")
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.itf.json")
	if err := os.WriteFile(trace, []byte(`{"vars":["x"],"states":[{"x":{"s":{"#bigint":"1"},"e":{"#bigint":"15"},"c":[{"#bigint":"10"}]}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := TraceReader{Quint: quint, Spec: "testdata/typed.qnt", Module: "m"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Read(ctx, trace); done <- err }()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
started:
	for {
		select {
		case err := <-done:
			t.Fatalf("typecheck returned before cancellation: %v", err)
		case <-timeout.C:
			t.Fatal("typecheck did not start")
		case <-tick.C:
			if _, err := os.Stat(quint + ".started"); err == nil {
				break started
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if _, ok := errors.AsType[*itf.AmbiguousError](err); !ok {
			t.Fatalf("error lost original trace ambiguity: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled typecheck did not return")
	}
	if r.schema != nil {
		t.Fatal("cancelled load cached a schema")
	}
	// Succeed on the next launch, then remove Quint to verify schema reuse.
	if err := os.WriteFile(quint, []byte("#!/bin/sh\n"+contextQuintVersion+"[ \"$1\" = typecheck ] || exit 99\n: > \"$0.retried\"\ncat > \"$4\" <<'JSON'\n"+`{"modules":[{"name":"m","declarations":[{"kind":"var","name":"x","typeAnnotation":{"kind":"int"}}]}]}`+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(t.Context(), trace); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if _, err := os.Stat(quint + ".retried"); err != nil {
		t.Fatalf("retry did not reach typecheck: %v", err)
	}
	if err := os.Remove(quint); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(t.Context(), trace); err != nil {
		t.Fatalf("cached schema failed: %v", err)
	}
	if _, err := r.Read(ctx, trace); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached schema ignored cancellation: %v", err)
	}
}
