package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

const scaffoldRoleModel = `module roles {
  var balance: int
  var completed: bool
  action init = all { balance' = 10, completed' = false }
  action observe = all { balance' = balance, completed' = completed }
  action finish = all { not(completed), balance' = 9, completed' = true }
  action step = any { observe, finish }
  val safe = balance >= 0
  val done = completed
  val helper = balance == 10
}`

func scaffoldRoleSpec(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "roles.qnt")
	if err := os.WriteFile(p, []byte(scaffoldRoleModel), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScaffoldPropertyRolesAreExplicit(t *testing.T) {
	model := &binding.Model{Module: "roles", Vals: map[string]bool{"safe": true, "done": true, "helper": true}}
	selected, err := selectScaffoldProperties(model, scaffoldProperties{invariants: "safe", eventually: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(selected.invariants, []string{"safe"}) || !slices.Equal(selected.eventually, []string{"done"}) || !slices.Equal(selected.unselected, []string{"helper"}) || selected.simulation {
		t.Fatalf("guessed property roles or model guarantees: %+v", selected)
	}
	selected, err = selectScaffoldProperties(model, scaffoldProperties{invariants: "safe", eventually: "done", holds: "safe"})
	if err != nil || !selected.simulation || !slices.Equal(selected.holds, []string{"safe"}) {
		t.Fatalf("lost explicit safety guarantee: %+v %v", selected, err)
	}
	for _, c := range []struct {
		p    scaffoldProperties
		want string
	}{
		{scaffoldProperties{}, "select properties"},
		{scaffoldProperties{invariants: "sfae"}, "not a Boolean val"},
		{scaffoldProperties{invariants: "safe,safe"}, "repeats safe"},
		{scaffoldProperties{invariants: "safe, "}, "empty property name"},
		{scaffoldProperties{invariants: "safe", eventually: "safe"}, "both -invariants and -eventually"},
		{scaffoldProperties{invariants: "safe", holds: "safe", allows: "safe"}, "both -holds and -allows"},
		{scaffoldProperties{invariants: "safe,helper", holds: "safe"}, "helper has no model classification"},
		{scaffoldProperties{eventually: "done", holds: "done"}, "not a selected invariant"},
		{scaffoldProperties{invariants: "safe", noProperties: true}, "conformance only"},
	} {
		if _, err := selectScaffoldProperties(model, c.p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.p, err, c.want)
		}
	}
	selected, err = selectScaffoldProperties(model, scaffoldProperties{noProperties: true})
	if err != nil || len(selected.invariants)+len(selected.eventually) != 0 || len(selected.unselected) != 3 {
		t.Fatalf("explicit conformance-only selection: %+v %v", selected, err)
	}
}

func TestScaffoldInvalidPropertySelectionLeavesNoFile(t *testing.T) {
	skip.Quint(t)
	spec := scaffoldRoleSpec(t)
	for _, flags := range [][]string{nil, {"-invariants", "missing"}, {"-invariants", "safe", "-eventually", "safe"}, {"-invariants", "safe", "-holds", "done"}} {
		out := filepath.Join(t.TempDir(), "roles.binding.yaml")
		args := append([]string{"-spec", spec, "-module", "roles", "-out", out}, flags...)
		if err := scaffold(args); err == nil {
			t.Fatalf("accepted invalid selection: %v", flags)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("failed generation left output %s: %v", out, err)
		}
	}
	// -system takes the existing binding as its sole property registration.
	out := filepath.Join(t.TempDir(), "system_test.go")
	err := scaffold([]string{"-system", "-binding", "missing.yaml", "-out", out, "-invariants", ""})
	if err == nil || !strings.Contains(err.Error(), "uses the binding's property selections") {
		t.Fatalf("system mode accepted property overrides: %v", err)
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("failed system generation left output: %v", err)
	}
}

func TestScaffoldSelectedCompletionIsNotASafetyInvariant(t *testing.T) {
	skip.Quint(t)
	spec := scaffoldRoleSpec(t)
	out := filepath.Join(t.TempDir(), "roles.binding.yaml")
	if err := scaffold([]string{"-spec", spec, "-module", "roles", "-out", out, "-invariants", "safe", "-eventually", "done"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := binding.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Invariants, []string{"safe"}) || !slices.Equal(b.Eventually, []string{"done"}) || b.Simulate != nil {
		t.Fatalf("scaffold invented safety/model claims: %+v", b)
	}
	if !strings.Contains(string(data), "# unselected Boolean vals (not runtime checks): [helper]") {
		t.Fatalf("unselected predicate disappeared: %s", data)
	}
	classified := filepath.Join(t.TempDir(), "classified.binding.yaml")
	if err := scaffold([]string{"-spec", spec, "-module", "roles", "-out", classified, "-invariants", "safe", "-eventually", "done", "-holds", "safe"}); err != nil {
		t.Fatal(err)
	}
	classifiedBinding, err := binding.Load(classified)
	if err != nil {
		t.Fatal(err)
	}
	if classifiedBinding.Simulate == nil || !slices.Equal(classifiedBinding.Simulate.Holds, []string{"safe"}) || !slices.Equal(classifiedBinding.Eventually, []string{"done"}) {
		t.Fatalf("explicit model classification changed runtime role: %+v", classifiedBinding)
	}
	conformanceOnly := filepath.Join(t.TempDir(), "conformance.binding.yaml")
	if err := scaffold([]string{"-spec", spec, "-module", "roles", "-out", conformanceOnly, "-no-properties"}); err != nil {
		t.Fatal(err)
	}
	only, err := binding.Load(conformanceOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Invariants)+len(only.Eventually) != 0 || only.Simulate != nil {
		t.Fatalf("conformance-only mode selected properties: %+v", only)
	}
	// Mapping observations remains an explicit author edit; classification must
	// not weaken the actual observed-state check of a source contrast.
	data = []byte(strings.ReplaceAll(string(data), "# expect: ''  # Quint bool over the post-state, from observed .obs.<name>", "expect: 'balance == {{.obs.balance}}'"))
	if err = os.WriteFile(out, data, 0600); err != nil {
		t.Fatal(err)
	}
	checker, err := validate.NewChecker(out, validate.Options{Dir: t.TempDir(), Eval: "go"})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	observed := func(action string, balance int64, seq uint64) qtrace.Step {
		return qtrace.Step{Actor: "a", Recorder: "native", Seq: seq, Action: action, Obs: map[string]any{"balance": balance}}
	}
	settle := qtrace.Step{Record: qtrace.Record{Settled: true}, Actor: "a", Recorder: "native", Seq: 3}
	for _, c := range []struct {
		name        string
		steps       []qtrace.Step
		conformance validate.Verdict
		completion  receipt.Status
	}{
		{"unfinished prefix", []qtrace.Step{observed("observe", 10, 1)}, validate.Pass, receipt.NotChecked},
		{"faithful completion", []qtrace.Step{observed("observe", 10, 1), observed("finish", 9, 2), settle}, validate.Pass, receipt.Pass},
		{"premature settle", []qtrace.Step{observed("observe", 10, 1), settle}, validate.Pass, receipt.Fail},
		{"double debit contrast", []qtrace.Step{observed("observe", 10, 1), observed("finish", 8, 2), settle}, validate.Fail, receipt.NotChecked},
	} {
		t.Run(c.name, func(t *testing.T) {
			for i := range c.steps {
				c.steps[i].Seq = uint64(i + 1)
			}
			report, err := checker.Check(t.Context(), c.steps)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Actors) != 1 || report.Actors[0].Conforms != c.conformance {
				t.Fatalf("wrong conformance: %s", report.Summary())
			}
			if c.conformance == validate.Fail {
				failure := report.Actors[0].Failure
				if failure == nil || failure.Kind != "state" || failure.Step == nil || failure.Step.Action != "finish" {
					t.Fatalf("lost observed mismatch: %s", report.Summary())
				}
				return
			}
			var found bool
			for _, check := range report.Actors[0].Receipt().Checks {
				if check.Kind == "invariant" && check.Status != receipt.Pass {
					t.Fatalf("completion became a safety failure: %s", report.Summary())
				}
				if check.Kind == "eventually" {
					found = true
					if check.Status != c.completion {
						t.Fatalf("completion got %s want %s: %s", check.Status, c.completion, report.Summary())
					}
				}
			}
			if !found {
				t.Fatalf("completion verdict absent: %s", report.Summary())
			}
		})
	}
}
