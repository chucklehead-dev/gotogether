package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceRuleValidation(t *testing.T) {
	model := &Model{Diagnostics: map[string]string{"complete": "outstanding"}, Actions: map[string][]Param{"request": {{Name: "id", Type: "str"}, {Name: "amount", Type: "int"}}}}
	for _, tc := range []struct{ name, projection, action, arg, field, want string }{
		{"valid", "outstanding", "request", "id", "intent", ""},
		{"wrong projection", "typo", "request", "id", "intent", "projection"},
		{"wrong action", "outstanding", "typo", "id", "intent", "model action"},
		{"wrong parameter", "outstanding", "request", "typo", "intent", "parameter"},
		{"nonstring", "outstanding", "request", "amount", "intent", "string model parameter"},
		{"field typo", "outstanding", "request", "id", "intyent", "intent or subject"},
		{"no intent", "outstanding", "request", "id", "subject", "intent selector"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Binding{DiagnosticEvidence: map[string]map[string]EvidenceRule{tc.projection: {"request": {Action: tc.action, Match: map[string]string{tc.arg: tc.field}}}}}
			err := b.checkEvidence(model)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEvidenceOriginLoadRejectsPartialAndMalformed(t *testing.T) {
	for _, origin := range []string{"actor: native", "actor: native\n        recorder: r\n        seq: '{{'"} {
		path := filepath.Join(t.TempDir(), "binding.yaml")
		doc := "spec: model.qnt\nmodule: M\ndiagnosticEvidence:\n  outstanding:\n    cancellation:\n      action: cancel\n      match: {id: intent}\n      origin:\n        " + origin + "\n"
		if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "origin.") {
			t.Fatalf("accepted invalid origin: %v", err)
		}
	}
}
