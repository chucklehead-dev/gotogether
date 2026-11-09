package qnemesis

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPauseProcessesErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if err := PauseProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	p := &Process{Name: "not-started"}
	if err := PauseProcesses(ctx, p); err == nil || !strings.Contains(err.Error(), "not-started") || !strings.Contains(err.Error(), "start it before") {
		t.Fatalf("unstarted process: %v", err)
	}
	dir := t.TempDir()
	running := child(t, dir, "running")
	if err := running.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "running", 1)
	err := pauseProcesses(ctx, func(context.Context, int) error { return context.Canceled }, running)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("confirmation cancellation: %v", err)
	}
	// A confirmation error leaves the already signaled child owned by its
	// caller, which can resume it and still use its lifecycle normally.
	if err := running.Resume(ctx); err != nil {
		t.Fatal(err)
	}
}
