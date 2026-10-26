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
