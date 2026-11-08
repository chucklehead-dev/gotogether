package model

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

type liveProgress struct {
	mu   sync.Mutex
	b    strings.Builder
	seen chan struct{}
}

func (w *liveProgress) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Write(b)
	select {
	case w.seen <- struct{}{}:
	default:
	}
	return len(b), nil
}

func (w *liveProgress) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestSimulationProgressBeforeCancellation(t *testing.T) {
	source := comparisonFakeSource(t, "sleep 60 &\necho $! > \"$0.worker\"\necho 'native diagnostic' >&2\necho '[violation] forged partial output'\nwait\n")
	dir := filepath.Dir(source.checker.Binding.Spec)
	if err := os.WriteFile(source.checker.Binding.Spec, []byte("module source { var x: int\naction init = x' = 0\naction step = x' = x\nval safe = true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source.yaml")
	if err := os.WriteFile(file, []byte("spec: source.qnt\nmodule: source\ninvariants: [safe]\nsimulate: {holds: [safe]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &liveProgress{seen: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		rep *Report
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := Simulate(ctx, file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 8, Progress: w})
		done <- result{rep, err}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(w.text(), "[violation] forged partial output") {
		select {
		case <-w.seen:
		case r := <-done:
			t.Fatalf("returned before live diagnostics: %+v", r)
		case <-deadline.C:
			t.Fatal("native output buffered until completion")
		}
	}
	if text := w.text(); !strings.Contains(text, "START invariant safe") || !strings.Contains(text, "native diagnostic") {
		t.Fatal(text)
	}
	select {
	case r := <-done:
		t.Fatalf("blocking verifier returned: %+v", r)
	default:
	}
	cancel()
	select {
	case r := <-done:
		if r.rep != nil || !errors.Is(r.err, context.Canceled) {
			t.Fatalf("partial marker became evidence: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned verifier did not exit on cancellation")
	}
	text := w.text()
	for _, want := range []string{"START binding", "START prepare", "END prepare status=completed elapsed=", "END invariant safe status=canceled elapsed=", "END binding status=canceled elapsed="} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if runtime.GOOS == "linux" {
		pid, err := os.ReadFile(source.checker.Options.Quint + ".worker")
		if err != nil {
			t.Fatal(err)
		}
		// A killed orphan may await init's reap as a zombie, but must do no
		// more work after the canceled command's Wait has returned.
		statPath := "/proc/" + strings.TrimSpace(string(pid)) + "/stat"
		deadline := time.Now().Add(time.Second)
		for {
			stat, err := os.ReadFile(statPath)
			// procfs can lose the process after opening its stat file and
			// return ESRCH instead of ENOENT when init reaps the worker.
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
			if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("owned worker still running: %s", stat)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func TestVerifiedPossibleProgressAndDiagnosticScope(t *testing.T) {
	source := comparisonFakeSource(t, "echo 'native stderr' >&2\n"+nativeFakeScript(nativeCexJSON, nativeCexITF, 1, "native stdout [violation]"))
	var progress bytes.Buffer
	ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: &progress, binding: "one.yaml"})
	c := &possibleCheck{p: binding.Possible{Name: "reachable"}, when: "true", not: "not_reachable"}
	if err := quintPossibles(ctx, source.checker.Options.Quint, t.TempDir(), "unused.qnt", []*possibleCheck{c}, 1, 1, "1", 8, nil); err != nil {
		t.Fatal(err)
	}
	if !c.quintFound {
		t.Fatal("completed violation lost its witness")
	}
	for _, want := range []string{"simulate one.yaml: START verified-possible reachable", "native stdout", "native stderr", "END verified-possible reachable status=completed elapsed="} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("missing %q:\n%s", want, progress.String())
		}
	}
	before := progress.String()
	out, err := runQuint(t.Context(), source.checker.Options.Quint, t.TempDir(), []string{"verify"})
	if err == nil || !strings.Contains(out, "native stdout") || !strings.Contains(out, "native stderr") || !strings.Contains(out, "[violation]") {
		t.Fatalf("capture lost diagnostics: %q, %v", out, err)
	}
	if progress.String() != before {
		t.Fatal("progress escaped its simulation context")
	}
}

type brokenProgressWriter struct{}

func (brokenProgressWriter) Write([]byte) (int, error) { return 0, errors.New("closed progress sink") }

func TestDiagnosticWriteFailureDoesNotChangeNativeResult(t *testing.T) {
	source := comparisonFakeSource(t, "echo 'native stdout'\necho 'native stderr' >&2\n")
	ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: brokenProgressWriter{}})
	out, err := runQuint(ctx, source.checker.Options.Quint, t.TempDir(), []string{"verify"})
	if err != nil || !strings.Contains(out, "native stdout") || !strings.Contains(out, "native stderr") {
		t.Fatalf("progress sink changed result: %q, %v", out, err)
	}
}

