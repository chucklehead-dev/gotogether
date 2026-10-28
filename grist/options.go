package grist

import (
	"fmt"
	"slices"
	"strings"
)

// An Option configures a block (a command, a query or a processor). One
// type serves every block (Authorize and Internal for commands and
// queries too), so an option a later block needs is one more
// constructor; an option given to a block it does not apply to is a
// problem Verify reports. The zero Option is invalid.
type Option struct {
	name  string
	kinds []string // the blocks it applies to
	apply func(*options) string
}

// options are a block's settings, the defaults below unless an Option
// says otherwise.
type options struct {
	unconditioned  string
	conditionTries int
	transientTries int
	effectTries    int
	batchSize      int
	// authorize is who an adapter serves the block to (Authorize);
	// internal says no adapter serves it (Internal). Neither: an adapter
	// refuses to serve the App (deny by default, docs/grist.md §4.3)
	authorize Policy
	internal  bool
	// refuses are a command's declared rejections (Refuses), nil when it
	// declares none; shape is a processor's declared result (Emits,
	// Delivers): its delivery, the event types it emits, its success
	// event's type
	refuses []string
	shape   shape
	// appends are a command's declared event types (Appends), nil when it
	// declares none
	appends  []EventName
	declared int // how many of Emits and Delivers were given
	// keyed says a call's dependency applies each key once (Keyed); uses
	// are the calls a processor's effect makes (Uses)
	keyed bool
	uses  []*callInfo
}

// shape is a processor's declared result: 0 when it declares none.
type shape struct {
	kind     resultKind // emitResult or effectResult
	delivery Delivery
	emits    []EventName
	success  EventName // nil: no OnSuccess
}

// The defaults of the tries and the batch size.
const (
	defaultConditionTries = 5
	defaultTransientTries = 10
	defaultEffectTries    = 3
	defaultBatchSize      = 100
)

func defaultOptions() options {
	return options{conditionTries: defaultConditionTries, transientTries: defaultTransientTries,
		effectTries: defaultEffectTries, batchSize: defaultBatchSize}
}

// options applies opts to kind's defaults, noting a problem for each
// option that is zero, invalid or not kind's.
func (a *App) options(kind, name string, opts []Option) options {
	o := defaultOptions()
	for _, opt := range opts {
		if opt.apply == nil {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: a zero grist.Option: build one with its constructor (Authorize, Internal, Unconditioned, Refuses, Appends, ConditionTries, TransientTries, EffectTries, BatchSize, Emits, Delivers, Uses, Keyed)", kind, name))
			continue
		}
		ok := false
		for _, k := range opt.kinds {
			ok = ok || k == kind
		}
		if !ok {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: option %s applies to %s blocks only: remove it", kind, name, opt.name, strings.Join(opt.kinds, " and ")))
			continue
		}
		if p := opt.apply(&o); p != "" {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: option %s: %s", kind, name, opt.name, p))
		}
	}
	if o.declared > 1 {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: more than one of Emits and Delivers: a processor declares one result shape", kind, name))
	}
	for _, e := range append(append(append([]EventName(nil), o.shape.emits...), o.shape.success), o.appends...) {
		if e != nil && a.events[e.Name()] != e.registered() {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: it declares %q, an event of another App: register it on %s", kind, name, e.Name(), a.Name))
		}
	}
	if o.authorize != nil && o.internal {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: both Authorize and Internal: an internal block is served to no one; drop one", kind, name))
	}
	return o
}

func tries(name string, n int, set func(*options, int)) Option {
	return Option{name: name, kinds: kindsOf(name), apply: func(o *options) string {
		if n < 1 {
			return fmt.Sprintf("%d: give at least 1", n)
		}
		set(o, n)
		return ""
	}}
}

func kindsOf(option string) []string {
	switch option {
	case "Unconditioned", "ConditionTries", "Appends":
		return []string{"command"}
	case "EffectTries", "BatchSize", "Emits", "Delivers", "Uses":
		return []string{"processor"}
	case "Refuses":
		return []string{"command", "call", "rule"}
	case "Keyed":
		return []string{"call"}
	}
	return []string{"command", "query"} // TransientTries (a poller retries a transient error forever), Authorize, Internal
}

// A Policy is who an adapter (the HTTP API, NewHandler) serves a command
// or query to: a predicate over the request's actor (WithActor, set by
// the application's authentication) and its tenant. It runs in the
// adapter, never in a decision: a processor's dispatch is subject to no
// user's policy.
type Policy func(actor, tenant string) bool

