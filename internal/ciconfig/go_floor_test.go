package ciconfig

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/version"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// Live modules and emitted modules share one supported Go floor. Adoption
// histories keep their frozen source/module inputs and are checked separately.
func TestLiveGoFloorsAgree(t *testing.T) {
	root := "../.."
	floor := moduleGoFloor(t, filepath.Join(root, "go.mod"))
	var modules []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() && rel != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules" || rel == "internal/adoptiontest") {
			return filepath.SkipDir
		}
		if entry.Name() == "go.mod" {
			modules = append(modules, rel)
			if got := moduleGoFloor(t, path); got != floor {
				t.Errorf("%s has Go floor %s; root go.mod requires %s: align live modules and fixtures", rel, got, floor)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"go.mod", "ci/consumer/go.mod", "grist/go.mod", "qgosim/go.mod", "qweave/testdata/oldabi/core/go.mod"} {
		if !slices.Contains(modules, required) {
			t.Errorf("Go-floor discovery omitted live module %s", required)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	work, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if work.Go == nil || work.Go.Version != floor {
		t.Errorf("go.work must use root go.mod's Go floor %s", floor)
	}
}

func moduleGoFloor(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	module, err := modfile.Parse(path, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if module.Go == nil {
		t.Fatalf("%s has no Go floor", path)
	}
	return module.Go.Version
}

func TestEmittedGoFloorsAgree(t *testing.T) {
	floor := moduleGoFloor(t, "../../go.mod")
	for _, name := range []string{
		"cmd/gotogether/init.go", "grist/cmd/grist/main.go",
		"qweave/qweave_test.go", "qanalysis/runtime_results_acceptance_test.go",
	} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../..", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var emitted []string
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			moduleText := strings.HasPrefix(value, "module ") || strings.Contains(value, "\nmodule ")
			workspaceText := strings.HasPrefix(value, "go ") && strings.Contains(value, "\n\nuse ")
			if !moduleText && !workspaceText {
				return true
			}
			for line := range strings.SplitSeq(value, "\n") {
				if version, ok := strings.CutPrefix(line, "go "); ok {
					emitted = append(emitted, version)
				}
			}
			return true
		})
		if len(emitted) == 0 {
			t.Errorf("%s has no discoverable emitted Go directive; update this guard with the generator", name)
		}
		for _, version := range emitted {
			if version != floor {
				t.Errorf("%s emits Go %s; use root go.mod's floor %s", name, version, floor)
			}
		}
	}
}

func TestPublicationGoFloorsAgree(t *testing.T) {
	floor := moduleGoFloor(t, "../../go.mod")
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Jobs["go-current"].Env["GOTOOLCHAIN"] != "local" {
		t.Error("current Go admission must pin GOTOOLCHAIN=local rather than silently switch compiler")
	}
	currentHost := ""
	for _, step := range workflow.Jobs["go-current"].Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-go@") {
			currentHost = step.With["go-version"]
		}
	}
	if !version.IsValid("go"+currentHost) || version.Compare("go"+currentHost, "go"+floor) < 0 {
		t.Errorf("current Go host must be pinned at or above floor %s, got %q", floor, currentHost)
	}
	found := false
	for _, step := range workflow.Jobs["consumable"].Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-go@") {
			found = true
			if step.With["go-version"] != floor {
				t.Errorf("consumable CI must use root go.mod's floor %s, got %q", floor, step.With["go-version"])
			}
		}
	}
	if !found {
		t.Fatal("consumable CI has no setup-go step: review its publication toolchain")
	}
	image, err := os.ReadFile("../../ci/image/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`(?m)^ARG GO_VERSION=([^\s]+)$`).FindSubmatch(image)
	if version == nil || string(version[1]) != floor {
		t.Errorf("CI image must default to root go.mod's Go floor %s", floor)
	}
}
