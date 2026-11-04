package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestModelDiffPreflightReportsErrorsBeforeQuint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	quint := filepath.Join(t.TempDir(), "quint")
	if err := os.WriteFile(quint, []byte("#!/bin/sh\n: > \"$0.started\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string][]string{
		"no observation":      {"-observe", ""},
		"empty variable":      {"-observe", "phase,,retries"},
		"duplicate variable":  {"-observe", "phase,phase"},
		"negative states":     {"-states", "-1"},
		"zero pairs":          {"-pairs", "0"},
		"negative work":       {"-work", "-1"},
		"negative depth":      {"-depth", "-1"},
		"malformed constant":  {"-const", "N"},
		"empty constant":      {"-const", "N="},
		"duplicate constant":  {"-const", "N=1,N=2"},
		"trailing constant":   {"-const", "N=1,"},
		"positional argument": {"unexpected"},
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"-before", "old.yaml", "-after", "new.yaml", "-observe", "phase", "-quint", quint, "-json"}
			args = append(args, invalid...)
			var output bytes.Buffer
			code, err := modelDiff(t.Context(), &output, args)
			if code != 2 || err == nil {
				t.Fatalf("exit=%d error=%v\n%s", code, err, &output)
			}
			var doc validate.Document
			if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Command != "model-diff" || doc.Verdict != "error" || doc.Exit != 2 || doc.Error == "" {
				t.Fatalf("request failure lost machine evidence: %+v", doc)
			}
			if _, err := os.Stat(quint + ".started"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid request launched Quint: %v", err)
			}
		})
	}
	// Positive control: the fake binary is executable and a valid request reaches
	// it. The malformed requests above must stop before this same launch path.
	var output bytes.Buffer
	const binding = "../../validate/model/testdata/compare/before.yaml"
	code, err := modelDiff(t.Context(), &output, []string{"-before", binding, "-after", binding, "-observe", "phase", "-quint", quint, "-json"})
	if code != 2 || err == nil {
		t.Fatalf("fake tool failure: exit=%d error=%v", code, err)
	}
	if _, err := os.Stat(quint + ".started"); err != nil {
		t.Fatalf("positive control did not reach fake Quint: %v", err)
	}
}

func TestModelDiffCanceledRequestIsOperational(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	code, err := modelDiff(ctx, &output, []string{"-before", "old.yaml", "-after", "new.yaml", "-observe", "phase", "-json"})
	if code != 2 || !errors.Is(err, context.Canceled) {
		t.Fatalf("exit=%d error=%v", code, err)
	}
	var doc validate.Document
	if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Verdict != "error" || doc.Comparison == nil || doc.Comparison.Before.Binding != "old.yaml" || doc.Comparison.After.Binding != "new.yaml" {
		t.Fatalf("cancellation lost request provenance: %+v", doc)
	}
}

func TestModelDiffFiniteContracts(t *testing.T) {
	skip.Quint(t)
	const fixtures = "../../validate/model/testdata/compare/"
	for _, test := range []struct {
		name, after, status, verdict string
		extra                        []string
		code                         int
	}{
		{name: "ghost projection", after: "ghost.yaml", status: "equivalent", verdict: "pass", code: 0},
		{name: "changed guard", after: "after.yaml", status: "different", verdict: "fail", code: 1},
		{name: "truncated graph", after: "before.yaml", status: "inconclusive", verdict: "no verdict", extra: []string{"-states", "1"}, code: 2},
		{name: "product bound", after: "before.yaml", status: "inconclusive", verdict: "no verdict", extra: []string{"-pairs", "1"}, code: 2},
		{name: "work bound", after: "before.yaml", status: "inconclusive", verdict: "no verdict", extra: []string{"-work", "1"}, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"-before", fixtures + "before.yaml", "-after", fixtures + test.after, "-observe", "phase,retries", "-json"}
			args = append(args, test.extra...)
			var output bytes.Buffer
			code, err := modelDiff(t.Context(), &output, args)
			if code != test.code || err != nil {
				t.Fatalf("exit=%d error=%v\n%s", code, err, &output)
			}
			var doc validate.Document
			if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			comparison := doc.Comparison
			if comparison == nil || comparison.Status != test.status || doc.Verdict != test.verdict || doc.Exit != test.code || comparison.Before.Module == "" || comparison.Before.Spec == "" {
				t.Fatalf("comparison scope or verdict lost: %+v\n%s", comparison, &output)
			}
			if comparison.Before.Binding != fixtures+"before.yaml" || comparison.After.Binding != fixtures+test.after {
				t.Fatalf("binding provenance %+v", comparison)
			}
			if test.code == 1 {
				confirmed := false
				for _, direction := range []validate.DocComparisonDirection{comparison.Added, comparison.Removed} {
					if witness := direction.Witness; witness != nil && witness.Native == "confirmed" {
						confirmed = true
						if witness.ExclusionBasis != "complete_target_graph" || len(witness.Observed) == 0 || witness.Source == nil || !strings.Contains(witness.Source.Replay, ".expect(") {
							t.Fatalf("witness lacks separate source/exclusion evidence: %+v", witness)
						}
					}
				}
				if !confirmed {
					t.Fatalf("difference has no native-confirmed source: %+v", comparison)
				}
			}
		})
	}
}

func TestModelDiffConstantExpressionsPreserveQuotedCommas(t *testing.T) {
	got, err := comparisonConstants(`LABEL="a,b", SET=Set("[", "x\"y,z"), ROW={ names: ["a,b", "c"] }`)
	want := []string{`LABEL="a,b"`, `SET=Set("[", "x\"y,z")`, `ROW={ names: ["a,b", "c"] }`}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("constant expressions = %q, %v; want %q", got, err, want)
	}
}

