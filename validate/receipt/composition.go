package receipt

import (
	"fmt"
	"strings"
)

// Component is a model or peer-agreement check on the composition's history.
// Applicable is false only when this history contains no work for the check.
type Component struct {
	Target     string   `json:"target"`
	Binding    string   `json:"binding,omitempty"`
	Status     Status   `json:"status"`
	Reason     string   `json:"reason,omitempty"`
	Applicable bool     `json:"applicable"`
	Judgment   *History `json:"judgment,omitempty"`
}

// Record identifies a native record in a protocol counterexample's ordering.
type Record struct {
	ID     string `json:"id"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
}

// Protocol describes a universal check over causal orderings of one captured
// history. Exhaustive does not mean all executions or all workloads were checked.
type Protocol struct {
	Component
	Configured     bool     `json:"configured"`
	Quantifier     string   `json:"quantifier"`
	Checked        int      `json:"checked"`
	Limit          int      `json:"limit"`
	Exhaustive     bool     `json:"exhaustive"`
	Counterexample []Record `json:"counterexample,omitempty"`
}

// Composition relates local models, peer agreement and an optional protocol
// model, all checked against the native history identified by History's SHA256.
// There is no aggregate green badge hiding unperformed component checks.
type Composition struct {
	History  string      `json:"history"`
	Run      string      `json:"run,omitempty"`
	Error    string      `json:"error,omitempty"`
	Local    []Component `json:"local"`
	Peers    []Component `json:"peers"`
	Protocol Protocol    `json:"protocol"`
}

// Problem is the strongest non-pass among applicable registered checks. An
// unused service or edge does not fail a run, but remains explicitly unjudged.
// At least one check must apply. The unconfigured protocol is never claimed.
func (c Composition) Problem() (Status, string) {
	if c.Error != "" {
		return Error, c.Error
	}
	status, reason := Pass, ""
	rank := map[Status]int{Pass: 0, NotChecked: 1, Inconclusive: 2, Error: 3, Fail: 4}
	attempted := 0
	consider := func(v Component) {
		if !v.Applicable {
			if v.Status != NotChecked {
				status, reason = Error, v.Target+": inactive check must be not_checked"
			}
			return
		}
		attempted++
		if _, ok := rank[v.Status]; !ok {
			v.Status = Error
			v.Reason = "invalid or missing check status"
		}
		if rank[v.Status] > rank[status] {
			status = v.Status
			reason = v.Target + ": " + v.Reason
			if v.Reason == "" {
				reason = v.Target + ": " + string(v.Status)
			}
		}
	}
	for _, v := range c.Local {
		consider(v)
	}
	for _, v := range c.Peers {
		consider(v)
	}
	if c.Protocol.Configured {
		if c.Protocol.Status == Pass && (c.Protocol.Checked == 0 || !c.Protocol.Exhaustive) {
			return Error, "protocol pass requires at least one checked ordering and an exhaustive search"
		}
		consider(c.Protocol.Component)
	}
	if status == Error {
		return status, reason
	}
	if attempted == 0 {
		return NotChecked, "composition: no recorded work was checked"
	}
	return status, reason
}

// String renders component scopes separately, retaining every unperformed check.
func (c Composition) String() string {
	var b strings.Builder
	b.WriteString("composition checks · this collected history\n")
	if c.Error != "" {
		fmt.Fprintf(&b, "  assessment error: %s\n", c.Error)
	}
	row := func(kind string, v Component) {
		label := kind
		if v.Target != "" && v.Target != kind {
			label += " " + v.Target
		}
		fmt.Fprintf(&b, "  %s: %s", label, strings.ReplaceAll(string(v.Status), "_", " "))
		if v.Reason != "" {
			fmt.Fprintf(&b, " · %s", v.Reason)
		}
		b.WriteByte('\n')
	}
	for _, v := range c.Local {
		row("local model", v)
	}
	for _, v := range c.Peers {
		row("peer agreement", v)
	}
	row("system protocol", c.Protocol.Component)
	if c.Protocol.Configured {
		fmt.Fprintf(&b, "    all causal orderings: %d checked; limit %d; exhaustive=%v\n", c.Protocol.Checked, c.Protocol.Limit, c.Protocol.Exhaustive)
	}
	if j := c.Protocol.Judgment; j != nil {
		for _, a := range j.Actors {
			for _, check := range a.Checks {
				if check.Kind == "eventually" && check.Status != Pass {
					fmt.Fprintf(&b, "    completion %s: %s\n", check.Name, strings.ReplaceAll(string(check.Status), "_", " "))
				}
			}
		}
	}
	details := func(v Component) {
		if v.Status == Pass || v.Judgment == nil {
			return
		}
		for _, a := range v.Judgment.Actors {
			for _, d := range a.Diagnostics {
				fmt.Fprintf(&b, "    %s\n", d.String())
			}
		}
	}
	for _, v := range c.Local {
		details(v)
	}
	details(c.Protocol.Component)
	b.WriteString("  limit: other executions were not checked\n")
	return b.String()
}
