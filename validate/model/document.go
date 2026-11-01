package model

import (
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// Doc projects native simulation facts into the shared machine report. Text is
// retained as context only; changing its wording cannot change checks or scope.
func (r *Report) Doc() *validate.Document {
	d := validate.NewDocument("simulate")
	if r == nil {
		return d
	}
	d.Binding, d.Exit = r.Binding, r.Code
	switch r.Code {
	case 0:
		d.Verdict = "pass"
	case 1:
		d.Verdict = "fail"
	default:
		d.Verdict = "no verdict"
	}
	if r.Skipped != "" {
		d.SkipReason, d.Reason, d.Verdict = r.Skipped, r.Skipped, "no verdict"
		d.Notes = append(d.Notes, r.Skipped)
	}
	if r.EvaluatorBug != "" {
		d.Verdict = "error"
		d.Error = r.EvaluatorBug
	}
	if r.Text != "" {
		d.Notes = append(d.Notes, strings.TrimSpace(r.Text))
	}
	if g := r.Graph; g != nil {
		d.Graph = &validate.DocGraph{Scope: "model_state_graph", States: g.States, Starts: g.Starts, Transitions: g.Transitions, Depth: g.Depth, Complete: g.Complete, Cut: g.Cut, Stopped: g.Stopped, Bound: validate.DocGraphBound{States: g.Bound.States, Depth: g.Bound.Depth}, Deadlocks: g.Deadlocks, Terminals: g.Terminals, Quiescent: g.Quiescent, Unfair: slices.Clone(g.Unfair), Stops: slices.Clone(g.Stops)}
	}
	for _, v := range r.Invariants {
		if v.Error != "" {
			d.Verdict, d.Error = "error", v.Error
		}
		d.Checks = append(d.Checks, modelDocCheck(v, "invariant"))
	}
	for _, v := range r.Eventually {
		if v.Error != "" {
			d.Verdict, d.Error = "error", v.Error
		}
		d.Checks = append(d.Checks, modelDocCheck(v, "eventually"))
	}
	if r.Deadlocks != nil {
		if r.Deadlocks.Error != "" {
			d.Verdict, d.Error = "error", r.Deadlocks.Error
		}
		d.Checks = append(d.Checks, modelDocCheck(*r.Deadlocks, "graph"))
	}
	for _, p := range r.Possibles {
		verdict := "fail"
		if p.Found {
			verdict = "reached"
		}
		goal := p.Goal
		if goal.Kind == "" && p.Eval != nil {
			goal = p.Eval.Goal
		}
		possible := &validate.DocPossible{Goal: possibleGoalDoc(goal), Found: p.Found, Impossible: p.Impossible, EvaluatorError: p.EvalErr}
		if e := p.Eval; e != nil {
			possible.Search = &validate.DocPossibleSearch{Found: e.Found, Complete: e.Complete, Cut: e.Cut, Nodes: e.Nodes, Depth: e.Depth, Path: possiblePathDoc(e.Witness)}
		}
		d.Checks = append(d.Checks, validate.DocCheck{Name: p.Possible.Text(), Kind: "possible", Verdict: verdict, Detail: p.Verdict, Possible: possible})
	}
	if d.Error == "" && r.Skipped == "" {
		for _, check := range d.Checks {
			if check.Verdict == "fail" {
				d.Verdict = "fail"
				break
			}
		}
	}
	if r.EvaluatorBug != "" {
		for i := range d.Checks {
			d.Checks[i].Verdict = "unchecked"
			d.Checks[i].Detail = "evaluator disagreement voids graph verdicts: " + r.EvaluatorBug
			if b := d.Checks[i].Bound; b != nil {
				b.Kind = "stopped"
				b.Proves = false
				b.Stopped = r.EvaluatorBug
			}
		}
	}
	return d
}

func modelDocCheck(v Verdict, kind string) validate.DocCheck {
	verdict := "unchecked"
	switch {
	case v.Error != "":
		verdict = "fail"
	case v.Unchecked != "" && !v.Violated:
		verdict = "unchecked"
	case kind == "eventually":
		switch {
		case v.Violated && v.Class == "holds":
			verdict = "fail"
		case v.Violated && v.Class == "allows":
			verdict = "allows"
		case v.Violated:
			verdict = "hot"
		case v.Held && v.Bound.Kind == "complete":
			verdict = "live"
		case v.Held:
			verdict = "bounded"
		}
	case kind == "graph":
		switch {
		case v.Violated:
			verdict = "fail"
		case v.Held && v.Bound.Kind == "complete":
			verdict = "holds"
		case v.Held:
			verdict = "bounded"
		}
	case v.Class == "allows":
		if v.Violated {
			verdict = "allows"
		} else {
			verdict = "fail"
		}
	case v.Violated:
		verdict = "fail"
	case v.Held:
		verdict = "holds"
		if v.Bound.Kind != "sampled" && v.Bound.Kind != "verified" && v.Bound.Kind != "complete" {
			verdict = "bounded"
		}
	}
	detail := v.Bound.String()
	if v.Error != "" {
		detail += "; " + v.Error
	}
	if v.Unchecked != "" {
		detail += "; " + v.Unchecked
	}
	if v.Counterexample != nil {
		detail += "\n" + cexText(v.Counterexample)
	}
	if v.Trace != "" {
		detail += "\n" + v.Trace
	}
	if v.Lasso != nil && v.Lasso.Prefix != nil {
		detail += "\n" + lassoText(v.Name, EventualVerdict{Name: v.Name, Lasso: v.Lasso})
	}
	return validate.DocCheck{Name: v.Name, Kind: kind, Verdict: verdict, Detail: detail, Bound: &validate.DocBound{Kind: v.Bound.Kind, States: v.Bound.States, Depth: v.Bound.Depth, Traces: v.Bound.Traces, Steps: v.Bound.Steps, Stopped: v.Bound.Stopped, Proves: v.Bound.Proves()}}
}

func possibleGoalDoc(g PossibleGoal) *validate.DocPossibleGoal {
	if g.Kind == "" {
		return nil // no resolved native goal; do not infer one from display text
	}
	d := &validate.DocPossibleGoal{Kind: g.Kind, Predicate: g.Predicate}
	for _, s := range g.Steps {
		d.Steps = append(d.Steps, validate.DocPossibleAction{Action: s.Action, Args: possibleArgsDoc(s.Args)})
	}
	return d
}

func possibleArgsDoc(args []PossibleArgument) []validate.DocPossibleArgument {
	out := make([]validate.DocPossibleArgument, len(args))
	for i, a := range args {
		out[i] = validate.DocPossibleArgument{Name: a.Name, Value: a.Value}
	}
	return out
}

func possiblePathDoc(path []PossibleWitnessStep) []validate.DocPossibleStep {
	out := make([]validate.DocPossibleStep, len(path))
	for i, s := range path {
		out[i] = validate.DocPossibleStep{Kind: s.Kind, Action: s.Action, Args: possibleArgsDoc(s.Args)}
	}
	return out
}
