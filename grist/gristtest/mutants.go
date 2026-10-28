package gristtest

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/ruledrop"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// dropPrefix starts the name of a rule's generated mutant.
const dropPrefix = "drop-"

// a rule's generated mutant (qtest.Mutant() "drop-<rule>") drops it in
// this binary, before any rule runs: grist skips the rule ruledrop names
// (only test binaries import gristtest, so no other binary can)
func init() {
	if r, ok := strings.CutPrefix(qtest.Mutant(), dropPrefix); ok {
		ruledrop.Dropped = r
	}
}

// DroppedRule says whether this binary is a rule's generated mutant
// (RuleMutants: the app as it is, the rule dropped), naming the rule, and
// the claim that catches it: some run rejects a command it guards, which
// committed where the model's guard (the rule's hole) refuses it. A
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
		for _, g := range r.Guards {
			q, _ := grist.QuintName(g)
			es = append(es, qtest.Rejects(q))
		}
		switch len(es) {
		case 0:
			panic(fmt.Sprintf("gristtest.DroppedRule: rule %s guards no command (none declares %s in grist.Appends): it has no generated mutant", name, r.On))
		case 1:
			return name, qtest.Some(es[0]), true
		}
		return name, qtest.Some(qtest.AnyOf(es...)), true
	}
	panic(fmt.Sprintf("gristtest.DroppedRule: this binary is mutant %s, but %s has no rule %s", qtest.Mutant(), app.Name, name))
}

// RuleMutants are app's generated mutants, one per rule that guards a
// command: "drop-<rule>", the app as it is with the rule dropped, built
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
