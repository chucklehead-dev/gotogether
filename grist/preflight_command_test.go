package grist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
	"github.com/chucklehead-dev/gotogether/internal/formats"
)

func modelFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[rel] = string(b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func modelCommand(t *testing.T, dir string, args ...string) ([]byte, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := grist.MainArgs(context.Background(), shopApp(), append([]string{"model", "-dir", dir}, args...), &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("model wrote unexpected stderr: %s", stderr.String())
	}
	return stdout.Bytes(), err
}

type preflightJSON struct {
	Format      string        `json:"format"`
	OK          bool          `json:"ok"`
	CanGenerate bool          `json:"canGenerate"`
	Complete    bool          `json:"complete"`
	Findings    []gen.Finding `json:"findings"`
	Checks      []gen.Check   `json:"checks"`
	Changes     []gen.Change  `json:"changes"`
}

func decodePreflight(t *testing.T, data []byte) preflightJSON {
	t.Helper()
	var report preflightJSON
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&report); err != nil {
		t.Fatalf("not one JSON report: %v\n%s", err, data)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("extra stdout after JSON: %v\n%s", err, data)
	}
	if report.Format != formats.ModelPreflight.Name() {
		t.Fatalf("format %q", report.Format)
	}
	return report
}

func TestModelPreflightJSONAggregatesAndNeverWrites(t *testing.T) {
	dir := t.TempDir()
	before := modelFiles(t, dir)
	data, err := modelCommand(t, dir, "-json")
	if err == nil {
		t.Fatal("missing handwritten and generated artifacts passed")
	}
	p := decodePreflight(t, data)
	if p.OK || !p.CanGenerate || !p.Complete || len(p.Changes) == 0 {
		t.Fatalf("wrong maintenance readiness: %+v", p)
	}
	codes := map[string]bool{}
	for _, f := range p.Findings {
		codes[f.Code] = true
		if f.Fix == "" {
			t.Errorf("no fix for %s", f.Code)
		}
	}
	for _, code := range []string{"model.invariants_missing", "model.rules_missing", "model.definition_missing", "model.generated_stale"} {
		if !codes[code] {
			t.Errorf("missing %s: %+v", code, p.Findings)
		}
	}
	if !reflect.DeepEqual(before, modelFiles(t, dir)) {
		t.Fatal("JSON mode mutated the model")
	}
	found := false
	for _, c := range p.Checks {
		if c.Name == "quint_typecheck" {
			found = c.Status == "not_checked"
		}
	}
	if !found {
		t.Fatal("JSON did not disclose the unperformed Quint typecheck")
	}
	if _, err := modelCommand(t, dir, "-init"); err != nil {
		t.Fatal(err)
	}
	data, err = modelCommand(t, dir, "-check", "-json")
	if err != nil {
		t.Fatal(err)
	}
	p = decodePreflight(t, data)
	if !p.OK || !p.CanGenerate || len(p.Changes) != 0 {
		t.Fatalf("fresh generated model not ready: %+v", p)
	}
	rulesPath := filepath.Join(dir, "model", "shop_rules.qnt")
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, bytes.Replace(rules, []byte("pure def addItemNeeds"), []byte("pure def addItemNeedz"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shop.binding.yaml"), []byte("# stale by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before = modelFiles(t, dir)
	data, err = modelCommand(t, dir, "-json")
	if err == nil {
		t.Fatal("combined drift and missing definition passed")
	}
	p = decodePreflight(t, data)
	codes = map[string]bool{}
	for _, f := range p.Findings {
		codes[f.Code] = true
	}
	if !codes["model.definition_missing"] || !codes["model.generated_stale"] {
		t.Fatalf("first error hid another: %+v", p.Findings)
	}
	if !reflect.DeepEqual(before, modelFiles(t, dir)) {
		t.Fatal("failing JSON check modified files")
	}
}

func TestModelPreflightRejectsWritingFlagCombinations(t *testing.T) {
	for _, args := range [][]string{{"-init", "-json"}, {"-init", "-check"}, {"-init", "-check", "-json"}} {
		dir := t.TempDir()
		data, err := modelCommand(t, dir, args...)
		var usage *grist.UsageError
		if !errors.As(err, &usage) || len(data) != 0 || len(modelFiles(t, dir)) != 0 {
			t.Fatalf("args %v: output %s err %v files %v", args, data, err, modelFiles(t, dir))
		}
	}
}

func TestModelPreflightMainMachineExit(t *testing.T) {
	if os.Getenv("GRIST_PREFLIGHT_CHILD") == "1" {
		os.Args = []string{"shop", "model", "-dir", os.Getenv("GRIST_PREFLIGHT_DIR"), "-json"}
		grist.Main(shopApp())
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestModelPreflightMainMachineExit$")
	cmd.Env = append(os.Environ(), "GRIST_PREFLIGHT_CHILD=1", "GRIST_PREFLIGHT_DIR="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("exit %v, stdout %s stderr %s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("preflight report duplicated on stderr: %s", stderr.String())
	}
	p := decodePreflight(t, stdout.Bytes())
	if p.OK || !strings.Contains(stdout.String(), "model.rules_missing") {
		t.Fatalf("failure lost structured report: %s", stdout.String())
	}
}
