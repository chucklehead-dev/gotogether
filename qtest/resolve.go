package qtest

import (
	"github.com/chucklehead-dev/gotogether/validate"
)

// DefaultResolveBudget is how many resolutions ResolvePending checks per
// actor at most.
const DefaultResolveBudget = validate.DefaultResolveBudget

// ResolvePending makes Record, Validate and explorations search what each call left
// pending (qobs.Invoke with no Done: the caller crashed or never heard
// back) may have done, as Jepsen treats an :info operation: either it had
// no effect, or it took effect after one of the steps recorded at or
// after its invocation and before the first settle after it (the system
// was quiet there), with no outcome or observation (its step is
// Ambiguous: no expect is checked after it). The actor conforms if some
// resolution does; the report says which, as an issue, and the run's
// Steps are the resolution checked. When none conforms, the one that got
// furthest is shown and the actor fails as it does. At most
// DefaultResolveBudget resolutions per actor, the fewest effects first; a
// search cut short is no verdict, as Linearize's. Pending calls stay
// pending (Completes still fails of them): their callers never knew.
//
// A resolution keeps the recorded order of the steps. With Linearize (or
// LinearizeByKey) too, both are searched: a pending call may take effect
// anywhere real time allows in an order of the steps, or not at all
// (Jepsen's indeterminate calls, qtrace.LinearizePending), and the report
// says which did.
func ResolvePending() Option {
	return option("ResolvePending", func(c *settings) { c.resolvePending = true })
}
