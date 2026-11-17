// Package telemetry is the second package of the attempts' sites.
package telemetry

// A Call is one attempt's record.
type Call struct{ n int }

// AttemptID numbers the call's attempt, refusing past five.
//
//quint:enforces attempts.boundedAttempts
func (c *Call) AttemptID() int { return min(c.n, 5) }
