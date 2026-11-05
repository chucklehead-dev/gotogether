package ciconfig

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The roster lives in the script used by developers and CI. These are required
// repository contracts, rather than a second list of tests to exclude from the
// ordinary suites. Newly added CLI contract tests must not silently miss the gate.
func TestRepositoryPreflightCoversContracts(t *testing.T) {
	script := readPreflightFile(t, "../../ci/preflight.sh")
	names := cliContractTests(t)
	if problems := preflightCoverage(script, names); len(problems) != 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	for _, tc := range []struct {
		name, script, want string
	}{
		{"native broadening", regexp.MustCompile(`(?m)^cli_tests='[^']+'$`).ReplaceAllString(script, `cli_tests='^Test.*$'`), "unaudited CLI test"},
		{"lost scope guard", strings.Replace(script, "  ./internal/pkgstate\n", "", 1), "./internal/pkgstate"},
		{"lost overview guard", strings.Replace(script, "Usage.*|", "", 1), "TestUsageOneLinePerCommand"},
		{"filtered guards", strings.Replace(script, `"${guards[@]}"`, `-run NoSuchTests "${guards[@]}"`, 1), "guards array"},
		{"unused roster", strings.Replace(script, `"${guards[@]}"`, "./internal/apisurface", 1), "guards array"},
		{"unused selector", strings.Replace(script, `-run "$cli_tests"`, `-run '^TestCommandHelp$'`, 1), "cli_tests selector"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := strings.Join(preflightCoverage(tc.script, names), "\n")
			if !strings.Contains(problems, tc.want) {
				t.Fatalf("weakened preflight escaped guard: %q, want %q", problems, tc.want)
			}
		})
	}
	// A test whose name is not covered forces an explicit selector decision;
	// discovering it from AST keeps additions to either file visible here.
	if problems := strings.Join(preflightCoverage(script, append(slices.Clone(names), cliContractTest{name: "TestNewContractBoundary", file: "help_test.go"})), "\n"); !strings.Contains(problems, "TestNewContractBoundary") {
		t.Fatal("a new unselected CLI contract test escaped the gate")
	}
	// A matching name in another file is not automatically tool-independent.
	// It must be audited rather than silently entering the early gate.
	unexpected := append(slices.Clone(names), cliContractTest{name: "TestUsageRunsNativeModel", file: "native_test.go"})
	if problems := strings.Join(preflightCoverage(script, unexpected), "\n"); !strings.Contains(problems, "unaudited CLI test TestUsageRunsNativeModel") {
		t.Fatal("a matching native test outside the audited files escaped the gate")
	}
}

type cliContractTest struct{ name, file string }

func auditedCLIFile(file string) bool {
	return file == "help_test.go" || file == "tutorial_commands_test.go"
}

func preflightCoverage(script string, cliNames []cliContractTest) []string {
	var problems []string
	guards := regexp.MustCompile(`(?ms)^guards=\(\n(.*?)^\)`).FindStringSubmatch(script)
	if guards == nil {
		return []string{"preflight must declare its guards array"}
	}
	packages := strings.Fields(guards[1])
	for _, required := range []string{"./internal/pkgstate", "./internal/apisurface", "./internal/ciconfig", "./internal/docsnippets", "./internal/exampleapps", "./internal/formats/...", "./schemas", "./skills", "./qlint"} {
		if !slices.Contains(packages, required) {
			problems = append(problems, "preflight omits required contract package "+required)
		}
	}
	guardCommand := regexp.MustCompile(`(?m)^go test [^\n]*"\$\{guards\[@\]\}"\s*$`).FindString(script)
	if slices.Contains(packages, "./...") || guardCommand == "" || filteredGoTests(guardCommand) || strings.Contains(guardCommand, " -short") {
		problems = append(problems, "preflight must execute the guards array without broadening to native suites")
	}
	selector := regexp.MustCompile(`(?m)^cli_tests='([^']+)'$`).FindStringSubmatch(script)
	if selector == nil {
		return append(problems, "preflight must declare its cli_tests selector")
	}
	selected, err := regexp.Compile(selector[1])
	if err != nil {
		return append(problems, "invalid cli_tests selector: "+err.Error())
	}
	for _, test := range cliNames {
		matched := selected.MatchString(test.name)
		if auditedCLIFile(test.file) && !matched {
			problems = append(problems, "preflight selector omits CLI contract "+test.name)
		}
		if !auditedCLIFile(test.file) && matched {
			problems = append(problems, "preflight selector selects unaudited CLI test "+test.name+" in "+test.file)
		}
	}
	if !regexp.MustCompile(`(?m)^go test [^\n]*\./cmd/gotogether [^\n]*-run "\$cli_tests"\s*$`).MatchString(script) {
		problems = append(problems, "preflight must execute the cli_tests selector")
	}
	return problems
}

