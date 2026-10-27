package grist

import (
	"fmt"
	"strings"
)

// An Option configures a block (a command, a query or a processor). One
// type serves every block, so an option later blocks need (Authorize for
// commands and queries, slice 2) is one more constructor; an option given
// to a block it does not apply to is a problem Verify reports. The zero
// Option is invalid.
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
			a.problems = append(a.problems, fmt.Sprintf("%s %q: a zero grist.Option: build one with Unconditioned, ConditionTries, TransientTries, EffectTries or BatchSize", kind, name))
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
	return []string{"command", "query"} // TransientTries: a poller retries a transient error forever
}

// Unconditioned appends a command's events whatever commits while it
// decides: its reads are advisory, for the reason given. It brings
// grain's unique-name race back on purpose; the model's precondition
// then judges each commit (docs/grist.md §2.4). The step still records
// the reads (readPos, reads), so the model can tell what was read.
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
