package validate

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

// resolveDiagnosticEvidence relates model-authored selectors to observed calls
// of this actor only. No evidence match changes the associated model judgment.
func (c *Checker) resolveDiagnosticEvidence(detail *diagnostic.Result, plan *binding.Plan) {
	resolved := 0
	for i := range detail.Items {
		item := &detail.Items[i]
		item.Evidence = diagnostic.Evidence{Status: "unconfigured", Reason: "configure diagnosticEvidence for this projection and kind"}
		rule, ok := c.Binding.DiagnosticEvidence[detail.Projection][item.Kind]
		if !ok {
			continue
		}
		if plan == nil || plan.Actor != detail.Actor {
			item.Evidence = diagnostic.Evidence{Status: "unavailable", Reason: "observed actor plan is unavailable"}
			continue
		}
		evidence := diagnostic.Evidence{Status: "missing", Reason: "no observed call matches the configured action and selectors"}
		bad := ""
		for k, call := range plan.Calls {
			if call.Action != rule.Action || call.Step == nil || call.Step.Ambiguous || call.Step.Invocation {
				continue
			}
			match := true
			for _, arg := range slices.Sorted(maps.Keys(rule.Match)) {
				want := item.Intent
				if rule.Match[arg] == "subject" {
					want = item.Subject
				}
				value, err := itf.ParseQuint(call.Args[arg])
				if err != nil || value.Kind != itf.Str {
					bad = "a configured selector is not a literal string in the observed plan"
					match = false
					break
				}
				if value.S != want {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			s := call.Step
			record := diagnostic.RecordRef{Actor: s.Actor, Recorder: s.Recorder, Seq: s.Seq, Source: s.Source}
			origin := record
			if rule.Origin != nil {
				actor, ea := rule.Origin.Render(rule.Origin.Actor, s)
				recorder, er := rule.Origin.Render(rule.Origin.Recorder, s)
				seq, es := rule.Origin.Render(rule.Origin.Seq, s)
				number, en := strconv.ParseUint(seq, 10, 64)
				if ea != nil || er != nil || es != nil || en != nil || number == 0 || strings.TrimSpace(actor) == "" || strings.TrimSpace(recorder) == "" || actor == "<no value>" || recorder == "<no value>" {
					bad = "configured origin has missing or invalid actor, recorder or positive sequence"
					continue
				}
				origin = diagnostic.RecordRef{Actor: actor, Recorder: recorder, Seq: number}
			}
			if record.Recorder == "" || record.Seq == 0 || record.Actor == "" {
				bad = "matched call has no complete captured record identity"
				continue
			}
			evidence.Total++
			if len(evidence.Matches) < diagnosticLimit {
				evidence.Matches = append(evidence.Matches, diagnostic.Match{Actor: plan.Actor, At: k + 1, Record: record, Origin: origin})
			}
		}
		evidence.Omitted = evidence.Total - len(evidence.Matches)
		switch {
		case bad != "":
			evidence.Status = "unavailable"
			evidence.Reason = bad
		case evidence.Total == 1:
			evidence.Status = "resolved"
			evidence.Reason = ""
		case evidence.Total > 1:
			evidence.Status = "ambiguous"
			evidence.Reason = fmt.Sprintf("%d observed calls match; no origin selected", evidence.Total)
		}
		item.Evidence = evidence
		if evidence.Status == "resolved" {
			resolved++
		}
	}
	detail.Evidence = "unresolved"
	if resolved > 0 {
		detail.Evidence = "partial"
		if resolved == len(detail.Items) && detail.Omitted == 0 {
			detail.Evidence = "resolved"
		}
	}
}
