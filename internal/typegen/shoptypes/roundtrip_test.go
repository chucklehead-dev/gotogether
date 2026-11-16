package shoptypes

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// Every state of a quint run of the model (`quint run --mbt` of
// ../testdata/shop.qnt, seed 2, in testdata/shop.itf.json) decodes into
// the generated State, strictly (DecodeProjection: each Go field one of
// the record's), and every field of State, at any depth, receives a value
// in some state: no field the generator wrote is left out of the decoding
// by a wrong tag, a wrong shape or a wrong name. The trace's variables are
// State's fields, so a variable the model gained is not silently unread.
//
// The trace is fixed, so the test needs no quint: write it again with
// quint run --mbt --max-samples 1 --max-steps 6 --seed 2 --out-itf
// ../shoptypes/testdata/shop.itf.json shop.qnt (in ../testdata) after the
// model changes.
func TestDecodeEveryState(t *testing.T) {
	tr, err := itf.ReadFile("testdata/shop.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]bool{}
	st := reflect.TypeFor[State]()
	for i := range st.NumField() {
		tags[st.Field(i).Tag.Get("itf")] = true
	}
	filled := map[string]bool{}
	phases, events := map[Phase]bool{}, map[EventTag]bool{}
	for i, s := range tr.States {
		for name := range s.Vars {
			if !tags[name] {
				t.Errorf("state %d: variable %s has no field of State", i, name)
			}
		}
		if len(s.Vars) != st.NumField() {
			t.Errorf("state %d: %d variables, State has %d fields", i, len(s.Vars), st.NumField())
		}
		var got State
		if err := itf.DecodeProjection(itf.Value{Kind: itf.Record, Fields: s.Vars}, &got); err != nil {
			t.Fatalf("state %d: %v", i, err)
		}
		walk(reflect.ValueOf(got), "State", filled)
		phases[got.Phase] = true
		for _, e := range got.Log {
			events[e.Tag] = true
		}
		if got.Last != nil {
			events[got.Last.Tag] = true
		}
	}
	var want []string
	paths(st, "State", map[reflect.Type]bool{}, &want)
	for _, p := range want {
		if !filled[p] {
			t.Errorf("%s never received a value", p)
		}
	}
	if len(want) < 25 {
		t.Errorf("only %d fields to fill: %v", len(want), want)
	}
	for _, p := range []Phase{PhaseIdle, PhaseOpen, PhaseClosed} {
		if !phases[p] {
			t.Errorf("no state's phase is %s", p)
		}
	}
	for _, e := range []EventTag{EventAdded, EventRemoved, EventMoved, EventCleared} {
		if !events[e] {
			t.Errorf("no event is %s", e)
		}
	}
}

// paths are the field paths of t, through pointers, slices and maps (an
// element's path is the collection's with []), each struct field one.
func paths(t reflect.Type, at string, seen map[reflect.Type]bool, out *[]string) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		paths(t.Elem(), at+"[]", seen, out)
	case reflect.Map:
		paths(t.Key(), at+"{key}", seen, out)
		paths(t.Elem(), at+"[]", seen, out)
	case reflect.Struct:
		if t == reflect.TypeFor[itf.Value]() || seen[t] {
			return
		}
		seen[t] = true
		defer delete(seen, t)
		for i := range t.NumField() {
			p := at + "." + t.Field(i).Name
			*out = append(*out, p)
			paths(t.Field(i).Type, p, seen, out)
		}
	}
}

// walk marks the paths of v's fields that hold a value other than their
// zero.
func walk(v reflect.Value, at string, filled map[string]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			walk(v.Elem(), at+"[]", filled)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walk(v.Index(i), at+"[]", filled)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walk(k, at+"{key}", filled)
			walk(v.MapIndex(k), at+"[]", filled)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			p := at + "." + v.Type().Field(i).Name
			if !v.Field(i).IsZero() {
				filled[p] = true
			}
			walk(v.Field(i), p, filled)
		}
	}
}

// The generated types are what Decode's rules expect of each kind: an
// enum a string type marked for Encode, a sum a struct tagged by its
// variant, an Option a pointer, a map with a record key a Go map, one
// whose key is a set a slice of pairs, and the uninterpreted Ref the ITF
// value itself.
func TestShapes(t *testing.T) {
	if v, err := itf.Encode(PhaseOpen); err != nil || v.Kind != itf.Variant || v.Tag != "Open" {
		t.Errorf("Encode(PhaseOpen) = %v, %v: want the variant Open", v, err)
	}
	ev := reflect.TypeFor[Event]()
	if f, ok := ev.FieldByName("Tag"); !ok || f.Tag.Get("itf") != ",tag" || f.Type != reflect.TypeFor[EventTag]() {
		t.Errorf("Event's Tag: %+v", f)
	}
	if _, ok := ev.FieldByName("Cleared"); ok {
		t.Error("Event has a field for Cleared, which carries no value")
	}
	s := reflect.TypeFor[State]()
	for name, kind := range map[string]reflect.Kind{"Last": reflect.Pointer, "Stock": reflect.Map, "Groups": reflect.Slice, "Pair": reflect.Struct} {
		if f, _ := s.FieldByName(name); f.Type.Kind() != kind {
			t.Errorf("State.%s is a %s, want a %s", name, f.Type.Kind(), kind)
		}
	}
	if f, _ := s.FieldByName("Stock"); f.Type.Key() != reflect.TypeFor[Slot]() {
		t.Errorf("State.Stock's key is %s, want Slot", f.Type.Key())
	}
	if reflect.TypeFor[Ref]() != reflect.TypeFor[itf.Value]() {
		t.Error("Ref is not itf.Value")
	}
	var empty []string
	for i := range s.NumField() {
		if !strings.HasPrefix(s.Field(i).Tag.Get("itf"), strings.ToLower(s.Field(i).Name[:1])) {
			empty = append(empty, s.Field(i).Name)
		}
	}
	if slices.Sort(empty); len(empty) > 0 {
		t.Errorf("fields not tagged with their variable's name: %v", empty)
	}
}
