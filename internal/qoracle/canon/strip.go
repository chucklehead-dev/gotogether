package canon

import (
	"encoding/json"
	"fmt"
)

// StripModule is data (a parse, typecheck or compile output) without the
// module named name, and without the table's, types' and effects' entries
// keyed by that module's ids: a module a test or a stub front end added
// only to consume ids (a shift), removed before its output is compared.
// also are more ids to drop entries of: a compile output keeps the main
// module alone, the others' entries in its table. It returns the ids
// dropped, for StripSpans and the outputs of the same parse.
func StripModule(data []byte, name string, also map[string]bool) ([]byte, map[string]bool, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, nil, err
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("canon: not a JSON object")
	}
	mods, _ := top["modules"].([]any)
	ids := map[string]bool{}
	for k := range also {
		ids[k] = true
	}
	var kept []any
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		if mm != nil && mm["name"] == name {
			collectIDs(mm, ids)
			continue
		}
		kept = append(kept, m)
	}
	top["modules"] = orEmpty(kept)
	for _, k := range []string{"table", "types", "effects"} {
		t, ok := top[k].(map[string]any)
		if !ok {
			continue
		}
		for key := range t {
			if ids[key] {
				delete(t, key)
			}
		}
	}
	b, err := json.Marshal(top)
	return b, ids, err
}

// StripSpans is a source map without the spans of ids (StripModule's).
func StripSpans(sourceMap []byte, ids map[string]bool) ([]byte, error) {
	sm, err := decode(sourceMap)
	if err != nil {
		return nil, err
	}
	top, ok := sm.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("canon: the source map is not a JSON object")
	}
	if m, ok := top["map"].(map[string]any); ok {
		for key := range m {
			if ids[key] {
				delete(m, key)
			}
		}
	}
	return json.Marshal(top)
}

func collectIDs(n any, ids map[string]bool) {
	switch x := n.(type) {
	case map[string]any:
		if k, ok := idText(x["id"]); ok {
			ids[k] = true
		}
		for _, v := range x {
			collectIDs(v, ids)
		}
	case []any:
		for _, v := range x {
			collectIDs(v, ids)
		}
	}
}
