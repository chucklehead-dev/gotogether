package qoracle

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qoracle/canon"
)

// qerr is an error of quint's JSON: its code, on a line of main.qnt (a
// QNT098 with its cycle's modules listed).
type qerr struct {
	code  string
	line  int
	cycle string
}

// outputOf is an output whose parse and typecheck documents hold no
// modules and errs, the typecheck's tc when given.
func outputOf(t *testing.T, errs []qerr, tc ...[]qerr) *Output {
	t.Helper()
	doc := func(es []qerr) []byte {
		var out []any
		for _, e := range es {
			pos := func(col int) map[string]any {
				return map[string]any{"line": e.line, "col": col, "index": 10*e.line + col}
			}
			expl := fmt.Sprintf("[%s] an error", e.code)
			if e.cycle != "" {
				expl = fmt.Sprintf("[%s] Cyclic imports among: %s", e.code, e.cycle)
			}
			out = append(out, map[string]any{
				"explanation": expl,
				"locs":        []any{map[string]any{"source": "/r/main.qnt", "start": pos(2), "end": pos(30)}},
			})
		}
		b, err := json.Marshal(map[string]any{"stage": "parsing", "modules": []any{}, "table": map[string]any{}, "errors": out})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	o := &Output{Root: "/r", Parse: doc(errs), Typecheck: doc(errs)}
	if len(tc) > 0 {
		o.Typecheck = doc(tc[0])
	}
	return o
}

// withModule is o with its parse holding a module main of `val a = v`.
func withModule(t *testing.T, o *Output, v int) *Output {
	t.Helper()
	doc, err := decodeJSON(o.Parse)
	if err != nil {
		t.Fatal(err)
	}
	def := map[string]any{"id": 2, "kind": "def", "name": "a", "qualifier": "val", "expr": map[string]any{"id": 1, "kind": "int", "value": v}}
	top := doc.(map[string]any)
	top["modules"] = []any{map[string]any{"id": 3, "name": "main", "declarations": []any{def}}}
	top["table"] = map[string]any{"2": def}
	if o.Parse, err = json.Marshal(top); err != nil {
		t.Fatal(err)
	}
	return o
}

// The cycle-order class is accepted (testdata/shift-known.txt lists it),
// and listed: QNT099's errors the same set with another first, a QNT098's
// modules the same set listed and pointed at otherwise. The form alone
// (CompareOutputs, the class not accepted) finds the disagreement it is.
// Anything else of the same shape is a disagreement: an error missing,
// extra or elsewhere, a first error that is no QNT099, another module in
// an import cycle, or another output differing beside or after it.
func TestKnownCycleOrder(t *testing.T) {
	cycle := []qerr{{"QNT099", 12, ""}, {"QNT099", 11, ""}}
	swapped := []qerr{{"QNT099", 11, ""}, {"QNT099", 12, ""}}
	both := []int{int(canon.Parse), int(canon.Typecheck)}
	cases := []struct {
		name  string
		a, b  *Output
		known bool  // accepted
		tier  Tier  // the disagreement's otherwise
		kinds []int // the outputs the class is listed for
	}{
		{"swapped", outputOf(t, cycle), outputOf(t, swapped), true, TierErrors, both},
		{"swapped among others", outputOf(t, []qerr{{"QNT099", 12, ""}, {"QNT404", 3, ""}, {"QNT099", 11, ""}}), outputOf(t, []qerr{{"QNT099", 11, ""}, {"QNT404", 3, ""}, {"QNT099", 12, ""}}), true, TierErrors, both},
		{"import cycle", outputOf(t, []qerr{{"QNT098", 1, "a, b"}}), outputOf(t, []qerr{{"QNT098", 5, "b, a"}}), true, TierErrors, both},
		{"import cycle of other modules", outputOf(t, []qerr{{"QNT098", 1, "a, b"}}), outputOf(t, []qerr{{"QNT098", 5, "b, c"}}), false, TierErrors, nil},
		{"one missing", outputOf(t, cycle), outputOf(t, []qerr{{"QNT099", 11, ""}}), false, TierErrors, nil},
		{"one extra", outputOf(t, cycle), outputOf(t, []qerr{{"QNT099", 11, ""}, {"QNT099", 12, ""}, {"QNT099", 13, ""}}), false, TierErrors, nil},
		{"one elsewhere", outputOf(t, cycle), outputOf(t, []qerr{{"QNT099", 11, ""}, {"QNT099", 13, ""}}), false, TierErrors, nil},
		{"first no cycle", outputOf(t, []qerr{{"QNT404", 3, ""}, {"QNT099", 11, ""}}), outputOf(t, []qerr{{"QNT099", 11, ""}, {"QNT404", 3, ""}}), false, TierErrors, nil},
		{"a module differs too", withModule(t, outputOf(t, cycle), 1), withModule(t, outputOf(t, swapped), 2), false, TierParse, nil},
		{"typecheck differs after", outputOf(t, cycle), outputOf(t, swapped, []qerr{{"QNT099", 11, ""}}), false, TierErrors, []int{int(canon.Parse)}},
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			d, known, err := CompareOutputsKnown(c.a, c.b)
			if err != nil {
				t.Fatal(err)
			}
			var kinds []int
			for _, k := range known {
				if k.Class != ClassCycleOrder {
					t.Errorf("known class %s, want %s", k.Class, ClassCycleOrder)
				}
				kinds = append(kinds, int(k.Kind))
			}
			if !slices.Equal(kinds, c.kinds) {
				t.Errorf("known in outputs %v, want %v", kinds, c.kinds)
			}
			if c.known && d != nil {
				t.Errorf("%v, want the class accepted", d)
			}
			if !c.known && (d == nil || d.Tier != c.tier) {
				t.Errorf("%v, want a disagreement at %s", d, c.tier)
			}
			// the form alone: the class is the disagreement it is
			if d, err := CompareOutputs(c.a, c.b); err != nil || d == nil || d.Tier != c.tier {
				t.Errorf("the form alone: %v %v, want a disagreement at %s", d, err, c.tier)
			}
		})
	}
}

