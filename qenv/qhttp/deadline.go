package qhttp

import (
	"context"
	"runtime"
	"sync"
	"time"
	"weak"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// deadlines holds, per simulation, the virtual instant of each request
// context's deadline it has seen, keyed by the deadline's wall time:
// converted once, so a deadline is used up in virtual time (a retry under
// the same context sees the time its first attempts took), and so every
// request under it, to any server, ends at the same instant. A simulation's
// table goes when the simulation is collected. Process-wide: simulations
// run in parallel share the lock, each its own table.
var deadlines = struct {
	sync.Mutex
	m map[weak.Pointer[qenv.Sim]]map[int64]time.Time
}{m: map[weak.Pointer[qenv.Sim]]map[int64]time.Time{}}

// table is s's table of deadlines; the caller holds the lock.
func table(s *qenv.Sim) map[int64]time.Time {
	key := weak.Make(s)
	tab := deadlines.m[key]
	if tab == nil {
		tab = map[int64]time.Time{}
		deadlines.m[key] = tab
		runtime.AddCleanup(s, func(k weak.Pointer[qenv.Sim]) {
			deadlines.Lock()
			delete(deadlines.m, k)
			deadlines.Unlock()
		}, key)
	}
	return tab
}

// virtualDeadline is the virtual instant of the wall-clock deadline dl in
// s: as WithTimeout recorded it, else, on first sight, now plus what is
// left of dl in real time (as if the real clock had kept pace with the
// simulation; a context made before virtual time passed still carries its
// whole timeout), rounded up by ceilGrid.
func virtualDeadline(s *qenv.Sim, dl time.Time) time.Time {
	deadlines.Lock()
	defer deadlines.Unlock()
	tab := table(s)
	v, ok := tab[dl.UnixNano()]
	if !ok {
		v = s.Now().Add(ceilGrid(s.ToVirtual(dl).Sub(s.Now())))
		tab[dl.UnixNano()] = v
	}
	return v
}

// WithTimeout is context.WithTimeout, and in a simulation it also tells the
// Transport the timeout exactly: a request under the context ends d of
// virtual time after this call, however much virtual time passes before
// it is sent and whatever real time the test process spends meanwhile.
// A plain context's deadline is known only as a wall-clock instant, whose
// remaining time the Transport rounds up to its grid (ceilGrid). A parent
// whose deadline is earlier keeps it, as with context.WithTimeout.
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(ctx, d)
	s := qenv.From(ctx)
	dl, _ := c.Deadline()
	if pd, ok := ctx.Deadline(); s == nil || ok && pd.Equal(dl) {
		return c, cancel // outside a simulation, or the parent's deadline
	}
	deadlines.Lock()
	table(s)[dl.UnixNano()] = s.Now().Add(d)
	deadlines.Unlock()
	return c, cancel
}

// grid are the values, in tenths of a decade, a remaining time is rounded
// up to: a preferred-number series on which the timeouts code sets lie
// (100 ms, 150 ms, 250 ms, 1 s, 30 s, 60 s).
var grid = [...]time.Duration{10, 15, 20, 25, 30, 40, 50, 60, 80, 100} // read-only

// ceilGrid rounds what is left of a deadline at its first sight up to the
// grid. It is the nominal timeout less the real time spent between setting
// it and sending: jitter of the test process (10 ms on a loaded CI runner),
// not virtual time, and unrounded, or rounded to the nearest, two runs of
// a seed differed. The jitter only shrinks it, so rounding up gives back a
// timeout on the grid whenever the jitter is less than the gap to the grid
// point below: at least a sixth of the timeout (20% at a power of ten,
// 50% at 200 ms), against 10% at a power of ten for one significant digit,
// where most timeouts are. A timeout off the grid is lengthened to the next
// point on it (700 ms to 800 ms, 45 s to 50 s); WithTimeout keeps it exact.
func ceilGrid(left time.Duration) time.Duration {
	if left <= 0 {
		return left
	}
	p := time.Duration(1)
	for p*10 <= left {
		p *= 10
	}
	if p < 10 {
		return left // below 10 ns: no tenths of a decade to round to
	}
	for _, m := range grid {
		if v := m * p / 10; v >= left {
			return v
		}
	}
	return left // unreachable: 100 tenths of p is 10p > left
}
