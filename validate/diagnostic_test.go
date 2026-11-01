package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func diagnosticSteps(t *testing.T, settle bool) []qtrace.Step {
	t.Helper()
	sink := &qobs.MemorySink{}
	for _, actor := range []string{"tenant-a", "tenant-b"} {
		rec := qobs.NewRecorder(sink, qobs.WithID(actor), qobs.WithActor(actor), qobs.WithProcess("kernel"))
		rec.Record(t.Context(), "request", nil, "id", "c1")
		rec.Record(t.Context(), "request", nil, "id", "c2")
		if settle {
			rec.Settled(t.Context())
		}
	}
	return sink.Steps()
}
func checkDiagnosticReport(t *testing.T, r *Report, available bool) {
	t.Helper()
	if len(r.Actors) != 2 {
		t.Fatalf("actors: %d", len(r.Actors))
	}
	for _, actor := range r.Actors {
		if actor.Conforms != Pass || len(actor.Absent) != 1 {
			t.Fatalf("unexpected result: %s", actor.Summary())
		}
		ab := actor.Absent[0]
		d := ab.Diagnostic
		if d == nil || d.Actor != actor.Actor || d.Property != "complete" || d.Projection != "outstanding" {
			t.Fatalf("missing scoped metadata: %+v", d)
		}
		if available {
			if ab.Cause != Defect {
				t.Fatalf("changed completion verdict: %s", actor.Summary())
			}
			if os.Getenv("QUINT_BACKEND") == "typescript" {
				// This backend does not emit failing-test ITF. The prototype
				// must report missing exact state, not independently replay one.
				if d.Status != "unavailable" || d.Reason != "exact judged model state is unavailable" {
					t.Fatalf("TypeScript state availability: %+v", d)
				}
			} else if d.Status != "available" || d.Total != 2 || len(d.Items) != 2 || d.Items[0].Intent != "c1" || d.Items[1].Intent != "c2" {
				t.Fatalf("wrong details: %+v (%s)", d, actor.Summary())
			}
		} else if ab.Cause != Uncollected || d.Status != "unavailable" || d.Scope != "checked_history_no_completion_verdict" {
			t.Fatalf("prefix overclaimed: %+v", d)
		}
		if d.Evidence != "unresolved" {
			t.Fatalf("invented native evidence: %+v", d)
		}
		if actor.Doc().Absent[0].Diagnostic.Status != d.Status {
			t.Fatal("JSON adapter lost details")
		}
		b, err := json.Marshal(actor.Doc())
		if err != nil || !strings.Contains(string(b), `"diagnostic"`) {
			t.Fatalf("JSON: %s %v", b, err)
		}
		if !strings.Contains(actor.Summary(), "complete → outstanding") {
			t.Fatal("text adapter lost details")
		}
	}
}
func TestDiagnosticPrototype(t *testing.T) {
	skip.Quint(t)
	for _, mode := range []string{"go", "quint"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if c.Model.Diagnostics["complete"] != "outstanding" {
				t.Fatal("typed doc annotation was not discovered")
			}
			steps := diagnosticSteps(t, true)
			r, err := c.Check(t.Context(), steps)
			if err != nil {
				t.Fatal(err)
			}
			checkDiagnosticReport(t, r, true)
			reps, errs := c.CheckBatch(t.Context(), [][]qtrace.Step{steps}, 1)
			if errs[0] != nil {
				t.Fatal(errs[0])
			}
			checkDiagnosticReport(t, reps[0], true)
			j, err := c.Judge(t.Context(), steps, SearchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			checkDiagnosticReport(t, j.Report, true)
			prefix, err := c.Check(t.Context(), diagnosticSteps(t, false))
			if err != nil {
				t.Fatal(err)
			}
			checkDiagnosticReport(t, prefix, false)
			nav, err := c.Nav(NavStart{Steps: steps, Actor: "tenant-a", At: 1})
			if err != nil {
				t.Fatal(err)
			}
			node, err := nav.Node()
			if err != nil {
				t.Fatal(err)
			}
			if len(node.Diagnostics) != 1 || node.Diagnostics[0].Scope != "navigated_state_no_verdict" || node.Diagnostics[0].Cause != "" || node.Diagnostics[0].Total != 1 {
				t.Fatalf("navigation mixed terminal and cursor state: %+v", node.Diagnostics)
			}
		})
	}
}

