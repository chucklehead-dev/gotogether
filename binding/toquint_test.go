package binding

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func typedModel(t *testing.T) *Model {
	t.Helper()
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/typed.qnt", "typed")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A parameter's type is printed as toQuint parses it, aliases by name,
// and a parameter whose type holds a record, a sum or a collection the
// older functions do not render defaults to toQuint by that type.
func TestTypedDefaults(t *testing.T) {
	m := typedModel(t)
	want := map[string][2]string{
		"batch":  {"Batch", `{{ toQuint "Batch" .batch }}`},
		"rs":     {"Set[Read]", `{{ toQuint "Set[Read]" .rs }}`},
		"tally":  {"str -> List[int]", `{{ toQuint "str -> List[int]" .tally }}`},
		"pair":   {"(int, bool)", `{{ toQuint "(int, bool)" .pair }}`},
		"nested": {"{ a: { b: List[(int, str)] } }", `{{ toQuint "{ a: { b: List[(int, str)] } }" .nested }}`},
		"n":      {"int", `{{.n}}`},
	}
	for _, p := range m.Actions["commit"] {
		w := want[p.Name]
		if p.Type != w[0] || p.DefaultTemplate() != w[1] {
			t.Errorf("%s: type %q, default %s; want %q, %s", p.Name, p.Type, p.DefaultTemplate(), w[0], w[1])
		}
	}
	// Option[str] reaches the model's IR expanded: its alternatives
	if p := m.Actions["reset"][0]; p.Type != "Some(str) | None" || p.DefaultTemplate() != `{{ toQuint "Some(str) | None" .why }}` {
		t.Errorf("why: type %q, default %s", p.Type, p.DefaultTemplate())
	}
}

// Read and Event as the application records them: Go structs without
// itf tags (qobs records a slice of them as JSON, fields by Go name).
type tRead struct {
	Typ  string
	Tags [][2]string
}

type tEvent struct {
	Tag   string         `json:"tag"`
	Value map[string]any `json:"value,omitempty"`
}

// A run recorded through qobs (slices, maps and a struct's JSON text)
// plans to calls whose arguments are the model's values: each the same
// itf.Value as built from Go, and each parsing back to itself.
func TestToQuintPlan(t *testing.T) {
	m := typedModel(t)
	b := &Binding{Spec: "typed.qnt", Module: "typed", Actions: map[string]Action{
		"commit": {Expect: `log == {{ toQuint "List[Event]" .obs.log }}`},
	}}
	batch, _ := json.Marshal(map[string]any{
		"events": []tEvent{{"Created", map[string]any{"counter": "c1", "name": "x"}}, {Tag: "Reset"}},
		"at":     1700000000000, "cause": map[string]any{"Some": "import"},
	})
	sink := &qobs.MemorySink{}
	r := qobs.NewRecorder(sink, qobs.WithActor("t1"))
	r.Record(context.Background(), "commit", nil,
		"batch", string(batch),
		"rs", []tRead{{"counter", [][2]string{{"name", "x"}, {"name", "x"}}}, {"counter", nil}},
		"tally", map[string][]int{"b": {2, 1}, "a": {}},
		"pair", []any{7, true},
		"nested", `{"a": {"b": [[1, "one"], [-2, "minus two"]]}}`,
		"n", 3,
		"obs.log", `[{"tag": "Created", "counter": "c1", "name": "x"}, "Reset"]`)
	r.Record(context.Background(), "reset", nil, "why", "None")
	r.Record(context.Background(), "reset", nil, "why", `{"tag": "Some", "value": "rollback"}`)
	tr := &qtrace.Trace{Actor: "t1", Processes: []string{sink.Steps()[0].Process}, Steps: sink.Steps()}
	plan, err := b.Plan(m, tr)
	if err != nil {
		t.Fatal(err)
	}
	created := itf.VariantOf("Created", itf.RecordOf("counter", itf.StrOf("c1"), "name", itf.StrOf("x")))
	wantArgs := map[string]itf.Value{
		"batch": itf.RecordOf("events", itf.ListOf(created, itf.Enum("Reset")), "at", itf.IntOf(1700000000000), "cause", itf.Some(itf.StrOf("import"))),
		"rs": itf.SetOf(
			itf.RecordOf("typ", itf.StrOf("counter"), "tags", itf.SetOf(itf.TupleOf(itf.StrOf("name"), itf.StrOf("x")))),
			itf.RecordOf("typ", itf.StrOf("counter"), "tags", itf.SetOf())),
		"tally":  {Kind: itf.Map, Pairs: [][2]itf.Value{{itf.StrOf("a"), itf.ListOf()}, {itf.StrOf("b"), itf.ListOf(itf.IntOf(2), itf.IntOf(1))}}},
		"pair":   itf.TupleOf(itf.IntOf(7), itf.BoolOf(true)),
		"nested": itf.RecordOf("a", itf.RecordOf("b", itf.ListOf(itf.TupleOf(itf.IntOf(1), itf.StrOf("one")), itf.TupleOf(itf.IntOf(-2), itf.StrOf("minus two"))))),
		"n":      itf.IntOf(3),
	}
	c := plan.Calls[0]
	for name, want := range wantArgs {
		got := c.Args[name]
		if got != want.String() {
			t.Errorf("%s: %s\n  want %s", name, got, want)
			continue
		}
		back, err := itf.ParseQuint(got)
		if err != nil || !back.Equal(want) {
			t.Errorf("%s: %s parses back as %v (%v)", name, got, back, err)
		}
	}
	if want := "log == " + itf.ListOf(created, itf.Enum("Reset")).String(); c.Expect != want {
		t.Errorf("expect %s, want %s", c.Expect, want)
	}
	if got := []string{plan.Calls[1].Quint, plan.Calls[2].Quint}; got[0] != "reset(None)" || got[1] != `reset(Some("rollback"))` {
		t.Errorf("resets: %q", got)
	}
}

// What toQuint refuses names where, and the fix: a number with a
// fraction (in a collection, or a float recorded whole), a time, a
// record's missing or extra field, a tag the sum lacks, an alias the
// model lacks, a field that is not JSON.
func TestToQuintRefuses(t *testing.T) {
	m := typedModel(t)
	for _, tc := range []struct {
		typ  string
		v    any
		says string
	}{
		{"Batch", `{"events": [], "at": 1.5, "cause": "None"}`, "at .at: 1.5 is not an integer: Quint has no fractions; record an integer unit (cents, milliseconds) and type the field int"},
		{"List[int]", `[1, 2e3]`, "at [1]: 2e3 is not an integer"},
		{"int", qfloat(2.5), "at the value: 2.5 is not an integer"},
		{"Batch", `{"events": [], "at": "2026-10-27T10:00:00Z", "cause": "None"}`, `at .at: "2026-10-27T10:00:00Z" is a time: Quint has none; record an integer unit (time.UnixMilli)`},
		{"str", qlit(quintLit(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).String())), "is a time"},
		{"Read", `{"typ": "x"}`, "the record has no field tags (it has: typ; Read needs every field)"},
		{"Read", `{"typ": "x", "tags": [], "extra": 1}`, "the field extra is none of Read's"},
		{"Event", `{"tag": "Deleted", "value": {}}`, `the tag "Deleted" is none of Event's (Created, Renamed, Reset)`},
		{"Event", `"Created"`, "Created carries { counter: str, name: str }; the value gives it nothing"},
		{"List[Evnt]", `[]`, "the type Evnt, which the model does not declare (its types: Batch, Event, Option, Read)"},
		{"Batch", "{c 1}", "which is not JSON: record Batch as JSON"},
		{"(int, int)", `[1]`, "where (int, int) goes (a JSON array of 2)"},
		{"int -> str", `{"x": "y"}`, `at ["x"](key): x is not an integer`},
		{"Set[int]", `[1, null]`, "at [1]: null where int goes"},
	} {
		_, err := toQuint(m, tc.typ, value(tc.v))
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("toQuint %s %v: %v, want it to say %q", tc.typ, tc.v, err, tc.says)
		}
	}
	// without a model, a type of built-ins still works and an alias does not
	if got, err := toQuint(nil, "Set[(str, int)]", value(`[["a", 1]]`)); err != nil || got != `Set(("a", 1))` {
		t.Errorf("no model: %s, %v", got, err)
	}
	if _, err := toQuint(nil, "Read", value(`{}`)); err == nil || !strings.Contains(err.Error(), "the model does not declare (its types: none)") {
		t.Errorf("no model, an alias: %v", err)
	}
}