// testdata/shift-known.txt accepts the classes classOf tells for every
// program, and its lines are well formed: a class the oracle knows, *
// for all but a verdict flip, which names its entry.
func TestShiftKnownLines(t *testing.T) {
	ls, err := ShiftKnownLines()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Class{ClassCycleOrder, ClassTypesAfterError, ClassLayerOrder} {
		if !accepted[c] {
			t.Errorf("testdata/shift-known.txt does not accept %s for every program", c)
		}
	}
	flips := 0
	for _, l := range ls {
		if l.Class == ClassVerdictFlip {
			flips++
		}
	}
	if flips == 0 {
		t.Error("no verdict-flip entries: quint 0.33.1 flips two corpus verdicts")
	}
	for _, bad := range []string{"no-such-class * # x", "cycle-order some-entry # x", "verdict-flip * # x", "cycle-order *"} {
		if _, err := parseShiftKnown(bad); err == nil {
			t.Errorf("%q: read without an error", bad)
		}
	}
}

// typecheckForm is a typecheck form with errs (codes) and types, each
// node's kind.
func typecheckForm(errs []string, types map[string]string) *canon.Form {
	es := []any{}
	for _, c := range errs {
		es = append(es, map[string]any{"code": c, "locs": []any{}})
	}
	ts := map[string]any{}
	for n, k := range types {
		ts[n] = map[string]any{"type": map[string]any{"kind": k}}
	}
	return &canon.Form{Tree: map[string]any{"modules": []any{}, "errors": es, "types": ts}}
}

// The types-after-error class (a typecheck with errors, other nodes
// typed, none typed otherwise) is known in the typecheck output alone;
// a node typed otherwise, other errors, no errors, or another output is
// a disagreement.
func TestKnownTypesAfterError(t *testing.T) {
	base := typecheckForm([]string{"QNT000"}, map[string]string{"m/a": "int", "m/b": "bool"})
	cases := []struct {
		name  string
		b     *canon.Form
		kind  canon.Kind
		known bool
	}{
		{"fewer typed", typecheckForm([]string{"QNT000"}, map[string]string{"m/a": "int"}), canon.Typecheck, true},
		{"others typed", typecheckForm([]string{"QNT000"}, map[string]string{"m/a": "int", "m/c": "str"}), canon.Typecheck, true},
		{"typed otherwise", typecheckForm([]string{"QNT000"}, map[string]string{"m/a": "str"}), canon.Typecheck, false},
		{"other errors", typecheckForm([]string{"QNT404"}, map[string]string{"m/a": "int"}), canon.Typecheck, false},
		{"parse output", typecheckForm([]string{"QNT000"}, map[string]string{"m/a": "int"}), canon.Parse, false},
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			d := canon.Diff(base, c.b)
			if len(d) == 0 {
				t.Fatal("the forms are equal")
			}
			cl, ok := classOf(c.kind, base, c.b, d, &Output{}, &Output{})
			if ok != c.known || (ok && cl != ClassTypesAfterError) {
				t.Errorf("class %q %v, want known %v", cl, ok, c.known)
			}
		})
	}
	// without errors, types differing are a disagreement
	a := typecheckForm(nil, map[string]string{"m/a": "int", "m/b": "bool"})
	b := typecheckForm(nil, map[string]string{"m/a": "int"})
	if cl, ok := classOf(canon.Typecheck, a, b, canon.Diff(a, b), &Output{}, &Output{}); ok {
		t.Errorf("a typecheck without errors: class %s, want none", cl)
	}
}

