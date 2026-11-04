package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

func comparisonForDocument() *Comparison {
	graph := ComparisonGraph{Binding: "before.yaml", Spec: "handshake.qnt", Module: "handshake",
		Constants: []validate.NamedValue{{Name: "LIMIT", Value: "1 + 1"}}, Complete: true,
		States: 3, Starts: 1, Transitions: 3, Depth: 2, Bound: GraphBound{States: 100}}
	return &Comparison{Status: "different", Basis: "complete_go_graph_product", WorkLimit: 1000, Observe: []string{"phase"}, PairLimit: 200,
		Before: graph, After: graph, Removed: BehaviorInclusion{Status: "included", Explored: 3},
		Added: BehaviorInclusion{Status: "witness", Explored: 2, Witness: &BehaviorWitness{
			Observed: [][]validate.NamedValue{{{Name: "phase", Value: `"idle"`}}, {{Name: "phase", Value: `"acked"`}}},
			Native:   "confirmed", NativeAttempts: 2, NativeSeed: "2",
			Source: &Counterexample{Replay: "exact-state replay", Sampled: []int{1}, Steps: []GraphStep{
				{Call: "init", State: []validate.NamedValue{{Name: "phase", Value: `"idle"`}}},
				{Call: "step", Picks: []validate.NamedValue{{Name: "value", Value: "9007199254740993"}}, State: []validate.NamedValue{{Name: "phase", Value: `"acked"`}}},
			}},
		}},
	}
}

func TestComparisonDocumentKeepsSourceAndExclusionEvidenceSeparate(t *testing.T) {
	r := comparisonForDocument()
	d := r.Doc()
	if d.Command != "model-diff" || d.Verdict != "fail" || d.Exit != 1 || d.Comparison.Status != "different" {
		t.Fatalf("comparison verdict lost: %+v", d)
	}
	c := d.Comparison
	if c.Scope != "finite_observation_traces" || c.PairLimit != 200 || c.Before.Bound.States != 100 || c.Before.Constants[0].Value != "1 + 1" {
		t.Fatalf("comparison scope lost: %+v", c)
	}
	w := c.Added.Witness
	if w.Native != "confirmed" || w.NativeAttempts != 2 || w.NativeSeed != "2" || w.ExclusionBasis != "complete_target_graph" {
		t.Fatalf("source admission and target exclusion lost: %+v", w)
	}
	var wire map[string]any
	if err := json.Unmarshal(d.JSON(), &wire); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.JSON()), `"value": "9007199254740993"`) {
		t.Fatalf("exact choice literal changed on wire: %s", d.JSON())
	}
	// A report snapshot must survive later changes to the native result.
	r.Observe[0] = "changed"
	r.Before.Constants[0].Value = "changed"
	r.Added.Witness.Observed[0][0].Value = "changed"
	r.Added.Witness.Source.Sampled[0] = 99
	r.Added.Witness.Source.Steps[1].Picks[0].Value = "changed"
	r.Added.Witness.Source.Steps[1].State[0].Value = "changed"
	if c.Observe[0] != "phase" || c.Before.Constants[0].Value != "1 + 1" || w.Observed[0][0].Value != `"idle"` || w.Source.Sampled[0] != 1 || w.Source.Steps[1].Picks[0].Value != "9007199254740993" || w.Source.Steps[1].State[0].Value != `"acked"` {
		t.Fatal("comparison document shares native mutable slices")
	}
}

func TestComparisonDocumentPreservesUnknownAndPartialResults(t *testing.T) {
	r := comparisonForDocument()
	r.Status = "inconclusive"
	r.Added.Witness.Native, r.Added.Witness.NativeReason = "unconfirmed", "native samples did not confirm source sequence"
	r.Removed = BehaviorInclusion{Status: "bounded", Reason: "pair limit", Explored: 200}
	d := r.Doc()
	if d.Verdict != "no verdict" || d.Exit != 2 || d.Comparison.Added.Witness.Native != "unconfirmed" || d.Comparison.Removed.Status != "bounded" || d.Error != "" {
		t.Fatalf("unknown source replay became difference or tool failure: %+v", d)
	}
	r.Added = BehaviorInclusion{Status: "inconclusive", Reason: "incomplete graph"}
	r.Before.Complete, r.Before.Cut, r.Before.Stopped, r.Before.Bound.Depth = false, "depth", "depth cap", 2
	d = r.Doc()
	if d.Comparison.Before.Complete || d.Comparison.Before.Cut != "depth" || d.Comparison.Before.Stopped != "depth cap" || d.Comparison.Before.Bound.Depth != 2 || d.Comparison.Added.Witness != nil {
		t.Fatalf("incomplete scope invented comparison evidence: %+v", d.Comparison)
	}
	r.Status = "equivalent"
	r.Added, r.Removed = BehaviorInclusion{Status: "included"}, BehaviorInclusion{Status: "included"}
	if d = r.Doc(); d.Verdict != "pass" || d.Exit != 0 {
		t.Fatalf("equivalence not projected: %+v", d)
	}
}

func TestComparisonDocumentOwnsPortableReplay(t *testing.T) {
	r := comparisonForDocument()
	r.Artifacts = "/tmp/exported-comparison"
	r.Added.Witness.Replay = &ComparisonReplay{Module: validate.File{Path: "added/replay.qnt", SHA256: "module-hash"},
		Sources: []validate.File{{Path: "added/sources/model.qnt", SHA256: "source-hash"}},
		Main:    "simulate", Run: "gtg_comparison_witness", Seed: "2",
		Args: []string{"test", "added/replay.qnt", "--main", "simulate", "--match", "^gtg_comparison_witness$", "--seed", "2", "--max-samples", "1"}}
	d := r.Doc()
	got := d.Comparison.Added.Witness.Replay
	if d.Artifacts != r.Artifacts || got == nil || got.Module.Path != "added/replay.qnt" || got.Sources[0].SHA256 != "source-hash" || got.Seed != "2" || len(got.Args) == 0 {
		t.Fatalf("portable replay not projected: %+v", d)
	}
	r.Added.Witness.Replay.Module.Path = "changed"
	r.Added.Witness.Replay.Sources[0].Path = "changed"
	r.Added.Witness.Replay.Args[1] = "changed"
	if got.Module.Path != "added/replay.qnt" || got.Sources[0].Path != "added/sources/model.qnt" || got.Args[1] != "added/replay.qnt" {
		t.Fatal("portable replay report shares mutable native evidence")
	}
}
