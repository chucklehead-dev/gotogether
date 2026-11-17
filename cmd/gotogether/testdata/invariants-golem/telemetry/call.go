// Package telemetry is the second package of the attempts' sites. Its
// annotations are all attempts': in publication's run, the action it
// records is attempts' too.
package telemetry

// A Call is one attempt's record.
type Call struct{ n int }

// AttemptID numbers the call's attempt, refusing past five.
//
//quint:enforces attempts.boundedAttempts
//quint:action action:attempt
func (c *Call) AttemptID() int { return min(c.n, 5) }
