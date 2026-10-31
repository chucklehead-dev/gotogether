package binding

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLintFindingsCarryRuleIdentityAtEmission(t *testing.T) {
	m := &Model{Module: "bank", Actions: map[string][]Param{"pay": {{Name: "amount"}}}, Vals: map[string]bool{}, Consts: map[string]string{"LIMIT": "int"}}
	b := &Binding{Actions: map[string]Action{"pay": {Args: map[string]string{"wrong": "{{"}}, "typo": {}}, Constants: map[string]string{"EXTRA": "1"}}
	fs := b.LintFindings(m)
	want := []string{"binding.parameter.unknown", "binding.template.invalid", "binding.action.unknown", "binding.constant.missing", "binding.constant.unknown"}
	var got []string
	for _, f := range fs {
		got = append(got, f.Code)
		if f.Severity != "error" || f.Fix == "" || f.Confidence != "model_metadata" || f.Symbol == "" {
			t.Fatalf("unactionable finding: %+v", f)
		}
		if f.Line != 0 || f.Column != 0 {
			t.Fatalf("invented YAML position: %+v", f)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codes %v, want %v", got, want)
	}
	for range 10 {
		if next := b.LintFindings(m); !reflect.DeepEqual(next, fs) {
			t.Fatal("map iteration changed diagnostic ordering")
		}
	}
	if !reflect.DeepEqual(b.Lint(m), lintMessages(fs)) {
		t.Fatal("text helper bypassed canonical typed findings")
	}
}

func TestLintSourceFindingsUseCapturedPositions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source:sample.go")
	src := "package app\nimport \"github.com/chucklehead-dev/gotogether/qobs\"\n//quint:action action:pay\nfunc run() {\n\tqobs.Record(nil, \"typo\", nil)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	anns, err := ScanAnnotations(dir)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := ScanRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || len(recs) != 1 {
		t.Fatalf("sites: %d annotations, %d records", len(anns), len(recs))
	}
	m := &Model{Module: "bank", Actions: map[string][]Param{"pay": {{Name: "amount"}}}}
	b := &Binding{Actions: map[string]Action{"pay": {}}}
	fs := LintSourceFindings([]Bound{{Binding: b, Model: m}}, anns, recs)
	if len(fs) != 2 {
		t.Fatalf("findings: %+v", fs)
	}
	for _, f := range fs {
		if strings.Contains(f.Message, path) || strings.Contains(f.Message, "source:sample.go") {
			t.Fatalf("canonical message duplicates source location: %+v", f)
		}
		if f.Path != path || f.Fix == "" || f.Confidence != "syntax" {
			t.Fatalf("lost source evidence: %+v", f)
		}
	}
	if f := fs[0]; f.Code != "source.annotation.field_missing" || f.Line != 3 || f.Column != 1 || f.Symbol != "pay" {
		t.Fatalf("annotation: %+v", f)
	}
	col := strings.Index(strings.Split(src, "\n")[4], "\"typo\"") + 1
	if f := fs[1]; f.Code != "source.action.unknown" || f.Line != 5 || f.Column != col || f.Symbol != "typo" {
		t.Fatalf("record: %+v", f)
	}
	// Locations remain structured even when presentation strings are changed.
	anns[0].Pos = "presentation only"
	recs[0].Pos = "presentation only"
	next := LintSourceFindings([]Bound{{Binding: b, Model: m}}, anns, recs)
	if next[0].Line != 3 || next[1].Column != col || next[1].Path != path {
		t.Fatal("derived location from prose")
	}
	errs, warns := LintSources([]Bound{{Binding: b, Model: m}}, anns, recs)
	if len(warns) != 0 || !reflect.DeepEqual(errs, lintMessages(next)) {
		t.Fatal("text source helper bypassed typed findings")
	}
}
