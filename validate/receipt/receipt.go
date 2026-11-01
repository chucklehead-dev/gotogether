// Package receipt describes what a history judgment checked, independently
// of the evaluator and presentation. It makes no claim about other executions.
package receipt

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

// Status distinguishes a conclusion, an inconclusive attempt, and no check.
type Status string

const (
	// Pass is a satisfied check within its stated scope.
	Pass Status = "pass"
	// Fail is a violated check within its stated scope.
	Fail Status = "fail"
	// Inconclusive is an attempted check with no conclusion.
	Inconclusive Status = "inconclusive"
	// NotChecked is a check that was not performed.
	NotChecked Status = "not_checked"
	// Error is a tool error, rather than a model counterexample.
	Error Status = "error"
)

// Check is one named obligation's result. Reason explains a missing conclusion.
type Check struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Search describes an existential history search, not exploration of all
// executions. BudgetExhausted concerns this actor's search, not the whole run.
type Search struct {
	Mode             string `json:"mode"` // "linearize", "resolve_pending", "linearize_resolve_pending"
	Goal             string `json:"goal"` // "one_conforming_history"
	BudgetExhausted  bool   `json:"budgetExhausted"`
	OrderBudget      int    `json:"orderBudget,omitempty"` // per phase, not a total order count
	ResolutionBudget int    `json:"resolutionBudget,omitempty"`
}

// Effect is an assumed pending-call effect in the checked candidate. Its
// candidate record identity is not evidence of an observed completion.
type Effect struct {
	CandidateRecord string         `json:"candidateRecord"`
	Position        int            `json:"position"` // 1-based position in the model candidate
	Action          string         `json:"action"`
	Args            map[string]any `json:"args,omitempty"`
}

// Actor describes a history checked against one model instance or partition.
type Actor struct {
	// Choices is a model-choice search, not a history-order search.
	Choices *ChoiceSearch `json:"choices,omitempty"`
	// Diagnostics retain model-authored detail from the judged history.
	Diagnostics []diagnostic.Result `json:"diagnostics,omitempty"`
	JudgedBy    string              `json:"judgedBy"`
	// SourceActor is the input actor identity, distinct from a partition's display name.
	SourceActor string `json:"sourceActor,omitempty"`
	// PartitionInfo identifies the recorded key/value and its receipt-local instance.
	PartitionInfo *PartitionInfo `json:"partitionInfo,omitempty"`
	Partition     string         `json:"partition,omitempty"`
	Scope         string         `json:"scope"` // "collected_history" or "model_trace"
	Actor         string         `json:"actor"`
	Status        Status         `json:"status"`
	Reason        string         `json:"reason,omitempty"`
	Ordering      string         `json:"ordering"` // "reconstructed", "searched", "model_trace"
	Search        *Search        `json:"search,omitempty"`
	// AssumedEffects identify pending calls placed in the checked candidate.
	// They are model assumptions, not evidence that the calls took effect.
	AssumedEffects []Effect `json:"assumedEffects,omitempty"`
	Checks         []Check  `json:"checks"`
}

// Summary describes the whole judged history, independently of a navigation
// cursor. OtherExecutions is always NotChecked for history validation.
type Summary struct {
	// Composition is the registered assessment of this same collected history.
	Composition     *Composition `json:"composition,omitempty"`
	Status          Status       `json:"status"`
	Reason          string       `json:"reason,omitempty"`
	Scope           string       `json:"scope"` // "collected_history", "model_trace", or "mixed_inputs"
	OtherExecutions Status       `json:"otherExecutions"`
	Actors          []Actor      `json:"actors"`
	// PartitionInstances stores whole-history constants once per partitioned instance.
	PartitionInstances []PartitionInstance `json:"partitionInstances,omitempty"`
}

// History is one model's judgment on one history. It cannot contain a
// composition recursively: component model checks and the aggregate have
// different scopes, so their data shapes express that distinction.
type History struct {
	Status          Status  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Scope           string  `json:"scope"` // "collected_history", "model_trace", or "mixed_inputs"
	OtherExecutions Status  `json:"otherExecutions"`
	Actors          []Actor `json:"actors"`
	// PartitionInstances uses IDs local to this history, never another component.
	PartitionInstances []PartitionInstance `json:"partitionInstances,omitempty"`
}