// value is v as a template sees a step field.
func value(v any) any {
	switch x := v.(type) {
	case qlit, qfloat:
		return x
	}
	return fieldValue(v)
}

// toQuint decodes every encoding it documents, over random types and
// values: a value of a random type, written as JSON in a random one of
// the encodings, decodes to the value itself (itf's).
func TestToQuintRoundTripAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	for i := range 3000 {
		typ := randType(r, 3)
		v := randValue(r, typ, 3)
		j := randJSON(r, v, typ)
		text, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		field := any(string(text))
		if typ.kind == "str" {
			field = v.S
		}
		got, err := toQuint(nil, typ.String(), value(field))
		if err != nil {
			t.Fatalf("%d: %s as %s: %v", i, text, typ, err)
		}
		if got != v.String() {
			t.Fatalf("%d: %s as %s: %s, want %s", i, text, typ, got, v)
		}
		if back, err := itf.ParseQuint(got); err != nil || !back.Equal(v) {
			t.Fatalf("%d: %s parses back as %v (%v)", i, got, back, err)
		}
	}
}

func randType(r *rand.Rand, depth int) *qtype {
	n := 3
	if depth > 0 {
		n = 10
	}
	switch k := r.IntN(n); k {
	case 0:
		return &qtype{kind: "int"}
	case 1:
		return &qtype{kind: "str"}
	case 2:
		return &qtype{kind: "bool"}
	case 3:
		return &qtype{kind: "set", elem: randType(r, depth-1)}
	case 4:
		return &qtype{kind: "list", elem: randType(r, depth-1)}
	case 5:
		return &qtype{kind: "map", elem: []*qtype{{kind: "int"}, {kind: "str"}, randType(r, depth-1)}[r.IntN(3)], val: randType(r, depth-1)}
	case 6:
		t := &qtype{kind: "tup"}
		for i := range 2 + r.IntN(2) {
			t.fields = append(t.fields, qfield{name: fmt.Sprint(i), typ: randType(r, depth-1)})
		}
		return t
	case 7, 8:
		t := &qtype{kind: "rec"}
		for _, f := range []string{"a", "b_c", "d"}[:1+r.IntN(3)] {
			t.fields = append(t.fields, qfield{name: f, typ: randType(r, depth-1)})
		}
		return t
	}
	t := &qtype{kind: "sum"}
	tags := []string{"A", "B", "None"}[:1+r.IntN(3)]
	for _, tag := range tags {
		p := &qtype{kind: "tup"}
		if r.IntN(2) == 0 || len(tags) == 1 { // one alternative alone written bare would read as an alias
			p = randType(r, depth-1)
		}
		t.fields = append(t.fields, qfield{name: tag, typ: p})
	}
	return t
}

