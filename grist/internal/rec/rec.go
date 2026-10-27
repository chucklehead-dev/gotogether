// Package rec is what grist's packages share about recording steps: a
// value's step fields, and a struct field's JSON name.
package rec

import (
	"encoding/json"
	"fmt"
	"math"
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
// struct's top-level scalar fields (strings, booleans, integers, and
// pointers to them that are not nil) by JSON name, each name after
// prefix ("obs." for observations); a scalar as prefix+"result". It is
// how grist records arguments and results, and how its clients record
// the same steps.
//
// body is decoded into typ itself, as a command's decision gets it, not
// into generic JSON: integers are exact (a float64 would round those
// past 2^53), and a field is named by its JSON name whatever case the
// body gave it in. An unsigned value past int64's range, which the
// model's integers cannot hold, is an error naming the field and the
// fix (the fields before it are returned), and so is a body that does
// not decode as typ.
func StepFields(typ reflect.Type, body []byte, prefix string) ([]any, error) {
	if typ == nil {
		return nil, nil
	}
	v := reflect.New(typ)
	if err := json.Unmarshal(body, v.Interface()); err != nil {
		return nil, fmt.Errorf("%s does not decode as %v: %w", body, typ, err)
	}
	v = v.Elem()
	if typ.Kind() != reflect.Struct {
		x, ok, err := scalar(prefix+"result", v)
		if err != nil || !ok {
			return nil, err
		}
		return []any{prefix + "result", x}, nil
	}
	var kv []any
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		n := JSONName(f)
		x, ok, err := scalar(n, v.Field(i))
		if err != nil {
			return kv, err
		}
		if ok {
			kv = append(kv, prefix+n, x)
		}
	}
	return kv, nil
}

// scalar is v as qobs records it (integers as int64), false if v is no
// scalar (a slice, a struct, a nil pointer).
func scalar(name string, v reflect.Value) (any, bool, error) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, false, nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), true, nil
	case reflect.Bool:
		return v.Bool(), true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if u := v.Uint(); u > math.MaxInt64 {
			return nil, false, fmt.Errorf("field %s is %d, past the model's integers (int64): give a value at most %d, or make the field an int64 (a string for an identifier)", name, u, int64(math.MaxInt64))
		}
		return int64(v.Uint()), true, nil
	}
	return nil, false, nil
}
