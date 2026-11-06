package rec

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

type args struct {
	Counter string   `json:"counter"`
	N       int64    `json:"n"`
	U       uint64   `json:"u"`
	P       *int     `json:"p"`
	Tags    []string `json:"tags"`
}

// StepFields decodes into the type: integers exact (through float64,
// 2^53+1 was 2^53 and MaxInt64 MinInt64), a field named in another case
// recorded by its JSON name (from a generic map, "Counter" was lost), an
// unsigned value past int64 an error naming the field and the fix.
func TestStepFields(t *testing.T) {
	typ := reflect.TypeFor[args]()
	kv, err := StepFields(typ, []byte(`{"Counter":"a","n":9007199254740993,"u":9223372036854775807,"p":3,"tags":["x"]}`), "")
	want := []any{"counter", "a", "n", int64(1<<53 + 1), "u", int64(math.MaxInt64), "p", int64(3)}
	if err != nil || !reflect.DeepEqual(kv, want) {
		t.Errorf("StepFields = %v, %v; want %v", kv, err, want)
	}
	kv, err = StepFields(typ, []byte(`{"counter":"a","n":-9223372036854775808,"u":0}`), "obs.")
	if want := []any{"obs.counter", "a", "obs.n", int64(math.MinInt64), "obs.u", int64(0)}; err != nil || !reflect.DeepEqual(kv, want) {
		t.Errorf("StepFields = %v, %v; want %v (a nil pointer is no field)", kv, err, want)
	}
	if _, err := StepFields(typ, []byte(`{"u":18446744073709551615}`), ""); err == nil || !strings.Contains(err.Error(), "field u") {
		t.Errorf("u past int64: %v, want an error naming it", err)
	}
	if kv, err := StepFields(reflect.TypeFor[uint64](), []byte(`7`), ""); err != nil || !reflect.DeepEqual(kv, []any{"result", int64(7)}) {
		t.Errorf("a scalar: %v, %v", kv, err)
	}
}

func TestQueryResultLists(t *testing.T) {
	type word string
	type words []word
	type result struct {
		Books   words    `json:"books"`
		Ns      []uint64 `json:"ns"`
		Flags   []bool   `json:"flags"`
		Omitted []string `json:"omitted" grist:"-"`
		Hidden  []string `json:"-"`
	}
	typ := reflect.TypeFor[result]()
	kv, err := QueryResultFields(typ, []byte(`{"books":["x","y","x"],"ns":[9007199254740993,9223372036854775807],"flags":[true,false],"omitted":["ignored"]}`), "obs.")
	want := []any{"obs.books", []any{"x", "y", "x"}, "obs.ns", []any{int64(1<<53 + 1), int64(math.MaxInt64)}, "obs.flags", []any{true, false}}
	if err != nil || !reflect.DeepEqual(kv, want) {
		t.Fatalf("lists = %#v, %v; want %#v", kv, err, want)
	}
	kv, err = QueryResultFields(typ, []byte(`{"books":null}`), "")
	want = []any{"books", []any{}, "ns", []any{}, "flags", []any{}}
	if err != nil || !reflect.DeepEqual(kv, want) {
		t.Fatalf("empty = %#v, %v; want %#v", kv, err, want)
	}
	if _, err := QueryResultFields(typ, []byte(`{"ns":[9223372036854775808]}`), ""); err == nil || !strings.Contains(err.Error(), "ns[0]") {
		t.Fatalf("overflow: %v", err)
	}
	// Arguments and events retain their scalar-only projection.
	if kv, err := StepFields(typ, []byte(`{"books":["x"]}`), ""); err != nil || len(kv) != 0 {
		t.Fatalf("scalar contract expanded: %#v %v", kv, err)
	}
}

func TestQueryResultRefusesUnsupported(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct{ Value *int }](),
		reflect.TypeFor[struct{ Value [][]string }](),
		reflect.TypeFor[struct{ Value map[string]int }](),
		reflect.TypeFor[struct{ Value []byte }](),
		reflect.TypeFor[[]string](),
	} {
		if _, err := QueryResultFields(typ, []byte(`null`), ""); err == nil {
			t.Errorf("accepted %v", typ)
		}
	}
	type opted struct {
		Value map[string]int `grist:"-"`
	}
	if kv, err := QueryResultFields(reflect.TypeFor[opted](), []byte(`{}`), ""); err != nil || len(kv) != 0 {
		t.Fatalf("opt-out: %v %v", kv, err)
	}
}
