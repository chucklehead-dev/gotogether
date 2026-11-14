package connect

import (
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strings"
)

// maxDiff bounds the number of differences reported.
const maxDiff = 20

// diffValues lists the paths at which want (the model) and got (the
// implementation) differ, as "path: model X, implementation Y". Equal
// values give nil.
func diffValues(want, got any) []string {
	return diffNamed(want, got, "model", "implementation")
}

// diffNamed is diffValues with the two sides named (two implementations
// of a Differential).
func diffNamed(want, got any, wantName, gotName string) []string {
	var out []string
	diff(reflect.ValueOf(want), reflect.ValueOf(got), "", &out, [2]string{wantName, gotName})
	if len(out) > maxDiff {
		out = append(out[:maxDiff], fmt.Sprintf("... and %d more", len(out)-maxDiff))
	}
	return out
}

// skipped reports whether f is tagged out of decoding (`itf:"-"`, or
// `json:"-"` without an itf tag, as itf.Decode reads them): it holds the
// zero value in the model's state, so comparing it would fail falsely.
func skipped(f reflect.StructField) bool {
	tag, ok := f.Tag.Lookup("itf")
	if !ok {
		tag = f.Tag.Get("json")
	}
	return strings.Split(tag, ",")[0] == "-"
}

func show(v reflect.Value) string {
	if !v.IsValid() {
		return "<absent>"
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return "nil"
	}
	if b, ok := v.Interface().(*big.Int); ok {
		return b.String()
	}
	return fmt.Sprintf("%#v", v.Interface())
}

func diff(a, b reflect.Value, path string, out *[]string, names [2]string) {
	if len(*out) > maxDiff {
		return
	}
	add := func() {
		p := path
		if p == "" {
			p = "(state)"
		}
		*out = append(*out, fmt.Sprintf("%s: %s %s, %s %s", p, names[0], show(a), names[1], show(b)))
	}
	if !a.IsValid() || !b.IsValid() {
		if a.IsValid() != b.IsValid() {
			add()
		}
		return
	}
	if a.Type() != b.Type() {
		add()
		return
	}
	if a.CanInterface() {
		if x, ok := a.Interface().(big.Int); ok {
			y := b.Interface().(big.Int)
			if x.Cmp(&y) != 0 {
				add()
			}
			return
		}
		if x, ok := a.Interface().(*big.Int); ok {
			y := b.Interface().(*big.Int)
			if (x == nil) != (y == nil) || (x != nil && x.Cmp(y) != 0) {
				add()
			}
			return
		}
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				add()
			}
			return
		}
		diff(a.Elem(), b.Elem(), path, out, names)
	case reflect.Struct:
		t := a.Type()
		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).IsExported() || skipped(t.Field(i)) {
				continue
			}
			diff(a.Field(i), b.Field(i), path+"."+t.Field(i).Name, out, names)
		}
	case reflect.Slice, reflect.Array:
		if a.Kind() == reflect.Slice && a.Len() != b.Len() {
			if !reflect.DeepEqual(a.Interface(), b.Interface()) {
				*out = append(*out, fmt.Sprintf("%s: %s has %d elements, %s %d: %s %s, %s %s",
					orState(path), names[0], a.Len(), names[1], b.Len(), names[0], show(a), names[1], show(b)))
			}
			return
		}
		for i := 0; i < a.Len(); i++ {
			diff(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), out, names)
		}
	case reflect.Map:
		keys := map[string]reflect.Value{}
		for _, k := range a.MapKeys() {
			keys[fmt.Sprint(k.Interface())] = k
		}
		for _, k := range b.MapKeys() {
			keys[fmt.Sprint(k.Interface())] = k
		}
		for _, n := range slices.Sorted(maps.Keys(keys)) {
			k := keys[n]
			diff(a.MapIndex(k), b.MapIndex(k), fmt.Sprintf("%s[%v]", path, n), out, names)
		}
	default:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			add()
		}
	}
}

func orState(p string) string {
	if p == "" {
		return "(state)"
	}
	return p
}
