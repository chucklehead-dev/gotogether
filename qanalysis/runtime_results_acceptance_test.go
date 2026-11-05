package qanalysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This uses the actual SDK and multichecker, rather than analysistest stubs.
// JSON mode exits successfully when it reports diagnostics: the category and
// source/fix data, not the exit code alone, establish the finding.
func TestRuntimeResultCLIWithActualSDK(t *testing.T) {
	run := func(dir string, env []string, program string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), program, args...)
		cmd.Dir, cmd.Env = dir, env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %q in %s: %v\nstdout:\n%s\nstderr:\n%s", program, args, dir, err, &stdout, &stderr)
		}
		return stdout.Bytes()
	}
	// The enclosing workspace may select the current checkout; standalone
	// module checks resolve the published SDK pin instead. Both exercise the
	// exact SDK that this tool build uses.
	core := strings.TrimSpace(string(run(".", os.Environ(), "go", "list", "-m", "-f", "{{.Dir}}", "github.com/chucklehead-dev/gotogether")))
	if core == "" {
		t.Fatal("go list did not resolve the actual core SDK")
	}
	tool := filepath.Join(t.TempDir(), "quintvet")
	run(".", os.Environ(), "go", "build", "-o", tool, "./cmd/quintvet")
	consumer := filepath.Join(t.TempDir(), "consumer with space")
	if err := os.Mkdir(consumer, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := fmt.Sprintf("module example.com/runtime-result-acceptance\n\ngo 1.26.10\n\nrequire github.com/chucklehead-dev/gotogether v0.0.0\n\nreplace github.com/chucklehead-dev/gotogether => %s\n", strconv.Quote(filepath.ToSlash(core)))
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	const source = `package consumer

import (
 "time"
 "github.com/chucklehead-dev/gotogether/qenv"
)

func drive(sim *qenv.Sim) error {
 sim.Run(time.Second)
 return nil
}

type custom struct{}
func (custom) Run(time.Duration) error { return nil }
func (custom) Settle(time.Duration) error { return nil }
func unrelated(c custom) {
 c.Run(time.Second)
 c.Settle(time.Second)
}
`
	file := filepath.Join(consumer, "consumer.go")
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	const runtimeTest = `package consumer

import (
 "context"
 "errors"
 "testing"

 "github.com/chucklehead-dev/gotogether/qenv"
)

func TestDrivePropagatesBudgetOutcome(t *testing.T) {
 sim := qenv.New(qenv.Nominal{})
 defer sim.Close()
 sim.MaxSteps = 1
 sim.Spawn("busy", func(ctx context.Context) {
  qenv.Yield(ctx)
  qenv.Yield(ctx)
 })
 if err := drive(sim); !errors.Is(err, qenv.ErrMaxSteps) {
  t.Fatalf("drive lost the runtime budget outcome: %v", err)
 }
}
`
	env := runtimeAcceptanceEnv(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	run(consumer, env, "go", "mod", "tidy")
	flags := []string{"-quintweave=false", "-quintaction=false", "-quintctx=false", "-quintraw=false"}
	jsonFlags := append(append([]string{}, flags...), "-json", "./...")
	before := runtimeAcceptanceDiagnostics(t, run(consumer, env, tool, jsonFlags...))
	if len(before) != 1 {
		t.Fatalf("want exactly one native diagnostic, custom Run/Settle must be excluded: %+v", before)
	}
	diagnostic := before[0]
	line := strings.Count(strings.Split(source, " sim.Run(time.Second)")[0], "\n") + 1
	if diagnostic.Category != "sim-runtime-result" || diagnostic.Posn != fmt.Sprintf("%s:%d:2", file, line) || !strings.Contains(diagnostic.Message, "discarded qenv.Sim.Run runtime error") {
		t.Fatalf("native category or source identity changed: %+v", diagnostic)
	}
	if len(diagnostic.Fixes) != 1 || len(diagnostic.Fixes[0].Edits) != 1 || diagnostic.Fixes[0].Edits[0].Filename != file || !strings.Contains(diagnostic.Fixes[0].Edits[0].New, "return err") {
		t.Fatalf("native diagnostic lost its safe fix: %+v", diagnostic)
	}
	fixFlags := append(append([]string{}, flags...), "-fix", "./...")
	run(consumer, env, tool, fixFlags...)
	corrected, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(corrected, []byte(source)) || !bytes.Contains(corrected, []byte("sim.Run(time.Second)")) || !bytes.Contains(corrected, []byte("return err")) {
		t.Fatalf("fix must preserve the call and propagate its error:\n%s", corrected)
	}
	// Add the behavioral test after the finding/fix gate, avoiding duplicate
	// diagnostics from Go's normal and test-augmented package variants.
	if err := os.WriteFile(filepath.Join(consumer, "consumer_test.go"), []byte(runtimeTest), 0o644); err != nil {
		t.Fatal(err)
	}
	// This executes the corrected driver against a real exhausted scheduler:
	// the original ignored call would return nil and fail the consumer test.
	run(consumer, env, "go", "test", "./...")
	if remaining := runtimeAcceptanceDiagnostics(t, run(consumer, env, tool, jsonFlags...)); len(remaining) != 0 {
		t.Fatalf("corrected actual SDK consumer still has diagnostics: %+v", remaining)
	}
}

type runtimeAcceptanceDiagnostic struct {
	Category string `json:"category"`
	Posn     string `json:"posn"`
	Message  string `json:"message"`
	Fixes    []struct {
		Edits []struct {
			Filename string `json:"filename"`
			New      string `json:"new"`
		} `json:"edits"`
	} `json:"suggested_fixes"`
}

func runtimeAcceptanceDiagnostics(t *testing.T, data []byte) []runtimeAcceptanceDiagnostic {
	t.Helper()
	var tree map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("invalid native multichecker JSON: %v\n%s", err, data)
	}
	var out []runtimeAcceptanceDiagnostic
	for pkg, analyzers := range tree {
		for name, raw := range analyzers {
			var diagnostics []runtimeAcceptanceDiagnostic
			if err := json.Unmarshal(raw, &diagnostics); err != nil {
				t.Fatalf("%s/%s: native analyzer error instead of diagnostics: %s", pkg, name, raw)
			}
			out = append(out, diagnostics...)
		}
	}
	return out
}

func runtimeAcceptanceEnv(base []string, overrides ...string) []string {
	values := map[string]bool{}
	for _, value := range overrides {
		key, _, _ := strings.Cut(value, "=")
		values[key] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		if !values[key] {
			out = append(out, value)
		}
	}
	return append(out, overrides...)
}
