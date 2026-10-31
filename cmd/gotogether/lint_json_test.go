package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintJSONKeepsIndependentFindingsAndBlockedChecks(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("spec: model.qnt\nmodule: counter\nactions:\n  pay:\n    arg: {}\n"), 0600)
	os.WriteFile(filepath.Join(dir, "escape.go"), []byte("package app\nimport (\"time\"; \"github.com/chucklehead-dev/gotogether/qenv\")\nvar _ = qenv.New\nfunc escape() { time.Sleep(time.Second) }\n"), 0600)
	args := []string{"-binding", bad, "-sim", dir}
	code, out := lintOut(t, append(args, "-json")...)
	var report struct {
		Format   string `json:"format"`
		Complete bool   `json:"complete"`
		Findings []struct {
			Code, Message, Path, Fix string
			Line, Column             int
		} `json:"findings"`
		Checks []struct{ Name, Target, Status, Reason string } `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output must be one JSON document: %v\n%s", err, out)
	}
	if code != 1 || report.Format != "gotogether.lint/1" || report.Complete {
		t.Fatalf("schema failure became clean or operational: code=%d %s", code, out)
	}
	schema, source, blocked := false, false, false
	for _, f := range report.Findings {
		if f.Path == bad && f.Line == 5 {
			schema = true
		}
		if strings.HasSuffix(f.Path, "escape.go") && f.Line == 4 && f.Column > 0 && f.Fix != "" {
			source = true
		}
	}
	for _, check := range report.Checks {
		if check.Status == "blocked" && check.Reason != "" {
			blocked = true
		}
	}
	if !schema || !source || !blocked {
		t.Fatalf("lost source/schema/blocked evidence: %s", out)
	}
	textCode, text := lintOut(t, args...)
	if textCode != code {
		t.Fatalf("text and JSON disagree: %d vs%d", textCode, code)
	}
	for _, finding := range report.Findings {
		if !strings.Contains(text, finding.Code) {
			t.Errorf("text omitted finding %s", finding.Code)
		}
	}
	_, again := lintOut(t, append(args, "-json")...)
	if again != out {
		t.Fatal("unchanged inputs changed report ordering")
	}
}

func TestLintJSONOperationalFailureIsStructured(t *testing.T) {
	code, out := lintOut(t, "-json", "-binding", filepath.Join(t.TempDir(), "missing.yaml"))
	var report struct {
		Format   string
		Complete bool
		ExitCode int
		Checks   []struct{ Status, Reason string }
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("IO failure lost JSON: %v %s", err, out)
	}
	if code != 2 || report.ExitCode != 2 || report.Complete || len(report.Checks) == 0 {
		t.Fatalf("missing file reported as checked: code=%d %s", code, out)
	}
}
