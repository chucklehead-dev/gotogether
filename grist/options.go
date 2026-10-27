package grist

import (
	"fmt"
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
			a.problems = append(a.problems, fmt.Sprintf("%s %q: a zero grist.Option: build one with Authorize, Internal, Unconditioned, ConditionTries, TransientTries, EffectTries or BatchSize", kind, name))
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
	case "Unconditioned", "ConditionTries":
		return []string{"command"}
	case "EffectTries", "BatchSize":
		return []string{"processor"}
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