func cliContractTests(t *testing.T) []cliContractTest {
	t.Helper()
	paths, err := filepath.Glob("../../cmd/gotogether/*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var tests []cliContractTest
	audited := 0
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				tests = append(tests, cliContractTest{name: fn.Name.Name, file: filepath.Base(path)})
				if auditedCLIFile(filepath.Base(path)) {
					audited++
				}
			}
		}
	}
	if audited == 0 {
		t.Fatal("no audited CLI contract tests discovered")
	}
	return tests
}

type preflightWorkflow struct {
	Jobs map[string]preflightJob `yaml:"jobs"`
}

type preflightJob struct {
	Name  string `yaml:"name"`
	Needs any    `yaml:"needs"`
	If    string `yaml:"if"`
	Steps []struct {
		Name string `yaml:"name"`
		Uses string `yaml:"uses"`
		Run  string `yaml:"run"`
		If   string `yaml:"if"`
	} `yaml:"steps"`
}

// Source contracts must finish before expensive jobs start, while the normal
// root and shuffled suites retain their complete package coverage.
func TestRepositoryPreflightGatesCI(t *testing.T) {
	var workflow preflightWorkflow
	if err := yaml.Unmarshal([]byte(readPreflightFile(t, "../../.github/workflows/ci.yml")), &workflow); err != nil {
		t.Fatal(err)
	}
	if problems := preflightGate(workflow); len(problems) != 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	for _, tc := range []struct {
		name, condition string
		missingNeed     bool
	}{
		{"ungated native job", "", true},
		{"always bypass", "always()", false},
		{"failure bypass", "failure()", false},
		{"negated cancelled bypass", "!cancelled()", false},
		{"success expression bypass", "success() || true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := preflightWorkflow{Jobs: maps.Clone(workflow.Jobs)}
			job := mutated.Jobs["test"]
			job.If = tc.condition
			if tc.missingNeed {
				job.Needs = nil
			}
			mutated.Jobs["test"] = job
			problems := strings.Join(preflightGate(mutated), "\n")
			if problems == "" {
				t.Fatal("native job bypassed the source-contract gate")
			}
		})
	}
	t.Run("filtered full suite", func(t *testing.T) {
		mutated := preflightWorkflow{Jobs: maps.Clone(workflow.Jobs)}
		job := mutated.Jobs["test"]
		job.Steps = slices.Clone(job.Steps)
		changed := false
		for index := range job.Steps {
			if job.Steps[index].If == "matrix.part == 'core'" && strings.Contains(job.Steps[index].Run, "go test -exec") {
				job.Steps[index].Run = strings.Replace(job.Steps[index].Run, "go test -exec", "go test -run=NoTests -exec", 1)
				changed = true
			}
		}
		if !changed {
			t.Fatal("full core command not found for the negative control")
		}
		mutated.Jobs["test"] = job
		if problems := strings.Join(preflightGate(mutated), "\n"); !strings.Contains(problems, "unfiltered") {
			t.Fatalf("filtered full suite escaped guard: %s", problems)
		}
	})
}

