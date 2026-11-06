package model

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
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func comparisonFakeSource(t *testing.T, script string) *GraphResult {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "quint")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &binding.Binding{Spec: filepath.Join(dir, "source.qnt"), Module: "source"}
	c := &modelChecker{&validate.Checker{Binding: b, Options: validate.Options{Quint: bin}}}
	return &GraphResult{checker: c}
}

func comparisonForReplay() *Comparison {
	r := &Comparison{Status: "inconclusive", Added: BehaviorInclusion{Status: "witness", Witness: &BehaviorWitness{
		Native: "not_run", replay: "init.expect(x == 0).then(step).expect(x == 1)", Source: &Counterexample{Replay: "init.expect(x == 0).then(step).expect(x == 1)"},
	}}}
	r.Added.Witness.evidence = witnessEvidence(r.Added.Witness)
	r.added = r.Added.Witness
	return r
}

func TestComparisonReplayMissIsInconclusive(t *testing.T) {
	source := comparisonFakeSource(t, "echo 'Error [QNT508]: Expect condition does not hold true'\nexit 1\n")
	r := comparisonForReplay()
	r.before, r.after = source, source
	r.Status = "different"
	r.Added.Witness.Native = "confirmed" // editable output must not bypass replay
	if err := ConfirmComparison(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	w := r.Added.Witness
	if r.Status != "inconclusive" || w.Native != "unconfirmed" || w.NativeAttempts != ComparisonReplayAttempts || w.NativeSeed != "" {
		t.Fatalf("miss became a verdict: %+v, %+v", r, w)
	}
	if !strings.Contains(w.NativeReason, "does not establish absence") {
		t.Fatalf("miss reason claims too much: %s", w.NativeReason)
	}
}

func TestComparisonReplayConfirmsSourceOnly(t *testing.T) {
	source := comparisonFakeSource(t, "echo 'ok gtg_comparison_witness passed 1 test(s)'\n")
	target := comparisonFakeSource(t, ": > \"$0.started\"\nexit 1\n")
	r := comparisonForReplay()
	r.before, r.after = target, source
	if err := ConfirmComparison(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "different" || r.Added.Witness.Native != "confirmed" {
		t.Fatalf("source not confirmed: %+v", r)
	}
	if _, err := os.Stat(target.checker.Options.Quint + ".started"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native target sampling used for exclusion: %v", err)
	}
}

func TestComparisonReplayRequiresExecutedWitness(t *testing.T) {
	for _, script := range []string{"exit 0\n", "echo 'Error [QNT404]: unknown name'\nexit 1\n"} {
		t.Run(script, func(t *testing.T) {
			source := comparisonFakeSource(t, script)
			r := comparisonForReplay()
			r.before, r.after = source, source
			if err := ConfirmComparison(t.Context(), r); err == nil {
				t.Fatal("tool failure or unexecuted run accepted")
			}
			if r.Status != "inconclusive" || r.Added.Witness.Native == "confirmed" || r.Added.Witness.NativeAttempts != 1 {
				t.Fatalf("invalid evidence: %+v", r)
			}
		})
	}
}

func TestComparisonReplayRefusesEditedEvidence(t *testing.T) {
	source := comparisonFakeSource(t, ": > \"$0.started\"\necho 'ok gtg_comparison_witness passed 1 test(s)'\n")
	for _, edit := range []func(*BehaviorWitness){
		func(w *BehaviorWitness) { w.Source.Replay = "init.expect(true)" },
		func(w *BehaviorWitness) { w.Observed = [][]validate.NamedValue{{{Name: "x", Value: "999"}}} },
		func(w *BehaviorWitness) { w.Source.Steps = []GraphStep{{Call: "fabricated"}} },
	} {
		r := comparisonForReplay()
		r.before, r.after = source, source
		edit(r.Added.Witness)
		if err := ConfirmComparison(t.Context(), r); err == nil || !strings.Contains(err.Error(), "modified") {
			t.Fatalf("edited public evidence was accepted: %v", err)
		}
	}
	if _, err := os.Stat(source.checker.Options.Quint + ".started"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("edited evidence launched native replay: %v", err)
	}
	a, b := comparisonForReplay(), comparisonForReplay()
	a.before, a.after = source, source
	a.Added.Witness = b.Added.Witness
	if err := ConfirmComparison(t.Context(), a); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("transplanted witness accepted: %v", err)
	}
	if err := ConfirmComparison(t.Context(), &Comparison{Status: "different"}); err == nil {
		t.Fatal("manufactured comparison has native provenance")
	}
}

func TestComparisonReplayCancellationIsOperational(t *testing.T) {
	source := comparisonFakeSource(t, ": > \"$0.started\"\nsleep 30\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := comparisonForReplay()
	r.before, r.after = source, source
	done := make(chan error, 1)
	go func() { done <- ConfirmComparison(ctx, r) }()
	started := source.checker.Options.Quint + ".started"
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
			t.Fatal("fake native replay did not start")
		case <-tick.C:
			if _, err := os.Stat(started); err == nil {
				break wait
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || r.Status != "inconclusive" {
			t.Fatalf("cancellation became comparison evidence: %v, %+v", err, r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native replay ignored cancellation")
	}
}

func TestComparisonReplayChecksOriginalActionsAndHiddenState(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	model := `module confirmation {
  var x: int
  var hidden: bool
  action init = all { x' = 0, hidden' = false }
  action step = all { x < 2, x' = x + 1, hidden' = not(hidden) }
}`
	if err := os.WriteFile(filepath.Join(dir, "source.qnt"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.yaml")
	if err := os.WriteFile(path, []byte("spec: source.qnt\nmodule: confirmation\nactions: {step: {}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := validate.NewChecker(t.Context(), path, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	g, err := For(c).Graph(t.Context(), GraphSearch{})
	if err != nil {
		t.Fatal(err)
	}
	r := comparisonForReplay()
	r.before, r.after = g, g
	r.Added.Witness.Source.Replay = "init.expect(and {x == 0, hidden == false}).then(step).expect(and {x == 1, hidden == true})"
	r.Added.Witness.replay = r.Added.Witness.Source.Replay
	r.Added.Witness.evidence = witnessEvidence(r.Added.Witness)
	if err := ConfirmComparison(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "different" || r.Added.Witness.Native != "confirmed" || r.Added.Witness.NativeSeed != "1" || r.Added.Witness.NativeAttempts != 1 {
		t.Fatalf("valid source path not confirmed: %+v", r)
	}
	// An observed-state-only checker would accept this path. The concrete
	// hidden state must also match; repeated native misses stay unknown.
	r = comparisonForReplay()
	r.before, r.after = g, g
	r.Added.Witness.Source.Replay = "init.expect(and {x == 0, hidden == false}).then(step).expect(and {x == 1, hidden == false})"
	r.Added.Witness.replay = r.Added.Witness.Source.Replay
	r.Added.Witness.evidence = witnessEvidence(r.Added.Witness)
	if err := ConfirmComparison(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "inconclusive" || r.Added.Witness.Native != "unconfirmed" {
		t.Fatalf("hidden-state mismatch accepted: %+v", r)
	}
	// A source edit after graph construction must not receive the graph's
	// old exclusion/admission claims under a freshly loaded native source.
	if err := os.WriteFile(filepath.Join(dir, "source.qnt"), []byte(strings.Replace(model, "x < 2", "x < 0", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r = comparisonForReplay()
	r.before, r.after = g, g
	if err := ConfirmComparison(t.Context(), r); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed source not refused: %v", err)
	}
}
