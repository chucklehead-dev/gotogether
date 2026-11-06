package catalog

import (
	"reflect"
	"testing"
)

func TestResultFields(t *testing.T) {
	type result struct {
		Names  []string `json:"names"`
		Counts []int64  `json:"counts"`
		Flags  []bool   `json:"flags"`
		Ignore []string `grist:"-"`
		Hidden []string `json:"-"`
	}
	fs, err := ResultFields(reflect.TypeFor[result]())
	if err != nil || len(fs) != 3 {
		t.Fatalf("fields = %#v, %v", fs, err)
	}
	for i, want := range []string{"List[str]", "List[int]", "List[bool]"} {
		if fs[i].Type != want {
			t.Errorf("type %s, want %s", fs[i].Type, want)
		}
	}
	if _, err := ScalarFields(reflect.TypeFor[result]()); err == nil {
		t.Fatal("event/argument scalar policy accepted lists")
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[struct{ Value []byte }](), reflect.TypeFor[struct{ Value [][]string }](), reflect.TypeFor[struct{ Value *int }](), reflect.TypeFor[struct{ Value map[string]int }]()} {
		if _, err := ResultFields(typ); err == nil {
			t.Errorf("accepted %v", typ)
		}
	}
}