func randValue(r *rand.Rand, t *qtype, depth int) itf.Value {
	switch t.kind {
	case "int":
		return itf.Value{Kind: itf.Int, I: big.NewInt(r.Int64N(2000) - 1000)}
	case "str":
		return itf.StrOf([]string{"", "a", "x y", "é", `q"uote`, "2026"}[r.IntN(6)])
	case "bool":
		return itf.BoolOf(r.IntN(2) == 0)
	case "set", "list":
		v := itf.Value{Kind: itf.List}
		if t.kind == "set" {
			v.Kind = itf.Set
		}
		seen := map[string]bool{}
		for range r.IntN(3) {
			e := randValue(r, t.elem, depth-1)
			if t.kind == "set" && seen[e.String()] {
				continue
			}
			seen[e.String()] = true
			v.Elems = append(v.Elems, e)
		}
		return v
	case "map":
		v := itf.Value{Kind: itf.Map}
		seen := map[string]bool{}
		for range r.IntN(3) {
			k := randValue(r, t.elem, depth-1)
			if seen[k.String()] {
				continue
			}
			seen[k.String()] = true
			v.Pairs = append(v.Pairs, [2]itf.Value{k, randValue(r, t.val, depth-1)})
		}
		return v
	case "tup":
		v := itf.Value{Kind: itf.Tuple}
		for _, f := range t.fields {
			v.Elems = append(v.Elems, randValue(r, f.typ, depth-1))
		}
		return v
	case "rec":
		v := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{}}
		for _, f := range t.fields {
			v.Fields[f.name] = randValue(r, f.typ, depth-1)
		}
		return v
	}
	f := t.fields[r.IntN(len(t.fields))]
	if f.typ.kind == "tup" && len(f.typ.fields) == 0 {
		return itf.Enum(f.name)
	}
	return itf.VariantOf(f.name, randValue(r, f.typ, depth-1))
}

