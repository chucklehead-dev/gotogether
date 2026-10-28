package harness

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The mutants are the planted bugs, each a directory of testdata/mutants/
// whose files replace payd's of the same name (a go build overlay, found
// there by qtest.Mutants): payd's own files stay as they are, and each
// mutant is a whole file to diff against them. Each, built with its
// overlay, is caught as TestMutant says: the test binary of each runs
// TestMutant (and the precheck mutant's real run) with this one's flags.
func TestMutants(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.Mutants{Package: "payd", Show: []string{"charges for", "conform"}}.Run(t)
}

// payd is third-party code: it and its SDK import the standard library
// alone, nothing of the toolkit.
func TestPaydImportsNoToolkit(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./payd/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasPrefix(p, "github.com/chucklehead-dev/gotogether/examples/payd/payd") {
			t.Errorf("payd depends on %s: it imports the standard library alone, as code that knows nothing of its tests", p)
		}
	}
	if !strings.Contains(string(out), "github.com/chucklehead-dev/gotogether/examples/payd/payd/client") {
		t.Errorf("go list -deps ./payd/... lists no SDK:\n%s", out)
	}
}
