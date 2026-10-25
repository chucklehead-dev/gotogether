package binding

import (
	"os/exec"
	"strings"
	"testing"
)

// An action other actions call and step never names is a helper: the
// model knows it, and a lint's list of actions leaves it out (scaffold
// and lint once offered sameY as a step to record).
func TestModelHelpers(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
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
