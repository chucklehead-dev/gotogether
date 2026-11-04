package model

import (
	"slices"

	"github.com/chucklehead-dev/gotogether/validate"
)

// Doc projects a comparison's native facts into the shared command report.
// Source replay evidence and complete-target-graph exclusion stay separate; the
// projection never infers facts from a human-readable explanation.
func (r *Comparison) Doc() *validate.Document {
	d := validate.NewDocument("model-diff")
	d.Verdict, d.Exit = "no verdict", 2
	if r == nil {
		return d
	}
	c := &validate.DocComparison{Scope: "finite_observation_traces", Basis: r.Basis, Status: r.Status,
		Observe: slices.Clone(r.Observe), PairLimit: r.PairLimit, WorkLimit: r.WorkLimit,
		Before: comparisonGraphDoc(r.Before), After: comparisonGraphDoc(r.After),
		Added: comparisonDirectionDoc(r.Added), Removed: comparisonDirectionDoc(r.Removed)}
	d.Comparison = c
	switch r.Status {
	case "equivalent":
		d.Verdict, d.Exit = "pass", 0
	case "different":
		d.Verdict, d.Exit = "fail", 1
	}
	return d
}

func comparisonGraphDoc(g ComparisonGraph) validate.DocComparisonGraph {
	return validate.DocComparisonGraph{Binding: g.Binding, Spec: g.Spec, Module: g.Module,
		Constants: slices.Clone(g.Constants), Complete: g.Complete, Cut: g.Cut, Stopped: g.Stopped,
		States: g.States, Starts: g.Starts, Transitions: g.Transitions, Depth: g.Depth,
		Bound: validate.DocGraphBound{States: g.Bound.States, Depth: g.Bound.Depth}}
}

func comparisonDirectionDoc(d BehaviorInclusion) validate.DocComparisonDirection {
	out := validate.DocComparisonDirection{Status: d.Status, Reason: d.Reason, Explored: d.Explored, Work: d.Work, Cut: d.Cut}
	if d.Witness == nil {
		return out
	}
	w := d.Witness
	out.Witness = &validate.DocComparisonWitness{Native: w.Native, NativeReason: w.NativeReason,
		NativeAttempts: w.NativeAttempts, NativeSeed: w.NativeSeed, ExclusionBasis: "complete_target_graph"}
	for _, state := range w.Observed {
		out.Witness.Observed = append(out.Witness.Observed, slices.Clone(state))
	}
	if w.Source != nil {
		path := &validate.DocComparisonPath{Replay: w.Source.Replay, Sampled: slices.Clone(w.Source.Sampled)}
		for _, step := range w.Source.Steps {
			path.Steps = append(path.Steps, validate.DocComparisonStep{Call: step.Call,
				Picks: slices.Clone(step.Picks), State: slices.Clone(step.State)})
		}
		out.Witness.Source = path
	}
	return out
}
