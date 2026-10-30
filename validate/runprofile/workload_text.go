package runprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// PhasesString describes this execution's observed workload prerequisites.
// Unmet prerequisites lead the detail; model judgments remain separate.
func (e Execution) PhasesString() string {
	if len(e.Phases) == 0 {
		return ""
	}
	var b strings.Builder
	state := "reached"
	for _, p := range e.Phases {
		if p.Status != "completed" {
			state = "not reached"
			break
		}
	}
	fmt.Fprintf(&b, "workload prerequisites %s: %s", e.Run, state)
	// Keep a failed prerequisite visible even after many successful phases.
	for _, p := range e.Phases {
		if p.Status != "completed" && p.Status != "not_started" {
			phaseText(&b, p, true)
		}
	}
	for _, p := range e.Phases {
		if p.Status == "completed" || p.Status == "not_started" {
			phaseText(&b, p, false)
		}
	}
	return b.String()
}

func phaseText(b *strings.Builder, p Phase, detail bool) {
	status := strings.ReplaceAll(p.Status, "_", " ")
	if status == "" {
		status = "unknown"
	}
	fmt.Fprintf(b, "\n  phase %s: %s; faults %s; timeout %s", p.Name, status, p.Faults, p.Timeout)
	if p.Reason != "" {
		fmt.Fprintf(b, "; %s", p.Reason)
	}
	if !detail {
		return
	}
	if p.Started != "" || p.Deadline != "" || p.Finished != "" {
		fmt.Fprintf(b, "\n    virtual time: started %s; deadline %s", absent(p.Started), absent(p.Deadline))
		if p.Finished != "" {
			fmt.Fprintf(b, "; finished %s", p.Finished)
		}
	}
	later := 0
	for _, r := range p.Requirements {
		if r.Status == "satisfied" {
			continue
		}
		if r.Status == "not_started" {
			later++
			continue
		}
		name := r.Name
		if name == "" {
			name = r.Action
		}
		fmt.Fprintf(b, "\n    %s: %s (%s/%s %s); %d %s", name, strings.ReplaceAll(r.Status, "_", " "), r.App, r.Tenant, r.Action, r.Attempts, countNoun(r.Attempts, "logical call"))
		if r.Caller != "" {
			fmt.Fprintf(b, "\n      caller %s", r.Caller)
		}
		if r.AttemptOutcome != "" {
			fmt.Fprintf(b, "\n      latest logical call status %s", r.AttemptOutcome)
		}
		if len(r.Arguments) > 0 {
			fmt.Fprintf(b, "\n      arguments %s", observedJSON(r.Arguments))
		}
		if r.RequestID != "" {
			fmt.Fprintf(b, "\n      driver request %s", r.RequestID)
			if r.InFlight {
				b.WriteString(" is in flight")
			}
		} else if r.InFlight {
			b.WriteString("\n      current driver request is in flight")
		}
		if r.ObservedRequestID != "" {
			fmt.Fprintf(b, "\n      last answered driver request %s", r.ObservedRequestID)
		}
		if r.ExpectedOutcome != "" {
			fmt.Fprintf(b, "\n      expected outcome %s", r.ExpectedOutcome)
		}
		if len(r.Expected) > 0 {
			fmt.Fprintf(b, "\n      expected result %s", observedJSON(r.Expected))
		}
		if r.ObservedOutcome != "" {
			fmt.Fprintf(b, "\n      last outcome %s", r.ObservedOutcome)
		} else if len(r.Observed) == 0 {
			b.WriteString("\n      no answer observed")
		} else {
			b.WriteString("\n      last outcome not reported")
		}
		if len(r.Observed) > 0 {
			fmt.Fprintf(b, "\n      last result %s", observedJSON(r.Observed))
		}
		if r.Reason != "" {
			fmt.Fprintf(b, "\n      %s", r.Reason)
		}
	}
	if later > 0 {
		fmt.Fprintf(b, "\n    %d later %s not started", later, countNoun(later, "requirement"))
	}
}

func absent(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func observedJSON(value json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, value); err != nil {
		b.Reset()
		b.Write(value)
	}
	text := []rune(b.String())
	if len(text) > 384 {
		return string(text[:384]) + "… (JSON retains the full value)"
	}
	return string(text)
}

// PhasesString summarizes observed workload prerequisites across executions.
// Repeated completed phases are folded; unsuccessful executions retain their
// reasons and observations, with every execution preserved in JSON.
func PhasesString(executions []Execution) string {
	type group struct {
		name, faults, timeout string
	}
	var order []group
	counts := map[group]int{}
	var incomplete []Execution
	runs, reached := 0, 0
	for _, e := range executions {
		if len(e.Phases) == 0 {
			continue
		}
		runs++
		complete := true
		for _, p := range e.Phases {
			if p.Status != "completed" {
				complete = false
				continue
			}
			g := group{p.Name, p.Faults, p.Timeout}
			if counts[g] == 0 {
				order = append(order, g)
			}
			counts[g]++
		}
		if complete {
			reached++
		} else {
			incomplete = append(incomplete, e)
		}
	}
	if runs == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workload prerequisites: %d %s; %d reached; %d not reached", runs, countNoun(runs, "run"), reached, len(incomplete))
	for _, e := range incomplete[:min(len(incomplete), 3)] {
		b.WriteByte('\n')
		b.WriteString(e.PhasesString())
	}
	if len(incomplete) > 3 {
		fmt.Fprintf(&b, "\n  %d more unreached executions; JSON retains every prerequisite", len(incomplete)-3)
	}
	for _, g := range order[:min(len(order), 8)] {
		fmt.Fprintf(&b, "\n  phase %s completed %d times; faults %s; timeout %s", g.name, counts[g], g.faults, g.timeout)
	}
	if len(order) > 8 {
		fmt.Fprintf(&b, "\n  %d more completed phase policies; JSON retains every phase", len(order)-8)
	}
	return b.String()
}