// What the nightly of 2026-11-25 met (seeds 20782002, 20782003,
// 20782014 and 20782015, every mutation), on fixed programs, through
// quint. Where quint's output changes under its own shift, a class
// accepted (and a disagreement of the form alone): a cycle's QNT099
// errors in another order, and other nodes typed after a type error.
// Where it does not, matched: a duplicated module (QNT102, both kept, in
// file order) whose declarations a layer reorders, and twin declarations
// swapped, which the form had keyed by quint's order. A real difference of the same
// shape still fails: a cycle error dropped, a node typed otherwise, a
// duplicate module's body changed.
func TestKnownIDDependence(t *testing.T) {
	requireQuint(t)
	var vals strings.Builder
	for i := 1; i <= 8; i++ {
		// eight declarations: the call graph's map is over 8 entries,
		// immutable's hashed trie, whose order is the ids'
		fmt.Fprintf(&vals, "  val v%d = %d\n", i, i)
	}
	progs := map[string]string{
		"cycle":     "module main {\n" + vals.String() + "  pure def cf(n: int): int = cg(n)\n  pure def cg(n: int): int = cf(n)\n}\n",
		"duplicate": "module main {\n  val a = 1\n  val b = 2\n  val c = 3\n  val d = 4\n  val e = 5\n}\n\nmodule main {\n  val again = 1\n}\n",
		"twins":     "module main {\n  var x: int\n  var x: int\n}\n",
		"typeerror": "module main {\n  val a = 1\n  val b = 2\n  val c = 3\n  val bad = 1 + true\n  val e = 5\n}\n",
	}
	for name, text := range progs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := Program{ID: name, File: filepath.Join(t.TempDir(), "main.qnt"), Text: text}
			oa, ob, err := RunBoth(context.Background(), p, Quint{}, Shifted{}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			exact, err := CompareOutputs(oa, ob)
			if err != nil {
				t.Fatal(err)
			}
			d, known, err := CompareOutputsKnown(oa, ob)
			if err != nil {
				t.Fatal(err)
			}
			if d != nil {
				t.Fatalf("quint and quint shifted disagree: %v", d)
			}
			switch name {
			case "cycle":
				if exact == nil || exact.Tier != TierErrors || !exact.First {
					t.Fatalf("the exact comparison: %v, want the first error to differ (does quint still order a cycle's errors by its ids?)", exact)
				}
				if len(known) == 0 || known[0].Class != ClassCycleOrder {
					t.Fatalf("known %v, want %s", known, ClassCycleOrder)
				}
				// a cycle error dropped from quint shifted's parse
				ob.Parse = dropError(t, ob.Parse)
				if d, _, _ := CompareOutputsKnown(oa, ob); d == nil || d.Tier != TierErrors || d.Kind != canon.Parse {
					t.Errorf("a cycle error dropped: %v, want a parse output's errors to differ", d)
				}
			case "typeerror":
				if exact == nil || exact.Tier != TierTypecheck {
					t.Fatalf("the exact comparison: %v, want the typecheck's types to differ (does quint still stop typing at an error?)", exact)
				}
				if len(known) != 1 || known[0].Class != ClassTypesAfterError {
					t.Fatalf("known %v, want %s", known, ClassTypesAfterError)
				}
				// a node both type, typed otherwise by quint shifted
				ob.Typecheck = retype(t, oa.Typecheck, ob.Typecheck)
				if d, _, _ := CompareOutputsKnown(oa, ob); d == nil || d.Tier != TierTypecheck || !strings.HasPrefix(d.Path(), "types.") {
					t.Errorf("a node typed otherwise: %v, want a typecheck disagreement in its types", d)
				}
			case "twins":
				if exact != nil || len(known) > 0 {
					t.Fatalf("the exact comparison %v, known %v: want agreement, twins told apart by their places", exact, known)
				}
				if !declOrderDiffers(t, oa.Parse, ob.Parse) {
					t.Fatal("quint shifted orders the twins as quint does: the program no longer shows the reordering")
				}
			case "duplicate":
				if exact != nil || len(known) > 0 {
					t.Fatalf("the exact comparison %v, known %v: want agreement, the form ordering a layer itself", exact, known)
				}
				if !declOrderDiffers(t, oa.Parse, ob.Parse) {
					t.Fatal("quint shifted orders the first module's declarations as quint does: the program no longer shows the reordering")
				}
				q := p
				q.Text = strings.Replace(text, "val again = 1", "val again = 2", 1)
				ob, err := Shifted{}.Run(context.Background(), q, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if d, known, _ := CompareOutputsKnown(oa, ob); d == nil || d.Tier != TierParse || len(known) > 0 {
					t.Errorf("a duplicate module's body changed: %v (known %v), want a parse disagreement", d, known)
				}
			}
		})
	}
}

