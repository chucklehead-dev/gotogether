package binding

import (
	"strings"
	"testing"
)

func TestLintChoicesExplainsBoundedValidation(t *testing.T) {
	b := &Binding{Actions: map[string]Action{"pick": {Expect: "x == observed"}, "silent": {}}}
	m := &Model{chooses: map[string][]string{"pick": {"nondet v"}, "silent": {"nondet w"}}}
	got := b.LintChoices(m)
	if len(got) != 1 {
		t.Fatalf("warnings: %v", got)
	}
	for _, want := range []string{"action pick", "nondet v", "bounded search", "original-action pinned replay", "unsupported shapes or exhaustion give an explicit no verdict", "parameter of pick"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("missing %q: %s", want, got[0])
		}
	}
	if strings.Contains(got[0], "draws the choice at random") {
		t.Errorf("finite choices described as sampled: %s", got[0])
	}
}
