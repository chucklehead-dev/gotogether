// Package lease is a lock that grants leases and the store it guards,
// checked under clock faults. A holder measures its lease on its own
// monotonic clock (qenv.Mono and Since, as time.Since does) and stops
// writing a margin before it ends; the store refuses a write of an epoch
// older than one it has accepted (a fencing token). The requirement
// (model/lease.qnt) is the store's: no write of an older epoch after a
// newer one.
//
// The mutants trust clocks instead. NoFencing's store accepts every
// write: correct while clocks are true, since the holder's margin covers
// its pauses, and still under skews and jumps, which a monotonic clock
// does not see; broken by a drift larger than the margin. WallInterval
// times the lease on the wall clock (Now().Sub, as a time that lost its
// monotonic reading would): broken by a jump. AbsoluteExpiry compares the
// lock's expiry time with the holder's own wall clock: broken by a skew
// alone.
package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Mutation injects a bug.
type Mutation int

const (
	Faithful Mutation = iota
	// NoFencing: the store accepts every write; the holders' lease
	// check is all that keeps an old holder out.
	NoFencing
	// AbsoluteExpiry: as NoFencing, and the holder compares the lock's
	// expiry time with its own clock instead of timing its lease itself.
	AbsoluteExpiry
	// WallInterval: as NoFencing, and the holder times its lease on its
	// wall clock (Now().Sub) instead of its monotonic one.
	WallInterval
)

// Timing.
const (
	TTL    = 2 * time.Second        // a lease, on the lock's clock
	Margin = 300 * time.Millisecond // a holder stops this long before its lease ends
	Pause  = 100 * time.Millisecond // a holder may pause up to 2 of these before a write (a GC, a slow disk)
)

// Holders are the processes that contend for the lock.
var Holders = []string{"a", "b"}

// ErrStale is the store refusing a write of an older epoch.
var ErrStale = staleErr{}

type staleErr struct{}

func (staleErr) Error() string        { return "lease: stale epoch" }
func (staleErr) QuintOutcome() string { return "stale" }

// Lock grants leases: the next epoch, when the last lease has run out on
// the lock's own clock.
type Lock struct {
	epoch   int
	holder  string
	expires time.Duration // on the lock's monotonic clock, as Go's time.Now().Before compares two readings
}

// Grant is a lease: its epoch, and when it ends on the lock's wall clock
// (which only AbsoluteExpiry reads).
type Grant struct {
	Epoch   int
	Expires time.Time
}

func (l *Lock) acquire(ctx context.Context, h string) (Grant, error) {
	now := qenv.Mono(ctx) // the lock's monotonic clock
	if l.holder != "" && now < l.expires {
		return Grant{}, errors.New("lease: held")
	}
	l.epoch++
	l.holder, l.expires = h, now+TTL
	qobs.Record(ctx, "grant", nil, "h", h, "e", l.epoch)
	return Grant{l.epoch, qenv.Now(ctx).Add(TTL)}, nil
}

// Store keeps the latest value, and the newest epoch it has accepted.
type Store struct {
	Mutation Mutation
	newest   int
	Value    string
}

// Write is a holder's write under its epoch.
type Write struct {
	H     string
	Epoch int
	Value string
}

func (s *Store) write(ctx context.Context, w Write) (struct{}, error) {
	if s.Mutation == Faithful && w.Epoch < s.newest {
		qobs.Record(ctx, "write", ErrStale, "h", w.H, "e", w.Epoch)
		return struct{}{}, ErrStale
	}
	s.newest = max(s.newest, w.Epoch)
	s.Value = w.Value
	qobs.Record(ctx, "write", nil, "h", w.H, "e", w.Epoch)
	return struct{}{}, nil
}

// Deployment is the lock, the store and the holders in one simulation.
type Deployment struct {
	Lock  *Lock
	Store *Store
}

// Run deploys the system on c with the holders' clock faults, and runs it
// for 10 s of virtual time.
func Run(c qenv.Chooser, m Mutation, clocks qenv.Clocks) (*Deployment, error) {
	return RunStalled(c, m, clocks, qenv.Stalls{})
}

// RunStalled is Run with the processes' pauses and slowdowns (qenv.Stalls).
func RunStalled(c qenv.Chooser, m Mutation, clocks qenv.Clocks, stalls qenv.Stalls) (*Deployment, error) {
	s := qenv.New(c)
	defer s.Close()
	s.Clocks, s.Stalls = clocks, stalls
	d := &Deployment{Lock: &Lock{}, Store: &Store{Mutation: m}}
	lock := qenv.NewService(s, "lock", nil, d.Lock.acquire)
	store := qenv.NewService(s, "store", nil, d.Store.write)
	for _, h := range Holders {
		s.Process(h, false, func(ctx context.Context) {
			qenv.Daemon(ctx)
			for n := 0; ; n++ {
				start, wall := qenv.Mono(ctx), qenv.Now(ctx) // the holder's clocks, before it asks
				g, err := lock.Call(ctx, h, time.Second)
				if err != nil {
					qenv.Sleep(ctx, 500*time.Millisecond)
					continue
				}
				valid := func() bool {
					switch m {
					case AbsoluteExpiry:
						return qenv.Now(ctx).Before(g.Expires.Add(-Margin))
					case WallInterval:
						return qenv.Now(ctx).Sub(wall) < TTL-Margin
					}
					return qenv.Since(ctx, start) < TTL-Margin
				}
				for i := 0; valid(); i++ {
					qenv.Sleep(ctx, time.Duration(s.Choose("sched.pause."+h, 3))*Pause)
					_, _ = store.Call(ctx, Write{h, g.Epoch, fmt.Sprintf("%s%d.%d", h, n, i)}, time.Second)
					qenv.Sleep(ctx, 200*time.Millisecond)
				}
			}
		})
	}
	if err := s.Run(10 * time.Second); err != nil {
		return d, err
	}
	return d, nil
}
