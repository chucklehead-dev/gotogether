package gristtest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Catalog is app's catalog as qtest's: a Take per block and outcome,
// each with how to reach it, judged by the store's binding. Its
// Exercised claims every one in some run but those excepted: every
// command committed ok and refused by each refusal it declares
// (grist.Refuses) and each its rules add (grist.NewRule), every query
// answered, every processor's steps (an Emits processor's handling, a
// Delivers processor's effect and checkpoint, an at-most-once outcome
// when it declares a success event), every site's record and every
// declared call's (answered ok at its caller, applied at its
// dependency). A run that exercised none of the app's own blocks passes
// every other claim (examples/library's first faithful test never
// committed a borrow): this one fails it, naming each block missed with
// how to reach it. A conflict, a reused key and a skip are not wanted
// (faults and workloads decide them). Its Take names one, for a test
// that wants it in a share of the runs:
//
//	cat := gristtest.Catalog(App)
//	/* ... */ Expect(t, cat.Exercised("borrow.limit"), cat.Take("borrow.ok").InAtLeast(0.5))
//
// A faithful test claims Exercised; a contrast or a mutant's test, whose
// workload is narrow by design, does not (docs/HISTORY.md (grist §0.11)).
func Catalog(app *grist.App) qtest.Catalog {
	c := app.Catalog()
	var out []qtest.Take
	for _, cmd := range c.Commands {
		out = append(out, qtest.Takes(cmd.Quint).Outcome("ok").Hint(fmt.Sprintf(
			"no %s committed: send it where its rule says ok (gristsim Config.Prologue for the calls it needs first, Config.Gen for its arguments, a name listed twice in Config.Send to send it twice as often)", cmd.Name)))
		for _, r := range cmd.Refuses {
			hint := fmt.Sprintf("no %s was refused %s: send it where its rule refuses it so (Config.Gen's arguments, the state Config.Prologue leaves), or except %q with the reason", cmd.Name, r, cmd.Quint+"."+r)
			if rules := refusing(c, cmd, r); len(rules) > 0 {
				hint = fmt.Sprintf("no %s was refused %s by its rules (%s): send it where its decision says ok and a rule refuses what it appends (Config.Gen's arguments, the state Config.Prologue leaves), or except %q with the reason", cmd.Name, r, strings.Join(rules, ", "), cmd.Quint+"."+r)
			}
			out = append(out, qtest.Takes(cmd.Quint).Outcome(r).Hint(hint))
		}
	}
	for _, q := range c.Queries {
		out = append(out, qtest.Takes(q.Quint).Hint(fmt.Sprintf("no %s answered: list it in Config.Send (a run's probes ask every query after its settle: a run that never settles asks none)", q.Name)))
	}
	trigger := func(p grist.CatalogProcessor) string {
		return fmt.Sprintf("a %s event must commit (the command that appends it, ok: see its own line)", p.On)
	}
	for _, p := range c.Processors {
		switch {
		case p.Shape == "emits":
			out = append(out, qtest.Takes(p.Quint).Hint(fmt.Sprintf("%s handled no trigger: %s", p.Name, trigger(p))))
		case p.Shape == "effect":
			out = append(out, qtest.Takes(p.Quint+"Effect").Hint(fmt.Sprintf("%s ran no effect: %s, and its handler return an Effect for it (not Skip)", p.Name, trigger(p))),
				qtest.Takes(p.Quint).Hint(fmt.Sprintf("%s checkpointed no trigger: %s, and the run settle (its checkpoint follows the effect)", p.Name, trigger(p))))
			if p.Guarantee == grist.AtMostOnce && p.Success != "" {
				out = append(out, qtest.Takes(p.Quint+"Outcome").Hint(fmt.Sprintf("%s appended no outcome: an effect must succeed with no crash after it", p.Name)))
			}
		}
	}
	for _, d := range c.Dependencies {
		for _, call := range d.Calls {
			if d.Peer != "" {
				// its site is the peer's commit, in the peer's own model
				out = append(out, qtest.Takes(call.Quint).Outcome("ok").Hint(fmt.Sprintf(
					"no call of peer %s's %s was answered ok: an effect that uses it must run (its processor's line) and the peer accept it (gristsim Config.Peers; its policy admits grist.PeerCaller(%q))", d.Name, call.Name, app.Name)))
				continue
			}
			out = append(out, qtest.Takes(call.Quint).Outcome("ok").Hint(fmt.Sprintf(
				"no call of %s/%s was answered ok: an effect that uses it must run (its processor's line) and its double answer (gristsim Config.Doubles)", d.Name, call.Name)),
				qtest.Takes(call.Quint+"Served").Hint(fmt.Sprintf(
					"%s applied no %s: its double must apply a call (not only replay one: a grist.Replayed answer is recorded as %sReplayed)", d.Name, call.Name, call.Quint)))
		}
	}
	for _, s := range c.Sites {
		out = append(out, qtest.Takes(s.Quint).Hint(fmt.Sprintf("the %s site recorded nothing: the effect that makes it must run (its processor's line) and reach the dependency (attach it: gristsim Config.Setup, TierConfig.Deps)", s.Name)))
	}
	return qtest.Catalog{Name: app.Name, Takes: out}
}

// refusing are the rules guarding cmd that refuse r (none when only its
// decision does).
func refusing(c grist.Catalog, cmd grist.CatalogBlock, r string) []string {
	var out []string
	if !slices.Contains(cmd.RuleRefuses, r) {
		return nil
	}
	for _, rule := range c.Rules {
		if slices.Contains(cmd.Rules, rule.Name) && slices.Contains(rule.Refuses, r) {
			out = append(out, rule.Name)
		}
	}
	return out
}
