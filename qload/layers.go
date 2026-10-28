package qload

import "time"

// Layered is the retry policy of two loops, one inside the other: a
// proxy's (a Gateway's, a mesh's) inner tries per attempt of the outer
// policy (the client's, outer; nil: retries at once), the proxy's at
// once. Attempts multiply: a request makes up to inner × the outer's
// attempts (set ClientConfig.Retries to that less one), and the outer
// policy sees, and budgets, only its own retries, so a client's budget
// does not bound the proxy's (DESIGN §11.6: a budget must cover every
// loop). Retry n (1-based) is the proxy's unless n is a multiple of
// inner, when the proxy has spent its tries and the outer policy's
// retry n/inner decides.
func Layered(inner int, outer Retrier) Retrier {
	return &layered{inner: max(inner, 1), outer: outer}
}

type layered struct {
	inner int
	outer Retrier
}

func (l *layered) Request(now time.Time) {
	if l.outer != nil {
		l.outer.Request(now)
	}
}

func (l *layered) Retry(n int, now time.Time) (time.Duration, bool) {
	if n%l.inner != 0 {
		return 0, true // the proxy's next try, at once
	}
	if l.outer == nil {
		return 0, true
	}
	return l.outer.Retry(n/l.inner, now)
}
