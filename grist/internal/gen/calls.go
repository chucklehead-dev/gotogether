package gen

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The generator's part for declared calls (docs/grist-instrumentation.md
// §3.4): per call c of a dependency, used by processors (Uses), the
// record types, the caller's ghost (<c>Calls) and the site's records
// (<c>Records), the caller action <c>, the site's actions <c>Served (an
// application) and <c>Replayed (an answer given again from one), a rule
// hole per (processor, call) and a generated <c>Admits unless the
// application's rules define one. A peer's call (NewPeerCall) has the
// caller's side alone: its site is the peer's commit, judged by the
// peer's own model, so it has no records, no site actions, no Admits and
// no served obligation (gristtest.Linked relates the two sides).

// aCall is one declared call as the generator writes it.
type aCall struct {
	CatalogCall
	dep       string
	req, resp []field
	users     []CatalogProcessor
	peer      bool // a peer's command (CatalogDependency.Peer): the caller's side alone
}

// calls are the catalog's calls, in registration order.
func (g *gen) calls() []aCall {
	var out []aCall
	for _, d := range g.c.Dependencies {
		for _, c := range d.Calls {
			req, _ := scalarFields(c.Request)
			resp, _ := scalarFields(c.Response)
			ac := aCall{CatalogCall: c, dep: d.Name, req: req, resp: resp, peer: d.Peer != ""}
			for _, p := range g.c.Processors {
				if slices.Contains(c.Users, p.Name) {
					ac.users = append(ac.users, p)
				}
			}
			out = append(out, ac)
		}
	}
	return out
}

// typeName is the call's capitalized Quint name (BankPay).
func (c aCall) typeName() string { return upper(c.Quint) }

// ruleName is processor p's rule hole for the call: <p><C>.
func (c aCall) ruleName(p CatalogProcessor) string { return p.Quint + c.typeName() }

// label names the call: <dependency>/<call>.
func (c aCall) label() string { return c.dep + "/" + c.Name }

// checkCalls lists why the catalog's calls cannot be modelled.
func (g *gen) checkCalls() []string {
	var ps []string
	for _, d := range g.c.Dependencies {
		for _, c := range d.Calls {
			for _, side := range []struct {
				name string
				fs   func() error
			}{{"request", func() error { _, err := scalarFields(c.Request); return err }}, {"response", func() error { _, err := scalarFields(c.Response); return err }}} {
				if err := side.fs(); err != nil {
					ps = append(ps, fmt.Sprintf("call %s/%s: its %s: %v", d.Name, c.Name, side.name, err))
				}
			}
		}
	}
	return ps
}

func (g *gen) callHoles() {
	for _, c := range g.calls() {
		for _, p := range c.users {
			g.hole(c.ruleName(p), fmt.Sprintf("the request %s's effect makes of %s for the trigger at position trigger, reading prefix (the log through it)", p.Name, c.label()),
				fmt.Sprintf(`pure def %s(prefix: List[Event], trigger: int): %sRequest = %s`, c.ruleName(p), c.typeName(), zeroRecord(c.req)))
		}
	}
}

func (g *gen) claimCalls() {
	for _, c := range g.calls() {
		what := "call " + c.label()
		for _, n := range []string{c.Quint, c.Quint + "Served", c.Quint + "Replayed", c.Quint + "Calls", c.Quint + "Records", c.Quint + "Admits", c.Quint + "Keys"} {
			g.claim(n, what)
		}
	}
}

