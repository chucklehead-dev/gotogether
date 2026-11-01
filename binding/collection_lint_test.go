package binding

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

func collectionLintFixture(typ, shape, field string) (*Binding, *Model) {
	return &Binding{Actions: map[string]Action{"take": {Args: map[string]string{"values": field}}}},
		&Model{Module: "collections", Actions: map[string][]Param{"take": {{Name: "values", Type: typ, Shape: shape}}}}
}

func TestCollectionLintRecordedFields(t *testing.T) {
	for _, tc := range []struct{ typ, shape, field string }{
		{"List[int]", "list", "{{.values}}"},
		{"Set[str]", "set", " \n{{ .args.values }}\t"},
		{"Vec", "map", "{{.payload}}"}, // Shape is resolved even for a named alias.
		{"List[int]", "list", "{{/* recorded JSON */}}{{.obs.values}}"},
	} {
		b, m := collectionLintFixture(tc.typ, tc.shape, tc.field)
		fs := b.LintFindings(m)
		if len(fs) != 1 {
			t.Fatalf("%s: findings %+v", tc.field, fs)
		}
		f := fs[0]
		if f.Code != "binding.parameter.collection_literal" || f.Severity != "error" || f.Symbol != "take.values" || f.Confidence != "model_metadata" || f.Fix == "" {
			t.Fatalf("lost actionable metadata: %+v", f)
		}
		if f.Line != 0 || f.Column != 0 || !strings.Contains(f.Message, tc.typ) {
			t.Fatalf("invented position or lost type: %+v", f)
		}
		if !reflect.DeepEqual(b.Lint(m), lintMessages(fs)) || !reflect.DeepEqual(b.LintFindings(m), fs) {
			t.Fatal("text or repeated lint differs from canonical findings")
		}
	}
}

func TestCollectionLintLeavesOtherExpressionsAlone(t *testing.T) {
	for _, expr := range []string{
		`{{toList .values}}`, `{{toQuint "List[int]" .values}}`,
		`[1, 2]`, `List({{.value}})`, `{{ .values | toList }}`,
		`{{$xs := .values}}{{toList $xs}}`, `{{if .value}}{{toList .values}}{{else}}[]{{end}}`,
		`{{define "render"}}[1]{{end}}{{template "render"}}`,
		`{{.outcome}}`, `{{.process}}`, `{{.step.seq}}`, `{{.seq}}`, `{{.action}}`,
	} {
		b, m := collectionLintFixture("List[int]", "list", expr)
		if fs := b.LintFindings(m); len(fs) != 0 {
			t.Errorf("%s: rule inferred beyond its scope: %+v", expr, fs)
		}
	}
	for _, shape := range []string{"", "unknown"} {
		b, m := collectionLintFixture("int", shape, "{{.values}}")
		if fs := b.LintFindings(m); len(fs) != 0 {
			t.Errorf("scalar/unknown shape: %+v", fs)
		}
	}
	b, m := collectionLintFixture("List[int]", "list", "{{")
	if fs := b.LintFindings(m); len(fs) != 1 || fs[0].Code != "binding.template.invalid" {
		t.Fatalf("syntax error was relabeled: %+v", fs)
	}
	delete(b.Actions, "take") // Omitted arguments retain model-aware defaults.
	if fs := b.LintFindings(m); len(fs) != 0 {
		t.Fatalf("omitted mapping: %+v", fs)
	}
}

func TestCollectionLintPreservesMappedOutcomeExpressions(t *testing.T) {
	b, m := collectionLintFixture("List[int]", "list", "{{.outcome}}")
	b.Outcomes = map[string]string{"ok": "[1, 2]"}
	if fs := b.LintFindings(m); len(fs) != 0 {
		t.Fatalf("mapped outcome is already Quint text: %+v", fs)
	}
	trace := &qtrace.Trace{Actor: "a", Processes: []string{"p"}, Steps: []qtrace.Step{{Action: "take", Process: "p", Recorder: "r", Seq: 1, Outcome: "ok"}}}
	plan, err := b.Plan(m, trace)
	if err != nil || len(plan.Calls) != 1 || plan.Calls[0].Quint != "take([1, 2])" {
		t.Fatalf("valid collection-valued outcome changed: %+v, %v", plan, err)
	}
}

func TestCollectionLintInternedFieldGetsScalarIDGuidance(t *testing.T) {
	b, m := collectionLintFixture("List[int]", "list", "{{.args.values}}")
	b.Intern = map[string]string{"values": "actor"}
	fs := b.LintFindings(m)
	if len(fs) != 1 || !strings.Contains(fs[0].Fix, "interned to a scalar ID") || strings.Contains(fs[0].Fix, "default") {
		t.Fatalf("interned field got an ineffective JSON/default fix: %+v", fs)
	}
}

// Partial in-memory metadata must not produce an executable-looking unknown type.
func TestCollectionLintUnknownTypeKeepsGuidanceGeneric(t *testing.T) {
	b, m := collectionLintFixture("List[?]", "list", "{{.payload}}")
	fs := b.LintFindings(m)
	if len(fs) != 1 || strings.Contains(fs[0].Fix, `toQuint "List[?]"`) || !strings.Contains(fs[0].Fix, "typed toQuint conversion") {
		t.Fatalf("invalid concrete fix from partial metadata: %+v", fs)
	}
}