func preflightGate(workflow preflightWorkflow) []string {
	var problems []string
	gate, exists := workflow.Jobs["preflight"]
	if !exists || gate.Name != "repository contracts" {
		return []string{"CI must expose the preflight repository contracts job"}
	}
	if gate.If != "" || gate.Needs != nil {
		problems = append(problems, "preflight must be an unconditional root job")
	}
	shared := 0
	goSetup := false
	for _, step := range gate.Steps {
		if strings.TrimSpace(step.Run) == "ci/preflight.sh" {
			shared++
		}
		goSetup = goSetup || strings.HasPrefix(step.Uses, "actions/setup-go@")
		if strings.Contains(step.Uses, "setup-node") || strings.Contains(step.Run, "install-quint") || strings.Contains(step.Run, "toxiproxy") {
			problems = append(problems, "preflight must run without native tool installation")
		}
	}
	if shared != 1 || !goSetup {
		problems = append(problems, "preflight must set up Go and run ci/preflight.sh once")
	}
	for name, job := range workflow.Jobs {
		if name == "preflight" {
			continue
		}
		if !hasPreflightNeed(job.Needs, "preflight") {
			problems = append(problems, name+" must need preflight")
		}
		if regexp.MustCompile(`\b(?:always|failure|cancelled|success)\s*\(`).MatchString(job.If) {
			problems = append(problems, name+" must not bypass the failed preflight gate")
		}
	}
	if !hasPreflightNeed(workflow.Jobs["action-compiled"].Needs, "action") {
		problems = append(problems, "action-compiled must retain its action artifact dependency")
	}
	var core, shuffled bool
	for _, step := range workflow.Jobs["test"].Steps {
		for _, line := range strings.Split(step.Run, "\n") {
			line = strings.TrimSpace(line)
			if !strings.Contains(line, "go test ") || !strings.HasSuffix(line, " ./...") || filteredGoTests(line) {
				continue
			}
			core = core || (step.If == "matrix.part == 'core'" && strings.Contains(line, " -count=1 "))
			shuffled = shuffled || (step.If == "matrix.part == 'shuffle'" && strings.Contains(line, " -shuffle=on ") && strings.Contains(line, " -count=2 "))
		}
	}
	if !core || !shuffled {
		problems = append(problems, "full core and twice-shuffled root suites must remain unfiltered")
	}
	return problems
}

func filteredGoTests(command string) bool {
	return regexp.MustCompile(`(?:^|\s)-(?:run|skip)(?:=|\s|$)`).MatchString(command)
}

func hasPreflightNeed(value any, name string) bool {
	switch needs := value.(type) {
	case string:
		return needs == name
	case []any:
		for _, need := range needs {
			if need == name {
				return true
			}
		}
	}
	return false
}

func readPreflightFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read repository contract %s: %w", path, err))
	}
	return string(data)
}

// Format failures must stop the same gate before any package tests run. The
// positive control proves that the fake tools can reach both test invocations.
func TestRepositoryPreflightFormattingIsNonwriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preflight is a Bash developer/CI command")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"clean", "dirty", "parse-error"} {
		t.Run(mode, func(t *testing.T) {
			bin := t.TempDir()
			formatCalls := filepath.Join(bin, "format-args")
			goCalls := filepath.Join(bin, "go-calls")
			tools := map[string]string{
				"gofmt": `#!/bin/sh
printf '%s\n' "$@" >> "$PREFLIGHT_TEST_FORMAT_CALLS"
case "$PREFLIGHT_TEST_FORMAT_MODE" in
  dirty) printf 'example.go\n' ;;
  parse-error) printf 'invalid Go syntax\n' >&2; exit 2 ;;
esac
`,
				"go": `#!/bin/sh
printf '%s\n' "$*" >> "$PREFLIGHT_TEST_GO_CALLS"
`,
			}
			for name, source := range tools {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), bash, filepath.Join(root, "ci/preflight.sh"))
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PREFLIGHT_TEST_FORMAT_CALLS="+formatCalls, "PREFLIGHT_TEST_GO_CALLS="+goCalls,
				"PREFLIGHT_TEST_FORMAT_MODE="+mode)
			output, runErr := cmd.CombinedOutput()
			args, err := os.ReadFile(formatCalls)
			files := strings.Split(strings.TrimSpace(string(args)), "\n")
			if err != nil || len(files) < 2 || files[0] != "-l" || !slices.Contains(files, "./internal/ciconfig/preflight_test.go") {
				t.Fatalf("format command must inspect source files without rewriting: %q, %v", args, err)
			}
			for _, file := range files[1:] {
				if file == "-l" { // xargs may split a large checkout into batches.
					continue
				}
				if !strings.HasSuffix(file, ".go") || strings.Contains(file, "/.") {
					t.Errorf("format command inspected a directory or hidden artifact: %q", file)
				}
			}
			calls, err := os.ReadFile(goCalls)
			if mode == "clean" {
				if runErr != nil || err != nil || strings.Count(string(calls), "\n") != 2 {
					t.Fatalf("clean positive control: %v; calls=%q (%v); output=%s", runErr, calls, err, output)
				}
			} else if runErr == nil || !os.IsNotExist(err) {
				t.Fatalf("format failure reached Go tests: %v; calls=%q (%v); output=%s", runErr, calls, err, output)
			}
		})
	}
}
