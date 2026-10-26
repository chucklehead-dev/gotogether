package qhttp

import (
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

// virtualDeadline is the virtual instant of the wall-clock deadline dl in
// s: on first sight, now plus what is left of dl in real time (as if the
// real clock had kept pace with the simulation; a context made before
// virtual time passed still carries its whole timeout), rounded by
// roundNear.
func virtualDeadline(s *qenv.Sim, dl time.Time) time.Time {
	key := weak.Make(s)
	deadlines.Lock()
	defer deadlines.Unlock()
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
	v, ok := tab[dl.UnixNano()]
	if !ok {
		v = s.Now().Add(roundNear(s.ToVirtual(dl).Sub(s.Now())))
		tab[dl.UnixNano()] = v
	}
	return v
}

// roundNear rounds what is left of a deadline at its first sight to the
// nearest two significant digits (148.7 ms to 150 ms, 1.9993 s to 2 s).
// What it lacks of the timeout set is the real time spent between setting
// it and sending: jitter of the test process (a pause of the runtime once
// took 2 ms of a 150 ms deadline), not virtual time, and unrounded, two
// runs of a seed differed. A timeout of two significant digits is stable
// under jitter of either sign below half its last digit (0.5% to 5% of
// it), a power of ten under jitter that shortens it below half the finer
// digit (9.97 ms is 10 ms). Any other timeout moves by at most that half.
func roundNear(left time.Duration) time.Duration {
	if left <= 0 {
		return left
	}
	g := time.Duration(1)
	for g*100 <= left {
		g *= 10
	}
	return (left + g/2) / g * g
}