func TestSimulationProgressCompletedAndSkipped(t *testing.T) {
	source := comparisonFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "native argv"))
	dir := filepath.Dir(source.checker.Binding.Spec)
	if err := os.WriteFile(source.checker.Binding.Spec, []byte("module source { var x: int\naction init = x' = 0\naction step = x' = x\nval safe = true\nval other = true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source.yaml")
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "skipped"}[skip], func(t *testing.T) {
			section := "simulate: {holds: [safe, other], verifySteps: 3}\n"
			if skip {
				section = "simulate: {verifySkip: 'not admitted'}\n"
			}
			if err := os.WriteFile(file, []byte("spec: source.qnt\nmodule: source\ninvariants: [safe, other]\n"+section), 0600); err != nil {
				t.Fatal(err)
			}
			var progress bytes.Buffer
			report, err := Simulate(t.Context(), file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 8, Progress: &progress})
			if err != nil || report == nil || report.Code != 0 {
				t.Fatalf("report=%+v error=%v", report, err)
			}
			text := progress.String()
			if skip {
				if report.Skipped != "not admitted" || len(report.Invariants) != 0 || !strings.Contains(text, "END binding status=skipped elapsed=") || strings.Contains(text, "START invariant") {
					t.Fatalf("skip became verification: %+v\n%s", report, text)
				}
				return
			}
			for _, want := range []string{"START invariant safe", "END invariant safe status=completed elapsed=", "START invariant other", "END invariant other status=completed elapsed=", "--max-steps 3", "END binding status=pass elapsed="} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q:\n%s", want, text)
				}
			}
			if len(report.Invariants) != 2 || report.Invariants[0].Bound.Steps != 3 || report.Invariants[1].Bound.Steps != 3 {
				t.Fatalf("progress changed bounded report: %+v", report)
			}
		})
	}
}

func TestSimulationProgressBindingLoadError(t *testing.T) {
	var progress bytes.Buffer
	report, err := Simulate(t.Context(), filepath.Join(t.TempDir(), "missing.yaml"), SimulateOptions{Progress: &progress})
	if report != nil || err == nil {
		t.Fatalf("report=%+v error=%v", report, err)
	}
	for _, want := range []string{"START binding", "START prepare", "END prepare status=error elapsed=", "END binding status=error elapsed="} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("missing %q:\n%s", want, progress.String())
		}
	}
}

type cancelAtProgress struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelAtProgress) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if strings.Contains(string(b), "START go-possible") {
		w.cancel()
	}
	return n, err
}

func TestSimulationGoPossibleProgress(t *testing.T) {
	skip.Quint(t)
	// Native verification is a fake even if a broken progress hook fails to
	// cancel. This test never starts Apalache or a long search.
	source := comparisonFakeSource(t, "echo 'unexpected verifier invocation'\n")
	a, err := New(t.Context(), "../testdata/graph.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	progress := &cancelAtProgress{cancel: cancel}
	report, err := a.Simulate(ctx, SimulateOptions{Quint: source.checker.Options.Quint, Progress: progress, Verify: 8, States: 10, Invariants: []string{}, Possibles: []binding.Possible{{Name: "positive", When: "positive"}}})
	if report != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("report=%+v error=%v", report, err)
	}
	text := progress.String()
	for _, want := range []string{"START prepare", "END prepare status=completed elapsed=", "START go-possible", "END go-possible status=canceled elapsed=", "END binding status=canceled elapsed="} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}
