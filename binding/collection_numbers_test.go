package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// These types take the flat collection defaults, rather than toQuint's typed
// decoder. Exercise the actual Planner so a precision fix in toQuint alone
// cannot conceal rounding in the default toList/toSet/toMap conversion.
func collectionNumberPlan(t *testing.T, typ, shape, input string) (*Plan, error) {
	t.Helper()
	qt, err := typeOf(typ, nil)
	if err != nil {
		t.Fatal(err)
	}
	param := Param{Name: "values", Type: typ, Shape: shape, typ: qt}
	helper := map[string]string{"list": "toList", "set": "toSet", "map": "toMap"}[shape]
	if got, want := param.DefaultTemplate(), "{{ "+helper+" .values }}"; got != want {
		t.Fatalf("fixture bypasses flat collection conversion: %s, want %s", got, want)
	}
	m := &Model{Module: "collections", Actions: map[string][]Param{"apply": {param}}}
	b := &Binding{Module: "collections"}
	trace := &qtrace.Trace{Actor: "native", Steps: []qtrace.Step{{Action: "apply", Recorder: "capture", Seq: 1, Args: map[string]any{"values": input}}}}
	return b.Planner(m, trace).Plan(trace)
}

func TestCollectionNumbersPlannerPreservesExactIntegers(t *testing.T) {
	// Constants are decimal strings on purpose. A float conversion cannot be
	// reused as the expected-value oracle for the bug this test guards.
	for _, tc := range []struct{ name, typ, shape, input, want string }{
		{"list", "List[int]", "list", `[9007199254740993,18446744073709551615,-9223372036854775809]`, `[9007199254740993, 18446744073709551615, -9223372036854775809]`},
		{"set", "Set[int]", "set", `[9007199254740993,18446744073709551615,-9223372036854775809]`, `Set(9007199254740993, 18446744073709551615, -9223372036854775809)`},
		{"map", "str -> int", "map", `{"a":9007199254740993,"b":18446744073709551615,"c":-9223372036854775809}`, `Map("a" -> 9007199254740993, "b" -> 18446744073709551615, "c" -> -9223372036854775809)`},
		{"tuple-list", "List[(int, int)]", "list", `[[9007199254740993,18446744073709551615],[-9223372036854775809,0]]`, `[(9007199254740993, 18446744073709551615), (-9223372036854775809, 0)]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := collectionNumberPlan(t, tc.typ, tc.shape, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Calls) != 1 {
				t.Fatalf("calls: %+v", plan.Calls)
			}
			call := plan.Calls[0]
			if got := call.Args["values"]; got != tc.want {
				t.Errorf("recorded integer changed in argument: got %s; want %s", got, tc.want)
			}
			if got, want := call.Quint, "apply("+tc.want+")"; got != want {
				t.Errorf("emitted replay changes the recorded operation: got %s; want %s", got, want)
			}
		})
	}
}

func TestCollectionNumbersNestedTupleHelperPreservesExactIntegers(t *testing.T) {
	for _, kind := range []string{"List", "Set"} {
		got, err := jsonQuint(qstr(`[[9007199254740993,[18446744073709551615,-9223372036854775809]]]`), kind)
		if err != nil {
			t.Fatal(err)
		}
		want := `[(9007199254740993, (18446744073709551615, -9223372036854775809))]`
		if kind == "Set" {
			want = `Set((9007199254740993, (18446744073709551615, -9223372036854775809)))`
		}
		if got != want {
			t.Errorf("%s nested tuple changed integers: got %s; want %s", kind, got, want)
		}
	}
}

func TestCollectionNumbersRefuseNonintegersWithPath(t *testing.T) {
	for _, tc := range []struct{ kind, input, path string }{
		{"List", `[1,1.5]`, `[1]`},
		{"List", `[1.0]`, `[0]`},
		{"Set", `[[2,-3.75]]`, `[0][1]`},
		{"Map", `{"n":2.5}`, `["n"]`},
		// Match the typed decoder's integer syntax policy: exponent notation is
		// not an integer literal even when its mathematical value is integral.
		{"List", `[1,2e3]`, `[1]`},
		{"Map", `{"n":2e3}`, `["n"]`},
	} {
		t.Run(tc.kind+tc.path, func(t *testing.T) {
			got, err := jsonQuint(qstr(tc.input), tc.kind)
			if err == nil {
				t.Fatalf("noninteger silently converted to %s", got)
			}
			if !strings.Contains(err.Error(), "at "+tc.path+": ") || !strings.Contains(err.Error(), "integer") {
				t.Fatalf("noninteger error lost path or integer guidance: %v", err)
			}
		})
	}
	plan, err := collectionNumberPlan(t, "List[int]", "list", `[1,1.5]`)
	if err == nil {
		t.Fatalf("native planner silently emitted noninteger operation: %+v", plan.Calls)
	}
	for _, part := range []string{"parameter values", "[1]", "integer"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("planner error lacks %q: %v", part, err)
		}
	}
}

func TestCollectionNumbersRefuseTrailingJSON(t *testing.T) {
	for _, tc := range []struct{ kind, input string }{
		{"List", `[1] [2]`},
		{"Set", `[1] true`},
		{"Map", `{"n":1} {"m":2}`},
		{"Map", `{"n":1}garbage`},
		{"List", `[1]}`},
		{"Map", `{"n":1}]`},
	} {
		if got, err := jsonQuint(qstr(tc.input), tc.kind); err == nil {
			t.Errorf("%s accepted trailing JSON as %s", tc.kind, got)
		}
	}
	if got, err := jsonQuint(qstr("[1] \n\t"), "List"); err != nil || got != "[1]" {
		t.Errorf("valid trailing whitespace: got %s, %v", got, err)
	}
}

// Switching the number decoder must not change the existing empty/null inputs.
func TestCollectionNumbersNullRemainsEmpty(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{{"List", "[]"}, {"Set", "Set()"}, {"Map", "Map()"}} {
		if got, err := jsonQuint(qstr("null"), tc.kind); err != nil || got != tc.want {
			t.Errorf("%s null: got %s, %v; want %s", tc.kind, got, err, tc.want)
		}
	}
}
