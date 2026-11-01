package lint

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// A parsed binding and real model carry the finding through the inspection's
// native data, rather than requiring a caller to parse Quint's later error.
func TestInspectionCollectionTemplateIsActionableAuthoredFinding(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	lintFile(t, dir, "collections.qnt", `module collections {
  var seenCount: int
  action init = seenCount' = 0
  action accept(values: List[int]): bool = seenCount' = values.length()
}
`)
	for _, field := range []string{".values", ".args.values", ".payload", ".obs.values"} {
		t.Run(field, func(t *testing.T) {
			path := lintFile(t, dir, "bad.yaml", fmt.Sprintf("format: gotogether.binding/2\nspec: collections.qnt\nmodule: collections\nactions:\n  accept:\n    args:\n      values: '{{%s}}'\n", field))
			r := Inspect(Options{Bindings: []string{path}})
			if r.ExitCode() != 1 || !r.Complete() {
				t.Fatalf("authored collection template must be a completed finding, not tool failure: %+v", r)
			}
			if c := lintCheck(t, r, "binding.compatibility", path); c.Status != CheckFail || c.Operational {
				t.Fatalf("template type mismatch understated or called operational: %+v", c)
			}
			if c := lintCheck(t, r, "binding.model", path); c.Status != CheckPass {
				t.Fatalf("valid model blamed for binding template: %+v", c)
			}
			if len(r.Findings) != 1 {
				t.Fatalf("want one precise binding finding: %+v", r.Findings)
			}
			f := r.Findings[0]
			if f.Code != "binding.parameter.collection_literal" || f.Severity != Error || f.Path != path || f.Symbol == "" || f.Confidence != "model_metadata" {
				t.Fatalf("finding lost rule/model identity: %+v", f)
			}
			if f.Line != 0 || f.Column != 0 {
				t.Fatalf("model check invented YAML location: %+v", f)
			}
			if !strings.Contains(f.Message, "accept") || !strings.Contains(f.Message, "values") || !strings.Contains(f.Fix, "values") {
				t.Fatalf("finding does not identify parameter and fix: %+v", f)
			}
			identity := field == ".values" || field == ".args.values"
			if identity && !strings.Contains(strings.ToLower(f.Fix), "default") {
				t.Fatalf("identity mapping did not recommend the default: %+v", f)
			}
			if !identity && !strings.Contains(f.Fix, "toQuint") {
				t.Fatalf("renamed/observed input did not retain typed conversion: %+v", f)
			}
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var native struct {
				Format   string       `json:"format"`
				Complete bool         `json:"complete"`
				ExitCode int          `json:"exitCode"`
				Findings []Diagnostic `json:"findings"`
			}
			if err := json.Unmarshal(encoded, &native); err != nil {
				t.Fatal(err)
			}
			if native.Format != "gotogether.lint/1" || !native.Complete || native.ExitCode != 1 || len(native.Findings) != 1 || native.Findings[0] != f {
				t.Fatalf("agent-facing JSON differs from native finding: %s", encoded)
			}
		})
	}
}

func TestInspectionCollectionTemplateSupportedFixesAreClean(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	lintFile(t, dir, "collections.qnt", `module collections {
  var seenCount: int
  action init = seenCount' = 0
  action accept(values: List[int]): bool = seenCount' = values.length()
}
`)
	for _, action := range []string{
		"  accept: {}\n",
		"  accept:\n    args:\n      values: '{{toQuint \"List[int]\" .payload}}'\n",
	} {
		path := lintFile(t, dir, "fixed.yaml", "format: gotogether.binding/2\nspec: collections.qnt\nmodule: collections\nactions:\n"+action)
		r := Inspect(Options{Bindings: []string{path}})
		if r.ExitCode() != 0 || !r.Complete() || len(r.Findings) != 0 {
			t.Fatalf("supported conversion workflow should be clean: %+v", r)
		}
		if c := lintCheck(t, r, "binding.compatibility", path); c.Status != CheckPass {
			t.Fatalf("corrected binding remained failed: %+v", c)
		}
	}
}
