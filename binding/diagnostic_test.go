package binding

import (
	"encoding/json"
	"strings"
	"testing"
)

func diagnosticIR(t *testing.T, doc string, kind any, target string, override bool) []byte {
	t.Helper()
	decls := []any{
		map[string]any{"id": 1, "kind": "def", "name": "complete", "qualifier": "val", "doc": doc, "expr": map[string]any{"kind": "bool", "value": false}},
		map[string]any{"id": 2, "kind": "def", "name": target, "qualifier": "val", "expr": map[string]any{"kind": "app", "opcode": "Set", "args": []any{}}},
	}
	if override {
		decls = append(decls, map[string]any{"id": 3, "kind": "def", "name": "complete", "qualifier": "val", "expr": map[string]any{"kind": "bool", "value": true}})
	}
	data := map[string]any{"modules": []any{map[string]any{"name": "M", "declarations": decls}}, "types": map[string]any{"1": map[string]any{"type": map[string]any{"kind": "bool"}}, "2": map[string]any{"type": kind}, "3": map[string]any{"type": map[string]any{"kind": "bool"}}}}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestDiagnosticDiscovery(t *testing.T) {
	fields := []any{}
	for _, name := range []string{"kind", "subject", "intent", "expected"} {
		fields = append(fields, map[string]any{"fieldName": name, "fieldType": map[string]any{"kind": "str"}})
	}
	shape := map[string]any{"kind": "set", "elem": map[string]any{"kind": "rec", "fields": map[string]any{"fields": fields}}}
	for _, tc := range []struct {
		name, doc, target, want string
		shape                   any
		override                bool
	}{
		{"valid", "gotogether: diagnostic outstanding", "outstanding", "", shape, false},
		{"unknown", "gotogether: diagnostic typo", "outstanding", "diagnostic.unknown-value", shape, false},
		{"duplicate", "gotogether: diagnostic outstanding\ngotogether: diagnostic outstanding", "outstanding", "diagnostic.duplicate", shape, false},
		{"malformed", "gotogether: diagnostic", "outstanding", "diagnostic.annotation", shape, false},
		{"shape", "gotogether: diagnostic outstanding", "outstanding", "diagnostic.shape", map[string]any{"kind": "bool"}, false},
		{"no-doc", "", "outstanding", "", shape, false},
		{"override-removes-link", "gotogether: diagnostic outstanding", "outstanding", "", shape, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseTypecheck(diagnosticIR(t, tc.doc, tc.shape, tc.target, tc.override), "M")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %s, got %v", tc.want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "valid" {
				if m.Diagnostics["complete"] != "outstanding" || m.Vals["outstanding"] {
					t.Fatalf("wrong discovery: %+v / %+v", m.Diagnostics, m.Vals)
				}
			} else if len(m.Diagnostics) != 0 {
				t.Fatalf("unexpected association: %+v", m.Diagnostics)
			}
		})
	}
}

func TestDiagnosticOverrideUsesOwnType(t *testing.T) {
	fields := []any{}
	for _, name := range []string{"kind", "subject", "intent", "expected"} {
		fields = append(fields, map[string]any{"fieldName": name, "fieldType": map[string]any{"kind": "str"}})
	}
	shape := map[string]any{"kind": "set", "elem": map[string]any{"kind": "rec", "fields": map[string]any{"fields": fields}}}
	raw := diagnosticIR(t, "", shape, "outstanding", true)
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	decls := data["modules"].([]any)[0].(map[string]any)["declarations"].([]any)
	decls[2].(map[string]any)["doc"] = "gotogether: diagnostic outstanding"
	data["types"].(map[string]any)["3"] = map[string]any{"type": map[string]any{"kind": "str"}}
	raw, _ = json.Marshal(data)
	if _, err := ParseTypecheck(raw, "M"); err == nil || !strings.Contains(err.Error(), "diagnostic.property") {
		t.Fatalf("inherited Boolean type masked override: %v", err)
	}
}
