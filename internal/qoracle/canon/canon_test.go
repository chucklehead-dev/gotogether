package canon

import (
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
