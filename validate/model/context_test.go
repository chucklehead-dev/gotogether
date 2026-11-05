package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestModelPreCanceledOperationsHaveNoResult(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if a, err := New(ctx, missing, validate.Options{}); a != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("New=%v,%v", a, err)
	}
	if report, err := Simulate(ctx, missing, SimulateOptions{}); report != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Simulate=%v,%v", report, err)
	}
	// Cancellation must win before an invalid/missing binding is inspected.
	a := &Analyzer{c: &modelChecker{Checker: &validate.Checker{}}}
	if graph, err := a.Graph(ctx, GraphSearch{States: 1}); graph != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Graph=%v,%v", graph, err)
	}
	if possible, err := a.Possible(ctx, nil, PossibleSearch{States: 1}); possible != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Possible=%v,%v", possible, err)
	}
}

// Cancellation triggered by an observed analysis checkpoint avoids a timing race
// between a fast graph and a timer. Done and Err still share one real context.
type cancelAnalysisAfterChecks struct {
	context.Context
	cancel    context.CancelFunc
	remaining atomic.Int64
}

func (c *cancelAnalysisAfterChecks) Err() error {
	if c.remaining.Add(-1) == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestModelExpiredDeadlineIsOperational(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Hour))
	defer cancel()
	report, err := Simulate(ctx, filepath.Join(t.TempDir(), "missing.yaml"), SimulateOptions{Graph: true, States: 1})
	if report != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired analysis=%v,%v", report, err)
	}
}

func TestModelAnalyzerRequestCancellationDoesNotPoisonReuse(t *testing.T) {
	skip.Quint(t)
	a, err := New(t.Context(), "../testdata/graph.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(t.Context())
	graph, err := a.Graph(ctx, GraphSearch{})
	if err != nil || !graph.Complete {
		t.Fatalf("first graph=%v,%v", graph, err)
	}
	cancel()
	// A completed graph must retain the reusable evaluator, not the request view.
	if _, err := graph.e.Model.Eval(map[string]qeval.Value{}, graph.graph.States[0], graph.e.Module.Possible["positive"]); err != nil {
		t.Fatalf("graph retained canceled evaluator: %v", err)
	}
	if failed, err := a.Graph(ctx, GraphSearch{States: 1}); failed != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled graph=%v,%v", failed, err)
	}
	active, stop := context.WithCancel(t.Context())
	defer stop()
	checkpoints := &cancelAnalysisAfterChecks{Context: active, cancel: stop}
	checkpoints.remaining.Store(50)
	if partial, err := a.Graph(checkpoints, GraphSearch{}); partial != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted graph became bounded evidence: %v,%v", partial, err)
	}
	// A canceled lazy state index must not turn a reachable state into a later
	// evaluator-disagreement report.
	state := graph.graph.States[len(graph.graph.States)-1]
	values := map[string]itf.Value{}
	for name, value := range state {
		literal, err := itf.ParseQuint(qeval.String(value))
		if err != nil {
			t.Fatal(err)
		}
		values[name[strings.LastIndex(name, ":")+1:]] = literal
	}
	trace := &itf.Trace{States: []itf.State{{Vars: values}}}
	indexContext, stopIndex := context.WithCancel(t.Context())
	defer stopIndex()
	indexCheckpoints := &cancelAnalysisAfterChecks{Context: indexContext, cancel: stopIndex}
	indexCheckpoints.remaining.Store(3)
	if _, _, err := graph.Lacks(indexCheckpoints, trace); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted index error=%v", err)
	}
	if graph.keys != nil {
		t.Fatal("canceled state index published partial keys")
	}
	if index, detail, err := graph.Lacks(t.Context(), trace); err != nil || index >= 0 {
		t.Fatalf("retry invented a missing state: index=%d detail=%s error=%v", index, detail, err)
	}
	again, err := a.Graph(t.Context(), GraphSearch{})
	if err != nil || !again.Complete || again.States != graph.States {
		t.Fatalf("reused graph=%v,%v", again, err)
	}
}

// Native process/file waits are deliberately outside synctest. The marker
// establishes that the managed command is live before cancellation.
func cancelModelCommand(t *testing.T, quint string, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case err := <-done:
			t.Fatalf("returned before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("managed Quint command did not start")
		case <-tick.C:
			if _, err := os.Stat(quint + ".started"); err == nil {
				break wait
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed Quint command ignored cancellation")
	}
}

func TestModelSamplingCancellationDiscardsForgedViolation(t *testing.T) {
	source := comparisonFakeSource(t, "echo '[violation] forged partial output'\n: > \"$0.started\"\nsleep 30\n")
	dir := filepath.Dir(source.checker.Binding.Spec)
	if err := os.WriteFile(source.checker.Binding.Spec, []byte("module source { var x: int\naction init = x' = 0\naction step = x' = x\nval safe = true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source.yaml")
	if err := os.WriteFile(file, []byte("spec: source.qnt\nmodule: source\ninvariants: [safe]\nsimulate: {holds: [safe]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var report *Report
	cancelModelCommand(t, source.checker.Options.Quint, func(ctx context.Context) error {
		var err error
		report, err = Simulate(ctx, file, SimulateOptions{Quint: source.checker.Options.Quint, Samples: 1, Steps: 1})
		return err
	})
	if report != nil {
		t.Fatalf("canceled sampling produced model evidence: %+v", report)
	}
}

func TestModelReplayCancellationIsNotAnEvaluatorDisagreement(t *testing.T) {
	source := comparisonFakeSource(t, "echo 'error failed forged partial output'\n: > \"$0.started\"\nsleep 30\n")
	s := &sim{rep: &Report{Invariants: []Verdict{{Name: "safe", Violated: true, Counterexample: &Counterexample{Replay: "init.expect(not(safe))"}}}}}
	cancelModelCommand(t, source.checker.Options.Quint, func(ctx context.Context) error {
		return s.replay(ctx, source.checker.Binding, nil, source.checker.Options.Quint)
	})
	if got := s.rep.Invariants[0].Replayed; got != "" {
		t.Fatalf("cancellation became native replay evidence: %s", got)
	}
}

func TestModelCanceledPossibleCannotLaunchFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	source := comparisonFakeSource(t, ": > \"$0.started\"\nexit 0\n")
	err := quintPossibles(ctx, source.checker.Options.Quint, t.TempDir(), "unused.qnt", []*possibleCheck{{p: binding.Possible{Name: "reach"}, val: "reach"}}, 1, 1, "1", 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("possible error=%v", err)
	}
	if _, err := os.Stat(source.checker.Options.Quint + ".started"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled possible launched native child: %v", err)
	}
}
