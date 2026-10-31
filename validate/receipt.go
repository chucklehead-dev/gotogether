package validate

import (
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"maps"
)

// Receipt describes what this report checked. It describes the whole judged
// history, never a model-navigation cursor or other executions.
func (r *Report) Receipt() receipt.Summary {
	v := runVerdictOf(r.Actors)
	s := receipt.Summary{Composition: r.Composition, Status: receiptStatus(v.Verdict), Reason: v.Reason, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Actors: []receipt.Actor{}}
	if len(r.Actors) == 0 {
		s.Status = receipt.NotChecked
	}
	if r.Unsettled != "" && s.Status == receipt.Pass {
		s.Status, s.Reason = receipt.Inconclusive, r.Unsettled
	}
	hasError, hasFailure := false, false
	for _, a := range r.Actors {
		entry := a.Receipt()
		if len(s.Actors) == 0 {
			s.Scope = entry.Scope
		} else if s.Scope != entry.Scope {
			s.Scope = "mixed_inputs"
		}
		s.Actors = append(s.Actors, entry)
		hasError = hasError || entry.Status == receipt.Error
		hasFailure = hasFailure || entry.Status == receipt.Fail
	}
	if hasError && !hasFailure {
		s.Status = receipt.Error
		for _, a := range s.Actors {
			if a.Status == receipt.Error {
				s.Reason = a.Reason
				break
			}
		}
	}
	if r.Composition != nil {
		status, reason := r.Composition.Problem()
		rank := map[receipt.Status]int{receipt.Pass: 0, receipt.NotChecked: 1, receipt.Inconclusive: 2, receipt.Error: 3, receipt.Fail: 4}
		if status == receipt.NotChecked {
			status = receipt.Inconclusive
		}
		if rank[status] > rank[s.Status] {
			s.Status, s.Reason = status, "composition: "+reason
		}
	}

	return s
}

// Receipt describes this actor's checks and any history-search assumptions.
func (r *Result) Receipt() receipt.Actor {
	v := runVerdictOf([]*Result{r})
	a := receipt.Actor{JudgedBy: r.JudgedBy(), Partition: r.Partition, Scope: "collected_history", Actor: r.Actor, Status: receiptStatus(v.Verdict), Reason: v.Reason, Ordering: "reconstructed", Search: r.historySearch, Checks: []receipt.Check{{Kind: "conformance", Name: "history", Status: receiptStatus(r.Conforms)}}}
	a.Choices = r.Choices.Clone()
	if r.inputScope != "" {
		a.Scope = r.inputScope
		a.Ordering = "model_trace"
	}
	if a.Actor == "" {
		a.Actor = "default"
	}
	if a.Search != nil {
		q := *a.Search
		a.Search = &q
		a.Ordering = "searched"
	}
	for idx, c := range r.Calls {
		if c.Step != nil && c.Step.Ambiguous {
			a.AssumedEffects = append(a.AssumedEffects, receipt.Effect{CandidateRecord: RecordID(c.Step), Position: idx + 1, Action: c.Step.Action, Args: maps.Clone(c.Step.Args)})
		}
	}
	for _, i := range r.Invariants {
		kind := "invariant"
		if i.Eventually {
			kind = "eventually"
		}
		if i.Within {
			kind = "within"
		}
		status := receiptStatus(i.Verdict)
		if i.Verdict == Fail && i.Failure != nil && i.Failure.Kind == "error" {
			status = receipt.Error
		}
		why := i.cause
		if i.Verdict == NotEvaluated && i.Failure == nil && why == "" && r.noVerdict == "" && (a.Search == nil || !a.Search.BudgetExhausted) {
			status = receipt.NotChecked
		}
		if i.Verdict == NotEvaluated && why == "" {
			why = r.noVerdict
		}
		a.Checks = append(a.Checks, receipt.Check{Kind: kind, Name: i.Name, Status: status, Reason: why})
	}
	// An unsettled history can omit eventual properties from Invariants.
	for _, ab := range r.Absent {
		if ab.Diagnostic != nil {
			a.Diagnostics = append(a.Diagnostics, *ab.Diagnostic)
		}
		if ab.Kind != "eventually" {
			continue
		}
		found := false
		for _, c := range a.Checks {
			if c.Kind == "eventually" && (c.Name == ab.Name || c.Name == EventuallyName(ab.Name)) {
				found = true
				break
			}
		}
		if !found {
			a.Checks = append(a.Checks, receipt.Check{Kind: "eventually", Name: ab.Name, Status: receipt.NotChecked, Reason: ab.Why})
		}
	}
	// An explicit evaluator error is not a model counterexample.
	if r.Failure != nil && r.Failure.Kind == "error" {
		a.Status = receipt.Error
		a.Checks[0].Status = receipt.Error
	}
	hasError, hasFailure := false, false
	for _, c := range a.Checks {
		hasError = hasError || c.Status == receipt.Error
		hasFailure = hasFailure || c.Status == receipt.Fail
	}
	if hasError && !hasFailure {
		a.Status = receipt.Error
		if r.Failure == nil || r.Failure.Kind != "error" {
			a.Reason = "evaluation error while checking model properties"
		}
	}
	return a
}

func receiptStatus(v Verdict) receipt.Status {
	switch v {
	case Pass:
		return receipt.Pass
	case Fail:
		return receipt.Fail
	default:
		return receipt.Inconclusive
	}
}

// mergeReceipts preserves every report when Document.AddReport is used more
// than once (including mixed recorded histories and model-level ITF inputs).
func mergeReceipts(a, b receipt.Summary) receipt.Summary {
	rank := map[receipt.Status]int{receipt.Pass: 1, receipt.NotChecked: 2, receipt.Inconclusive: 4, receipt.Error: 5, receipt.Fail: 6}
	if rank[b.Status] > rank[a.Status] {
		a.Status, a.Reason = b.Status, b.Reason
	}
	if a.Status == receipt.NotChecked && (len(a.Actors) > 0 || len(b.Actors) > 0) {
		a.Status = receipt.Inconclusive
	}
	if a.Scope != b.Scope {
		a.Scope = "mixed_inputs"
	}
	a.Actors = append(append([]receipt.Actor{}, a.Actors...), b.Actors...)
	return a
}
