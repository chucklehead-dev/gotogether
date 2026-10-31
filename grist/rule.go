package grist

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist/internal/ruledrop"
)

// ruleInfo is a rule's registration, untyped.
type ruleInfo struct {
	name, quint string
	on          *eventInfo
	opts        options // refuses
	// check decodes an appended event's body and runs the rule on it
	check func(r *Reads, e Event) error
}

// A Rule is a check on every append of an event of type E by a command
// (NewRule): a requirement stated once, beside the event, instead of in
// every command that can append it.
type Rule[E any] struct {
	info *ruleInfo
}

// NewRule registers a rule on the events of type on: whenever a
// command's decision would append one, check runs in that command's
// transaction on the command's own Reads, after the decision and before
// the append. Its reads join the command's condition (a concurrent change
// to what it read fails the condition, and the command decides again, its
// rules with it); a Rejection it returns is the command's refusal,
// recorded and remembered as any refusal is, and added to the command's
// declared refusals in the catalog. Any other error is the code's fault,
// as a decision's is. Its options: Refuses, which it requires (the
// rejections check may return: the generated model's outcomes).
//
// Several rules run in the order the events are in the batch, and on one
// event in the order they were registered; the first refusal wins, and
// the rules after it do not run. A command that appends an event a rule
// guards declares it (Appends), so the catalog knows which commands the
// rule guards; Verify refuses a rule on an event no block appends (no
// command declares it in Appends, no processor in Emits or Delivers).
//
// Rules guard commands, a processor's Dispatch included. A processor
// whose Emit (or an effect's OnSuccess) a rule refuses fails its trigger
// (tried, then grist.failed), naming the rule: a processor's own appends
// are not guarded yet.
func NewRule[E any](app *App, name string, on *EventType[E], check func(r *Reads, e E) error, opts ...Option) *Rule[E] {
	app.register("rule", name)
	q, _ := catalog.QuintName(name)
	info := &ruleInfo{name: name, quint: q, on: on.info, opts: app.options("rule", name, opts)}
	if len(info.opts.refuses) == 0 {
		app.problems = append(app.problems, fmt.Sprintf("rule %q declares no refusal: give the rejections it returns (grist.Refuses), so its commands' models have the outcome", name))
	}
	info.check = func(r *Reads, e Event) error {
		// Rules always judge the registered newest shape, including a raw
		// older candidate accepted through the event's version chain.
		v, _, _, err := on.info.storedValue(e, on.info.version)
		if err != nil {
			return fmt.Errorf("grist: rule %s: event %s: %w", name, e.Type, err)
		}
		return check(r, v.Interface().(E))
	}
	app.rules = append(app.rules, info)
	return &Rule[E]{info: info}
}

// Name is the rule's name.
func (r *Rule[E]) Name() string { return r.info.name }

// rulesOn are the App's rules on event type t, in registration order.
func (a *App) rulesOn(t string) []*ruleInfo {
	var out []*ruleInfo
	for _, ri := range a.rules {
		if ri.on.name == t {
			out = append(out, ri)
		}
	}
	return out
}

// checkAppends is the error of a block appending events against its
// declared Appends (nil: none declared): an event of another type is the
// code's error, as is one a rule guards when it declares none.
func (a *App) checkAppends(block string, appends []EventName, events []Event) error {
	for _, e := range events {
		if appends != nil && !slices.ContainsFunc(appends, func(t EventName) bool { return t.Name() == e.Type }) {
			return fmt.Errorf("grist: %s appends %q, which its Appends does not declare (%v): add it", block, e.Type, typeNames(appends))
		}
		if rs := a.rulesOn(e.Type); appends == nil && len(rs) > 0 {
			return fmt.Errorf("grist: %s appends %q, which rule %s guards, and declares no Appends: declare the events it appends (grist.Appends(...) on its NewCommand), so the catalog and the model give it the rule's refusal", block, e.Type, rs[0].name)
		}
	}
	return nil
}

func typeNames(ts []EventName) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name()
	}
	return out
}

