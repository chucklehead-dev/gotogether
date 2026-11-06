package validate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// The existing receipt-history fixture requires the unobserved deposit to have
// taken effect. Export must retain that assumption and the search bound rather
// than turning the selected history into observed input or depending on Quint.
func TestCompiledPendingManifestPortable(t *testing.T) {
	skip.Quint(t)
	bp, artifact := compiledCopy(t, "blind", "account.qnt")
	base := time.Unix(1, 0)
	steps := []qtrace.Step{
		{Recorder: "r", Seq: 1, Actor: "a", Thread: "t", Process: "p", Action: "deposit", Args: map[string]any{"amount": int64(10)}, Time: base, Record: qtrace.Record{Invocation: true}},
		{Recorder: "r", Seq: 2, Actor: "a", Thread: "t", Process: "p", Action: "withdraw", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}, Time: base.Add(time.Second)},
	}
	input := filepath.Join(filepath.Dir(bp), "pending.jsonl")
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	opt := SearchOptions{ResolvePending: true, ResolveBudget: 12}
	j, err := c.Judge(t.Context(), steps, opt)
	if err != nil {
		t.Fatal(err)
	}
	check := func(s receipt.Summary) {
		t.Helper()
		if s.Status != receipt.Pass || len(s.Actors) != 1 {
			t.Fatalf("receipt: %+v", s)
		}
		a := s.Actors[0]
		if a.JudgedBy != "evaluator" || a.Search == nil || a.Search.Mode != "resolve_pending" || a.Search.ResolutionBudget != 12 || a.Search.BudgetExhausted || len(a.AssumedEffects) != 1 || a.AssumedEffects[0].Action != "deposit" || fmt.Sprint(a.AssumedEffects[0].Args["amount"]) != "10" {
			t.Fatalf("pending provenance: %+v", a)
		}
	}
	check(j.Report.Receipt())
	m, err := NewManifest(RunRecord{Compiled: artifact, Binding: c.Binding, Search: opt, Results: j.Report.Actors, Inputs: []RunInput{{Path: input, Format: "steps", Steps: steps}}})
	if err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(t.TempDir(), "bundle")
	if err := m.Bundle(exported); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(filepath.Join(exported, "run.json")); err != nil {
		t.Fatal(err)
	}
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(exported, relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(bp)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(filepath.Join(relocated, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.Tools.Quint != "" || got.Tools.Backend != "" || got.Model.Compiled == nil || got.Check.SearchOptions().ResolveBudget != 12 {
		t.Fatalf("portable recipe: %+v", got)
	}
	if got.Receipt == nil {
		t.Fatal("missing stored receipt")
	}
	check(*got.Receipt)
	in, err := os.Open(got.Inputs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	replaySteps, err := qtrace.ReadJSONL(in)
	in.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(replaySteps) != 2 || !replaySteps[0].Record.Invocation || replaySteps[0].Ambiguous {
		t.Fatalf("assumption replaced original input: %+v", replaySteps)
	}
	replay, err := NewCompiledChecker(got.Model.Binding.Path, got.Model.Compiled.Path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	result, err := replay.Judge(t.Context(), replaySteps, got.Check.SearchOptions())
	if err != nil {
		t.Fatal(err)
	}
	check(result.Report.Receipt())
}

func TestCompiledPendingUnsupportedIsOperational(t *testing.T) {
	skip.Quint(t)
	bp, artifact := compiledCopy(t, "rr", "rr.qnt")
	c, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	s := compiledHistoryStep(0, "place", 1, 0)
	s.Obs = map[string]any{"s2": int64(0)}
	j, err := c.Judge(t.Context(), []qtrace.Step{s}, SearchOptions{ResolvePending: true, ResolveBudget: 12})
	if j != nil || !errors.Is(err, ErrNeedsQuint) {
		t.Fatalf("unsupported candidate became evidence: %+v %v", j, err)
	}
}