// retype is b's typecheck document with the type of a node both a and b
// type (a declaration's expression, by the declaration's name: quint
// types a literal even after an error) made a str.
func retype(t *testing.T, a, b []byte) []byte {
	t.Helper()
	decode := func(doc []byte) map[string]any {
		v, err := decodeJSON(doc)
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)
	}
	ta, tb := decode(a), decode(b)
	// a declaration's expression's id, by its name, in each
	ids := func(top map[string]any) map[string]string {
		out := map[string]string{}
		for _, m := range top["modules"].([]any) {
			for _, d := range m.(map[string]any)["declarations"].([]any) {
				dm := d.(map[string]any)
				if n, ok := dm["name"].(string); ok {
					if ex, ok := dm["expr"].(map[string]any); ok {
						out[n] = fmt.Sprint(ex["id"])
					}
				}
			}
		}
		return out
	}
	ia, ib := ids(ta), ids(tb)
	ya, yb := ta["types"].(map[string]any), tb["types"].(map[string]any)
	for _, n := range slices.Sorted(maps.Keys(ib)) {
		if _, ok := ya[ia[n]]; !ok {
			continue
		}
		e, ok := yb[ib[n]].(map[string]any)
		if !ok {
			continue
		}
		e["type"] = map[string]any{"kind": "str", "id": json.Number("0")}
		out, err := json.Marshal(tb)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Fatal("no declaration typed by both")
	return nil
}

// dropError is a document with its last error taken out.
func dropError(t *testing.T, doc []byte) []byte {
	t.Helper()
	v, err := decodeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	top := v.(map[string]any)
	es, _ := top["errors"].([]any)
	if len(es) < 2 {
		t.Fatalf("%d errors, want a cycle's two", len(es))
	}
	top["errors"] = es[:len(es)-1]
	b, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// declOrderDiffers says whether two parse documents' first modules list
// their declarations in another order: each by its name and the rank of
// its id among the module's (the shift keeps the ranks, and twins are
// told apart by theirs).
func declOrderDiffers(t *testing.T, a, b []byte) bool {
	t.Helper()
	names := func(doc []byte) []string {
		v, err := decodeJSON(doc)
		if err != nil {
			t.Fatal(err)
		}
		mods, _ := v.(map[string]any)["modules"].([]any)
		if len(mods) == 0 {
			t.Fatal("no modules")
		}
		ds, _ := mods[0].(map[string]any)["declarations"].([]any)
		var ids []int64
		for _, d := range ds {
			n, _ := d.(map[string]any)["id"].(json.Number).Int64()
			ids = append(ids, n)
		}
		sorted := slices.Sorted(slices.Values(ids))
		var out []string
		for i, d := range ds {
			n, _ := d.(map[string]any)["name"].(string)
			out = append(out, fmt.Sprintf("%s#%d", n, slices.Index(sorted, ids[i])))
		}
		return out
	}
	return !slices.Equal(names(a), names(b))
}