// zeroRecord is a record of fs at their zero values.
func zeroRecord(fs []field) string {
	if len(fs) == 0 {
		return "{ unit: true }"
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.json + ": " + zeroOf(f.typ)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// callTypes are the call's record types, in module <app>Events.
func (g *gen) callTypes(b *strings.Builder) {
	for _, c := range g.calls() {
		t := c.typeName()
		fmt.Fprintf(b, "  // call %s: its request and response, and a step of it (the caller's or the site's: the outcome as result,\n  // the response zero unless it is ok)\n", c.label())
		fmt.Fprintf(b, "  type %s = %s\n", g.claim(t+"Request", "call "+c.label()), recordType(c.req))
		fmt.Fprintf(b, "  type %s = %s\n", g.claim(t+"Response", "call "+c.label()), recordType(c.resp))
		fmt.Fprintf(b, "  type %s = { processor: str, trigger: int, key: str, request: %sRequest, result: str, response: %sResponse }\n",
			g.claim(t+"Record", "call "+c.label()), t, t)
	}
}

// callVars are the store side's variables of the calls.
func (g *gen) callVars() [][2]string {
	var vs [][2]string
	for _, c := range g.calls() {
		vs = append(vs, [2]string{c.Quint + "Calls", "[]"})
		if !c.peer {
			vs = append(vs, [2]string{c.Quint + "Records", "[]"})
		}
	}
	return vs
}

func (g *gen) callVarDecls(b *strings.Builder) {
	for _, c := range g.calls() {
		if c.peer {
			fmt.Fprintf(b, "  // the calls of %s, a peer's command, as their callers saw them (Call.Do), in\n  // order; the peer's model judges what it did\n", c.label())
			fmt.Fprintf(b, "  var %sCalls: List[%sRecord]\n", c.Quint, c.typeName())
			continue
		}
		fmt.Fprintf(b, "  // the calls of %s as their callers saw them (Call.Do), and as the dependency\n  // applied them (its served records), in order\n", c.label())
		fmt.Fprintf(b, "  var %sCalls: List[%sRecord]\n  var %sRecords: List[%sRecord]\n", c.Quint, c.typeName(), c.Quint, c.typeName())
	}
}

// callParams are the action's parameters, and the record built from them.
func (c aCall) callParams() (string, string) {
	all := []string{"processor: str", "trigger: int", "key: str"}
	for _, f := range c.req {
		all = append(all, f.param+": "+f.typ)
	}
	all = append(all, "result: str")
	for _, f := range c.resp {
		all = append(all, f.param+": "+f.typ)
	}
	rec := fmt.Sprintf("{ processor: processor, trigger: trigger, key: key, request: %s, result: result, response: %s }", recordValue(c.req), recordValue(c.resp))
	return strings.Join(all, ", "), rec
}

// isTriggerOf is the condition that trigger names one of p's events.
func isTriggerOf(p CatalogProcessor, trigger string) string {
	return fmt.Sprintf("%s > 0 and %s <= log.length() and log[%s - 1].typ == %q", trigger, trigger, trigger, p.On)
}

func (g *gen) callActions(b *strings.Builder) {
	for _, c := range g.calls() {
		t := c.typeName()
		ps, rec := c.callParams()
		keys := c.Quint + "Keys"
		fmt.Fprintf(b, "\n  // the keys of %s's steps: (processor, trigger, key)\n  pure def %s(l: List[%sRecord]): List[CallKey] = l.foldl([], (acc, c) => acc.append((c.processor, c.trigger, c.key)))\n", c.label(), keys, t)
		if !c.peer && !g.defined[c.Quint+"Admits"] {
			body := "true"
			doc := "applies every call it gets (not Keyed): any record"
			if c.Keyed {
				body = fmt.Sprintf("not(keyCalled(%s(records), r.key))", keys)
				doc = "applies each key once (Keyed): no earlier record of the key"
			}
			b.WriteString("\n" + comment("  ", fmt.Sprintf("the %s dependency %s; the application's rules may define %sAdmits instead, reading only prefix (the log through the trigger)", c.dep, doc, c.Quint)))
			fmt.Fprintf(b, "  pure def %sAdmits(prefix: List[Event], records: List[%sRecord], r: %sRecord): bool = %s\n", c.Quint, t, t, body)
		}
		// the caller: from an effect of a processor that uses it, its
		// request that processor's rule for the trigger
		var callers, sites, replays []string
		for _, p := range c.users {
			cond := []string{
				fmt.Sprintf("log[trigger - 1].typ == %q", p.On),
				fmt.Sprintf("rec_.request == %s(log.slice(0, trigger), trigger)", c.ruleName(p)),
				fmt.Sprintf("not(%sSkips(log.slice(0, trigger), trigger))", p.Quint),
			}
			if p.Guarantee == AtLeastOnce {
				cond = append(cond, fmt.Sprintf("trigger == nextOf(log, checkpoints.get(%q), %q)", p.Name, p.On))
			} else {
				cond = append(cond, fmt.Sprintf("checkpoints.get(%q) >= trigger", p.Name), fmt.Sprintf("triesOf(effects, %q, trigger) == 0", p.Name), fmt.Sprintf("not(keyCalled(%s(%sCalls), key))", keys, c.Quint))
			}
			callers = append(callers, fmt.Sprintf("processor == %q and (if (trigger > 0 and trigger <= log.length()) and {\n          %s,\n        } else false)", p.Name, strings.Join(cond, ",\n          ")))
			// the site judges the request too: where a caller's step is never
			// completed (its node crashed after the dependency applied the
			// call, and an at-most-once effect never calls again) the served
			// record is the only one of the request
			sites = append(sites, fmt.Sprintf("processor == %q and (if (trigger > 0 and trigger <= log.length()) and {\n          %s,\n          %s,\n          %s,\n          %sAdmits(log.slice(0, trigger), %sRecords, rec_),\n        } else false)", p.Name, cond[0], cond[1], cond[2], c.Quint, c.Quint))
			replays = append(replays, fmt.Sprintf("processor == %q and (if (trigger > 0 and trigger <= log.length()) log[trigger - 1].typ == %q else false)", p.Name, p.On))
		}
		or := func(xs []string) string {
			if len(xs) == 0 {
				return "false"
			}
			return "or {\n        " + strings.Join(xs, ",\n        ") + ",\n      }"
		}
		atMostOnce := ""
		for _, p := range c.users {
			if p.Guarantee == AtMostOnce {
				atMostOnce = ", at most once with no earlier call of the key"
			}
		}
		b.WriteString("\n" + comment("  ", fmt.Sprintf("%s as its caller saw it (Call.Do: invoked before the request, done with the answer, the outcome ok, a declared refusal or unknown): from an effect of a processor that uses it, for one of its triggers before the effect step, under the trigger's key (callKeyStable), its request the processor's rule's%s", c.label(), atMostOnce)))
		fmt.Fprintf(b, "  action %s(%s): bool = {\n    val rec_: %sRecord = %s\n    all {\n      %s,\n      callKeyStable(effects, %s(%sCalls), processor, trigger, key),\n",
			c.Quint, ps, t, rec, or(callers), keys, c.Quint)
		b.WriteString(g.frame("      ", map[string]string{"appended": "[]", c.Quint + "Calls": c.Quint + "Calls.append(rec_)"}))
		b.WriteString("    }\n  }\n")
		if c.peer {
			continue // its site is the peer's commit, in the peer's model
		}
		b.WriteString("\n" + comment("  ", fmt.Sprintf("%s as the dependency applied it (its served record, placed at its cause: after the commit holding its trigger): for one of a user's triggers, its request the processor's rule's (as the caller's: a caller crashed during the call leaves the served record the request's only one), as %sAdmits allows given the log through the trigger", c.label(), c.Quint)))
		fmt.Fprintf(b, "  action %sServed(%s): bool = {\n    val rec_: %sRecord = %s\n    all {\n      %s,\n", c.Quint, ps, t, rec, or(sites))
		b.WriteString(g.frame("      ", map[string]string{"appended": "[]", c.Quint + "Records": c.Quint + "Records.append(rec_)"}))
		b.WriteString("    }\n  }\n")
		b.WriteString("\n" + comment("  ", fmt.Sprintf("%s answered again from an application (grist.Replayed: a dependency answering a retry of a key from its first answer): an earlier served record, request and answer alike; it applies nothing", c.label())))
		fmt.Fprintf(b, "  action %sReplayed(%s): bool = {\n    val rec_: %sRecord = %s\n    all {\n      %s,\n      %sRecords.indices().exists(i => %sRecords[i] == rec_),\n", c.Quint, ps, t, rec, or(replays), c.Quint, c.Quint)
		b.WriteString(g.frame("      ", map[string]string{"appended": "[]"}))
		b.WriteString("    }\n  }\n")
	}
}

// callSteps are the store step's branches of the calls: per user, a
// caller's, a site's application and its replay, each drawing its
// trigger, key, outcome and response, its request the rule's.
func (g *gen) callSteps() []string {
	var out []string
	for _, c := range g.calls() {
		results := []string{`"ok"`}
		for _, r := range c.Refuses {
			results = append(results, fmt.Sprintf("%q", r))
		}
		actions := []string{c.Quint, c.Quint + "Served", c.Quint + "Replayed"}
		if c.peer {
			actions = actions[:1]
		}
		for _, p := range c.users {
			for _, action := range actions {
				site := action != c.Quint
				d := newDraws()
				d.pick("trigger_", "1.to(log.length() + 1)")
				d.pick("key_", "KEYS")
				rs := results
				if !site {
					rs = append(append([]string(nil), results...), fmt.Sprintf("%q", unknownOutcome))
				}
				d.pick("result_", "Set("+strings.Join(rs, ", ")+")")
				resp := d.add(c.resp)
				args := []string{fmt.Sprintf("%q", p.Name), "trigger_", "key_"}
				for _, f := range c.req {
					args = append(args, "req_."+f.json)
				}
				args = append(args, "result_")
				for _, f := range resp {
					args = append(args, f.param)
				}
				call := fmt.Sprintf("{\n        val req_: %sRequest = if (trigger_ <= log.length() and log[trigger_ - 1].typ == %q) %s(log.slice(0, trigger_), trigger_) else %s\n        %s(%s)\n      }",
					c.typeName(), p.On, c.ruleName(p), zeroRecord(c.req), action, strings.Join(args, ", "))
				out = append(out, d.branch(call))
			}
		}
	}
	return out
}

// callBindings are the binding's entries of the calls: every parameter
// the same-named step field, the outcome as result.
func (g *gen) callBindings(entry func(action, args, expect string)) {
	for _, c := range g.calls() {
		args := argTemplates(append(append([]field(nil), c.req...), c.resp...), "result: '{{.outcome}}'")
		entry(c.Quint, args, "")
		if c.peer {
			continue
		}
		entry(c.Quint+"Served", args, "")
		entry(c.Quint+"Replayed", args, "")
	}
}

// callOutcomes are the calls' outcomes beside ok: their refusals, and
// unknown (a caller's answer that says nothing).
func (g *gen) callOutcomes() []string {
	set := map[string]bool{}
	for _, c := range g.calls() {
		set[unknownOutcome] = true
		for _, r := range c.Refuses {
			set[r] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
