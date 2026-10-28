package gristsim_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/internal/gen"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

type lent struct {
	Member string `json:"member" grist:"tag"`
	Book   string `json:"book"`
}

type lend struct {
	Member string `json:"member"`
	Book   string `json:"book"`
}

var errLimit = grist.Rejection("limit")

// lending is an App whose lend command appends a loan, guarded (with
// rule) by loan-limit: a member holds at most two loans, read from the
// member's loans.
func lending(rule bool) *grist.App {
	a := grist.New("lending")
	ev := grist.NewEvent[lent](a, "lent")
	grist.NewCommand(a, "lend", func(r *grist.Reads, c lend) ([]grist.Event, error) {
		return []grist.Event{ev.Of(lent(c))}, nil
	}, grist.Appends(ev), grist.Authorize(grist.Authenticated))
	if rule {
		grist.NewRule(a, "loan-limit", ev, func(r *grist.Reads, e lent) error {
			if len(grist.Events(r, ev, grist.Tag("member", e.Member))) >= 2 {
				return errLimit
			}
			return nil
		}, grist.Refuses(errLimit))
	}
	return a
}

// lendingRules are the lending App's rules, needs the loan-limit rule's
// Needs.
func lendingRules(needs string) string {
	return `module lendingRules {
  import grist.* from "./grist"
  import lendingEvents.* from "./lending_gen"
  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)
  pure def lendOutcome(log: List[Event], c: LendArgs): str = "ok"
  pure def lendEvents(log: List[Event], c: LendArgs): List[Event] = [lent(c.member, c.book)]
  pure def lendNeeds(c: LendArgs, result: str): Set[Read] = Set()
  pure def loanLimitRefusal(log: List[Event], e: Event): str =
    if (log.select(x => x.typ == "lent" and lentMember(x) == lentMember(e)).length() >= 2) "limit" else ""
  pure def loanLimitNeeds(e: Event, refusal: str): Set[Read] = ` + needs + `
}
`
}

const lendingModel = `module lending {
  import grist.* from "./grist"
  import lendingEvents.* from "./lending_gen"
  import lendingRules.* from "./lending_rules"
  import lendingGen.* from "./lending_steps"
  export lendingGen.*
}

module lendingCalls {
  import grist.* from "./grist"
  import lendingEvents.* from "./lending_gen"
  import lendingRules.* from "./lending_rules"
  import lendingCallsGen.* from "./lending_steps"
  export lendingCallsGen.*
}
`

// The generated model guards every action that appends a rule's event
// with the rule's hole: the faithful app conforms at the store and at the
// clients (the clients' in full mode), refusing at the limit; the app run without the rule (the model
// still the ruled app's) commits a third loan the model refuses, and is
// rejected; a model whose rule needs a read the rule never makes rejects
// the faithful app's steps, since every step the rule ran in must cover
// its needs (in full mode).
func TestRuleGuardsActions(t *testing.T) {
	qtest.RequireQuint(t)
	byMember := "Set(lentByMember(lentMember(e)))"
	for _, c := range []struct {
		name  string
		rule  bool
		needs string
		want  []qtest.Claim
	}{
		{"faithful", true, byMember, []qtest.Claim{qtest.Conforms(), qtest.Takes("lend").Outcome("limit")}},
		{"dropped", false, byMember, []qtest.Claim{qtest.Some(qtest.Rejects("lend"))}},
		{"uncovered", true, byMember + ".union(Set(lentRead))", []qtest.Claim{qtest.All(qtest.Rejects("lend"))}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if testing.Short() && c.name == "uncovered" {
				t.Skip("full mode: the needs' check, beside the guard's")
			}
			dir := t.TempDir()
			os.MkdirAll(filepath.Join(dir, "model"), 0o755)
			os.WriteFile(filepath.Join(dir, "model", "lending_rules.qnt"), []byte(lendingRules(c.needs)), 0o644)
			os.WriteFile(filepath.Join(dir, "model", "lending.qnt"), []byte(lendingModel), 0o644)
			g, err := gen.Generate(lending(true), dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(g.Missing) > 0 {
				t.Fatalf("holes missing: %v", g.Missing)
			}
			if err := g.Write(dir); err != nil {
				t.Fatal(err)
			}
			if steps := string(g.Files["model/lending_steps.qnt"]); !strings.Contains(steps, "result == verdict_.outcome") || !strings.Contains(steps, "loanLimitRuled(log, e, acc)") {
				t.Fatalf("lend's action is not guarded by the rule:\n%s", steps)
			}
			gen := map[string]func(pick func(int) int) any{"lend": func(p func(int) int) any { return lend{Member: []string{"a", "b"}[p(2)], Book: "x"} }}
			cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 3, Gen: gen}
			a := lending(c.rule)
			qtest.System{Binding: filepath.Join(dir, "lending.binding.yaml"), Rates: qenv.NoFaults,
				Run: func(ch qenv.Chooser) error { return gristsim.Run(ch, a, cfg) }}.
				Explore(t, qtest.ExploreConfig{Seeds: 3}).Expect(t, c.want...)
			if c.name == "faithful" && !testing.Short() {
				qtest.System{Binding: filepath.Join(dir, "lending.calls.binding.yaml"), Rates: qenv.NoFaults, Opts: []qtest.Option{qtest.Linearize()},
					Run: func(ch qenv.Chooser) error { return gristsim.Run(ch, a, cfg) }}.
					Explore(t, qtest.ExploreConfig{Seeds: 3}).Expect(t, qtest.Conforms())
			}
		})
	}
}
