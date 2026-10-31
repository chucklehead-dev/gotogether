package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func lintFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func lintCheck(t *testing.T, r *Report, name, target string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name && c.Target == target {
			return c
		}
	}
	t.Fatalf("missing check %s for %s: %+v", name, target, r.Checks)
	return Check{}
}

func TestInspectionKeepsIndependentFindingsAfterSchemaFailure(t *testing.T) {
	dir := t.TempDir()
	bad := lintFile(t, dir, "bad.yaml", "unexpected: true\n")
	lintFile(t, dir, "app.go", `package app
import "github.com/chucklehead-dev/gotogether/qenv"
func run() { go func() {}() }
`)
	r := Inspect(Options{Bindings: []string{bad}, Simulation: dir, Source: dir})
	if r.ExitCode() != 1 || r.Complete() {
		t.Fatalf("schema failure should remain an incomplete authored finding, not operational error: %+v", r)
	}
	if c := lintCheck(t, r, "simulation.syntax", dir); c.Status != CheckFail {
		t.Fatalf("independent simulation scan lost: %+v", c)
	}
	if c := lintCheck(t, r, "binding.model", bad); c.Status != CheckBlocked || c.Operational {
		t.Fatalf("bad schema must block dependent model check: %+v", c)
	}
	if c := lintCheck(t, r, "source.bindings", dir); c.Status != CheckBlocked {
		t.Fatalf("partial binding union was checked: %+v", c)
	}
	var gotSource bool
	for _, f := range r.Findings {
		if f.Code == "simulation.goroutine" {
			gotSource = true
			if f.Path != filepath.Join(dir, "app.go") || f.Line != 3 || f.Column == 0 || f.Fix == "" || f.Confidence != "syntax" {
				t.Fatalf("native source location or fix lost: %+v", f)
			}
		}
	}
	if !gotSource {
		t.Fatal("independent source finding omitted")
	}
	for _, f := range r.Findings {
		if f.Code == "source.action.unknown" {
			t.Fatalf("incomplete binding set invented unknown action: %+v", f)
		}
	}
}

func TestInspectionMissingInputIsOperationalButOtherTargetsRun(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")
	lintFile(t, dir, "app.go", "package app\n")
	r := Inspect(Options{Bindings: []string{missing}, Weave: dir})
	if r.ExitCode() != 2 || r.Complete() {
		t.Fatalf("missing file must be operational and incomplete: %+v", r)
	}
	if c := lintCheck(t, r, "weave.syntax", dir); c.Status != CheckPass {
		t.Fatalf("independent check did not run: %+v", c)
	}
	if c := lintCheck(t, r, "binding.schema", missing); c.Status != CheckBlocked || !c.Operational {
		t.Fatalf("missing input understated: %+v", c)
	}
}

func TestInspectionMissingQuintDoesNotHideSourceInventory(t *testing.T) {
	dir := t.TempDir()
	missingTool := filepath.Join(dir, "missing-quint")
	lintFile(t, dir, "app.go", `package app
import "github.com/chucklehead-dev/gotogether/qobs"
func emit() { qobs.Record(nil, "flipit", nil) }
`)
	path := "../qtest/testdata/flip.yaml"
	r := Inspect(Options{Bindings: []string{path}, Source: dir, Quint: missingTool})
	if r.ExitCode() != 2 || r.Complete() {
		t.Fatalf("missing Quint must not look clean: %+v", r)
	}
	if c := lintCheck(t, r, "binding.model", path); c.Status != CheckBlocked || !c.Operational {
		t.Fatalf("model tool absence understated: %+v", c)
	}
	if c := lintCheck(t, r, "source.records", dir); c.Status != CheckPass {
		t.Fatalf("source inventory unnecessarily blocked: %+v", c)
	}
	for _, e := range r.Evidence {
		if e.Kind == "source.records" {
			if e.Count != 1 || len(e.Items) != 1 || e.Items[0].Action != "flipit" || e.Items[0].Line != 3 {
				t.Fatalf("source evidence lost native identity: %+v", e)
			}
			return
		}
	}
	t.Fatal("source inventory was omitted")
}

func TestInspectionSourceNeedsBindingAndNoTargetsIsInvalid(t *testing.T) {
	for _, o := range []Options{{}, {Source: t.TempDir()}} {
		r := Inspect(o)
		if r.ExitCode() != 2 || r.Complete() {
			t.Fatalf("invalid request looked successful: %+v", r)
		}
	}
}

