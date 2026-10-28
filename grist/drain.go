package grist

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/drainhook"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
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

// A drainServer is a drain's server: Main's HTTP server, or a
// simulated node's API (gristsim's Cluster).
type drainServer interface {
	// keep goes on serving, not ready, until until (the delay); an error
	// is the server's own end, which ends the drain
	keep(ctx context.Context, until time.Time) error
	// shutdown stops taking requests and waits for those in flight until
	// until: errShutdownCut if some still were then
	shutdown(ctx context.Context, until time.Time) error
}

// errShutdownCut is a drain's shutdown ended by its bound with requests
// in flight.
var errShutdownCut = errors.New("the bound passed with requests in flight")

// runDrain runs d's schedule from the signal, now: the server keeps
// serving through the delay, then shuts down; the node quiets (a settle
// recorded into ctx's recorder when record) and stop ends its work; the
// relay records every committed step. Each phase within its share of the
// grace (schedule). A node that does not quiet in time is warned of, not
// an error. Main and gristsim's Cluster run it, in real and virtual time
// (qenv.Now and qenv.Sleep).
func runDrain(ctx context.Context, d Drain, srv drainServer, st Store, relay *Relay, n *node, stop func(), record bool, warn func(string)) error {
	sched := newSchedule(d, qenv.Now(ctx))
	if err := srv.keep(ctx, sched.delay(qenv.Now(ctx))); err != nil {
		return err
	}
	var err error
	if serr := srv.shutdown(ctx, sched.shutdown(qenv.Now(ctx))); serr != nil {
		err = fmt.Errorf("%s: requests still in flight when the drain's shutdown ended (%w): their clients retry under their keys", n.app.Name, serr)
	}
	// no request is left: once the processors and the relay are done with
	// what the requests committed, the run has settled, and a settle in
	// the file is what judges the bindings' eventual vals (qobs.Settled)
	if serr := settle(ctx, st, relay, n, qenv.Now(ctx), sched.settle(qenv.Now(ctx)), record); serr != nil {
		warn(fmt.Sprintf("%s: no settle recorded (the steps file's eventual vals get no verdict): %v", n.app.Name, serr))
	}
	stop()
	// every committed step into the file before it closes: a row the
	// relay had not reached would otherwise be recorded only by the next
	// node's relay, into its own file. Its own time: a shutdown that used
	// its whole timeout draining slow requests leaves the flush its own
	flush, cancel := phase(ctx, sched.flush(qenv.Now(ctx)))
	defer cancel()
	if ferr := relay.Flush(flush); err == nil {
		err = ferr
	}
	return err
}

// phase is ctx for a drain's phase ending at until: its values, not its
// cancellation, and outside a simulation the deadline (a simulation's
// phases end by virtual time: a real deadline would pass at the wall
// clock's pace).
func phase(ctx context.Context, until time.Time) (context.Context, context.CancelFunc) {
	ctx = context.WithoutCancel(ctx)
	if qenv.From(ctx) != nil {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, until)
}

// settle waits, from start until until, for n to quiet, and records a
// settle into ctx's recorder (when record): every poller waiting with
// nothing after its checkpoint (an effect in flight is work in hand),
// the relay caught up (every committed step recorded, Behind 0) and no
// tenant's head moved across two looks 20 ms apart. It returns why not
// if until passes first or the relay failed. The settle is the last
// thing the file holds of the node's run: it stops after it, with
// nothing left to do.
func settle(ctx context.Context, st Store, relay *Relay, n *node, start, until time.Time, record bool) error {
	ctx, cancel := phase(ctx, until)
	defer cancel()
	var last map[string]Position
	for {
		if err := relay.Err(); err != nil {
			return err
		}
		heads, err := st.Heads(ctx)
		quiet := err == nil && n != nil && n.idle() && maps.Equal(heads, last)
		if quiet {
			behind, berr := relay.Behind(ctx)
			quiet = berr == nil && behind == 0
		}
		if quiet {
			if record {
				qobs.Settled(ctx)
			}
			return nil
		}
		last = heads
		now := qenv.Now(ctx)
		if !now.Before(until) || ctx.Err() != nil {
			return fmt.Errorf("the node did not quiet within %v (a processor still at work, or the relay behind)", until.Sub(start).Round(time.Millisecond))
		}
		qenv.Sleep(ctx, min(settlePoll, until.Sub(now)))
	}
}

// settlePoll is how often settle looks.
const settlePoll = 20 * time.Millisecond

// simNode is a node as gristsim's Cluster drains it (drainhook).
type simNode struct{ n *node }

func (sn simNode) Drain(ctx context.Context, d any, srv drainhook.Server, stop func()) error {
	warn := func(string) {}
	if sim := qenv.From(ctx); sim != nil && sim.Log != nil {
		warn = func(msg string) { sim.Log(fmt.Sprintf("%v %s", sim.Elapsed(), msg)) }
	}
	return runDrain(ctx, d.(Drain).withDefaults(true), hookServer{srv}, sn.n.s, sn.n.relay, sn.n, stop, false, warn)
}

// hookServer is a drainhook.Server as a drain's.
type hookServer struct{ s drainhook.Server }

func (h hookServer) keep(ctx context.Context, until time.Time) error { return h.s.Keep(ctx, until) }
func (h hookServer) shutdown(ctx context.Context, until time.Time) error {
	if err := h.s.Shutdown(ctx, until); err != nil {
		return fmt.Errorf("%w: %w", errShutdownCut, err)
	}
	return nil
}

// signalKey carries Main's signal context in the node's (Terminating).
type signalKey struct{}

// Terminating returns a context derived from ctx (its values, its
// cancellation) that is also cancelled when the node is told to
// terminate: under Main at the first SIGTERM or interrupt (the drain's
// start), in a simulation at its process's termination
// (qenv.Terminating: gristsim's Cluster). Elsewhere it returns ctx.
//
// grist's own node does not stop at the signal: its effects in flight
// finish, and it stops after the drain's settle. An effect run with a
// context Terminating gave is cancelled at the signal, and an
// at-most-once one cancelled in flight has an unknown outcome
// (grist.unknown): give it only to work that may stop at once.
func Terminating(ctx context.Context) context.Context {
	if qenv.From(ctx) != nil {
		return qenv.Terminating(ctx)
	}
	sig, _ := ctx.Value(signalKey{}).(context.Context)
	if sig == nil {
		return ctx
	}
	c, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(sig, cancel)
	context.AfterFunc(c, func() { stop() }) // c ended first (ctx did): the signal's hook goes
	return c
}
