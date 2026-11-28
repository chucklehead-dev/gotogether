package veilfrag

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func parse(t *testing.T, file string) *Parsed {
	t.Helper()
	skip.Quint(t)
	p, err := Parse(t.Context(), "quint", "testdata/"+file)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func codes(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%s@%d:%s", f.Code, f.Line, f.Symbol))
	}
	return out
}

// Each rule finds its idiom and not the form beside it that Veil proves
// (blocking.qnt pairs them): a Map literal of variable keys and its
// put, a key removal and a total map, a match in mapBy and an if, folds
// over a parameter and a constant (not over a state variable), sizes
// against small literals (not against 10).
func TestFindingsPositiveAndNegative(t *testing.T) {
	p := parse(t, "blocking.qnt")
	got := codes(p.Findings(""))
	want := []string{
		CodeMapLiteral + "@14:pay",
		CodeKeysRemove + "@25:drop",
		CodeMatchMapBy + "@26:drop",
		CodeFold + "@36:total",
		CodeFold + "@37:joint",
		CodeSize + "@39:empty",
		CodeSize + "@40:atMostOne",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings:\n got %v\nwant %v", got, want)
	}
	for _, f := range p.Findings("") {
		if f.Fix == "" || f.Message == "" || f.Column == 0 {
			t.Errorf("%s: no message, fix or position: %+v", f.Code, f)
		}
	}
	fixes := map[string]string{}
	for _, f := range p.Findings("") {
		fixes[f.Symbol+" "+f.Code] = f.Fix
	}
	if fix := fixes["empty "+CodeSize]; !strings.Contains(fix, "applied == Set()") {
		t.Errorf("size == 0's fix: %q", fix)
	}
	if fix := fixes["atMostOne "+CodeSize]; !strings.Contains(fix, "applied.forall(a => applied.forall(b => a == b))") {
		t.Errorf("1 >= size's fix (flipped to size <= 1): %q", fix)
	}
}

// An assume of an imported module is flagged in the module that imports
// it, and not where main restates it.
func TestAssumeInImportedModule(t *testing.T) {
	p := parse(t, "assumes.qnt")
	if got := codes(p.Findings("assumes")); !slices.Equal(got, []string{CodeAssume + "@5:nNat"}) {
		t.Errorf("main assumes: %v", got)
	}
	if fs := p.Findings("assumes"); len(fs) == 1 && !strings.Contains(fs[0].Fix, "assume nNat_ = N >= 0") {
		t.Errorf("fix: %q", fs[0].Fix)
	}
	if got := codes(p.Findings("restated")); len(got) != 0 {
		t.Errorf("main restated: %v", got)
	}
	if got := codes(p.Findings("base")); len(got) != 0 {
		t.Errorf("main base: %v", got)
	}
}

// A holds invariant conditioned on a flag no action of step sets is
// vacuous; one on a flag step sets (done) is not, nor one on both.
func TestVacuousSettle(t *testing.T) {
	p := parse(t, "settle.qnt")
	got := codes(p.Vacuous("settle", []string{"settledCorrectly", "doneCorrectly", "bothCorrectly"}))
	want := []string{CodeVacuous + "@14:settledCorrectly", CodeVacuous + "@16:bothCorrectly"}
	if !slices.Equal(got, want) {
		t.Errorf("vacuous: got %v, want %v", got, want)
	}
	a, neg := p.Antecedent("settle", "settledCorrectly", []string{"neverSettled", "doneCorrectly"})
	if a != "settled" || !slices.Equal(neg, []string{"neverSettled"}) {
		t.Errorf("antecedent %q, negating lemmas %v", a, neg)
	}
	if a, neg := p.Antecedent("settle", "neverSettled", []string{"neverSettled"}); a != "" || neg != nil {
		t.Errorf("not an implication: %q %v", a, neg)
	}
}
