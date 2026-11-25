package qenv

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// effect records the send of v as an effect of kind (qobs.Effect) through
// ctx's recorder, if it has one: the doubles record what the code sent,
// at the send, before the environment decides its fate (a lost request
// is the network's, one effect; a retry is the code's, another). A step's
// effects are compared with the model's where a binding says so (I/O
// specifications); otherwise they are evidence.
func effect(ctx context.Context, kind string, v any) {
	if qobs.From(ctx) == nil {
		return // no run records here: nothing to build
	}
	qobs.Effect(ctx, kind, EffectFields(v)...)
}

// EffectFields are v's fields as a double's effect record carries them
// (qobs.Effect's keys and values): a struct's exported fields by their Go
// names (a pointer's, followed), a map's entries by their keys in sorted
// order, any other value as "value". A binding's effects section maps
// them onto the model's constructors ({{.Amount}}).
func EffectFields(v any) []any {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		t := rv.Type()
		kv := make([]any, 0, 2*t.NumField())
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() {
				kv = append(kv, f.Name, rv.Field(i).Interface())
			}
		}
		return kv
	case reflect.Map:
		keys := rv.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return cmp.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface())) })
		kv := make([]any, 0, 2*len(keys))
		for _, k := range keys {
			kv = append(kv, fmt.Sprint(k.Interface()), rv.MapIndex(k).Interface())
		}
		return kv
	case reflect.Invalid:
		return nil
	}
	return []any{"value", rv.Interface()}
}
