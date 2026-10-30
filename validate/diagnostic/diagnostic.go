// Package diagnostic carries optional model-authored explanations between
// validation, JSON and navigation. It makes no correctness judgments.
package diagnostic

import (
	"fmt"
	"strings"
)

// Item is one model-authored outstanding intent. Intent identifies business
// evidence; it is not a recorder/sequence reference or an inferred root cause.
type Item struct {
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Intent   string `json:"intent"`
	Expected string `json:"expected"`
}

// Result is an optional explanation at one precisely identified model state.
// Cause, when present, is copied from the validator's existing absence judgment.
// Unavailable explanations never alter the associated check's verdict.
type Result struct {
	Actor      string `json:"actor,omitempty"`
	Property   string `json:"property"`
	Projection string `json:"projection"`
	Scope      string `json:"scope"`
	Cause      string `json:"cause,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Items      []Item `json:"items,omitempty"`
	Total      int    `json:"total"`
	Omitted    int    `json:"omitted,omitempty"`
	// Evidence describes native record resolution. The prototype only supplies
	// business selectors; it deliberately does not fabricate clickable links.
	Evidence string `json:"evidence"`
}

// String renders these same structured facts for people, not for machine parsing.
func (r Result) String() string {
	var b strings.Builder
	if r.Actor != "" {
		fmt.Fprintf(&b, "actor %s: ", r.Actor)
	}
	scope := r.Scope
	switch scope {
	case "settled_checked_history":
		scope = "at recorded settle"
	case "checked_history_no_completion_verdict":
		scope = "completion not established"
	case "navigated_state_no_verdict":
		scope = "selected model state; no verdict"
	}
	fmt.Fprintf(&b, "%s → %s [%s; %s", r.Property, r.Projection, scope, r.Status)
	if r.Cause != "" {
		fmt.Fprintf(&b, "; %s", r.Cause)
	}
	b.WriteString("]")
	if r.Reason != "" {
		fmt.Fprintf(&b, ": %s", r.Reason)
	}
	for _, item := range r.Items {
		fmt.Fprintf(&b, "\n  %q %q · intent %q: %q", item.Kind, item.Subject, item.Intent, item.Expected)
	}
	if r.Evidence == "unresolved" {
		b.WriteString("\n  evidence: business intent selectors only; native records unresolved")
	}
	if r.Omitted > 0 {
		fmt.Fprintf(&b, "\n  %d more items omitted", r.Omitted)
	}
	return b.String()
}
