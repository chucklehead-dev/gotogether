package walletgrist

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The rules' balances (one fold over the log, the credit line's one
// condition per event) equal the read model's selection folded, which they
// replaced for quint verify's sake (docs/performance.md, wallet-grist's
// noOverdraft proof): testdata/balances.qnt keeps the earlier definitions.
// Sampled here on a machine that appends every event the models make, and
// in the states both models reach; quint verify of the machine's agree
// bounds every log. The planted mistakes show the check can fail.
func TestBalancesAgree(t *testing.T) {
	qtest.RequireQuint(t)
	spec, err := filepath.Abs(filepath.Join("testdata", "balances.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		module, invariant string
		holds             bool
	}{
		{"balances", "agree", true},
		{"balancesStore", "agree", true},
		{"balancesCalls", "agree", true},
		{"balances", "plantedLineAgrees", false},
		{"balances", "plantedBalanceAgrees", false},
	} {
		t.Run(c.module+"."+c.invariant, func(t *testing.T) {
			t.Parallel()
			out, err := quintRun(t, spec, "run", "--main="+c.module, "--max-samples=1000", "--max-steps=10",
				"--seed=7", "--invariant="+c.invariant, spec)
			violated := strings.Contains(out, "[violation]")
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

// quintRun runs quint by argv in spec's directory through quintcmd: the
// test's context cancels it whole (node and its Rust evaluator), and it
// waits for a slot of quint's admission like every other launch.
func quintRun(t *testing.T, spec string, args ...string) (string, error) {
	out, err := quintcmd.Command(t.Context(), "quint", filepath.Dir(spec), args...).CombinedOutput()
	return string(out), err
}
