package canon

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

var kinds = map[string]Kind{"typecheck": Typecheck, "compile": Compile, "parse": Parse}

func form(t *testing.T, variant, o string, tier Tier) *Form {
	t.Helper()
	data := output(t, variant, o)
	var sm []byte
	if o == "parse" {
		sm = output(t, variant, "map")
	}
	if variant == "shifted" {
		// the module's ids, from the typecheck output (one parse numbers
		// every output alike): the compile output holds the main module alone
		_, also, err := StripModule(output(t, variant, "typecheck"), "zzshift", nil)
		if err != nil {
			t.Fatal(err)
		}
		var ids map[string]bool
		if data, ids, err = StripModule(data, "zzshift", also); err != nil {
			t.Fatal(err)
		}
		if sm != nil {
			if sm, err = StripSpans(sm, ids); err != nil {
				t.Fatal(err)
			}
		}
	}
	f, err := Of(data, sm, Options{Kind: kinds[o], Tier: tier, Roots: []string{"/ROOT"}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func tiers() map[string]Tier { return map[string]Tier{"read": Read, "all": All} }

// The form of a form is itself.
func TestIdempotent(t *testing.T) {
	for _, v := range []string{"base", "shifted"} {
		for o, k := range kinds {
			for tn, tier := range tiers() {
				f := form(t, v, o, tier)
				g, err := Of(f.JSON(), nil, Options{Kind: k, Tier: tier})
				if err != nil {
					t.Fatal(err)
				}
				if d := Diff(f, g); len(d) > 0 {
					t.Errorf("%s %s %s: canon of canon differs at %d places, first %v", v, o, tn, len(d), d[0])
				}
			}
		}
	}
}

// Every id of the shifted model differs from the model's (a module
// consumed ids first), and their forms are equal. Their parse outputs
// differ in the first line's columns too, which the harness's shifted
// front end corrects (qoracle); here, the typecheck and compile outputs,
// which hold no positions.
func TestShiftedIDs(t *testing.T) {
	for _, o := range []string{"typecheck", "compile"} {
		if string(output(t, "base", o)) == string(output(t, "shifted", o)) {
			t.Fatal("the shifted outputs equal the model's: the variant shifts nothing")
		}
		for tn, tier := range tiers() {
			a, b := form(t, "base", o, tier), form(t, "shifted", o, tier)
			if d := Diff(a, b); len(d) > 0 {
				t.Errorf("%s %s: shifted ids differ at %d places, first %v", o, tn, len(d), d[0])
			}
		}
	}
}

// within is whether a difference's path is under one of the named
// declarations (a declaration's path, or an instance's copy of it), or
// is an entry of the table, which keeps a whole copy of a definition at
// each reference to it in a parse, and in every tier All.
func within(x Difference, o string, tier Tier, names ...string) bool {
	if strings.HasPrefix(x.Path, "table.") && (o == "parse" || tier == All) {
		return true
	}
	for _, n := range names {
		for _, p := range []string{"/" + n + "/", "/" + n + "]", "::" + n + "/", "::" + n + "]", "/" + n + "."} {
			if strings.Contains(x.Path, p) || strings.HasSuffix(x.Path, "/"+n) {
				return true
			}
		}
	}
	return false
}

// A definition renamed (one no other references, of the same length, so
// no position moves) differs at that definition alone: its entry, and
// its nodes' types and spans, each keyed from it. The compile IR holds
// only what its main module references, so there it differs nowhere.
func TestRenameIsLocal(t *testing.T) {
	for o := range kinds {
		for tn, tier := range tiers() {
			d := Diff(form(t, "base", o, tier), form(t, "renamed", o, tier))
			if o == "compile" {
				if len(d) > 0 {
					t.Errorf("%s %s: an unreferenced definition renamed changed the IR: %v", o, tn, d[0])
				}
				continue
			}
			if len(d) == 0 {
				t.Fatalf("%s %s: no difference for a renamed definition", o, tn)
			}
			for _, x := range d {
				if !within(x, o, tier, "upd", "upq") {
					t.Errorf("%s %s: a difference outside upd: %v", o, tn, x)
				}
			}
		}
	}
}

// One operator changed differs at it, in each definition that holds it:
// f, and in the compile IR both instances' copies of f.
func TestChangeIsLocal(t *testing.T) {
	for o := range kinds {
		for tn, tier := range tiers() {
			d := Diff(form(t, "base", o, tier), form(t, "changed", o, tier))
			if len(d) == 0 {
				t.Fatalf("%s %s: no difference for a changed operator", o, tn)
			}
			for _, x := range d {
				if !within(x, o, tier, "f") {
					t.Errorf("%s %s: a difference outside f: %v", o, tn, x)
				}
			}
		}
	}
}

// The compile IR's suffixed names: an instance's copies keep the id of
// the original's lambda, which the IR does not hold (`_#k`); a binder
// the IR holds (`_@path`) is TestAgainstBruteForce's.
func TestSuffixedNames(t *testing.T) {
	f := form(t, "base", "compile", Read)
	js := string(f.JSON())
	for _, want := range []string{`"m::L::n_#1"`, `"m::lib::a_#2"`} {
		if !strings.Contains(js, want) {
			t.Errorf("no %s in the compile form", want)
		}
	}
	for _, bad := range []string{`"m::L::n_30"`, `"_t`} {
		if strings.Contains(js, bad) {
			t.Errorf("%s survived canonicalization", bad)
		}
	}
}

// dupModules is a parse output of two modules named main, as quint writes
// it for QNT102 (both kept), its ids shifted by shift and the first
// module's declarations in order (quint's: a layer sorted by its ids as
// strings), the second's value again.
func dupModules(t *testing.T, shift int, order []string, again int) []byte {
	t.Helper()
	ids := map[string]int{"a": 2, "b": 4, "e": 10}
	val := map[string]int{"a": 1, "b": 2, "e": 5}
	def := func(name string, id, v int) map[string]any {
		return map[string]any{"id": id + shift, "kind": "def", "name": name, "qualifier": "val",
			"expr": map[string]any{"id": id - 1 + shift, "kind": "int", "value": v}}
	}
	table := map[string]any{}
	var decls []any
	for _, n := range order {
		d := def(n, ids[n], val[n])
		decls = append(decls, d)
		table[strconv.Itoa(ids[n]+shift)] = d
	}
	doc := map[string]any{
		"stage": "parsing",
		"modules": []any{
			map[string]any{"id": 11 + shift, "name": "main", "declarations": decls},
			map[string]any{"id": 14 + shift, "name": "main", "declarations": []any{def("again", 13, again)}},
		},
		"table":  table,
		"errors": []any{},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Two modules of one name are keyed by their content, not by quint's
// order of a layer, which is its ids': shifted by 7, a, b and e (ids 2,
// 4, 10) come out b, e, a (ids "11", "17", "9"), and the forms are equal.
// The duplicate's content still counts: another value differs.
func TestDuplicateModulesKeyedByContent(t *testing.T) {
	of := func(doc []byte) *Form {
		f, err := Of(doc, nil, Options{Kind: Parse, Tier: Read})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	base := of(dupModules(t, 0, []string{"e", "a", "b"}, 1))
	shifted := of(dupModules(t, 7, []string{"b", "e", "a"}, 1))
	if d := Diff(base, shifted); len(d) > 0 {
		t.Fatalf("duplicate modules with a layer reordered by shifted ids differ at %d places, first %v", len(d), d[0])
	}
	// the form of the form is the form: its key reads no $layer
	if d := Diff(base, of(base.JSON())); len(d) > 0 {
		t.Fatalf("canon of canon differs for duplicate modules at %d places, first %v", len(d), d[0])
	}
	other := of(dupModules(t, 7, []string{"b", "e", "a"}, 2))
	d := Diff(base, other)
	if len(d) == 0 {
		t.Fatal("a duplicate module with another body has the same form")
	}
	for _, x := range d {
		if !strings.HasPrefix(x.Path, "modules[main~~") {
			t.Errorf("a difference outside the duplicate modules: %v", x)
		}
	}
}

// twinVars is a parse output and its source map of a module declaring
// `var x: int` twice (QNT101), the first on line first, the second on
// line second, written in order (quint's: by their ids as strings).
func twinVars(t *testing.T, shift, first, second int, order []int) ([]byte, []byte) {
	t.Helper()
	ids := []int{2 + shift, 4 + shift}
	lines := []int{first, second}
	table := map[string]any{}
	spans := map[string]any{}
	var decls []any
	for _, i := range order {
		d := map[string]any{"id": ids[i], "kind": "var", "name": "x", "typeAnnotation": map[string]any{"id": ids[i] - 1, "kind": "int"}}
		decls = append(decls, d)
		table[strconv.Itoa(ids[i])] = d
	}
	for i, id := range ids {
		pos := func(col int) map[string]any {
			return map[string]any{"line": lines[i], "col": col, "index": 20*lines[i] + col}
		}
		spans[strconv.Itoa(id)] = []any{0, pos(2), pos(12)}
		spans[strconv.Itoa(id-1)] = []any{0, pos(9), pos(12)}
	}
	doc, err := json.Marshal(map[string]any{"stage": "parsing", "modules": []any{map[string]any{"id": 5 + shift, "name": "main", "declarations": decls}}, "table": table, "errors": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	sm, err := json.Marshal(map[string]any{"sourceIndex": map[string]any{"0": "/ROOT/main.qnt"}, "map": spans})
	if err != nil {
		t.Fatal(err)
	}
	return doc, sm
}

// Declarations alike in name, kind and content are told apart by their
// places in the source, not by quint's order (its ids'): shifted by 7,
// ids 2 and 4 become "9" and "11", which sort the other way, and the
// forms are equal, spans included. A twin at another place differs.
func TestTwinDeclarationsByPlace(t *testing.T) {
	of := func(doc, sm []byte) *Form {
		f, err := Of(doc, sm, Options{Kind: Parse, Tier: Read, Roots: []string{"/ROOT"}})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	base := of(twinVars(t, 0, 1, 2, []int{0, 1}))
	shifted := of(twinVars(t, 7, 1, 2, []int{1, 0}))
	if d := Diff(base, shifted); len(d) > 0 {
		t.Fatalf("twin declarations in another order differ at %d places, first %v", len(d), d[0])
	}
	if d := Diff(base, of(base.JSON(), nil)); len(d) > 0 {
		t.Fatalf("canon of canon differs for twin declarations at %d places, first %v", len(d), d[0])
	}
	moved := of(twinVars(t, 7, 1, 3, []int{1, 0}))
	if len(Diff(base, moved)) == 0 {
		t.Fatal("a twin declaration on another line has the same form")
	}
}
