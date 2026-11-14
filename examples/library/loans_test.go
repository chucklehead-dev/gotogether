package library

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The rules' read-model folds (one pass over the log, the event's own
// member or book as the condition) equal the read model's selection
// folded, which they replaced for quint verify's sake (docs/performance.md,
// the library's proofs): testdata/loans.qnt keeps the earlier definitions.
// Sampled here on a machine that appends every event the models make, and
// in the states both models reach; quint verify of the machine's agree
// bounds every log. The planted mistakes show the check can fail.
func TestLoansAgree(t *testing.T) {
	qtest.RequireQuint(t)
	spec, err := filepath.Abs(filepath.Join("testdata", "loans.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		module, invariant string
		holds             bool
	}{
		{"loans", "agree", true},
		{"loansStore", "agree", true},
		{"loansCalls", "agree", true},
		{"loans", "plantedLoanCountAgrees", false},
		{"loans", "plantedCopiesAgree", false},
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
