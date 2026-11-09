package gristtest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/peerkey"
	"github.com/chucklehead-dev/gotogether/grist/internal/ruledrop"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// dropPrefix starts the name of a rule's generated mutant.
const dropPrefix = "drop-"

// a rule's generated mutant (qtest.Mutant() "drop-<rule>") drops it in
// this binary, before any rule runs: grist ignores that rule's declared refusals
// while still running its callback, collecting its reads and checking errors
// (only test binaries import gristtest, so no other binary can)
func init() {
	if r, ok := strings.CutPrefix(qtest.Mutant(), dropPrefix); ok {
		ruledrop.Dropped = r
	}
	// and the peers' (PeerMutants): every peer call's try under a new id
	peerkey.Fresh = qtest.Mutant() == peerkey.Mutant
}

// PeerMutants are app's generated mutants of its peer calls
// (grist.NewPeer): freshKey when it makes one, every try of a call sent
// under a new command id (its key stamped with the try's time, as a
// client minting a request id per request does) instead of the effect's
// key, built without an overlay (qtest.Mutants' Generated). The peer's
// marker then answers no retry: an at-least-once effect retried after a
// lost answer commits at the peer twice, which an invariant of the
// peer's model over its log catches, and Links finds the second commit's
// id naming the caller with no call of it.
//
//	qtest.Mutants{Generated: gristtest.PeerMutants(App)}.Run(t)
func PeerMutants(app *grist.App) []string {
	for _, d := range app.Catalog().Dependencies {
		if d.Peer != "" && len(d.Calls) > 0 {
			return []string{peerkey.Mutant}
		}
	}
	return nil
}

// DroppedRule says whether this binary is a rule's generated mutant
// (RuleMutants: the app as it is, the rule dropped), naming the rule, and
// the claim that catches it: some run rejects a successful command it guards,
// and its structured explanation says the decision was ok but its rules required
// one of the dropped rule's declared refusals. A missing read or another kind
// of rejection does not establish this claim. A
// TestMutant explores, before its switch, a workload where the rule
// refuses, and expects caught:
//
//	if rule, caught, ok := gristtest.DroppedRule(App); ok {
//		_ = rule // the workload that reaches its refusal
//		/* ... */
//		return
//	}
//
// A rule that guards no command (its event only a processor's) panics:
// no generated mutant is made of it.
func DroppedRule(app *grist.App) (rule string, caught qtest.Claim, ok bool) {
	name, ok := strings.CutPrefix(qtest.Mutant(), dropPrefix)
	if !ok {
		return "", nil, false
	}
	for _, r := range app.Catalog().Rules {
		if r.Name != name {
			continue
		}
		var es []qtest.Expectation
		var guards []string
		for _, g := range r.Guards {
			q, _ := catalog.QuintName(g)
			es = append(es, qtest.Rejects(q))
			guards = append(guards, q)
		}
		if len(es) == 0 {
			panic(fmt.Sprintf("gristtest.DroppedRule: rule %s guards no command (none declares %s in grist.Appends): it has no generated mutant", name, r.On))
		}
		return name, qtest.Some(qtest.AnyOf(es...), droppedRefusal(r, guards)), true
	}
	panic(fmt.Sprintf("gristtest.DroppedRule: this binary is mutant %s, but %s has no rule %s", qtest.Mutant(), app.Name, name))
}

func droppedRefusal(rule grist.CatalogRule, guards []string) qtest.Expectation {
	return qtest.Satisfies("successful guarded decision rejected for rule "+rule.Name+"'s refusal", func(report *validate.Report) error {
		unavailable := ""
		for _, actor := range report.Actors {
			f := actor.Failure
			if actor.Conforms != validate.Fail || f == nil || f.Kind != "disabled" || f.Step == nil ||
				f.Step.Outcome != "ok" {
				continue
			}
			guarded := slices.Contains(guards, f.Step.Action)
			for _, guard := range guards {
				guarded = guarded || f.Call == guard || strings.HasPrefix(f.Call, guard+"(")
			}
			if !guarded {
				continue
			}
			if f.Why == nil || f.Why.Unexplained != "" {
				unavailable = "no structured guard explanation for dropped rule " + rule.Name
				continue
			}
			for _, condition := range f.Why.Conditions {
				// This is the generated action's outcome guard, not rendered
				// diagnostic prose. Parse its structured operands as Quint values.
				if condition.Expr != "result == verdict_.outcome" {
					continue
				}
				values := map[string]itf.Value{}
				valid := true
				for _, operand := range condition.Values {
					if operand.Name != "verdict_" && operand.Name != "result" {
						continue
					}
					value, err := itf.ParseQuint(operand.Value)
					if _, duplicate := values[operand.Name]; duplicate || err != nil {
						valid = false
					}
					values[operand.Name] = value
				}
				verdict, result := values["verdict_"], values["result"]
				decided, outcome, origin := verdict.Fields["decided"], verdict.Fields["outcome"], verdict.Fields["rule"]
				if !valid || verdict.Kind != itf.Record || decided.Kind != itf.Str || result.Kind != itf.Str ||
					outcome.Kind != itf.Str || origin.Kind != itf.Str {
					unavailable = "guard verdict for dropped rule " + rule.Name + " lacks canonical data; regenerate the application model"
					continue
				}
				if decided.S == "ok" && result.S == "ok" && slices.Contains(rule.Refuses, outcome.S) && origin.S == rule.Name {
					return nil
				}
			}
		}
		if unavailable != "" {
			return fmt.Errorf("%w: %s", qtest.ErrNoVerdict, unavailable)
		}
		return fmt.Errorf("no successful guarded decision was rejected for rule %s's declared refusal (%v)", rule.Name, rule.Refuses)
	})
}

// RuleMutants are app's generated mutants, one per rule that guards a
// command: "drop-<rule>", the app as it is with that rule's declared refusals
// ignored. The rule still reads and operational errors still fail. Built
// without an overlay (qtest.Mutants' Generated). Its TestMutant explores
// where the rule refuses and expects DroppedRule's claim:
//
//	func TestMutants(t *testing.T) {
//		qtest.Mutants{Generated: gristtest.RuleMutants(App)}.Run(t)
//	}
func RuleMutants(app *grist.App) []string {
	var out []string
	for _, r := range app.Catalog().Rules {
		if len(r.Guards) > 0 {
			out = append(out, dropPrefix+r.Name)
		}
	}
	return out
}