// ruleRefusal is a refusal of a rule: which, and the rejection.
type ruleRefusal struct {
	rule string
	rej  *Rejected
}

// applyRules runs the App's rules on events, on r (a command's Reads, or
// a processor's): the first refusal (nil for none), or the code's error
// (a rule's misuse of its reads, an error that is no rejection, a
// rejection the rule does not declare). A store error is r.err's: the
// caller retries the try.
func (a *App) applyRules(r *Reads, events []Event) (*ruleRefusal, error) {
	for _, e := range events {
		for _, ri := range a.rulesOn(e.Type) {
			if ruledrop.Dropped == ri.name {
				continue // a generated mutant's binary (gristtest.RuleMutants): the rule dropped
			}
			err := ri.check(r, e)
			if r.err != nil {
				return nil, nil
			}
			if r.bad != nil {
				return nil, fmt.Errorf("grist: rule %s: %w", ri.name, r.bad)
			}
			if err == nil {
				continue
			}
			var rej *Rejected
			if !errors.As(err, &rej) {
				return nil, fmt.Errorf("grist: rule %s: %w", ri.name, err)
			}
			if !slices.Contains(ri.opts.refuses, rej.Name) {
				return nil, fmt.Errorf("grist: rule %s refused %s, which its Refuses does not declare (%v): add it, so its commands' models have the outcome", ri.name, rej.Name, ri.opts.refuses)
			}
			return &ruleRefusal{rule: ri.name, rej: rej}, nil
		}
	}
	return nil, nil
}

// Appends declares the event types a command's decision may append: a
// decision that appends another is its error (recorded with its text, not
// remembered), naming the fix. A command that appends an event a rule
// guards (NewRule) must declare it, so the catalog knows which commands
// the rule guards; one that declares none may append any event no rule
// guards.
func Appends(types ...EventName) Option {
	return Option{name: "Appends", kinds: kindsOf("Appends"), apply: func(o *options) string {
		o.appends = []EventName{}
		for _, t := range types {
			if t == nil || reflect.ValueOf(t).IsNil() {
				return "a nil event type: give registered event types (NewEvent's)"
			}
			o.appends = append(o.appends, t)
		}
		return ""
	}}
}

// verifyRules are the problems of the App's rules: one on an event of
// another App, or on an event no block appends (a rule that can never
// run: pavlov's impossible event).
func (a *App) verifyRules() []string {
	var ps []string
	for _, ri := range a.rules {
		if a.events[ri.on.name] != ri.on {
			ps = append(ps, fmt.Sprintf("rule %q guards %q, an event of another App: register it on %s", ri.name, ri.on.name, a.Name))
			continue
		}
		if len(a.appenders(ri.on.name)) == 0 {
			ps = append(ps, fmt.Sprintf("rule %q guards %q, which no block appends (no command declares it in grist.Appends, no processor in Emits or Delivers): it can never run; declare the commands that append it, or remove the rule", ri.name, ri.on.name))
		}
	}
	return ps
}

// appenders are the blocks that declare appending events of type t:
// commands (Appends, by name, sorted) and processors (Emits, Delivers'
// success).
func (a *App) appenders(t string) []string {
	var out []string
	for _, n := range a.Commands() {
		if slices.ContainsFunc(a.commands[n].opts.appends, func(e EventName) bool { return e.Name() == t }) {
			out = append(out, n)
		}
	}
	for _, p := range a.processors {
		sh := p.opts.shape
		if slices.ContainsFunc(sh.emits, func(e EventName) bool { return e.Name() == t }) || sh.success != nil && sh.success.Name() == t {
			out = append(out, p.name)
		}
	}
	return out
}

// guards are the commands rule ri guards: those declaring its event in
// Appends, sorted.
func (a *App) guards(ri *ruleInfo) []string {
	var out []string
	for _, n := range a.Commands() {
		if slices.ContainsFunc(a.commands[n].opts.appends, func(e EventName) bool { return e.Name() == ri.on.name }) {
			out = append(out, n)
		}
	}
	return out
}