// A selected export is owned evidence: an existing destination or a generic
// report writer targeting that directory must fail before model subprocesses.
func TestModelDiffExportPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	quint := filepath.Join(t.TempDir(), "quint")
	if err := os.WriteFile(quint, []byte("#!/bin/sh\n: > \"$0.started\"\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	existing := t.TempDir()
	sentinel := filepath.Join(existing, "evidence")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "new-evidence")
	for name, args := range map[string][]string{
		"existing":                  {"-out", existing},
		"existing report collision": {"-out", existing, "-report", "json=" + sentinel},
		"report collision":          {"-out", fresh, "-report", "json=" + filepath.Join(fresh, "report.json")},
		"unrelated report":          {"-out", fresh, "-report", "json=" + filepath.Join(existing, "other-report.json")},
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			base := []string{"-before", "old.yaml", "-after", "new.yaml", "-observe", "phase", "-quint", quint, "-json"}
			code, err := modelDiff(t.Context(), &output, append(base, args...))
			if code != 2 || err == nil {
				t.Fatalf("exit=%d error=%v", code, err)
			}
			var d validate.Document
			if err := json.Unmarshal(output.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			if d.Verdict != "error" || !strings.Contains(d.Error, "-out") || d.Comparison != nil {
				t.Fatalf("missing operational report: %+v", d)
			}
			if _, err := os.Stat(quint + ".started"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid export launched Quint: %v", err)
			}
		})
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatalf("existing evidence changed: %q %v", b, err)
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid export created destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(existing, "other-report.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused unrelated report was written: %v", err)
	}
}

func TestModelDiffExportReplayRelocates(t *testing.T) {
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	skip.Quint(t)
	original := filepath.Join(t.TempDir(), "original")
	if err := os.MkdirAll(original, 0755); err != nil {
		t.Fatal(err)
	}
	const fixture = "../../validate/model/testdata/compare/"
	for _, name := range []string{"before.qnt", "before.yaml", "after.qnt", "after.yaml"} {
		b, err := os.ReadFile(fixture + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(original, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	export := filepath.Join(t.TempDir(), "comparison")
	var output bytes.Buffer
	code, err := modelDiff(t.Context(), &output, []string{"-before", filepath.Join(original, "before.yaml"), "-after", filepath.Join(original, "after.yaml"), "-observe", "phase,retries", "-out", export, "-json"})
	if code != 1 || err != nil {
		t.Fatalf("exit=%d error=%v\n%s", code, err, &output)
	}
	var stdoutDoc validate.Document
	if err := json.Unmarshal(output.Bytes(), &stdoutDoc); err != nil {
		t.Fatal(err)
	}
	if stdoutDoc.Artifacts != export {
		t.Fatalf("stdout artifact root=%q, want %q", stdoutDoc.Artifacts, export)
	}
	wire, err := os.ReadFile(filepath.Join(export, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d validate.Document
	if err := json.Unmarshal(wire, &d); err != nil {
		t.Fatal(err)
	}
	if d.Artifacts != "." || d.Comparison.Status != "different" || d.Exit != 1 {
		t.Fatalf("nonportable exported report: %+v", d)
	}
	replay := d.Comparison.Added.Witness.Replay
	if replay == nil || replay.Module.Path == "" || len(replay.Sources) == 0 || len(replay.Args) == 0 || replay.Seed != d.Comparison.Added.Witness.NativeSeed {
		t.Fatalf("missing native replay artifact: %+v", replay)
	}
	if err := os.RemoveAll(original); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(export, moved); err != nil {
		t.Fatal(err)
	}
	for _, file := range append([]validate.File{replay.Module}, replay.Sources...) {
		if filepath.IsAbs(file.Path) || strings.Contains(file.Path, "\\") {
			t.Fatalf("nonportable path %q", file.Path)
		}
		b, err := os.ReadFile(filepath.Join(moved, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != file.SHA256 {
			t.Fatalf("artifact %s hash=%s want %s", file.Path, got, file.SHA256)
		}
	}
	cmd := quintcmd.Command(t.Context(), "quint", moved, replay.Args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("relocated replay: %v\n%s", err, b)
	}
}

func TestModelDiffExportRefusalCannotCreateEnvironmentReports(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "refused")
	t.Setenv("GOTOGETHER_REPORT_DIR", fresh)
	var output bytes.Buffer
	code, err := modelDiff(t.Context(), &output, []string{"-before", "old.yaml", "-after", "new.yaml", "-observe", "phase", "-out", fresh, "-json"})
	if code != 2 || err == nil {
		t.Fatalf("exit=%d error=%v", code, err)
	}
	var d validate.Document
	if err := json.Unmarshal(output.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Verdict != "error" || !strings.Contains(d.Error, "GOTOGETHER_REPORT_DIR") {
		t.Fatalf("lost export refusal: %+v", d)
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused export created environment reports: %v", err)
	}
}

func TestModelDiffExportRejectsAliasedReportWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink support varies on Windows")
	}
	root := t.TempDir()
	fresh, alias := filepath.Join(root, "evidence"), filepath.Join(root, "alias")
	if err := os.Symlink(fresh, alias); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code, err := modelDiff(t.Context(), &output, []string{"-before", "old.yaml", "-after", "new.yaml", "-observe", "phase", "-out", fresh, "-report", "json=" + filepath.Join(alias, "report.json"), "-json"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "cannot be combined with -report") {
		t.Fatalf("exit=%d error=%v", code, err)
	}
	var d validate.Document
	if err := json.Unmarshal(output.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Verdict != "error" || d.Comparison != nil {
		t.Fatalf("aliased writer bypassed preflight: %+v", d)
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aliased report created refused evidence directory: %v", err)
	}
}
