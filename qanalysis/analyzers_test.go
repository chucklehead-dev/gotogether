package qanalysis_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/chucklehead-dev/gotogether/qanalysis"
)

// The analyzers report what gotogether lint reports, at the same places
// (qlint's own test has the same fixtures).
func TestSim(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), qanalysis.Sim, "simapp")
}

func TestWeave(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), qanalysis.Weave, "weaveapp")
}

// Without a binding: the action is named and the expressions see only
// what the advice can (parameters, receiver, named results).
func TestAction(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), qanalysis.Action, "actionapp")
}

// With a binding: the model has the action, and the annotation records
// the fields the binding uses. Loading the model runs quint.
func TestActionWithBinding(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	b, err := filepath.Abs(filepath.Join(analysistest.TestData(), "bank.binding.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := qanalysis.Action.Flags.Set("binding", b); err != nil {
		t.Fatal(err)
	}
	defer qanalysis.Action.Flags.Set("binding", "")
	analysistest.Run(t, analysistest.TestData(), qanalysis.Action, "bindingapp")
}

// quintsim's findings carry their fix where a context is in scope: a
// `go` statement without arguments started with qenv.Go, time.Now and
// time.Sleep as qenv's.
func TestSimFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), qanalysis.Sim, "simfix")
}

// context.Background() and TODO() in a function with a context of its
// own (a ctx parameter, a test's t), and the fix that passes it; not in
// a closure, which may outlive it.
func TestCtx(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), qanalysis.Ctx, "ctxapp")
}
