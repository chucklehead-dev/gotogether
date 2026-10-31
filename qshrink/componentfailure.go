package qshrink

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// ComponentFailure identifies a failed named obligation in a registered local
// or protocol model. Actor names, native records and counterexample order are
// deliberately absent: reruns may rename or reorder them.
type ComponentFailure struct {
	Role    string `json:"role"`
	Target  string `json:"target"`
	Binding string `json:"binding"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

// ComponentFailures returns an independent, sorted snapshot of the component
// obligations preserved by keep. Criteria can declare this metadata through an
// optional ComponentFailures method; a custom criterion without it has no
// inferred identity.
func ComponentFailures(keep Keep) []ComponentFailure {
	if s, ok := keep.(interface{ ComponentFailures() []ComponentFailure }); ok {
		out := slices.Clone(s.ComponentFailures())
		slices.SortFunc(out, compareComponent)
		return out
	}
	return nil
}

func (s sameFailure) ComponentFailures() []ComponentFailure { return slices.Clone(s.components) }

func compareComponent(a, b ComponentFailure) int {
	for _, pair := range [][2]string{{a.Role, b.Role}, {a.Target, b.Target}, {a.Binding, b.Binding}, {a.Kind, b.Kind}, {a.Name, b.Name}} {
		if n := cmp.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	return 0
}
func (f ComponentFailure) description() string {
	return fmt.Sprintf("%s component %q binding %q %s %q", f.Role, f.Target, f.Binding, f.Kind, f.Name)
}

// componentObligations admits only receipt evidence strong enough to identify
// named failures. In particular, an aggregate Fail is not proof its nested
// judgments ran, and an error beside a violation must not be silently discarded.
func componentObligations(c receipt.Component, role string) ([]ComponentFailure, string) {
	if !c.Applicable || c.Status == receipt.NotChecked || c.Status == receipt.Inconclusive || c.Status == receipt.Error {
		return nil, "component was not conclusively checked"
	}
	if c.Status != receipt.Pass && c.Status != receipt.Fail {
		return nil, "component has an invalid check status"
	}
	if c.Status == receipt.Pass {
		if role != "peer" && !passedComponentJudgment(c.Judgment) {
			return nil, "component pass has no consistent model judgment"
		}
		return nil, ""
	}
	if c.Target == "" || c.Binding == "" {
		return nil, "component failure has no stable target and binding"
	}
	if c.Judgment == nil || c.Judgment.Status != receipt.Fail {
		return nil, "component failure has no consistent model judgment"
	}
	var out []ComponentFailure
	for _, a := range c.Judgment.Actors {
		if a.Status != receipt.Pass && a.Status != receipt.Fail {
			return nil, "component actor was not conclusively checked"
		}
		if a.Search != nil && a.Search.BudgetExhausted {
			return nil, "component history search exhausted its budget"
		}
		if !componentChoiceJudged(a) {
			return nil, "component model-choice search lacks complete failure proof or a confirmed matching witness"
		}
		conformed := false
		failed := false
		for _, check := range a.Checks {
			if check.Status != receipt.Pass && check.Status != receipt.Fail {
				return nil, "component obligation was not conclusively checked"
			}
			if check.Kind == "conformance" {
				if check.Status != receipt.Pass {
					return nil, "component conformance failure has no supported named-obligation identity"
				}
				conformed = true
				continue
			}
			if check.Status != receipt.Fail {
				continue
			}
			if check.Name == "" || (check.Kind != "invariant" && check.Kind != "within" && check.Kind != "eventually") {
				return nil, "component failed check has no supported named-obligation identity"
			}
			failed = true
			out = append(out, ComponentFailure{Role: role, Target: c.Target, Binding: c.Binding, Kind: check.Kind, Name: check.Name})
		}
		if !conformed || (failed && a.Status != receipt.Fail) || (!failed && a.Status != receipt.Pass) {
			return nil, "component actor status contradicts its checks"
		}
	}
	if len(out) == 0 {
		return nil, "component failure has no supported named obligation"
	}
	slices.SortFunc(out, compareComponent)
	return out, ""
}

func compositionFailures(rep *validate.Report) ([]ComponentFailure, string) {
	if rep == nil || rep.Composition == nil {
		return nil, ""
	}
	c := rep.Composition
	if c.Error != "" {
		return nil, "composition assessment failed"
	}
	var out []ComponentFailure
	seen := map[ComponentFailure]bool{}
	add := func(v receipt.Component, role string) string {
		scope := ComponentFailure{Role: role, Target: v.Target, Binding: v.Binding}
		if seen[scope] {
			return fmt.Sprintf("%s component %q binding %q is registered more than once", role, v.Target, v.Binding)
		}
		seen[scope] = true
		if !v.Applicable {
			if v.Status != receipt.NotChecked {
				return fmt.Sprintf("%s component %q binding %q: inactive component has a judgment", role, v.Target, v.Binding)
			}
			return ""
		}
		fs, why := componentObligations(v, role)
		out = append(out, fs...)
		if why != "" {
			return fmt.Sprintf("%s component %q binding %q: %s", role, v.Target, v.Binding, why)
		}
		return ""
	}
	for _, v := range c.Local {
		if why := add(v, "local"); why != "" {
			return nil, why
		}
	}
	for _, v := range c.Peers {
		if v.Applicable && v.Status == receipt.Fail {
			return nil, fmt.Sprintf("peer component %q binding %q: peer-agreement failure has no supported named-obligation identity", v.Target, v.Binding)
		}
		if why := add(v, "peer"); why != "" {
			return nil, why
		}
	}
	p := c.Protocol
	if p.Configured {
		if p.Status == receipt.Pass && (p.Checked < 1 || !p.Exhaustive) {
			return nil, fmt.Sprintf("protocol component %q binding %q: pass lacks exhaustive checked orderings", p.Target, p.Binding)
		}
		if p.Status == receipt.Fail && p.Checked < 1 {
			return nil, fmt.Sprintf("protocol component %q binding %q: failure has no checked ordering", p.Target, p.Binding)
		}
		if why := add(p.Component, "protocol"); why != "" {
			return nil, why
		}
	}
	slices.SortFunc(out, compareComponent)
	return out, ""
}

func componentShows(rep *validate.Report, want []ComponentFailure) Shown {
	if len(want) == 0 {
		return Reproduced
	}
	if rep == nil || rep.Composition == nil || rep.Composition.Error != "" {
		return Undecided
	}
	comp := rep.Composition
	// A tool failure anywhere in the registered assessment is incomplete evidence,
	// even if Composition.Problem's ranking presents a simultaneous violation.
	for _, v := range append(slices.Clone(comp.Local), comp.Peers...) {
		if v.Applicable && v.Status == receipt.Error {
			return Undecided
		}
	}
	if comp.Protocol.Configured && comp.Protocol.Applicable && comp.Protocol.Status == receipt.Error {
		return Undecided
	}
	byScope := map[ComponentFailure][]ComponentFailure{}
	judgments := map[ComponentFailure]*receipt.History{}
	for _, w := range want {
		scope := w
		scope.Kind = ""
		scope.Name = ""
		if _, done := byScope[scope]; done {
			if !namedCheckPresent(judgments[scope], w) {
				return Undecided
			}
			continue
		}
		var matches []receipt.Component
		if w.Role == "local" {
			for _, v := range comp.Local {
				if v.Target == w.Target && v.Binding == w.Binding {
					matches = append(matches, v)
				}
			}
		} else if w.Role == "protocol" {
			p := comp.Protocol
			if !p.Configured {
				return Undecided
			}
			if p.Target == w.Target && p.Binding == w.Binding {
				if p.Status == receipt.Pass && (p.Checked < 1 || !p.Exhaustive) {
					return Undecided
				}
				if p.Status == receipt.Fail && p.Checked < 1 {
					return Undecided
				}
				matches = append(matches, p.Component)
			}
		}
		if len(matches) != 1 {
			return Undecided
		}
		var got []ComponentFailure
		for _, v := range matches {
			if !v.Applicable || v.Judgment == nil {
				return Undecided
			}
			if v.Status == receipt.Pass && !passedComponentJudgment(v.Judgment) {
				return Undecided
			}
			fs, why := componentObligations(v, w.Role)
			if why != "" {
				return Undecided
			}
			// A missing wanted check cannot be relabeled as a passed one.
			if !namedCheckPresent(v.Judgment, w) {
				return Undecided
			}
			judgments[scope] = v.Judgment

			got = append(got, fs...)
		}
		byScope[scope] = got
	}
	for _, w := range want {
		scope := w
		scope.Kind = ""
		scope.Name = ""
		got := byScope[scope]
		at := slices.Index(got, w)
		if at < 0 {
			return NotReproduced
		}
		byScope[scope] = slices.Delete(got, at, at+1)
	}
	return Reproduced
}

func failureDescription(primary []string, components []ComponentFailure) string {
	parts := slices.Clone(primary)
	for _, f := range components {
		parts = append(parts, f.description())
	}
	return strings.Join(parts, "; ")
}

func namedCheckPresent(j *receipt.History, w ComponentFailure) bool {
	if j == nil {
		return false
	}
	found := false
	for _, a := range j.Actors {
		for _, check := range a.Checks {
			if check.Kind == w.Kind && check.Name == w.Name {
				if check.Status != receipt.Pass && check.Status != receipt.Fail {
					return false
				}
				found = true
			}
		}
	}
	return found
}
func passedComponentJudgment(j *receipt.History) bool {
	if j == nil || j.Status != receipt.Pass || len(j.Actors) == 0 {
		return false
	}
	for _, a := range j.Actors {
		if a.Status != receipt.Pass || (a.Search != nil && a.Search.BudgetExhausted) || !componentChoiceJudged(a) {
			return false
		}
		conformed := false
		for _, check := range a.Checks {
			if check.Status != receipt.Pass {
				return false
			}
			if check.Kind == "conformance" {
				conformed = true
			}
		}
		if !conformed {
			return false
		}
	}
	return true
}

// A confirmed path establishes a positive match, not the absence of a path
// satisfying a named property. Model-choice property failures additionally
// require exhaustive enumeration, as the validator's proof contract states.
func componentChoiceJudged(a receipt.Actor) bool {
	if a.Choices == nil {
		return true
	}
	if a.Choices.Status != "matched" || a.Choices.Confirmation != "path_confirmed" {
		return false
	}
	return a.Status != receipt.Fail || a.Choices.Exhaustive
}