func TestDiagnosticUnavailablePreservesVerdict(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	steps := diagnosticSteps(t, true)
	base, err := c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	// An ambiguous/search context may inspect the same failure, but must not
	// carry the pre-search explanatory data forward as an established answer.
	before, _ := json.Marshal(base.Actors)
	copy := base.clone()
	c.enrichDiagnostics(copy, steps, true)
	for i, a := range copy.Actors {
		if a.Absent[0].Diagnostic.Status != "unavailable" || a.Absent[0].Cause != Defect || a.Reason() != base.Actors[i].Reason() {
			t.Fatal("diagnostic changed search verdict")
		}

	}
	after, _ := json.Marshal(base.Actors)
	if string(after) != string(before) {
		t.Fatal("enrichment mutated a cached report")
	}
	// Supplying a real gap downgrades the original absence; details cannot
	// promote it back to a defect, regardless of the projection's value.
	gap := append([]qtrace.Step{}, steps...)
	gap = append(gap[:1], gap[2:]...)
	r, err := c.Check(t.Context(), gap)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range r.Actors {
		if a.Actor == "tenant-a" {
			for _, ab := range a.Absent {
				if ab.Kind == "eventually" {
					found = true
					if ab.Cause != Dropped || ab.Diagnostic == nil || ab.Diagnostic.Status != "unavailable" {
						t.Fatalf("gap overclaimed or explanation omitted: %+v", ab)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("gapped actor lost its absence classification")
	}

}

func TestDiagnosticSearchCacheBoundary(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	steps := diagnosticSteps(t, true)
	cached, err := c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(cached.Actors)
	calls := 0
	judgments, errs := c.JudgeBatch(t.Context(), [][]qtrace.Step{steps}, SearchOptions{ResolvePending: true}, func(_ context.Context, runs [][]qtrace.Step) ([]*Report, []error) {
		calls++
		if len(runs) != 1 {
			t.Fatalf("unexpected cache lookup count: %d", len(runs))
		}
		return []*Report{cached}, []error{nil}
	})
	if calls == 0 || errs[0] != nil || judgments[0] == nil {
		t.Fatalf("search callback: %d, %v", calls, errs)
	}
	for i, actor := range judgments[0].Report.Actors {
		d := actor.Absent[0].Diagnostic
		if d == nil || d.Status != "unavailable" || !strings.Contains(d.Reason, "searched") {
			t.Fatalf("pre-search explanation survived: %+v", d)
		}
		if actor.Reason() != cached.Actors[i].Reason() {
			t.Fatal("cached verdict/details mutated")
		}
	}
	after, _ := json.Marshal(cached.Actors)
	if string(after) != string(before) {
		t.Fatal("search mutated cached report details")
	}
}

func TestDiagnosticOutputLimits(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	nav, err := c.Nav(NavStart{})
	if err != nil {
		t.Fatal(err)
	}
	state := maps.Clone(nav.start)
	var items []qeval.Value
	for i := range 40 {
		items = append(items, qeval.Str(fmt.Sprintf("c%02d", i)))
	}
	state[nav.e.m.Vars[0]] = qeval.NewSet(items)
	detail := c.diagnosticValue(nav.e, nav.consts, state, "complete", "navigated_state_no_verdict")
	if detail.Status != "truncated" || detail.Total != 40 || len(detail.Items) != 32 || detail.Omitted != 8 {
		t.Fatalf("limits: %+v", detail)
	}
	state[nav.e.m.Vars[0]] = qeval.NewSet([]qeval.Value{qeval.Str(strings.Repeat("x", 513))})
	detail = c.diagnosticValue(nav.e, nav.consts, state, "complete", "navigated_state_no_verdict")
	if detail.Status != "unavailable" || len(detail.Items) != 0 || !strings.Contains(detail.Reason, "512-byte") {
		t.Fatalf("oversized identity was truncated or admitted: %+v", detail)
	}
}

func TestDiagnosticAnnotationDoesNotSelectCheck(t *testing.T) {
	skip.Quint(t)
	c, err := checkerFixtureVariant(t, "testdata/diagnostic/binding.yaml", map[string]any{"eventually": nil})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Model.Diagnostics["complete"] != "outstanding" {
		t.Fatal("inactive annotation disappeared")
	}
	r, err := c.Check(t.Context(), diagnosticSteps(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() {
		t.Fatal("annotation introduced a check: ", r.Reason())
	}
	for _, a := range r.Actors {
		if len(a.Invariants) != 0 || len(a.Absent) != 0 {
			t.Fatal("annotation changed checked set")
		}
	}
}

func TestDiagnosticCompiledMetadata(t *testing.T) {
	skip.Quint(t)
	path := "testdata/diagnostic/binding.yaml"
	cm, err := Compile(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "model.json")
	if err := cm.Write(artifact); err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiledChecker(path, artifact, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Model.Diagnostics["complete"] != "outstanding" {
		t.Fatal("compiled typecheck lost documentation")
	}
	nav, err := c.Nav(NavStart{Steps: diagnosticSteps(t, true), Actor: "tenant-a", At: -1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := nav.Node()
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Diagnostics) != 1 || node.Diagnostics[0].Total != 2 || node.Diagnostics[0].Status != "available" {
		t.Fatalf("compiled bridge lost value: %+v", node.Diagnostics)
	}
}

func TestDiagnosticEvaluationFailureDoesNotChangeVerdict(t *testing.T) {
	skip.Quint(t)
	for _, kind := range []string{"evaluation-error", "empty-explanation"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			source, err := os.ReadFile("testdata/diagnostic/model.qnt")
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Replace(string(source), "val complete = outstanding.size() == 0", "val complete = pending.size() == 0", 1)
			if kind == "evaluation-error" {
				text = strings.Replace(text, `expected: "modeled inventory cancellation answer"`, `expected: if (10 / 0 == 1) "answer" else "answer"`, 1)
			} else {
				text = strings.Replace(text, "val outstanding = pending.map", "val outstanding = (if (pending.size() < 0) pending else Set()).map", 1)
			}
			if err := os.WriteFile(filepath.Join(dir, "model.qnt"), []byte(text), 0644); err != nil {
				t.Fatal(err)
			}
			binding, err := os.ReadFile("testdata/diagnostic/binding.yaml")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "binding.yaml")
			if err := os.WriteFile(path, binding, 0644); err != nil {
				t.Fatal(err)
			}
			c, err := NewChecker(path, Options{Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			report, err := c.Check(t.Context(), diagnosticSteps(t, true))
			if err != nil {
				t.Fatal(err)
			}
			for _, actor := range report.Actors {
				if actor.Conforms != Pass || len(actor.Absent) != 1 || actor.Absent[0].Cause != Defect || actor.Absent[0].Diagnostic.Status != "unavailable" || actor.Absent[0].Diagnostic.Reason == "" {
					t.Fatalf("explanation changed verdict: %s", actor.Summary())
				}
			}
		})
	}
}

// checkerFixtureVariant constructs the variant before source identity is frozen.
// Keeping the model path absolute preserves its real transitive import layout.
func checkerFixtureVariant(t *testing.T, path string, fields map[string]any) (*Checker, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs(filepath.Join(filepath.Dir(path), doc["spec"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	doc["spec"] = spec
	for key, value := range fields {
		if value == nil {
			delete(doc, key)
		} else {
			doc[key] = value
		}
	}
	raw, err = yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(t.TempDir(), "binding.yaml")
	if err := os.WriteFile(variant, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return NewChecker(variant, Options{Dir: t.TempDir()})
}