// Authenticated is the policy of any authenticated actor: one whose
// request carries an actor (WithActor), whatever the tenant.
func Authenticated(actor, tenant string) bool { return actor != "" }

// Actors is the policy of the named actors only, in every tenant.
func Actors(names ...string) Policy {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(actor, tenant string) bool { return actor != "" && set[actor] }
}

// Authorize serves a command or query to the actors p admits, at every
// adapter. Every command and query is denied by default: an adapter
// refuses to serve an App with a block that has neither Authorize nor
// Internal (NewHandler's error), and answers a request p refuses with
// 403 (ErrDenied).
func Authorize(p Policy) Option {
	return Option{name: "Authorize", kinds: kindsOf("Authorize"), apply: func(o *options) string {
		if p == nil {
			return "a nil Policy: give one (Authenticated, Actors, or a func(actor, tenant string) bool)"
		}
		o.authorize = p
		return ""
	}}
}

// Internal marks a command or query no adapter serves: only processors
// dispatch it, and tests call it directly (App.Execute, App.Query).
func Internal() Option {
	return Option{name: "Internal", kinds: kindsOf("Internal"), apply: func(o *options) string {
		o.internal = true
		return ""
	}}
}

// Unconditioned appends a command's events whatever commits while it
// decides: its reads are advisory, for the reason given. It brings
// grain's unique-name race back on purpose; the model's precondition
// then judges each commit (docs/grist.md §2.4). The step still records
// the reads (readPos, reads), so the model can tell what was read.
//
// No isolation level of the store protects it: the decision's reads are
// pinned before the append, each a read of its own, and the append's
// transaction begins after them. Under Serializable or Snapshot a race
// whose two appends overlap makes the loser decide again (a transient
// conflict), but a decision that read before the other's commit and
// appends after it commits on what it read, as at ReadCommitted. Only
// the condition (the default) closes the window, at every level.
func Unconditioned(reason string) Option {
	return Option{name: "Unconditioned", kinds: kindsOf("Unconditioned"), apply: func(o *options) string {
		if reason == "" {
			return "no reason given: say why the reads are advisory"
		}
		o.unconditioned = reason
		return ""
	}}
}

// ConditionTries is how many times a command decides on fresh reads when
// its condition fails, before it is refused as a conflict (default 5).
func ConditionTries(n int) Option {
	return tries("ConditionTries", n, func(o *options, n int) { o.conditionTries = n })
}

// TransientTries is a command's or query's budget of transient store
// errors (a lost race for the tenant's lock, a commit of unknown
// outcome), apart from a command's condition tries (default 10). A
// processor's poller has none: it backs off and tries again, forever.
func TransientTries(n int) Option {
	return tries("TransientTries", n, func(o *options, n int) { o.transientTries = n })
}

// EffectTries is how many times a processor's handler or effect error is
// tried before its trigger is checkpointed with a grist.failed event
// (default 3). An application's model states the same number (grist.qnt's
// atLeastOnceDone takes it).
func EffectTries(n int) Option {
	return tries("EffectTries", n, func(o *options, n int) { o.effectTries = n })
}

// BatchSize is how many triggers a processor's poller reads at a time
// (default 100).
func BatchSize(n int) Option {
	return tries("BatchSize", n, func(o *options, n int) { o.batchSize = n })
}

// Refuses declares the rejections a command's decision may return (each
// a Rejection's value), so the model generator (Generate) writes an
// outcome branch for each and the binding an outcome. On a rule
// (NewRule), the rejections its check may return, which every command it
// guards may then refuse with (required). On a call
// (NewCall), the dependency's answers that say the call did not happen
// (a declined payment): Do returns each as NotApplied of its *Rejected. A decision that
// returns a rejection it does not declare is its error (recorded with its
// text, not remembered), naming the fix: a declaration the code
// contradicts would make the generated model wrong silently. A command
// that declares none may refuse with any rejection, and its generated
// model has none.
func Refuses(rejections ...error) Option {
	return Option{name: "Refuses", kinds: kindsOf("Refuses"), apply: func(o *options) string {
		o.refuses = []string{}
		for _, r := range rejections {
			rej, ok := r.(*Rejected)
			if !ok || rej == nil || rej.Name == "" {
				return fmt.Sprintf("%v is no rejection: give values of grist.Rejection", r)
			}
			if rej.Name == ErrKeyReused.(*Rejected).Name {
				return "idempotency-key-reused is grist's own refusal, never a decision's: remove it"
			}
			if reservedOutcomes[rej.Name] {
				// the generated model and bindings give these names their
				// own meaning: a refusal of one would be judged as a commit,
				// a failed condition, a 403 or the code's error
				return fmt.Sprintf("a rejection named %q: grist's steps already mean something by it (ok, conflict, denied, error): give the rejection another name", rej.Name)
			}
			o.refuses = append(o.refuses, rej.Name)
		}
		return ""
	}}
}