// randJSON writes v (of type t) as JSON, in one of the encodings toQuint
// takes, chosen at random at every level.
func randJSON(r *rand.Rand, v itf.Value, t *qtype) any {
	switch t.kind {
	case "int":
		return json.Number(v.I.String())
	case "str":
		return v.S
	case "bool":
		return v.B
	case "set", "list", "tup":
		out := []any{}
		for i, e := range v.Elems {
			et := t.elem
			if t.kind == "tup" {
				et = t.fields[i].typ
			}
			out = append(out, randJSON(r, e, et))
		}
		if t.kind == "set" && len(out) > 1 && r.IntN(2) == 0 { // a set's order and duplicates do not matter
			out = append(out[1:], out[0], out[0])
		}
		return out
	case "map":
		if k := t.elem.kind; (k == "str" || k == "int") && r.IntN(2) == 0 {
			out := map[string]any{}
			for _, p := range v.Pairs {
				key := p[0].S
				if k == "int" {
					key = p[0].I.String()
				}
				out[key] = randJSON(r, p[1], t.val)
			}
			return out
		}
		out := []any{}
		for _, p := range v.Pairs {
			out = append(out, []any{randJSON(r, p[0], t.elem), randJSON(r, p[1], t.val)})
		}
		return out
	case "rec":
		out := map[string]any{}
		for _, f := range t.fields {
			key := f.name
			if r.IntN(2) == 0 { // as Go names a field
				key = strings.ToUpper(key[:1]) + strings.ReplaceAll(key[1:], "_", "")
			}
			out[key] = randJSON(r, v.Fields[f.name], f.typ)
		}
		return out
	}
	var payload *qtype
	for _, f := range t.fields {
		if f.name == v.Tag {
			payload = f.typ
		}
	}
	if v.Inner == nil || len(v.Inner.Elems) == 0 && v.Inner.Kind == itf.Tuple && payload.kind == "tup" && len(payload.fields) == 0 {
		switch r.IntN(3) {
		case 0:
			return v.Tag
		case 1:
			if v.Tag == "None" {
				return nil
			}
		}
		return map[string]any{"tag": v.Tag}
	}
	p := randJSON(r, *v.Inner, payload)
	switch r.IntN(3) {
	case 0:
		return map[string]any{v.Tag: p}
	case 1:
		if m, ok := p.(map[string]any); ok && payload.kind == "rec" {
			if _, clash := m["tag"]; !clash {
				m["tag"] = v.Tag
				return m
			}
		}
	}
	return map[string]any{"tag": v.Tag, "value": p}
}
