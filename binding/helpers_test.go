package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// An action other actions call and step never names is a helper: the
// model knows it, and a lint's list of actions leaves it out (scaffold
// and lint once offered sameY as a step to record).
func TestModelHelpers(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/helpers.qnt", "helpers")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Helpers) != 1 || !m.Helpers["sameY"] {
		t.Errorf("helpers %v, want sameY alone", m.Helpers)
	}
	if msg := m.notAnAction("sameZ"); strings.Contains(msg, "sameY,") || strings.HasSuffix(msg, "sameY") || !strings.Contains(msg, "inc, write") {
		t.Errorf("the actions listed: %s", msg)
	}
}

// CalledBy finds an action the given ones call, directly or through
// others: appendTo (every command's part) by add and bump, and by the
// dispatcher through them; never an action by itself, nor through step.
func TestModelCalledBy(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/parts.qnt", "parts")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		action  string
		callers []string
		want    bool
	}{
		{"appendTo", []string{"add"}, true},
		{"appendTo", []string{"dispatch"}, true},
		{"appendTo", []string{"idle"}, false},
		{"add", []string{"add"}, false},
		{"add", []string{"step"}, false},
		{"same", []string{"idle"}, true},
	} {
		callers := map[string]bool{}
		for _, a := range c.callers {
			callers[a] = true
		}
		if got := m.CalledBy(c.action, callers); got != c.want {
			t.Errorf("CalledBy(%s, %v) = %v, want %v", c.action, c.callers, got, c.want)
		}
	}
}
