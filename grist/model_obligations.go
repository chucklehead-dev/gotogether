package grist

import (
	"fmt"
	"strings"
)

// The generator's obligations (docs/grist.md §3.3.1): what the catalog
// promises will eventually happen, written as eventual vals of module
// <app>Gen and judged by the store's bindings at a settle, beside the
// application's own eventual vals. Each says that every trigger is
// eventually followed by its processor's handling, or every answered call
// of a Keyed dependency by the dependency's record of the key:
//
//   - an Emits processor, or an at-most-once one: its checkpoint passed
//     every trigger (<p>EventuallyHandled). An Emit, a Skip and a Fail
//     commit with the checkpoint; at most once the checkpoint commits
//     first, and a crash after it loses the effect or its outcome by
//     right (§0.6), so no outcome is promised;
//   - an at-least-once processor: its checkpoint passed every trigger,
//     and the trigger was skipped (<p>Skips), had an ok effect step, or
//     failed every try (its grist.failed) (<p>EventuallyHandled);
//   - a Keyed call a processor Uses: every call its caller saw answered
//     (ok or a declared refusal, not unknown) has the dependency's served
//     record of its key (<c>EventuallyServed): a dependency that applies
//     each key once records what it applied.
//
// They are a live system's: a settle is judged only when nothing but
// daemons is at work, and a node down for good is no settled system
// (gristsim's supervisor restarts every node; the real tier's Settle
// waits for every processor to catch up).

// An obligation is one generated eventual val.
type obligation struct {
	name, doc, expr string
}

// obligations are the App's, in catalog order: the processors', then the
// calls'.
func (g *gen) obligations() []obligation {
	var out []obligation
	for _, p := range g.c.Processors {
		trigger := fmt.Sprintf("log[i].typ == %q", p.On)
		cp := fmt.Sprintf("checkpoints.get(%q) >= i + 1", p.Name)
		switch {
		case p.Shape == "emits":
			out = append(out, obligation{
				name: p.Quint + "EventuallyHandled",
				doc:  fmt.Sprintf("after a settle, %s (Emits) has handled every %s: its checkpoint passed it, with the events it emitted, a Skip's none or a Fail's grist.failed", p.Name, p.On),
				expr: fmt.Sprintf("log.indices().forall(i => %s implies %s)", trigger, cp),
			})
		case p.Delivery == AtLeastOnce:
			out = append(out, obligation{
				name: p.Quint + "EventuallyHandled",
				doc:  fmt.Sprintf("after a settle, %s (at least once) has handled every %s: its checkpoint passed it, and it was skipped (%sSkips), had an ok effect step, or failed every try (its grist.failed)", p.Name, p.On, p.Quint),
				expr: fmt.Sprintf("log.indices().forall(i => %s implies and {\n      %s,\n      or {\n        %sSkips(log.slice(0, i + 1), i + 1),\n        effectsOf(effects, %q, i + 1, true) > 0,\n        holds(log, failed(%q, i + 1)),\n      },\n    })",
					trigger, cp, p.Quint, p.Name, p.Name),
			})
		default: // at most once
			out = append(out, obligation{
				name: p.Quint + "EventuallyHandled",
				doc:  fmt.Sprintf("after a settle, %s (at most once) has handled every %s: its checkpoint passed it. The checkpoint commits first, and a crash after it loses the effect or its outcome by right, so no outcome is promised (an application states one test-only: atMostOnceOutcomesLogged)", p.Name, p.On),
				expr: fmt.Sprintf("log.indices().forall(i => %s implies %s)", trigger, cp),
			})
		}
	}
	for _, c := range g.calls() {
		if !c.Keyed || len(c.users) == 0 {
			continue
		}
		users := make([]string, len(c.users))
		for i, p := range c.users {
			users[i] = p.Name
		}
		out = append(out, obligation{
			name: c.Quint + "EventuallyServed",
			doc:  fmt.Sprintf("after a settle, every call of %s (Keyed, used by %s) its caller saw answered, ok or refused, has the dependency's served record of its key; an unknown answer promises nothing (the request may have been lost)", c.label(), strings.Join(users, ", ")),
			expr: fmt.Sprintf("%sCalls.indices().forall(i => %sCalls[i].result != %q implies\n      %sRecords.indices().exists(j => %sRecords[j].key == %sCalls[i].key))",
				c.Quint, c.Quint, unknownOutcome, c.Quint, c.Quint, c.Quint),
		})
	}
	return out
}

// claimObligations claims the obligations' names.
func (g *gen) claimObligations() {
	for _, o := range g.obligations() {
		g.claim(o.name, "the obligation "+o.name)
	}
}

// obligationNames are the obligations' names, in order.
func (g *gen) obligationNames() []string {
	var out []string
	for _, o := range g.obligations() {
		out = append(out, o.name)
	}
	return out
}

// obligationVals writes the obligations into module <app>Gen.
func (g *gen) obligationVals(b *strings.Builder) {
	obs := g.obligations()
	if len(obs) == 0 {
		return
	}
	b.WriteString("\n" + comment("  ", "The obligations the catalog implies (docs/grist.md §3.3.1): eventual vals, judged by the store's bindings at a settle beside the app's own (the default binding's, and a variant's that judges every invariant of the default binding)"))
	for _, o := range obs {
		fmt.Fprintf(b, "\n%s  val %s =\n    %s\n", comment("  ", o.doc), o.name, o.expr)
	}
}

// ownClashes are the generated obligations the application's model
// defines itself (module <aq>), or its rules (module <aq>Rules, which
// <aq>Gen imports), which quint would refuse as a name defined twice
// (QNT101, at a line of the generated file the app never edits).
func (g *gen) ownClashes(own []byte) (model, rules []string) {
	defined := definedIn(own, g.aq)
	for _, n := range g.obligationNames() {
		if defined[n] {
			model = append(model, n)
		}
		if g.defined[n] {
			rules = append(rules, n)
		}
	}
	return model, rules
}