// History projects an uncomposed model judgment. A combined assessment must
// use its component Judgments rather than relabeling an aggregate as one model.
func (s Summary) History() History {
	if s.Composition != nil {
		panic("receipt: cannot relabel a composed assessment as one model judgment; use its component Judgments")
	}
	return History{Status: s.Status, Reason: s.Reason, Scope: s.Scope, OtherExecutions: s.OtherExecutions, Actors: s.Actors, PartitionInstances: s.PartitionInstances}
}

// String renders the same scope and assumptions exposed as JSON.
func (s Summary) String() string {
	var b strings.Builder
	scope := strings.ReplaceAll(s.Scope, "_", " ")
	fmt.Fprintf(&b, "what was checked: %s · %s\n", s.Status, scope)
	if s.Reason != "" {
		fmt.Fprintf(&b, "  reason: %s\n", s.Reason)
	}
	counts := s.Counts()
	if counts.Partitions > 0 || counts.Judgments != counts.Actors {
		actorLabel := "source actor"
		if counts.UnknownSourceJudgments > 0 {
			actorLabel = "known source actor"
		}
		fmt.Fprintf(&b, "  scope: %s, %s, %s total\n", scopeQuantity(counts.Actors, actorLabel), scopeQuantity(counts.Partitions, "partition judgment"), scopeQuantity(counts.Judgments, "judgment"))
		if counts.UnknownSourceJudgments > 0 {
			fmt.Fprintf(&b, "  source identities unavailable for %s\n", scopeQuantity(counts.UnknownSourceJudgments, "judgment"))
		}
	}
	for _, instance := range s.PartitionInstances {
		for _, line := range strings.Split(instance.String(), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	visible := []Actor{}
	folded := 0
	for _, a := range s.Actors {
		if a.Partition != "" && a.Status == Pass && a.Search == nil && a.Choices == nil && len(a.AssumedEffects) == 0 {
			folded++
			continue
		}
		visible = append(visible, a)
	}
	if folded > 0 {
		fmt.Fprintf(&b, "  %d passing partitions (details in the structured receipt)\n", folded)
	}
	slices.SortStableFunc(visible, func(a, b Actor) int {
		if a.Status == Pass && b.Status != Pass {
			return 1
		}
		if a.Status != Pass && b.Status == Pass {
			return -1
		}
		return 0
	})
	for idx, a := range visible {
		if idx == 3 {
			fmt.Fprintf(&b, "  %d more actors or partitions in the structured receipt\n", len(visible)-idx)
			break
		}
		fmt.Fprintf(&b, "  actor %s: %s; %s order\n", a.Actor, a.Status, a.Ordering)
		if a.Choices != nil {
			fmt.Fprintf(&b, "    %s\n", a.Choices.String())
		}
		if q := a.Search; q != nil {
			fmt.Fprintf(&b, "    search: %s; seeks one conforming history", q.Mode)
			if q.BudgetExhausted {
				b.WriteString("; budget exhausted")
			}
			if q.OrderBudget > 0 {
				fmt.Fprintf(&b, "; order budget %d per phase", q.OrderBudget)
			}
			if q.ResolutionBudget > 0 {
				fmt.Fprintf(&b, "; resolution budget %d", q.ResolutionBudget)
			}
			b.WriteByte('\n')
		}
		kinds := map[string]map[Status]int{}
		for _, c := range a.Checks {
			if kinds[c.Kind] == nil {
				kinds[c.Kind] = map[Status]int{}
			}
			kinds[c.Kind][c.Status]++
		}
		var groups []string
		for _, kind := range slices.Sorted(maps.Keys(kinds)) {
			var counts []string
			for _, status := range []Status{Fail, Error, Inconclusive, NotChecked, Pass} {
				if n := kinds[kind][status]; n > 0 {
					counts = append(counts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(string(status), "_", " ")))
				}
			}
			groups = append(groups, kind+" ("+strings.Join(counts, ", ")+")")
		}
		if len(groups) > 0 {
			fmt.Fprintf(&b, "    checks: %s\n", strings.Join(groups, "; "))
		}
		for idx, effect := range a.AssumedEffects {
			if idx == 3 {
				fmt.Fprintf(&b, "    %d more assumed effects in the structured receipt\n", len(a.AssumedEffects)-idx)
				break
			}
			fmt.Fprintf(&b, "    assumed effect: %s at candidate %s (not observed)\n", effect.Action, effect.CandidateRecord)
		}
	}
	if s.Composition != nil {
		b.WriteString(s.Composition.String())
	} else {
		b.WriteString("  limit: other executions were not checked\n")
	}
	return b.String()
}
