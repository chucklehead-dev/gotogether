package connect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestReadTracesCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, files := range [][]string{nil, {"does-not-exist.itf.json"}} {
		if _, err := ReadTraces(ctx, Config{}, files); !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadTraces = %v, want cancellation before reading files", err)
		}
	}
}

// Generation has already finished here: cancellation must reach the separate
// typecheck needed to decode a native Rust integer, without becoming a mismatch.
func TestReadTracesCancelsSchemaRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Quint command is a POSIX shell script")
	}
	// Keep unrelated native Quint jobs out of this fake command's startup bound.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	spec := filepath.Join(dir, "model.qnt")
	if err := os.WriteFile(spec, []byte("module model {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	quint := filepath.Join(dir, "quint")
	if err := os.WriteFile(quint, []byte("#!/bin/sh\nprintf started > started\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ReadTraces(ctx, Config{Quint: quint, Spec: spec}, []string{"../itf/testdata/bignumber.rust.itf.json"})
		done <- err
	}()
	started := filepath.Join(dir, "started")
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("trace reader returned before schema recovery started: %v", err)
		case <-ctx.Done():
			t.Fatal("schema recovery did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("schema recovery = %v, want cancellation", err)
		}
		if _, ok := errors.AsType[*StepError](err); ok {
			t.Fatalf("schema recovery cancellation reported a model mismatch: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled schema recovery did not stop")
	}
}
