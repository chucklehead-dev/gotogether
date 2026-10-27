package binding

import (
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// A module that re-exports another whole (`export lib.*`) has the
// exported module's actions, variables and vals as well as its own: a
// binding names it, and the validator's replay calls the actions through
// it (grist's generated skeleton is such a module, its application's
// model the one exporting it).
func TestLoadModelFollowsExports(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/exported.qnt", "exported")
	if err != nil {
		t.Fatal(err)
	}
	if ps, ok := m.Actions["add"]; !ok || len(ps) != 1 || ps[0].Name != "by" || ps[0].Type != "int" {
		t.Errorf("add: %v (found %v): want the exported module's action add(by: int)", ps, ok)
	}
	if _, ok := m.Actions["step"]; !ok {
		t.Error("step: want the exported module's step")
	}
	if !slices.Equal(m.Vars, []string{"n"}) {
		t.Errorf("vars %v: want the exported module's [n]", m.Vars)
	}
	if !m.Vals["nonNegative"] || !m.Vals["small"] {
		t.Errorf("vals %v: want its own nonNegative and the exported small", m.Vals)
	}
}