// reservedOutcomes are the outcomes a step of grist's has without its
// decision's refusal: a commit (ok), a failed condition (conflict), the
// API's 403 at the clients (denied) and the code's error (error).
// Read-only.
var reservedOutcomes = map[string]bool{"ok": true, conflictOutcome: true, "denied": true, "error": true}

// Emits declares a processor whose handler returns Emit (of events of
// the given types only) or Skip: exactly once in the log, its appends
// committed with its checkpoint. The model generator writes its action
// from it. A handler that returns anything else (an Effect, an event of
// another type) fails its trigger (Fail: tried EffectTries times, then
// grist.failed), naming the fix.
func Emits(types ...EventName) Option {
	return Option{name: "Emits", kinds: kindsOf("Emits"), apply: func(o *options) string {
		for _, t := range types {
			if t == nil {
				return "a nil event type: give registered event types (NewEvent's)"
			}
		}
		o.declared++
		o.shape = shape{kind: emitResult, emits: types}
		return ""
	}}
}

// Delivers declares a processor whose handler returns an Effect of
// delivery d, with OnSuccess of an event of type success (at most one;
// none: no success event). The model generator writes its effect,
// checkpoint and outcome actions from it (grist.qnt's at-least-once or
// at-most-once defs). A handler that returns another delivery, no
// success event where one is declared or another's, or an Emit, fails
// its trigger (Fail), naming the fix: the guarantee the model judges is
// the declared one. A Skip or a handler's Fail is allowed too: its
// trigger's checkpoint commits with no effect, recorded as the
// processor's skip step (<processor>Skip, Fail's with its grist.failed),
// which the generated model takes where the app's rule <processor>Skips
// says the handler skips the trigger (docs/grist.md §0.11); a Fail, whose
// grist.failed the skip's empty batch does not match, is a mismatch, as
// an Emits processor's is.
func Delivers(d Delivery, success ...EventName) Option {
	return Option{name: "Delivers", kinds: kindsOf("Delivers"), apply: func(o *options) string {
		if d != AtLeastOnce && d != AtMostOnce {
			return fmt.Sprintf("delivery %d: give AtLeastOnce or AtMostOnce", d)
		}
		if len(success) > 1 {
			return "more than one success event: an effect's OnSuccess appends one"
		}
		o.declared++
		o.shape = shape{kind: effectResult, delivery: d}
		if len(success) == 1 {
			if success[0] == nil {
				return "a nil success event type: give a registered event type, or none"
			}
			o.shape.success = success[0]
		}
		return ""
	}}
}

// conforms is res's contradiction of the processor's declared shape, if
// any: nil when it declares none, and for Skip and Fail whatever it
// declares.
func (s shape) conforms(processor string, res Result) error {
	if s.kind == 0 || res.kind == failResult || res.kind == emitResult && len(res.events) == 0 {
		return nil
	}
	switch {
	case s.kind == emitResult && res.kind != emitResult:
		return fmt.Errorf("grist: processor %s returned an Effect, but it declares Emits: return Emit or Skip, or declare Delivers", processor)
	case s.kind == emitResult:
		for _, e := range res.events {
			if !slices.ContainsFunc(s.emits, func(t EventName) bool { return t.Name() == e.Type }) {
				return fmt.Errorf("grist: processor %s emits %q, which its Emits does not declare: add it", processor, e.Type)
			}
		}
	case res.kind != effectResult:
		return fmt.Errorf("grist: processor %s returned Emit, but it declares Delivers: return an Effect, or declare Emits", processor)
	case res.delivery != s.delivery:
		return fmt.Errorf("grist: processor %s returned an Effect %s, but it declares Delivers(%s): make them agree", processor, res.delivery, s.delivery)
	case s.success == nil && res.onSuccess != nil:
		return fmt.Errorf("grist: processor %s's Effect has OnSuccess(%s), which its Delivers does not declare: add it", processor, res.onSuccess.Type)
	case s.success != nil && (res.onSuccess == nil || res.onSuccess.Type != s.success.Name()):
		return fmt.Errorf("grist: processor %s's Effect lacks OnSuccess of %s, which its Delivers declares: append it, or drop it from Delivers", processor, s.success.Name())
	}
	return nil
}
