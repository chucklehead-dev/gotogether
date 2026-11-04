package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chucklehead-dev/gotogether/internal/atomicdir"
	"github.com/chucklehead-dev/gotogether/validate"
)

// ExportComparison publishes report.json and portable source-confirmed witness
// modules in a fresh directory. Replay paths in the result are relative to dir;
// Artifacts identifies dir, while report.json uses "." so moving the directory
// preserves its references. Export does not rerun or strengthen graph exclusion.
// Equivalent or inconclusive comparisons export their report, but an unconfirmed
// witness receives no replay artifact. Failures leave destination/result unchanged.
func ExportComparison(ctx context.Context, comparison *Comparison, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dir == "" {
		return fmt.Errorf("model comparison export: choose a fresh output directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("model comparison export: %s already exists: choose a fresh directory", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := verifyComparisonExport(comparison); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	copy := *comparison
	copy.Artifacts = dir
	for _, direction := range []struct {
		name          string
		source        *GraphResult
		input, output *BehaviorInclusion
	}{
		{"added", comparison.after, &comparison.Added, &copy.Added},
		{"removed", comparison.before, &comparison.Removed, &copy.Removed},
	} {
		if direction.input.Witness == nil {
			continue
		}
		witness := *direction.input.Witness
		witness.Replay = nil
		direction.output.Witness = &witness
		if witness.confirmedSeed == "" {
			continue
		}
		replay, err := exportComparisonWitness(ctx, stage, direction.name, direction.source, &witness)
		if err != nil {
			return fmt.Errorf("model comparison export %s: %w", direction.name, err)
		}
		witness.Replay = replay
	}
	if err := verifyComparisonExport(comparison); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	doc := copy.Doc()
	doc.Artifacts = "."
	if err := os.WriteFile(filepath.Join(stage, "report.json"), doc.JSON(), 0o644); err != nil {
		return err
	}
	if err := atomicdir.Publish(stage, dir); err != nil {
		return err
	}
	comparison.Artifacts = dir
	if comparison.Added.Witness != nil {
		comparison.Added.Witness.Replay = copy.Added.Witness.Replay
	}
	if comparison.Removed.Witness != nil {
		comparison.Removed.Witness.Replay = copy.Removed.Witness.Replay
	}
	return nil
}

func exportComparisonWitness(ctx context.Context, stage, name string, source *GraphResult, witness *BehaviorWitness) (*ComparisonReplay, error) {
	if source == nil || source.sourceChecker == nil || source.checker == nil {
		return nil, fmt.Errorf("source graph has no captured Quint sources: create a new checker and compare again")
	}
	guard, err := source.sourceChecker.CapturedModelSources()
	if err != nil {
		return nil, err
	}
	rel := filepath.Join(name, "model")
	snapshot, err := guard.Export(ctx, filepath.Join(stage, rel))
	if err != nil {
		return nil, err
	}
	binding := *source.checker.Binding
	binding.Spec = filepath.Join(stage, rel, filepath.FromSlash(snapshot.Spec.Path))
	module := filepath.Join(stage, name, "witness.qnt")
	const run = "gtg_comparison_witness"
	if err := writeReplays(&binding, nil, module, []string{"  run " + run + " = " + witness.replay}); err != nil {
		return nil, err
	}
	file, err := comparisonArtifact(stage, module)
	if err != nil {
		return nil, err
	}
	replay := &ComparisonReplay{Module: file, Main: "simulate", Run: run, Seed: witness.confirmedSeed}
	replay.Args = []string{"test", replay.Module.Path, "--main", replay.Main, "--backend", "rust", "--match", "^" + run + "$", "--seed", replay.Seed, "--max-samples", "1"}
	for _, f := range snapshot.Files {
		f.Path = filepath.ToSlash(filepath.Join(rel, filepath.FromSlash(f.Path)))
		replay.Sources = append(replay.Sources, f)
	}
	if err := guard.Verify(); err != nil {
		return nil, err
	}
	return replay, nil
}

func comparisonArtifact(root, path string) (validate.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return validate.File{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return validate.File{}, err
	}
	hash := sha256.Sum256(data)
	return validate.File{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(hash[:])}, nil
}

func verifyComparisonExport(r *Comparison) error {
	if r == nil || r.before == nil || r.after == nil || r.descriptor != comparisonDescriptor(r) {
		return fmt.Errorf("model comparison export: comparison metadata was not retained or was modified: compare the graphs again")
	}
	if r.Added.Witness != r.added || r.Removed.Witness != r.removed {
		return fmt.Errorf("model comparison export: comparison witnesses were replaced: compare the graphs again")
	}
	status := "inconclusive"
	if r.equivalent {
		status = "equivalent"
	}
	for _, witness := range []*BehaviorWitness{r.added, r.removed} {
		if witness == nil {
			continue
		}
		if witness.Source == nil || witness.Source.Replay != witness.replay || witness.evidence != witnessEvidence(witness) {
			return fmt.Errorf("model comparison export: witness evidence was modified: compare the graphs again")
		}
		if witness.confirmedSeed == "" {
			if witness.Native == "confirmed" {
				return fmt.Errorf("model comparison export: source confirmation was not retained: confirm the comparison again")
			}
			continue
		}
		attempt, err := strconv.Atoi(witness.confirmedSeed)
		if err != nil || witness.Native != "confirmed" || witness.NativeSeed != witness.confirmedSeed || witness.NativeAttempts != attempt {
			return fmt.Errorf("model comparison export: native evidence was modified: confirm the comparison again")
		}
		status = "different"
	}
	if r.Status != status {
		return fmt.Errorf("model comparison export: comparison verdict was modified: confirm the comparison again")
	}
	for _, graph := range []*GraphResult{r.before, r.after} {
		if graph.sourceChecker != nil {
			if err := graph.sourceChecker.VerifySource(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retain scope, instance and search facts; native confirmation and file
// locations are separate evolving facts, not comparison configuration.
func comparisonDescriptor(r *Comparison) [32]byte {
	type direction struct {
		Status         string
		Explored, Work int
		Cut            string
	}
	value := struct {
		Basis                string
		Observe              []string
		PairLimit, WorkLimit int
		Before, After        ComparisonGraph
		Added, Removed       direction
	}{r.Basis, r.Observe, r.PairLimit, r.WorkLimit, r.Before, r.After,
		direction{r.Added.Status, r.Added.Explored, r.Added.Work, r.Added.Cut},
		direction{r.Removed.Status, r.Removed.Explored, r.Removed.Work, r.Removed.Cut}}
	data, _ := json.Marshal(value)
	return sha256.Sum256(data)
}
