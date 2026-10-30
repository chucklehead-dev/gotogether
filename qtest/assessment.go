package qtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"strings"
)

// Assess only captured histories, outside the simulation. The report is attached
// after primary judgment so verdict-cache entries never acquire run-local checks.
func assessHistory(ctx context.Context, c settings, r *Run) {
	if r == nil || r.Err != nil || c.assess == nil {
		return
	}
	before, err := json.Marshal(r.Recorded)
	if err != nil {
		r.assessment = &receipt.Composition{Local: []receipt.Component{}, Peers: []receipt.Component{}, Error: fmt.Sprintf("encode captured history: %v", err)}
		return
	}
	expected := fmt.Sprintf("sha256:%x", sha256.Sum256(before))
	result, err := c.assess(ctx, r.Recorded)
	if result.Local == nil {
		result.Local = []receipt.Component{}
	}
	if result.Peers == nil {
		result.Peers = []receipt.Component{}
	}
	if err != nil {
		result.Error = err.Error()
	}
	if result.History != expected && err == nil {
		result.Error = "composition assessment does not identify this captured history"
	}
	after, aerr := json.Marshal(r.Recorded)
	if aerr != nil || string(before) != string(after) {
		result.Error = "composition assessment mutated the captured history"
	}
	r.assessment = &result
}

// compositionSummary keeps unperformed phases visible even in a passing suite.
func compositionSummary(compositions []receipt.Composition) string {
	if len(compositions) == 0 {
		return ""
	}
	groups := []struct {
		name   string
		counts map[receipt.Status]int
	}{{"local models", map[receipt.Status]int{}}, {"peer agreement", map[receipt.Status]int{}}, {"system protocol", map[receipt.Status]int{}}}
	errors := 0
	for _, c := range compositions {
		if c.Error != "" {
			errors++
		}
		for _, v := range c.Local {
			groups[0].counts[v.Status]++
		}
		for _, v := range c.Peers {
			groups[1].counts[v.Status]++
		}
		groups[2].counts[c.Protocol.Status]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  composition: %d captured histories", len(compositions))
	if errors > 0 {
		fmt.Fprintf(&b, "; %d assessment errors", errors)
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "\n    %s:", g.name)
		for _, status := range []receipt.Status{receipt.Fail, receipt.Error, receipt.Inconclusive, receipt.NotChecked, receipt.Pass} {
			if n := g.counts[status]; n > 0 {
				fmt.Fprintf(&b, " %d %s", n, strings.ReplaceAll(string(status), "_", " "))
			}
		}
	}
	b.WriteString("\n    other executions were not checked; per-history detail in evidence JSON")
	return b.String()
}
