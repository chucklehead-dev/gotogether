package model

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/tracecompare"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/internal/evalshare"
)

// CompareOptions selects the shared state variables to observe and bounds each
// directional inclusion search. Observe is required: no action, state variable,
// or stuttering transition is implicitly hidden. Pairs bounds product states
// (a source state and a set of compatible target states), default 100000. Work
// bounds examined target initial states and successor candidates, default 1000000.
type CompareOptions struct {
	Observe []string
	Pairs   int
	Work    int
}

// Comparison compares finite observation sequences in two complete state graphs.
// Equivalent means both directional inclusions hold for these instances;
// different means a native-confirmed source path is absent from the other's
// complete graph. Inconclusive means a graph or product search was incomplete.
// This is not action equivalence, stutter abstraction, refinement or liveness.
// Native confirmation of witnesses is separate from complete-graph exclusion.
type Comparison struct {
	// Artifacts locates exported replay paths; empty until successful publication.
	Artifacts      string
	Status         string
	Basis          string
	Observe        []string
	PairLimit      int
	WorkLimit      int
	Before         ComparisonGraph
	After          ComparisonGraph
	Added          BehaviorInclusion
	Removed        BehaviorInclusion
	before, after  *GraphResult
	added, removed *BehaviorWitness
	equivalent     bool
	descriptor     [32]byte
}

// ComparisonGraph records an inclusion input's instance and search extent.
type ComparisonGraph struct {
	Binding, Spec, Module string
	Constants             []validate.NamedValue
	Complete              bool
	Cut, Stopped          string
	States, Starts        int
	Transitions, Depth    int
	Bound                 GraphBound
}

// BehaviorInclusion is one direction: included, witness, bounded or inconclusive.
// Added checks after against before; Removed checks before against after.
// Explored counts admitted product states, including an empty-target witness.
// Work counts examined target candidates; Cut names a reached pairs or work limit.
type BehaviorInclusion struct {
	Status   string
	Reason   string
	Explored int
	Work     int
	Cut      string
	Witness  *BehaviorWitness
}

// BehaviorWitness is a shortest concrete source path whose complete observed
// sequence the other graph cannot follow. Source keeps the full states and an
// original-init/original-step replay checking every state. Native is not_run,
// confirmed or unconfirmed; native confirmation proves source feasibility only.
type BehaviorWitness struct {
	Observed       [][]validate.NamedValue
	Source         *Counterexample
	Native         string
	NativeReason   string
	NativeAttempts int
	NativeSeed     string
	// Replay is the published, portable native source witness and its source files.
	Replay        *ComparisonReplay
	confirmedSeed string
	replay        string
	evidence      [32]byte
}

// ComparisonReplay is a confirmed source witness exported with its model sources.
// Module and Sources paths are slash-separated and relative to Comparison.Artifacts.
// Main, Run and Seed are the exact native invocation selection; this replay
// confirms source admission only, not target exclusion or comparison equivalence.
type ComparisonReplay struct {
	Module          validate.File
	Sources         []validate.File
	Main, Run, Seed string
	// Args is the exact Quint argument vector, relative to the exported root.
	Args []string
}

