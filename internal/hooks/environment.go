package hooks

import "context"

// SimEnvironment is the environment policy captured before a driven run.
// Passing it through the run context prevents simulations from re-reading
// process configuration after the execution profile has been resolved.
type SimEnvironment struct {
	AllFaults bool
	Check     bool
}

type simEnvironmentKey struct{}

// WithSimEnvironment carries a value copy of the resolved policy.
func WithSimEnvironment(ctx context.Context, e SimEnvironment) context.Context {
	return context.WithValue(ctx, simEnvironmentKey{}, e)
}

// SimEnvironmentOf returns a driven run's policy, if one was installed.
func SimEnvironmentOf(ctx context.Context) (SimEnvironment, bool) {
	e, ok := ctx.Value(simEnvironmentKey{}).(SimEnvironment)
	return e, ok
}
