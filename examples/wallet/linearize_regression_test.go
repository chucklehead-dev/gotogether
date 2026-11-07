package wallet

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Seed 20764114 under all fault families collected these 19 completed calls.
// A delayed read overlaps transfers: accepting prefixes without recovering
// their exact model state exhausted the 200-order complete-search budget.
// Replaying the collected history directly keeps this one case independent
// of nightly seed-count overrides and reuses the actual wallet model.
func TestLinearizeDelayedReadWithinBudget(t *testing.T) {
	qtest.RequireQuint(t)
	f, err := os.Open("testdata/seed20764114.steps.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	traces, _ := qtrace.Reconstruct(steps)
	if len(traces) != 1 || len(traces[0].Steps) != 19 || len(traces[0].Pending) != 0 {
		t.Fatalf("fixture no longer contains the original 19 completed calls: %+v", traces)
	}
	checker, err := validate.NewChecker(t.Context(), "wallet.binding.yaml", validate.Options{Eval: "go", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	judged, err := checker.Judge(t.Context(), steps, validate.SearchOptions{Linearize: true, Budget: 200})
	if err != nil {
		t.Fatal(err)
	}
	assertWalletRegressionPass(t, judged.Report)
	if search := judged.Report.Actors[0].Receipt().Search; search == nil || search.BudgetExhausted {
		t.Fatalf("wallet witness was not established within its configured search budget: %+v", search)
	}
	positions := map[string]int{}
	identity := func(s qtrace.Step) string {
		b, err := json.Marshal([]any{s.Action, s.Args, s.Obs, s.Outcome, s.Invoked, s.Time})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, s := range judged.Steps {
		if s.Settled {
			continue
		}
		k := identity(s)
		if _, duplicate := positions[k]; duplicate {
			t.Fatal("witness repeated a recorded call")
		}
		positions[k] = len(positions)
	}
	if len(positions) != 19 {
		t.Fatalf("returned witness has %d calls, want 19", len(positions))
	}
	for _, a := range traces[0].Steps {
		pa, found := positions[identity(a)]
		if !found {
			t.Fatalf("witness omitted or changed recorded call: %+v", a)
		}
		for _, b := range traces[0].Steps {
			// One recorder gives exact real-time edges without clock slack.
			if a.Recorder == b.Recorder && b.Call != 0 && a.Seq < b.Call && pa >= positions[identity(b)] {
				t.Fatalf("witness violated recorded precedence: %s before %s", a.String(), b.String())
			}
		}
	}
	// Independently replay the returned witness with Quint, rather than
	// relying on the same native evaluator that guided the search.
	replay, err := validate.NewChecker(t.Context(), "wallet.binding.yaml", validate.Options{Eval: "quint", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	report, err := replay.Check(t.Context(), judged.Steps)
	if err != nil {
		t.Fatal(err)
	}
	assertWalletRegressionPass(t, report)
}

func assertWalletRegressionPass(t *testing.T, r *validate.Report) {
	t.Helper()
	if !r.OK() || len(r.Actors) != 1 || r.Actors[0].Conforms != validate.Pass {
		t.Fatalf("retained wallet history needs a positive witness within budget 200: %s", r.Summary())
	}
	inv := map[string]bool{}
	for _, v := range r.Actors[0].Invariants {
		inv[v.Name] = v.Verdict == validate.Pass
	}
	for _, name := range []string{"noOverdraft", validate.EventuallyName("allPosted"), validate.EventuallyName("postedApplied")} {
		if !inv[name] {
			t.Fatalf("wallet invariant %s did not pass: %s", name, r.Summary())
		}
	}
}
