package counters

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The rules' one-pass folds (the event's own counter, name or owner as the
// condition) equal the selections and tag matches they replaced for quint
// verify's sake (docs/performance.md, counters' proofs):
// testdata/rules.qnt keeps the earlier definitions.
// Sampled here on a machine that appends every event the models make, and
// in the states both models reach; quint verify of the machine's agree
// bounds every log. The planted mistakes show the check can fail.
func TestRulesAgree(t *testing.T) {
	qtest.RequireQuint(t)
	spec, err := filepath.Abs(filepath.Join("testdata", "rules.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		module, invariant string
		holds             bool
	}{
		{"rules", "agree", true},
		{"rulesStore", "agree", true},
		{"rulesCalls", "agree", true},
		{"rules", "plantedTotalAgrees", false},
		{"rules", "plantedCreatedByAgrees", false},
	} {
		t.Run(c.module+"."+c.invariant, func(t *testing.T) {
			t.Parallel()
			out, err := quintcmd.Command(t.Context(), "quint", filepath.Dir(spec), "run", "--main="+c.module, "--max-samples=1000", "--max-steps=10",
				"--seed=7", "--invariant="+c.invariant, spec).CombinedOutput()
			violated := strings.Contains(string(out), "[violation]")
			if exit, ok := errors.AsType[*exec.ExitError](err); err != nil && (!ok || exit.ExitCode() != 1 || !violated) {
				t.Fatalf("quint run %s: %v\n%s", c.invariant, err, out)
			}
			switch {
			case c.holds && violated:
				t.Fatalf("%s.%s: the new definitions disagree with the old\n%s", c.module, c.invariant, out)
			case !c.holds && !violated:
				t.Fatalf("%s.%s: the planted mistake was not caught: the check cannot fail\n%s", c.module, c.invariant, out)
			}
		})
	}
}
