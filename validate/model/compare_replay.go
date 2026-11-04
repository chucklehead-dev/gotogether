package model

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/validate"
)

// ComparisonReplayAttempts is the maximum number of seeded, single-sample
// native executions used to confirm one comparison witness. Failure to draw
// its concrete path is inconclusive, never evidence of exclusion.
const ComparisonReplayAttempts = 8

// ConfirmComparison checks comparison witnesses with Quint's native evaluator.
// It checks only source admission: exclusion remains the complete target graph's
// result. Every replay uses the original init and step and checks every full
// concrete state, including hidden state. A comparison with graph witnesses but
// no successfully replayed source witness becomes inconclusive. Tool errors and
// cancellation are returned as errors, not comparison differences.
func ConfirmComparison(ctx context.Context, comparison *Comparison) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if comparison == nil || comparison.before == nil || comparison.after == nil {
		return fmt.Errorf("model comparison replay: no comparison produced by Compare")
	}
	if comparison.Added.Witness != comparison.added || comparison.Removed.Witness != comparison.removed {
		comparison.Status = "inconclusive"
		return fmt.Errorf("model comparison replay: the comparison witnesses were replaced; compare the graphs again")
	}
	// Public verdict/native fields are output, not caller-supplied evidence.
	comparison.Status = "inconclusive"
	if comparison.equivalent {
		comparison.Status = "equivalent"
	}
	verify := func() error {
		for _, graph := range []*GraphResult{comparison.before, comparison.after} {
			if graph != nil && graph.sourceChecker != nil {
				if err := graph.sourceChecker.VerifySource(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := verify(); err != nil {
		comparison.Status = "inconclusive"
		return err
	}
	for _, one := range []struct {
		name   string
		dir    *BehaviorInclusion
		source *GraphResult
	}{
		{"added", &comparison.Added, comparison.after},
		{"removed", &comparison.Removed, comparison.before},
	} {
		if one.dir.Witness == nil {
			continue
		}
		if err := confirmBehavior(ctx, one.source, one.dir.Witness); err != nil {
			comparison.Status = "inconclusive"
			return fmt.Errorf("model comparison %s source replay: %w", one.name, err)
		}
		if one.dir.Witness.Native == "confirmed" {
			one.dir.Reason = "source admission natively confirmed; exclusion is the complete target graph result"
		} else {
			one.dir.Reason = "complete-graph difference lacks native source confirmation"
		}
		if err := verify(); err != nil {
			comparison.Status = "inconclusive"
			return err
		}
	}
	confirmed := false
	witness := false
	for _, dir := range []*BehaviorInclusion{&comparison.Added, &comparison.Removed} {
		if dir.Witness != nil {
			witness = true
			confirmed = confirmed || dir.Witness.Native == "confirmed"
		}
	}
	if confirmed {
		comparison.Status = "different"
	} else if witness {
		comparison.Status = "inconclusive"
	}
	return nil
}

func confirmBehavior(ctx context.Context, source *GraphResult, witness *BehaviorWitness) error {
	if source == nil || source.checker == nil || witness.Source == nil || witness.replay == "" {
		return fmt.Errorf("the source graph has no replayable model instance")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if witness.Source.Replay != witness.replay || witness.evidence != witnessEvidence(witness) {
		return fmt.Errorf("the comparison witness was modified: compare the graphs again before native confirmation")
	}
	if source.sourceChecker != nil {
		if err := source.sourceChecker.VerifySource(); err != nil {
			return err
		}
	}
	witness.Native, witness.NativeReason = "unconfirmed", ""
	witness.confirmedSeed, witness.Replay = "", nil
	witness.NativeAttempts, witness.NativeSeed = 0, ""
	dir, err := os.MkdirTemp("", "gotogether-comparison-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "comparison.qnt")
	const runName = "gtg_comparison_witness"
	if err := writeReplays(source.checker.Binding, nil, file, []string{"  run " + runName + " = " + witness.replay}); err != nil {
		return err
	}
	for attempt := 1; attempt <= ComparisonReplayAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		seed := strconv.Itoa(attempt)
		cmd := quintcmd.Command(ctx, source.checker.Options.Quint, dir,
			"test", file, "--main", "simulate", "--backend", "rust", "--match", "^"+runName+"$", "--seed", seed, "--max-samples", "1")
		out, err := cmd.CombinedOutput()
		witness.NativeAttempts = attempt
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if source.sourceChecker != nil {
			if err := source.sourceChecker.VerifySource(); err != nil {
				return err
			}
		}
		if err == nil {
			// A zero exit code from a command that selected no run is not
			// positive evidence. Quint names the run and number actually run.
			if !strings.Contains(string(out), "ok "+runName+" passed 1 test(s)") {
				return fmt.Errorf("quint returned success without confirming %s: %s", runName, out)
			}
			witness.Native, witness.NativeSeed = "confirmed", seed
			witness.confirmedSeed = seed
			witness.NativeReason = "native source execution matched every concrete state; target exclusion is the complete graph result"
			return nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || !strings.Contains(string(out), "[QNT508]") {
			return fmt.Errorf("quint test: %w\n%s", err, out)
		}
		// QNT508 is a failed run/expect: nondeterminism may have drawn a
		// different init or step. A miss cannot disprove this path.
	}
	witness.NativeReason = fmt.Sprintf("none of %d seeded native executions reproduced the concrete source path; this does not establish absence", ComparisonReplayAttempts)
	return nil
}

// The public result is editable output, not caller-supplied replay evidence.
// Keep a compact identity of the source states/observations actually compared.
func witnessEvidence(witness *BehaviorWitness) [32]byte {
	b, _ := json.Marshal(struct {
		Observed [][]validate.NamedValue
		Source   *Counterexample
	}{witness.Observed, witness.Source})
	return sha256.Sum256(b)
}
