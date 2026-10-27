package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The CLI does not depend on testing nor on the simulation: shrinking,
// replay, observation mutants and evidence are not test-only by nature
// (qshrink, validate.Replay, Checker.MutateSteps, the evidence view), and
// qtest once brought testing and qenv into every gotogether binary.
func TestCLIDependencies(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		for _, banned := range []string{"testing", "github.com/chucklehead-dev/gotogether/qtest", "github.com/chucklehead-dev/gotogether/qenv", "github.com/chucklehead-dev/gotogether/connect"} {
			if p == banned {
				t.Errorf("the CLI depends on %s (go list -deps . | grep it, and its importer with go list -f '{{.ImportPath}}: {{.Imports}}')", p)
			}
		}
	}
}
