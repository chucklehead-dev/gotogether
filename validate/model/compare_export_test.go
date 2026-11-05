package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func comparisonExportCase(t *testing.T) (*Comparison, string) {
	t.Helper()
	skip.Quint(t)
	root := t.TempDir()
	var graphs []*GraphResult
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, ext := range []string{"yaml", "qnt"} {
			data, err := os.ReadFile(filepath.Join("testdata", "compare", name+"."+ext))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+"."+ext), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		c, err := validate.NewChecker(t.Context(), filepath.Join(dir, name+".yaml"), validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		graph, err := For(c).Graph(t.Context(), GraphSearch{})
		if err != nil {
			t.Fatal(err)
		}
		graphs = append(graphs, graph)
	}
	r, err := Compare(t.Context(), graphs[0], graphs[1], CompareOptions{Observe: []string{"phase", "retries"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmComparison(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "different" || r.Added.Witness.Native != "confirmed" {
		t.Fatalf("native source not confirmed: %+v", r)
	}
	return r, root
}

func TestComparisonExportMovesWithoutOriginalSources(t *testing.T) {
	r, original := comparisonExportCase(t)
	verdict := r.Status
	out := filepath.Join(t.TempDir(), "review")
	if err := ExportComparison(t.Context(), r, out); err != nil {
		t.Fatal(err)
	}
	if r.Status != verdict || r.Artifacts != out || r.Removed.Witness != nil {
		t.Fatalf("export changed verdict: %+v", r)
	}
	replay := r.Added.Witness.Replay
	if replay == nil || replay.Seed != r.Added.Witness.NativeSeed || replay.Main != "simulate" || replay.Run != "gtg_comparison_witness" {
		t.Fatalf("missing replay: %+v", replay)
	}
	// Every exported file is content-addressed and confined to the export root.
	for _, file := range append([]validate.File{replay.Module}, replay.Sources...) {
		if filepath.IsAbs(file.Path) || strings.HasPrefix(file.Path, "../") {
			t.Fatalf("nonportable artifact: %+v", file)
		}
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != file.SHA256 {
			t.Fatalf("artifact changed: %+v", file)
		}
	}
	data, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report validate.Document
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Artifacts != "." || report.Comparison.Status != verdict || report.Comparison.Added.Witness.ExclusionBasis != "complete_target_graph" {
		t.Fatalf("report evidence: %+v", report)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(original); err != nil {
		t.Fatal(err)
	}
	args := slices.Clone(replay.Args)
	// Native --out paths are unnecessary: this is the exact confirmed source run.
	cmd := quintcmd.Command(t.Context(), "quint", moved, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("relocated replay: %v\n%s", err, output)
	}
}

func TestComparisonExportRejectsModifiedEvidenceAndLeavesDestination(t *testing.T) {
	r, _ := comparisonExportCase(t)
	for _, tc := range []struct {
		name string
		edit func(*Comparison)
	}{
		{"projection", func(r *Comparison) { r.Observe = []string{"phase"} }},
		{"instance", func(r *Comparison) { r.Before.Module = "other" }},
		{"budget", func(r *Comparison) { r.PairLimit++ }},
		{"direction", func(r *Comparison) { r.Added.Status = "included" }},
		{"verdict", func(r *Comparison) { r.Status = "equivalent" }},
		{"native seed", func(r *Comparison) { r.Added.Witness.NativeSeed = "999" }},
		{"native status", func(r *Comparison) { r.Added.Witness.Native = "unconfirmed" }},
		{"witness", func(r *Comparison) { r.Added.Witness.Source.Replay = "init" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved, witness := *r, *r.Added.Witness
			source := *r.Added.Witness.Source
			defer func() { *r = saved; *r.Added.Witness = witness; *r.Added.Witness.Source = source }()
			tc.edit(r)
			before := string(r.Doc().JSON())
			out := filepath.Join(t.TempDir(), "new")
			if err := ExportComparison(t.Context(), r, out); err == nil {
				t.Fatal("modified evidence exported")
			}
			if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed export published directory: %v", err)
			}
			if string(r.Doc().JSON()) != before {
				t.Fatal("failed export modified result")
			}
		})
	}
	out := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(out, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := string(r.Doc().JSON())
	if err := ExportComparison(t.Context(), r, out); err == nil {
		t.Fatal("existing destination replaced")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("existing evidence changed: %s %v", data, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ExportComparison(ctx, r, filepath.Join(t.TempDir(), "cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if string(r.Doc().JSON()) != before {
		t.Fatal("failure changed result")
	}
}

func TestComparisonExportUnknownAndEquivalentHaveReportOnly(t *testing.T) {
	one := syntheticComparisonGraph([]string{"x"}, []qeval.State{{"x": qeval.Int(1)}}, 1, [][]qeval.Edge{{}})
	empty := syntheticComparisonGraph([]string{"x"}, nil, 0, nil)
	for _, tc := range []struct {
		name          string
		before, after *GraphResult
		status        string
	}{
		{"equivalent", one, one, "equivalent"},
		{"candidate", empty, one, "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Compare(t.Context(), tc.before, tc.after, CompareOptions{Observe: []string{"x"}})
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "review")
			if err := ExportComparison(t.Context(), r, out); err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.status {
				t.Fatalf("export changed scope: %+v", r)
			}
			files, err := os.ReadDir(out)
			if err != nil || len(files) != 1 || files[0].Name() != "report.json" {
				t.Fatalf("report-only export produced replay: %+v %v", files, err)
			}
			if r.Added.Witness != nil && r.Added.Witness.Replay != nil {
				t.Fatal("unconfirmed source received replay artifact")
			}
		})
	}
	r, err := Compare(t.Context(), empty, one, CompareOptions{Observe: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Status, r.Added.Witness.Native, r.Added.Witness.NativeSeed = "different", "confirmed", "1"
	if err := ExportComparison(t.Context(), r, filepath.Join(t.TempDir(), "forged")); err == nil {
		t.Fatal("forged native flag exported")
	}
}

func TestComparisonExportRefusesChangedModelSources(t *testing.T) {
	r, original := comparisonExportCase(t)
	path := filepath.Join(original, "after", "after.qnt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// changed after native confirmation\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	before := string(r.Doc().JSON())
	out := filepath.Join(t.TempDir(), "review")
	if err := ExportComparison(t.Context(), r, out); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale sources exported: %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed export published directory: %v", err)
	}
	if string(r.Doc().JSON()) != before {
		t.Fatal("stale-source refusal changed comparison")
	}
}
