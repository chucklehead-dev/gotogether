package gristtest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Exercised is the claim that an exploration's runs took every block of
// app's catalog, judged by the store's binding: every command committed
// ok and refused by each refusal it declares (grist.Refuses), every query
// answered, every processor's steps (an Emits processor's handling, a
// Delivers processor's effect and checkpoint, an at-most-once outcome
// when it declares a success event), every site's record and every
// declared call's (answered ok at its caller, applied at its
// dependency), each in some run. A run that exercised none of the app's own blocks passes every
// other claim (examples/library's first faithful test never committed a
// borrow): this one fails it, naming each block missed with how to reach
// it. A conflict, a reused key and a skip are not wanted (faults and
// workloads decide them). except names what the test does not expect,
// each as the claim lists it: a block's action ("receipts"), a command's
// by outcome ("borrow.limit"), or a command for all of its outcomes
// ("borrow"); a name that is none of them panics, naming them.
//
// It is qtest.TakesEach of the catalog's qtest.Takes, so a summary and a
// failure read as any Takes' do. A faithful test claims it; a contrast or
// a mutant's test, whose workload is narrow by design, does not
// (docs/grist.md §0.11).
func Exercised(app *grist.App, except ...string) qtest.Claim {
	takes := Takes(app)
	names := map[string]bool{}
	for _, k := range takes {
		names[k.Name()] = true
		names[strings.SplitN(k.Name(), ".", 2)[0]] = true
	}
	skip := map[string]bool{}
	for _, x := range except {
		if !names[x] {
			panic(fmt.Sprintf("gristtest.Exercised(%s, %q): %q names no block of the catalog (its names: %s)", app.Name, x, x, strings.Join(sortedNames(names), ", ")))
		}
		skip[x] = true
	}
	var want []qtest.Take
	for _, k := range takes {
		if !skip[k.Name()] && !skip[strings.SplitN(k.Name(), ".", 2)[0]] {
			want = append(want, k)
		}
	}
	return qtest.TakesEach(want...)
}

// Takes are the claims Exercised makes of app's catalog, one per block
// and outcome, each with how to reach it: for a test that wants some of
// them in a share of the runs (qtest.Take.InAtLeast), or one of them.
func Takes(app *grist.App) []qtest.Take {
	c := app.Catalog()
	var out []qtest.Take
	for _, cmd := range c.Commands {
		out = append(out, qtest.Takes(cmd.Quint).Outcome("ok").Hint(fmt.Sprintf(
			"no %s committed: send it where its rule says ok (gristsim Config.Prologue for the calls it needs first, Config.Gen for its arguments, a name listed twice in Config.Send to send it twice as often)", cmd.Name)))
		for _, r := range cmd.Refuses {
			out = append(out, qtest.Takes(cmd.Quint).Outcome(r).Hint(fmt.Sprintf(
				"no %s was refused %s: send it where its rule refuses it so (Config.Gen's arguments, the state Config.Prologue leaves), or except %q with the reason", cmd.Name, r, cmd.Quint+"."+r)))
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
			if p.Delivery == grist.AtMostOnce && p.Success != "" {
				out = append(out, qtest.Takes(p.Quint+"Outcome").Hint(fmt.Sprintf("%s appended no outcome: an effect must succeed with no crash after it", p.Name)))
			}
		}
	}
	for _, d := range c.Dependencies {
		for _, call := range d.Calls {
			out = append(out, qtest.Takes(call.Quint).Outcome("ok").Hint(fmt.Sprintf(
				"no call of %s/%s was answered ok: an effect that uses it must run (its processor's line) and its double answer (gristsim Config.Doubles)", d.Name, call.Name)),
				qtest.Takes(call.Quint+"Served").Hint(fmt.Sprintf(
					"%s applied no %s: its double must apply a call (not only replay one: a grist.Replayed answer is recorded as %sReplayed)", d.Name, call.Name, call.Quint)))
		}
	}
	for _, s := range c.Sites {
		out = append(out, qtest.Takes(s.Quint).Hint(fmt.Sprintf("the %s site recorded nothing: the effect that makes it must run (its processor's line) and reach the dependency (attach it: gristsim Config.Setup, TierConfig.Deps)", s.Name)))
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