func TestInspectionMalformedGoIsIncompleteAuthoredFinding(t *testing.T) {
	dir := t.TempDir()
	lintFile(t, dir, "bad.go", "package app\nfunc broken(\n")
	r := Inspect(Options{Simulation: dir})
	if r.ExitCode() != 1 || r.Complete() {
		t.Fatalf("syntax failure must block a complete scan without claiming unavailable tools: %+v", r)
	}
	if c := lintCheck(t, r, "simulation.syntax", dir); c.Status != CheckBlocked || c.Operational {
		t.Fatalf("scan status: %+v", c)
	}
}

func TestInspectionJSONUsesDerivedReadiness(t *testing.T) {
	r := Inspect(Options{Simulation: t.TempDir()})
	if !r.Complete() || r.ExitCode() != 0 {
		t.Fatalf("empty valid scope: %+v", r)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"format":"gotogether.lint/1"`, `"scope":"selected_static_checks"`, `"limits":["trace conformance and application behavior were not checked"`, `"complete":true`, `"exitCode":0`, `"findings":[]`, `"evidence":`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s: %s", want, b)
		}
	}
	r.Checks[0].Status = CheckBlocked
	r.Checks[0].Reason = "dependent input unavailable"
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"complete":true`) || !strings.Contains(string(b), `"exitCode":1`) {
		t.Fatalf("serialized stale readiness: %s", b)
	}
}

func TestInspectionNativeRulesHaveStableDistinctCodes(t *testing.T) {
	dir := t.TempDir()
	lintFile(t, dir, "app.go", `package app
import (
 "github.com/chucklehead-dev/gotogether/qenv"
 "time"
 "math/rand"
)
func run() { go func(){}(); _ = time.Now(); _ = rand.Int() }
`)
	r := Inspect(Options{Simulation: dir, Weave: dir})
	codes := map[string]bool{}
	for _, f := range r.Findings {
		codes[f.Code] = true
		if f.Line != 7 || f.Column == 0 || f.Path == "" {
			t.Fatalf("lost native location: %+v", f)
		}
	}
	for _, code := range []string{"simulation.goroutine", "simulation.wall_clock", "simulation.random", "weave.goroutine"} {
		if !codes[code] {
			t.Fatalf("missing native rule %s: %+v", code, r.Findings)
		}
	}
}

func TestInspectionMissingModelIsOperational(t *testing.T) {
	dir := t.TempDir()
	path := lintFile(t, dir, "binding.yaml", "spec: missing.qnt\nmodule: missing\nactions:\n  act: {}\n")
	r := Inspect(Options{Bindings: []string{path}})
	if r.ExitCode() != 2 || r.Complete() {
		t.Fatalf("missing model input should be operational: %+v", r)
	}
	if c := lintCheck(t, r, "binding.spec", path); !c.Operational || c.Status != CheckBlocked {
		t.Fatalf("missing model status: %+v", c)
	}
}

func TestInspectionModelTypeFailureIsAuthoredNotToolFailure(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	lintFile(t, dir, "bad.qnt", `module bad {
 var n: int
 action init = n' = "bad"
 action act = n' = n
 }
`)
	path := lintFile(t, dir, "binding.yaml", "spec: bad.qnt\nmodule: bad\nactions:\n  act: {}\n")
	r := Inspect(Options{Bindings: []string{path}, Simulation: dir})
	if r.ExitCode() != 1 || r.Complete() {
		t.Fatalf("authored Quint type error should block dependencies with exit1: %+v", r)
	}
	if c := lintCheck(t, r, "binding.model", path); c.Status != CheckFail || c.Operational {
		t.Fatalf("authored model error misclassified: %+v", c)
	}
	if c := lintCheck(t, r, "simulation.syntax", dir); c.Status != CheckPass {
		t.Fatalf("independent check lost: %+v", c)
	}
}

func TestInspectionLimitsFollowRequestedSourceScope(t *testing.T) {
	r := Inspect(Options{Source: t.TempDir()})
	if r.Scope != "selected_static_checks" || len(r.Limits) != 3 {
		t.Fatalf("source scope exclusions missing: %+v", r)
	}
	r = Inspect(Options{Simulation: t.TempDir()})
	if len(r.Limits) != 2 {
		t.Fatalf("simulation scope should not invent recording inventory claim: %+v", r.Limits)
	}
	r = Inspect(Options{Conversions: []string{"missing.yaml"}})
	if len(r.Limits) != 1 {
		t.Fatalf("conversion scope should not imply Go syntax inspection: %+v", r.Limits)
	}
}