// Compare checks both inclusions of finite projected state sequences, preserving
// every transition, including stutters. Hidden-state nondeterminism is retained
// by following all target states compatible with the entire prefix. Both inputs
// must be complete graphs produced by Analyzer.Graph; truncated or unsupported
// inputs yield an inconclusive result without a witness. Returned witnesses are
// graph candidates with Native not_run, keeping Status inconclusive until
// ConfirmComparison confirms their source feasibility with native Quint.
func Compare(ctx context.Context, before, after *GraphResult, opt CompareOptions) (*Comparison, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(opt.Observe) == 0 {
		return nil, fmt.Errorf("model comparison: observe must name at least one shared model variable")
	}
	seen := map[string]bool{}
	for _, name := range opt.Observe {
		if name == "" || strings.TrimSpace(name) != name {
			return nil, fmt.Errorf("model comparison: observation %q must be a model variable name", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("model comparison: observation %q is repeated", name)
		}
		seen[name] = true
	}
	if opt.Work < 0 {
		return nil, fmt.Errorf("model comparison: target-work limit must be nonnegative (0 uses the default)")
	}
	if opt.Work == 0 {
		opt.Work = 1000000
	}
	if opt.Pairs < 0 {
		return nil, fmt.Errorf("model comparison: product-state limit must be nonnegative (0 uses the default)")
	}
	if opt.Pairs == 0 {
		opt.Pairs = DefaultSearchStates
	}
	if before == nil || after == nil || before.graph == nil || after.graph == nil || before.e == nil || after.e == nil {
		return nil, fmt.Errorf("model comparison: both inputs must be state graphs produced by Analyzer.Graph")
	}
	for _, g := range []*GraphResult{before, after} {
		if g.sourceChecker != nil {
			if err := g.sourceChecker.VerifySource(); err != nil {
				return nil, err
			}
		}
	}
	bv, err := comparisonVars(before, opt.Observe, "before")
	if err != nil {
		return nil, err
	}
	av, err := comparisonVars(after, opt.Observe, "after")
	if err != nil {
		return nil, err
	}
	r := &Comparison{Status: "inconclusive", Basis: "complete_go_graph_product", Observe: slices.Clone(opt.Observe), PairLimit: opt.Pairs, WorkLimit: opt.Work,
		Before: comparisonExtent(before), After: comparisonExtent(after), before: before, after: after}
	defer func() { r.descriptor = comparisonDescriptor(r) }()
	if !before.graph.Complete || !after.graph.Complete || before.graph.Cut != "" || after.graph.Cut != "" {
		r.Added = BehaviorInclusion{Status: "inconclusive", Reason: "comparison requires two complete supported state graphs"}
		r.Removed = r.Added
		return r, nil
	}
	b, err := comparisonInput(ctx, before, bv)
	if err != nil {
		return nil, err
	}
	a, err := comparisonInput(ctx, after, av)
	if err != nil {
		return nil, err
	}
	added, err := tracecompare.Inclusion(ctx, a, b, tracecompare.Budget{Pairs: opt.Pairs, Work: opt.Work})
	if err != nil {
		return nil, err
	}
	removed, err := tracecompare.Inclusion(ctx, b, a, tracecompare.Budget{Pairs: opt.Pairs, Work: opt.Work})
	if err != nil {
		return nil, err
	}
	r.Added = comparisonDirection(after, av, opt.Observe, added)
	r.Removed = comparisonDirection(before, bv, opt.Observe, removed)
	r.added, r.removed = r.Added.Witness, r.Removed.Witness
	switch {
	case added.Status == "included" && removed.Status == "included":
		r.Status = "equivalent"
		r.equivalent = true
	}
	return r, nil
}

func comparisonVars(r *GraphResult, names []string, side string) ([]string, error) {
	var vars []string
	for _, name := range names {
		var matches []string
		for _, v := range r.e.Model.Vars {
			if evalshare.ShortName(v) == name {
				matches = append(matches, v)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("model comparison: %s observation %q must name exactly one model variable (found %d)", side, name, len(matches))
		}
		vars = append(vars, matches[0])
	}
	return vars, nil
}

func comparisonInput(ctx context.Context, r *GraphResult, vars []string) (tracecompare.Graph, error) {
	g := tracecompare.Graph{Observations: make([]string, len(r.graph.States)), Out: make([][]int, len(r.graph.States))}
	projection := &qeval.Model{Vars: vars}
	for i, state := range r.graph.States {
		if err := ctx.Err(); err != nil {
			return g, err
		}
		for _, v := range vars {
			if _, ok := state[v]; !ok {
				return g, fmt.Errorf("model comparison: state %d lacks observation %s", i, evalshare.ShortName(v))
			}
		}
		g.Observations[i] = projection.Key(state)
		if i < r.graph.Starts {
			g.Starts = append(g.Starts, i)
		}
		for _, edge := range r.graph.Out[i] {
			g.Out[i] = append(g.Out[i], edge.To)
		}
	}
	return g, nil
}

func comparisonExtent(r *GraphResult) ComparisonGraph {
	x := ComparisonGraph{Complete: r.graph.Complete, Cut: r.graph.Cut, Stopped: r.Stopped, States: len(r.graph.States),
		Starts: r.graph.Starts, Transitions: r.graph.Edges(), Bound: r.Bound}
	for _, depth := range r.graph.Depth {
		x.Depth = max(x.Depth, depth)
	}
	if r.checker != nil {
		b := r.checker.Binding
		x.Binding, x.Spec, x.Module = b.Path, b.SpecPath(), b.Module
		for _, name := range slices.Sorted(maps.Keys(b.Constants)) {
			x.Constants = append(x.Constants, validate.NamedValue{Name: name, Value: b.Constants[name]})
		}
	}
	return x
}

func comparisonDirection(r *GraphResult, vars, names []string, result tracecompare.Result) BehaviorInclusion {
	d := BehaviorInclusion{Status: result.Status, Explored: result.Explored, Work: result.Work, Cut: result.Cut}
	if result.Status == "bounded" {
		d.Reason = result.Cut + " limit reached: no inclusion or exclusion verdict"
	}
	if result.Status != "witness" {
		return d
	}
	w := &BehaviorWitness{Native: "not_run", Source: &Counterexample{}}
	for index, id := range result.Path {
		state := r.graph.States[id]
		var observed []validate.NamedValue
		for i, v := range vars {
			observed = append(observed, validate.NamedValue{Name: names[i], Value: qeval.String(state[v])})
		}
		w.Observed = append(w.Observed, observed)
		call := "step"
		if index == 0 {
			call = "init"
		}
		var full []validate.NamedValue
		var checks []string
		for _, v := range r.e.Model.Vars {
			name, value := evalshare.ShortName(v), qeval.String(state[v])
			full = append(full, validate.NamedValue{Name: name, Value: value})
			checks = append(checks, name+" == "+value)
		}
		w.Source.Steps = append(w.Source.Steps, GraphStep{Call: call, State: full})
		if index == 0 {
			w.Source.Replay = "init"
			if r.graph.Starts > 1 {
				w.Source.Sampled = append(w.Source.Sampled, 0)
			}
		} else {
			w.Source.Replay += ".then(step)"
			for _, edge := range r.graph.Out[result.Path[index-1]] {
				if edge.To != id {
					w.Source.Sampled = append(w.Source.Sampled, index)
					break
				}
			}
		}
		w.Source.Replay += ".expect(and { " + strings.Join(checks, ", ") + " })"
	}
	d.Reason = "complete-graph difference awaits native confirmation of the source path"
	w.replay = w.Source.Replay
	w.evidence = witnessEvidence(w)
	d.Witness = w
	return d
}
