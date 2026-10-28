package grist

import (
	"fmt"
	"time"
)

// Drain is Main's schedule after SIGTERM, each phase a bound: /readyz
// fails at once and the server keeps serving for Delay (the router's
// lag: endpoints still send requests for a few seconds), then drains its
// requests (Shutdown), the node quiets and a settle is recorded
// (Settle), and the relay records every committed step (Flush). A phase
// that ends early leaves its time to the next. A zero field is its
// default (a delay of 5 s on an orchestrator, else none; 10 s for each
// other phase), a negative one none.
//
// Grace is the orchestrator's kill deadline (-grace;
// terminationGracePeriodSeconds), 0 for none. When the phases need more,
// they are budgeted into it from the deadline backwards: the flush keeps
// its share first (at most half the grace: a flush cut is commits
// unrecorded in this file), the rest shared by the delay, the shutdown
// and the settle in proportion (a settle skipped is an unjudged eventual
// val, a request cut a client's retry under its key), and each phase
// ends in time to leave the later ones their shares.
type Drain struct {
	// Delay is how long the server keeps serving, not ready, before it
	// drains (default 5 s on an orchestrator, else none).
	Delay time.Duration
	// Shutdown bounds the server's drain of its requests (default 10 s).
	Shutdown time.Duration
	// Settle bounds the wait for the node to quiet before a settle is
	// recorded (default 10 s).
	Settle time.Duration
	// Flush bounds the relay's recording of every committed step
	// (default 10 s).
	Flush time.Duration
	// Grace is the orchestrator's kill deadline after SIGTERM (-grace), 0
	// for none: the phases are budgeted into it.
	Grace time.Duration
}

// drainDelay is the drain's default Delay on an orchestrator: endpoints
// take a few seconds to stop sending a terminating pod requests.
const drainDelay = 5 * time.Second

// WithDrain sets Main's drain schedule (Drain's zero fields keep their
// defaults); -grace, when given, overrides its Grace.
func WithDrain(d Drain) ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.drain = d }}
}

// withDefaults is d with its zero fields' defaults and its negative ones
// 0.
func (d Drain) withDefaults(orchestrator bool) Drain {
	def := func(v, dflt time.Duration) time.Duration {
		switch {
		case v < 0:
			return 0
		case v == 0:
			return dflt
		}
		return v
	}
	delay := time.Duration(0)
	if orchestrator {
		delay = drainDelay
	}
	return Drain{Delay: def(d.Delay, delay), Shutdown: def(d.Shutdown, shutdownTimeout),
		Settle: def(d.Settle, settleTimeout), Flush: def(d.Flush, flushTimeout), Grace: max(d.Grace, 0)}
}

// needs is the grace the phases need: their sum.
func (d Drain) needs() time.Duration { return d.Delay + d.Shutdown + d.Settle + d.Flush }

// budget is d's phases' shares of its Grace: d itself when they fit (or
// no grace is given), else the flush's first (at most half the grace)
// and the rest in proportion, each rounded down to the millisecond.
func (d Drain) budget() Drain {
	if d.Grace <= 0 || d.needs() <= d.Grace {
		return d
	}
	b := Drain{Grace: d.Grace}
	b.Flush = min(d.Flush, d.Grace/2)
	rest, others := d.Grace-b.Flush, d.Delay+d.Shutdown+d.Settle
	if others == 0 {
		b.Flush = min(d.Flush, d.Grace)
		return b
	}
	share := func(v time.Duration) time.Duration {
		return time.Duration(float64(rest) * float64(v) / float64(others)).Truncate(time.Millisecond)
	}
	b.Delay, b.Shutdown, b.Settle = share(d.Delay), share(d.Shutdown), share(d.Settle)
	return b
}

// line is Main's startup line's account of d (defaults applied): what it
// needs, and what -grace leaves each phase.
func (d Drain) line() string {
	s := fmt.Sprintf("drain needs a grace of %v (delay %v, shutdown %v, settle %v, flush %v)", d.needs(), d.Delay, d.Shutdown, d.Settle, d.Flush)
	switch b := d.budget(); {
	case d.Grace <= 0:
		s += "; -grace budgets it into a shorter one"
	case b == d:
		s += fmt.Sprintf("; -grace %v fits it", d.Grace)
	default:
		s += fmt.Sprintf("; -grace %v leaves delay %v, shutdown %v, settle %v, flush %v", d.Grace, b.Delay, b.Shutdown, b.Settle, b.Flush)
	}
	return s
}

// A schedule is a drain under way: its phases' bounds (d), their shares
// of the grace (shares) and the kill deadline (zero for none).
type schedule struct {
	d, shares Drain
	deadline  time.Time
}

// newSchedule starts d's schedule at the signal, at now.
func newSchedule(d Drain, now time.Time) schedule {
	s := schedule{d: d, shares: d.budget()}
	if d.Grace > 0 {
		s.deadline = now.Add(d.Grace)
	}
	return s
}

// end is when a phase begun at now with bound phase ends: at its bound,
// or earlier, in time to leave the later phases their shares (later)
// before the deadline.
func (s schedule) end(now time.Time, phase, later time.Duration) time.Time {
	e := now.Add(phase)
	if !s.deadline.IsZero() {
		if d := s.deadline.Add(-later); d.Before(e) {
			e = d
		}
	}
	return e
}

// delay, shutdown, settle and flush are each phase's end, begun at now.
func (s schedule) delay(now time.Time) time.Time {
	return s.end(now, s.shares.Delay, s.shares.Shutdown+s.shares.Settle+s.shares.Flush)
}
func (s schedule) shutdown(now time.Time) time.Time {
	return s.end(now, s.d.Shutdown, s.shares.Settle+s.shares.Flush)
}
func (s schedule) settle(now time.Time) time.Time { return s.end(now, s.d.Settle, s.shares.Flush) }
func (s schedule) flush(now time.Time) time.Time  { return s.end(now, s.d.Flush, 0) }
