package validate

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

const diagnosticLimit = 32

// diagnosticValue reads only the supplied exact state. The explanation cannot
// change a verdict, execute an action or supply missing native evidence.
func (c *Checker) diagnosticValue(e *evaluator, consts map[string]qeval.Value, state qeval.State, property, scope string) diagnostic.Result {
	out := diagnostic.Result{Property: property, Projection: c.Model.Diagnostics[property], Scope: scope, Status: "unavailable", Evidence: "unresolved"}
	if e.err != nil {
		out.Reason = "model evaluator unavailable"
		return out
	}
	name := e.em.Diagnostics[property]
	if name == "" {
		out.Reason = "compiled model has no diagnostic bridge; recompile it"
		return out
	}
	value, err := e.m.Eval(consts, state, name)
	if err != nil {
		out.Reason = fmt.Sprintf("diagnostic evaluation: %.240s", err)
		return out
	}
	set, ok := value.(*qeval.Set)
	if !ok {
		out.Reason = "diagnostic value is not a record set"
		return out
	}
	out.Total = len(set.Elems)
	for _, v := range set.Elems[:min(len(set.Elems), diagnosticLimit)] {
		rec, ok := v.(*qeval.Rec)
		if !ok {
			out.Reason = "diagnostic item is not a record"
			out.Items = nil
			return out
		}
		fields := map[string]string{}
		for i, name := range rec.Names {
			text, ok := rec.Vals[i].(qeval.Str)
			if !ok {
				out.Reason = "diagnostic field is not a string"
				out.Items = nil
				return out
			}
			if len(text) > 512 {
				out.Reason = "diagnostic field exceeds the prototype 512-byte limit"
				out.Items = nil
				return out
			}
			fields[name] = string(text)
		}
		out.Items = append(out.Items, diagnostic.Item{Kind: fields["kind"], Subject: fields["subject"], Intent: fields["intent"], Expected: fields["expected"]})
	}
	out.Omitted = out.Total - len(out.Items)
	out.Status = "available"
	if out.Omitted > 0 {
		out.Status = "truncated"
	}
	return out
}

// enrichDiagnostics runs after final absence classification. It replaces data
// on copies so cached reports cannot retain another judgment's explanations.
// Prototype scope is a conforming, settled, unsearched eventual failure with
// the precise state recovered from Quint's failing replay; other cases are
// explicitly unavailable. No independent replay guesses a failure state.
func (c *Checker) enrichDiagnostics(rep *Report, steps []qtrace.Step, searched bool) {
	if rep == nil || len(c.Model.Diagnostics) == 0 {
		return
	}
	traces, _ := c.Binding.Reconstruct(steps)
	byActor := map[string]*qtrace.Trace{}
	for _, tr := range traces {
		byActor[tr.Actor] = tr
	}
	for _, r := range rep.Actors {
		r.Absent = slices.Clone(r.Absent)
		for i := range r.Absent {
			ab := &r.Absent[i]
			ab.Diagnostic = nil
			property := strings.TrimPrefix(ab.Name, "eventually ")
			projection := c.Model.Diagnostics[property]
			if ab.Kind != "eventually" || projection == "" {
				continue
			}
			detail := diagnostic.Result{Actor: r.Actor, Property: property, Projection: projection, Scope: "checked_history_no_completion_verdict", Cause: string(ab.Cause), Status: "unavailable", Evidence: "unresolved"}
			ab.Diagnostic = &detail
			if ab.Cause == Defect && r.Settled {
				detail.Scope = "settled_checked_history"
			}
			switch {
			case ab.Cause != Defect:
				detail.Reason = "completion was not established; see the absence cause"
				continue
			case searched || c.mode.history != nil || len(c.Binding.Hidden) > 0 || c.Binding.Partition != "":
				detail.Reason = "prototype does not explain searched, hidden or partitioned histories"
				continue
			case r.Conforms != Pass || !r.Settled:
				detail.Reason = "prototype requires a conforming settled history"
				continue
			}
			tr := byActor[r.Actor]
			if tr == nil || c.resumes(tr) {
				detail.Reason = "prototype requires a complete known initial state"
				continue
			}
			var f *Failure
			for _, inv := range r.Invariants {
				if inv.Name == ab.Name && inv.Verdict == Fail {
					f = inv.Failure
					break
				}
			}
			if f == nil || len(f.State) == 0 {
				detail.Reason = "exact judged model state is unavailable"
				continue
			}
			e := evaluatorFor(c)
			if e.err != nil {
				detail.Reason = "model evaluator unavailable"
				continue
			}
			plan, err := c.Binding.Plan(c.Model, tr)
			if err != nil {
				detail.Reason = "cannot recover judged constants"
				continue
			}
			consts, err := e.constants(plan)
			if err != nil {
				detail.Reason = "cannot recover judged constants"
				continue
			}
			state, err := qeval.StateOf(e.m, itf.State{Vars: f.State})
			if err != nil {
				detail.Reason = "judged state is incompatible with the evaluator"
				continue
			}
			detail = c.diagnosticValue(e, consts, state, property, "settled_checked_history")
			detail.Actor, detail.Cause = r.Actor, string(ab.Cause)
			if detail.Status == "available" && detail.Total == 0 {
				detail.Status = "unavailable"
				detail.Reason = "projection returned no items for a failed property"
			}
		}
	}
}

func (n *Navigator) diagnosticValues(state qeval.State) []diagnostic.Result {
	var out []diagnostic.Result
	for _, property := range slices.Sorted(maps.Keys(n.c.Model.Diagnostics)) {
		if !slices.Contains(n.c.Binding.Invariants, property) && !slices.Contains(n.c.Binding.Eventually, property) {
			continue
		}
		detail := n.c.diagnosticValue(n.e, n.consts, state, property, "navigated_state_no_verdict")
		detail.Actor = n.actor
		out = append(out, detail)
	}
	return out
}
