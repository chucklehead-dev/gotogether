package gen

import (
	"reflect"
	"testing"
)

func TestDrawsIdentityAndFallbackCollisions(t *testing.T) {
	d := newDraws()
	fs := []field{{json: "x", param: "x", typ: "int"}, {json: "x", param: "x", typ: "str"}, {json: "x_str", param: "x_str", typ: "bool"}}
	got := d.add(fs)
	names := []string{got[0].param, got[1].param, got[2].param}
	if want := []string{"x", "x_str", "x_str_bool"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if again := d.add(fs); !reflect.DeepEqual(got, again) {
		t.Fatalf("repeated fields changed: %v -> %v", got, again)
	}
	if len(d.order) != 3 {
		t.Fatalf("draws = %v", d.order)
	}
	for i, f := range got {
		if f.json != fs[i].json || f.typ != fs[i].typ {
			t.Fatalf("field metadata changed: %v", f)
		}
	}
}

func TestDrawsAvoidReferencesAndTemporaries(t *testing.T) {
	g := &gen{names: map[string]string{"order": "command", "arg_order": "query"}, defined: map[string]bool{"helper": true}}
	d := g.newDraws()
	fs := []field{{param: "order", typ: "str"}, {param: "helper", typ: "int"}, {param: "req_", typ: "str"}, {param: "verdict_", typ: "bool"}, {param: "STRS", typ: "str"}, {param: "log_", typ: "str"}, {param: "item_", typ: "str"}}
	got := d.add(fs)
	want := []string{"arg_order_2", "arg_helper", "arg_req_", "arg_verdict_", "arg_STRS", "log_", "item_"}
	for i, f := range got {
		if f.param != want[i] {
			t.Errorf("field %d = %q, want %q", i, f.param, want[i])
		}
	}
	d.pick("caller", "STRS")
	if d.dom["caller"] != "STRS" {
		t.Fatal("framework pick changed")
	}
}

func TestDrawsDoNotMergeArgumentWithFrameworkPick(t *testing.T) {
	g := &gen{}
	for _, controlsFirst := range []bool{false, true} {
		d := g.newDraws()
		if controlsFirst {
			d.pick("result_", "Set(\"ok\")")
		}
		got := d.add([]field{{param: "result_", typ: "str"}})
		if !controlsFirst {
			d.pick("result_", "Set(\"ok\")")
		}
		if got[0].param != "arg_result_" || d.dom["result_"] != "Set(\"ok\")" || d.dom["arg_result_"] != "STRS" {
			t.Fatalf("controlsFirst=%v: %v %v", controlsFirst, got, d.dom)
		}
	}
}

func TestControlDrawsAvoidActionsAndPreserveIdentity(t *testing.T) {
	g := &gen{names: map[string]string{"caller": "command", "id": "command", "key_": "processor", "arg_caller": "query"}}
	d := g.newDraws()
	for _, tc := range []struct{ name, domain, want string }{{"caller", "STRS", "arg_caller_2"}, {"id", "nextID(answered)", "arg_id"}, {"key_", "KEYS", "arg_key_"}, {"ok_", "Bool", "ok_"}} {
		if got := d.pick(tc.name, tc.domain); got != tc.want {
			t.Errorf("pick %q = %q, want %q", tc.name, got, tc.want)
		}
		if got := d.pick(tc.name, tc.domain); got != tc.want {
			t.Errorf("repeat pick %q = %q", tc.name, got)
		}
	}
	if len(d.order) != 4 {
		t.Fatalf("duplicate draws: %v", d.order)
	}
}

func TestDrawsReserveImportedLibraryDeclarations(t *testing.T) {
	saved := Library
	Library = []byte("module grist {\n  pure def triplesOf(reads: Set[int]) = reads\n  action nextOf = true\n  pure val HELPER = 1\n}\n")
	t.Cleanup(func() { Library = saved })
	d := (&gen{}).newDraws()
	fs := d.add([]field{{param: "triplesOf", typ: "str"}, {param: "nextOf", typ: "int"}, {param: "HELPER", typ: "int"}})
	for i, want := range []string{"arg_triplesOf", "arg_nextOf", "arg_HELPER"} {
		if fs[i].param != want {
			t.Errorf("field %d = %q, want %q", i, fs[i].param, want)
		}
	}
	if got := d.pick("nextOf", "INTS"); got == "nextOf" {
		t.Fatal("control shadows imported helper")
	}
}
