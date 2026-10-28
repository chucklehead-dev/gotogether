// Package values draws values of a Go type by reflection, from a few
// strings and integers: gristsim's generated arguments (gristsim.Gen),
// the values grist runs each upcaster over (App.CheckUpcasters) and the
// events a read model's fingerprint folds. It imports nothing of grist.
package values

import (
	"reflect"
	"sort"
)

// Gen is a value of typ drawn by pick: strings from alphabet, integers
// from ints (an unsigned one from its non-negative values), booleans,
// structs field by field (unexported fields left zero), slices of zero to
// two elements; anything else its zero value (a map, a pointer).
// A nil typ is an empty struct.
func Gen(typ reflect.Type, alphabet []string, ints []int, pick func(n int) int) any {
	if typ == nil {
		return struct{}{}
	}
	var uints []int
	for _, n := range ints {
		if n >= 0 {
			uints = append(uints, n)
		}
	}
	if len(uints) == 0 {
		uints = []int{0}
	}
	return gen(typ, alphabet, ints, uints, pick).Interface()
}

func gen(typ reflect.Type, alphabet []string, ints, uints []int, pick func(n int) int) reflect.Value {
	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		v.SetString(alphabet[pick(len(alphabet))])
	case reflect.Bool:
		v.SetBool(pick(2) == 1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(ints[pick(len(ints))]))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(uints[pick(len(uints))]))
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				v.Field(i).Set(gen(typ.Field(i).Type, alphabet, ints, uints, pick))
			}
		}
	case reflect.Slice:
		n := pick(3)
		s := reflect.MakeSlice(typ, n, n)
		for i := 0; i < n; i++ {
			s.Index(i).Set(gen(typ.Elem(), alphabet, ints, uints, pick))
		}
		v.Set(s)
	}
	return v
}

// All are the values of typ every combination of Gen's picks makes, at
// most max of them (the first in the picks' mixed-radix order): every
// string of the alphabet and every integer at each field, for a type of
// few fields.
func All(typ reflect.Type, alphabet []string, ints []int, max int) []any {
	var radix []int
	Gen(typ, alphabet, ints, func(n int) int { radix = append(radix, n); return 0 })
	total := 1
	for _, r := range radix {
		total = min(total*r, max)
	}
	out := make([]any, 0, total)
	for k := 0; k < total; k++ {
		n := k
		out = append(out, Gen(typ, alphabet, ints, func(m int) int { c := n % m; n /= m; return c }))
	}
	return out
}

// Perturb sets v (settable) to a value of its type that differs from
// its own: another string, integer or boolean, a slice one element
// longer, a map with one more key, a pointer nil or not, a struct with
// its first field it can perturb perturbed. It reports false for a type
// it cannot change (a struct of no such field, a func).
func Perturb(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		if v.String() == "zz" {
			v.SetString("yy")
		} else {
			v.SetString("zz")
		}
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 7)
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			m.SetMapIndex(k, v.MapIndex(k))
		}
		k := reflect.New(v.Type().Key()).Elem()
		for tries := 0; tries < 4 && m.MapIndex(k).IsValid(); tries++ {
			if !Perturb(k) {
				return false
			}
		}
		if m.MapIndex(k).IsValid() {
			return false
		}
		m.SetMapIndex(k, reflect.New(v.Type().Elem()).Elem())
		v.Set(m)
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		} else {
			v.Set(reflect.Zero(v.Type()))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() && Perturb(v.Field(i)) {
				return true
			}
		}
		return false
	default:
		return false
	}
	return true
}
