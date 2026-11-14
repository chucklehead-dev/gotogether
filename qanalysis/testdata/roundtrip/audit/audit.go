// Package audit is another package of RoundTrips' fixture module: a call
// into it that takes a context is followed, not counted.
package audit

import "context"

// Log records an admission, in memory.
type Log struct{ n int }

// Note takes a context, as a client's method would, but is the module's
// own code: only what it calls could be a round trip.
func (l *Log) Note(_ context.Context, _ string) { l.n++ }
