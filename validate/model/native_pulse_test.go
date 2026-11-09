package model

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pulseObservation struct {
	bytes.Buffer
	seen chan struct{}
}

func (w *pulseObservation) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if bytes.Contains(b, []byte("RUN native verification")) {
		select {
		case w.seen <- struct{}{}:
		default:
		}
	}
	return n, err
}

func TestNativePulseBeforeSilentCommandCompletion(t *testing.T) {
	source := comparisonFakeSource(t, "echo diagnostic >&2\nwhile [ ! -f \"$0.release\" ]; do sleep 0.01; done\necho completed\n")
	w := &pulseObservation{seen: make(chan struct{}, 1)}
	ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: w, binding: "silent.yaml"})
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runQuintWithPulse(ctx, source.checker.Options.Quint, t.TempDir(), []string{"verify"}, time.Millisecond)
		done <- result{out, err}
	}()
	select {
	case <-w.seen:
	case r := <-done:
		t.Fatalf("command returned before pulse: %+v", r)
	case <-time.After(5 * time.Second):
		t.Fatal("silent command has no pulse")
	}
	select {
	case r := <-done:
		t.Fatalf("pulse ended command: %+v", r)
	default:
	}
	if err := os.WriteFile(source.checker.Options.Quint+".release", nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.out != "diagnostic\ncompleted\n" {
			t.Fatalf("captured output=%q err=%v", r.out, r.err)
		}
		if !strings.Contains(w.String(), "simulate silent.yaml: RUN native verification elapsed=") {
			t.Fatal(w.String())
		}
		before := w.String()
		// The returned command has joined its pulse; a second command owns a new sink.
		if _, err := runQuintWithPulse(t.Context(), source.checker.Options.Quint, t.TempDir(), []string{"test"}, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if w.String() != before {
			t.Fatal("pulse wrote after command returned")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command did not finish")
	}
}

func TestNativePulseCancellationStopsCommandAndWriter(t *testing.T) {
	source := comparisonFakeSource(t, "sleep 60\n")
	w := &pulseObservation{seen: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = context.WithValue(ctx, simulationProgressKey{}, &simulationProgress{w: w, binding: "cancel.yaml"})
	done := make(chan error, 1)
	go func() {
		_, err := runQuintWithPulse(ctx, source.checker.Options.Quint, t.TempDir(), []string{"verify"}, time.Millisecond)
		done <- err
	}()
	select {
	case <-w.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("no pulse")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop command and pulse")
	}
	// Buffer reads after the joined command/pulse are safe under the race detector.
	if !strings.Contains(w.String(), "RUN native verification") {
		t.Fatal(w.String())
	}
}

func TestNativePulseScopeAndWriteFailure(t *testing.T) {
	source := comparisonFakeSource(t, "sleep 0.02\necho result\n")
	for _, tc := range []struct {
		name, command string
		writer        io.Writer
	}{{"nil", "verify", nil}, {"sampling", "run", &bytes.Buffer{}}, {"failed-writer", "verify", brokenProgressWriter{}}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: tc.writer, binding: "scope.yaml"})
			out, err := runQuintWithPulse(ctx, source.checker.Options.Quint, t.TempDir(), []string{tc.command}, time.Millisecond)
			if err != nil || out != "result\n" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if b, ok := tc.writer.(*bytes.Buffer); ok && strings.Contains(b.String(), "RUN native") {
				t.Fatal("sampling got native pulse")
			}
		})
	}
	var w bytes.Buffer
	ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: &w, binding: "missing.yaml"})
	if _, err := runQuintWithPulse(ctx, "/missing/quint", t.TempDir(), []string{"verify"}, time.Millisecond); err == nil {
		t.Fatal("missing command succeeded")
	}
	if w.Len() != 0 {
		t.Fatalf("unstarted command emitted progress: %s", w.String())
	}
}

type exclusiveProgress struct{ active, writes atomic.Int32 }

func (w *exclusiveProgress) Write(b []byte) (int, error) {
	if w.active.Add(1) != 1 {
		panic("concurrent writes to caller's progress sink")
	}
	runtime.Gosched()
	w.writes.Add(1)
	w.active.Add(-1)
	return len(b), nil
}
func TestNativePulseSerializesConcurrentDiagnostics(t *testing.T) {
	w := &exclusiveProgress{}
	sink := diagnosticWriter{&nativeProgressWriter{w: w}}
	stop := startNativePulse(t.Context(), sink, &simulationProgress{binding: "parallel.yaml"}, time.Millisecond)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				sink.Write([]byte("diagnostic\n"))
			}
		})
	}
	wg.Wait()
	stop()
	stop()
	if w.active.Load() != 0 || w.writes.Load() < 800 {
		t.Fatalf("active=%d writes=%d", w.active.Load(), w.writes.Load())
	}
}

type blockedPulseWriter struct {
	once                       sync.Once
	entered, release, finished chan struct{}
}

func (w *blockedPulseWriter) Write(b []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.release
		close(w.finished)
	})
	return len(b), nil
}

func TestNativePulseStopDrainsPendingWrite(t *testing.T) {
	w := &blockedPulseWriter{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	stop := startNativePulse(t.Context(), w, &simulationProgress{binding: "drain.yaml"}, time.Millisecond)
	release := sync.OnceFunc(func() { close(w.release) })
	defer func() { release(); stop() }()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no pending progress write")
	}
	// A slow caller-owned writer may still be writing when the command exits.
	// Completion must join that write before its parent can emit END.
	timer := time.AfterFunc(20*time.Millisecond, release)
	defer timer.Stop()
	stop()
	select {
	case <-w.finished:
	default:
		t.Fatal("stop returned while the pulse was still writing")
	}
}
