package qanalysis

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qlint"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRuntimeResultFixes(t *testing.T) {
	// Exercise the transformation against the shared typed findings independently
	// of the public analyzer's dispatch to each class of suggested fix.
	a := &analysis.Analyzer{Name: "runtimeresultfix", Doc: "test runtime result propagation", Run: func(pass *analysis.Pass) (any, error) {
		for _, f := range pass.Files {
			for _, finding := range qlint.SimFileTyped(pass.Fset, f, pass.TypesInfo) {
				if finding.Code == "sim-runtime-result" {
					pass.Report(analysis.Diagnostic{Pos: finding.At, Message: finding.What, SuggestedFixes: runtimeResultFixes(pass, f, finding.At)})
				}
			}
		}
		return nil, nil
	}}
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), a, "runtimeresult", "runtimeresult/nilshadow")
}

func TestRuntimeResultAnalyzerIntegration(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Sim, "runtimeresult", "runtimeresult/nilshadow")
}

func TestRuntimeResultGoldenBuilds(t *testing.T) {
	data := analysistest.TestData()
	replacements := map[string]string{}
	err := filepath.WalkDir(filepath.Join(data, "src", "runtimeresult"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".go.golden") {
			replacements[strings.TrimSuffix(path, ".golden")] = path
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replacements) == 0 {
		t.Fatal("no fixed source files to compile")
	}
	encoded, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-vet=off", "-overlay", overlay, "runtimeresult", "runtimeresult/nilshadow")
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if key != "GO111MODULE" && key != "GOWORK" && key != "GOPATH" && key != "GOFLAGS" {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GO111MODULE=off", "GOWORK=off", "GOFLAGS=", "GOPATH="+data)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixed source does not build: %v\n%s", err, output)
	}
}
