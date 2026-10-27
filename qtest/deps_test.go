package qtest

import (
	"os/exec"
	"strings"
	"testing"
)

// qtest, which every test of the toolkit's users links, does not bring the
// OTLP reader and the OpenTelemetry SDK: validate.Command (now validate/cli.Check) once did,
// through input and otelio, and every example's go.sum needed the SDK.
// The CLI's check of files is validate/cli's.
func TestQtestDependencies(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "go.opentelemetry.io/otel/sdk") || p == "github.com/chucklehead-dev/gotogether/otelio" || p == "github.com/chucklehead-dev/gotogether/validate/cli" {
			t.Errorf("qtest depends on %s (go list -deps . | grep it, and its importer with go list -f '{{.ImportPath}}: {{.Imports}}')", p)
		}
	}
}
