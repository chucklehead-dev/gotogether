package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Use the real command entrypoint in a child: ExitOnError must not be able to
// bypass the JSON request report or terminate the parent test process.
func TestLintRequestProcess(t *testing.T) {
	if at := slices.Index(os.Args, "--"); at >= 0 {
		os.Args = append([]string{"gotogether", "lint"}, os.Args[at+1:]...)
		main()
		return
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "quint-called")
	quint := filepath.Join(dir, "quint")
	if err := os.WriteFile(quint, []byte("#!/bin/sh\nprintf called > \"$GOTOGETHER_LINT_REQUEST_MARKER\"\nexit 77\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// A real binding would otherwise reach metadata compilation. Independently
	// missing scan targets also must not acquire checks on an invalid request.
	targets := []string{"-binding", "../../examples/bank/bank.binding.yaml", "-quint", quint, "-sim", filepath.Join(dir, "missing"), "-convert", filepath.Join(dir, "missing.yaml")}
	for _, tc := range []struct {
		name    string
		args    []string
		finding string
		json    bool
		exit    int
	}{
		{"positional", append(append([]string{"-json"}, targets...), "unexpected"), "request.arguments", true, 2},
		{"unknown flag", append(append([]string{"-json"}, targets...), "-jsno"), "request.flags", true, 2},
		{"missing value", []string{"-json", "-binding"}, "request.flags", true, 2},
		{"invalid boolean", []string{"-json", "-json=maybe"}, "request.flags", true, 2},
		{"invalid boolean without selection", []string{"-json=maybe"}, "request.flags", false, 2},
		{"unreached json flag", []string{"-jsno", "-json"}, "request.flags", false, 2},
		{"json is string value", []string{"-binding", "-json", "-jsno"}, "request.flags", false, 2},
		{"disabled json", []string{"-json", "-json=false", "-jsno"}, "request.flags", false, 2},
		{"delimiter", []string{"--", "-json"}, "request.arguments", false, 2},
		{"positional before json", []string{"unexpected", "-json"}, "request.arguments", false, 2},
		{"help", append(append([]string{"-json"}, targets...), "-h"), "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], append([]string{"-test.run=^TestLintRequestProcess$", "--"}, tc.args...)...)
			cmd.Env = append(os.Environ(), "GOTOGETHER_LINT_REQUEST_MARKER="+marker)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			got := 0
			if err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatal(err)
				}
				got = ee.ExitCode()
			}
			if got != tc.exit {
				t.Fatalf("exit %d want %d: stdout=%s stderr=%s", got, tc.exit, &stdout, &stderr)
			}
			if tc.json {
				var report struct {
					Format   string
					Complete bool
					ExitCode int
					Checks   []struct {
						Name, Status, Reason string
						Operational          bool
					}
					Findings []struct{ Code, Severity, Fix string }
					Evidence []json.RawMessage
				}
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatalf("request error must be one JSON document: %v\nstdout=%s stderr=%s", err, &stdout, &stderr)
				}
				if report.Format != "gotogether.lint/1" || report.Complete || report.ExitCode != 2 || len(report.Checks) != 1 || len(report.Findings) != 1 || len(report.Evidence) != 0 {
					t.Fatalf("request was inspected or misclassified: %s", &stdout)
				}
				c, f := report.Checks[0], report.Findings[0]
				if c.Name != tc.finding || c.Status != "blocked" || !c.Operational || c.Reason == "" || f.Code != tc.finding || f.Severity != "error" || f.Fix == "" {
					t.Fatalf("missing actionable operational request failure: %s", &stdout)
				}
			} else {
				all := stdout.String() + stderr.String()
				if json.Valid(stdout.Bytes()) {
					t.Fatalf("unparsed/disabled JSON or help unexpectedly produced JSON: %s", &stdout)
				}
				if tc.finding != "" && !strings.Contains(all, tc.finding) {
					t.Fatalf("missing text request diagnostic %q: stdout=%s stderr=%s", tc.finding, &stdout, &stderr)
				}
				if tc.exit == 0 && !strings.Contains(all, "gotogether lint:") {
					t.Fatalf("help not printed: %s", all)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("invalid request/help invoked Quint: %v", err)
			}
		})
	}
}
