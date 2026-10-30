package binding

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// discoverDiagnostics uses documentation in typed declarations, not source
// adjacency: Quint may reorder declarations. A local override removes an
// inherited association even when the override has no documentation.
func (m *Model) discoverDiagnostics(visible []tcDecl, tc tcOut) error {
	defs := map[string]tcDecl{}
	for _, d := range visible {
		if d.Kind == "def" {
			defs[d.Name] = d
		}
	}
	m.Diagnostics = map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		d := defs[name]
		for _, line := range strings.Split(d.Doc, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "gotogether:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[1] != "diagnostic" {
				return fmt.Errorf("diagnostic.annotation %s.%s: use /// gotogether: diagnostic <val>", m.Module, name)
			}
			if m.Diagnostics[name] != "" {
				return fmt.Errorf("diagnostic.duplicate %s.%s: give one diagnostic val", m.Module, name)
			}
			propertyType, known := tc.Types[string(d.ID)]
			if !known || renderType(propertyType.Type) != "bool" || d.Qualifier != "val" {
				return fmt.Errorf("diagnostic.property %s.%s: annotate a Boolean state val", m.Module, name)
			}
			projection, ok := defs[fields[2]]
			if !ok {
				return fmt.Errorf("diagnostic.unknown-value %s.%s: %s is not a visible model val", m.Module, name, fields[2])
			}
			if projection.Qualifier != "val" && projection.Qualifier != "pureval" || projection.Expr == nil || projection.Expr.Kind == "lambda" {
				return fmt.Errorf("diagnostic.value %s.%s: %s must be a zero-argument read-only val", m.Module, name, fields[2])
			}
			inferred, ok := tc.Types[string(projection.ID)]
			typ, err := irType(inferred.Type, m.typedefs, nil, 0)
			if !ok || err != nil || !diagnosticShape(typ) {
				return fmt.Errorf("diagnostic.shape %s.%s: %s must have type Set[{kind: str, subject: str, intent: str, expected: str}]", m.Module, name, fields[2])
			}
			m.Diagnostics[name] = fields[2]
		}
	}
	return nil
}
func diagnosticShape(t *qtype) bool {
	if t == nil {
		return false
	}
	t = t.resolved()
	if t == nil || t.kind != "set" {
		return false
	}
	t = t.elem.resolved()
	if t == nil || t.kind != "rec" || len(t.fields) != 4 {
		return false
	}
	want := map[string]bool{"kind": true, "subject": true, "intent": true, "expected": true}
	for _, f := range t.fields {
		v := f.typ.resolved()
		if !want[f.name] || v == nil || v.kind != "str" {
			return false
		}
		delete(want, f.name)
	}
	return len(want) == 0
}
