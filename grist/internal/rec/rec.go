// Package rec is what grist's packages share about recording steps: a
// value's step fields, and a struct field's JSON name.
package rec

import (
	"encoding/json"
	"reflect"
	"strings"
)

// JSONName is a struct field's JSON name.
func JSONName(f reflect.StructField) string {
	if tag, ok := f.Tag.Lookup("json"); ok {
		if n, _, _ := strings.Cut(tag, ","); n != "" && n != "-" {
			return n
		}
	}
	return f.Name
}

// StepFields are a JSON-encoded value of type typ as a step's keys: a
// struct's top-level scalar fields by JSON name (numbers as integers),
// each name after prefix ("obs." for observations); a scalar as
// prefix+"result". It is how grist records arguments and results, and
// how gristsim's clients record the same steps.
func StepFields(typ reflect.Type, body []byte, prefix string) []any {
	if typ == nil {
		return nil
	}
	if typ.Kind() != reflect.Struct {
		var v any
		if json.Unmarshal(body, &v) == nil {
			return []any{prefix + "result", scalar(v)}
		}
		return nil
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	var kv []any
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		n := JSONName(f)
		if v, ok := m[n]; ok {
			switch v.(type) {
			case string, bool, float64:
				kv = append(kv, prefix+n, scalar(v))
			}
		}
	}
	return kv
}

// scalar is a decoded JSON scalar as qobs records it: numbers as integers
// (the model holds no floats).
func scalar(v any) any {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return v
}
