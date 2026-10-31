package schemas_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	lintcheck "github.com/chucklehead-dev/gotogether/lint"
	"github.com/chucklehead-dev/gotogether/schemas"
)

// lintDocument mirrors Report.MarshalJSON's derived fields. Validation below
// checks the actual marshaled report, so the mirror cannot hide a wire change.
type lintDocument struct {
	lintcheck.Report
	Complete bool `json:"complete"`
	ExitCode int  `json:"exitCode"`
}

func lintSchema(t *testing.T) []byte {
	g := &generator{docs: docs(t, "../lint"), tag: "json",
		structs: map[string]map[string]any{
			"lintDocument": {"required": []string{"format", "scope", "limits", "checks", "findings", "evidence", "complete", "exitCode"}},
			"Check":        {"type": "object", "required": []string{"name", "target", "status"}},
			"Diagnostic":   {"type": "object", "required": []string{"code", "severity", "message", "fix", "confidence"}},
			"Evidence":     {"type": "object", "required": []string{"kind", "target", "count"}},
			"EvidenceItem": {"type": "object", "required": []string{"path", "line", "column"}},
		},
		fields: map[string]map[string]any{
			"Check.Status":          {"enum": []any{lintcheck.CheckPass, lintcheck.CheckFail, lintcheck.CheckBlocked}},
			"Diagnostic.Severity":   {"enum": []any{lintcheck.Error, lintcheck.Warning, lintcheck.Info}},
			"lintDocument.ExitCode": {"enum": []any{0, 1, 2}},
		},
	}
	return g.root(reflect.TypeFor[lintDocument](), formats.Lint.Name(), schemas.LintURL, "gotogether lint report",
		"A nonwriting lint inspection: selected checks, actionable findings, source evidence and derived completeness and exit status.")
}

func TestLintReportValidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(p, []byte("rules:\n  - span: GET /orders\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A schema failure produces a finding plus a blocked dependent check;
	// inspect real output rather than a hand-constructed DTO approximation.
	r := lintcheck.Inspect(lintcheck.Options{Conversions: []string{p}})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	problems, err := schemas.Check(schemas.Lint(), data)
	if err != nil || len(problems) != 0 {
		t.Fatalf("lint schema: %v %v\n%s", err, problems, data)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["complete"] != false || document["exitCode"] != float64(1) {
		t.Fatalf("missing derived readiness: %s", data)
	}
	for _, key := range []string{"complete", "exitCode"} {
		t.Run("required_"+key, func(t *testing.T) {
			original := document[key]
			delete(document, key)
			bad, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if problems, err := schemas.Check(schemas.Lint(), bad); err != nil || len(problems) != 1 {
				t.Fatalf("missing %s accepted or wrong diagnostic: %v %v", key, err, problems)
			}
			document[key] = original
		})
	}
	// Lint returns a copy, as the other schema accessors do.
	copy := schemas.Lint()
	copy[0] = '!'
	if schemas.Lint()[0] == '!' {
		t.Fatal("schema bytes share mutable storage")
	}
}
