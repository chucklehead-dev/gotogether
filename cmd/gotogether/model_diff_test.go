package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

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
