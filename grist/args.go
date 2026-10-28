package grist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// ErrBadArgs is a command's or query's arguments grist refuses before
// anything runs (a 400 at the API): a body that is not valid UTF-8 or
// does not decode as the block's type, a field the type does not have,
// a required field missing or null (the catalog's JSON Schema says which are required),
// text no store holds alike (a NUL byte, invalid UTF-8), or an integer
// past the model's (int64). The error wrapping it names the field and
// the fix.
var ErrBadArgs = errors.New("grist: arguments refused")

// badArgs is an ErrBadArgs naming block and what is wrong.
func badArgs(block string, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrBadArgs, block, fmt.Sprintf(format, args...))
}

// decodeArgs decodes body as a value of typ, the arguments of block, as
// every adapter's path does (the API's, App.Execute's and App.Query's):
// valid UTF-8, strictly (no field typ lacks, one JSON value), every
// required field present and not null (requiredIn), and every string
// text a store holds (checkStrings). Its errors are ErrBadArgs.
func decodeArgs(block string, body []byte, typ reflect.Type) (reflect.Value, error) {
	if !utf8.Valid(body) {
		return reflect.Value{}, badArgs(block, "the arguments are not valid UTF-8, which JSON would rewrite silently: send UTF-8 (encode binary data, e.g. as hex or base64)")
	}
	v := reflect.New(typ)
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v.Interface()); err != nil {
		return reflect.Value{}, badArgs(block, "arguments %s: %v (want %s)", body, err, schemaText(typ))
	}
	if d.More() {
		return reflect.Value{}, badArgs(block, "arguments %s: more than one JSON value (want %s)", body, schemaText(typ))
	}
	var raw any
	g := json.NewDecoder(bytes.NewReader(body))
	g.UseNumber()
	g.Decode(&raw) // decoded above, so it decodes
	if err := requiredIn(typ, raw, "the arguments"); err != nil {
		return reflect.Value{}, badArgs(block, "%v (want %s)", err, schemaText(typ))
	}
	if err := checkStrings(v.Elem(), "argument"); err != nil {
		return reflect.Value{}, badArgs(block, "%v", strings.TrimPrefix(err.Error(), "grist: "))
	}
	return v.Elem(), nil
}

// requiredIn is the error of a JSON value (decoded with UseNumber) that
// lacks a field typ requires, or holds null where typ does not take it:
// every exported field of a struct is required but a pointer's (which
// may be absent or null), and only a pointer, a slice or a map takes
// null (Go's nil, as encoding/json writes it). Keys match fields as
// encoding/json matches them: case-insensitively.
func requiredIn(typ reflect.Type, v any, path string) error {
	if v == nil {
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			return nil
		}
		return fmt.Errorf("%s: null where a %v is required", path, typ)
	}
	switch typ.Kind() {
	case reflect.Pointer:
		return requiredIn(typ.Elem(), v, path)
	case reflect.Slice, reflect.Array:
		if xs, ok := v.([]any); ok {
			for i, x := range xs {
				if err := requiredIn(typ.Elem(), x, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if m, ok := v.(map[string]any); ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if err := requiredIn(typ.Elem(), m[k], fmt.Sprintf("%s[%q]", path, k)); err != nil {
					return err
				}
			}
		}
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return nil // not an object: the typed decode refused it
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			n := rec.JSONName(f)
			x, found := lookupFold(m, n)
			if !found {
				if f.Type.Kind() == reflect.Pointer {
					continue
				}
				return fmt.Errorf("%s: field %q missing: give every field (a pointer field may be left out)", path, n)
			}
			if err := requiredIn(f.Type, x, path+"."+n); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookupFold is m's value at key k, matched as encoding/json matches a
// field's name: exactly, else case-insensitively.
func lookupFold(m map[string]any, k string) (any, bool) {
	if x, ok := m[k]; ok {
		return x, true
	}
	for mk, x := range m {
		if strings.EqualFold(mk, k) {
			return x, true
		}
	}
	return nil, false
}

// checkStrings is the error of a string anywhere in v (a field, an
// element, a map's key or value) that no store holds alike (checkText).
func checkStrings(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.String:
		return checkText(path, v.String())
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return checkStrings(v.Elem(), path)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := checkStrings(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if err := checkStrings(it.Key(), path+" key"); err != nil {
				return err
			}
			if err := checkStrings(it.Value(), fmt.Sprintf("%s[%v]", path, it.Key())); err != nil {
				return err
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.IsExported() {
				if err := checkStrings(v.Field(i), path+" "+rec.JSONName(f)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// canonicalArgs is a command's arguments (encoding/json's encoding of
// its typed value) as its marker's hash covers them: object members
// sorted by name, and every member whose value is its type's zero (null,
// "", 0, false, an empty array or object, after the same at every
// depth) left out. So a retry is the same command across a deploy that
// adds a field (absent before, its zero now) or reorders the fields;
// one that renames a field or changes a value is another (a reused key).
func canonicalArgs(body []byte) []byte {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return body
	}
	v, _ = prune(v)
	b, err := json.Marshal(v) // map keys sorted
	if err != nil {
		return body
	}
	return b
}

// prune is v without its zero-valued object members, and whether v
// itself is zero.
func prune(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case string:
		return x, x == ""
	case bool:
		return x, !x
	case json.Number:
		f, err := x.Float64()
		return x, err == nil && f == 0
	case []any:
		for i := range x {
			x[i], _ = prune(x[i])
		}
		return x, len(x) == 0
	case map[string]any:
		for k, e := range x {
			if p, zero := prune(e); zero {
				delete(x, k)
			} else {
				x[k] = p
			}
		}
		return x, len(x) == 0
	}
	return v, false
}
