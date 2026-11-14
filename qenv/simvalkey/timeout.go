package simvalkey

import (
	"context"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// WithTimeout is a simulation-time context deadline. Outside qenv it is
// context.WithTimeout. Inside, a passive virtual timer cancels the context;
// callers must still cancel when done. Real context deadlines inside qenv
// are refused: their wall-clock timers cannot drive replayable virtual time.
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	s := qenv.From(ctx)
	if s == nil {
		return context.WithTimeout(ctx, d)
	}
	if dl, ok := ctx.Deadline(); ok {
		if known, ok := ctx.Value(timeoutKey{}).(*timeoutContext); !ok || !known.deadline.Equal(dl) {
			panic("simvalkey: use WithTimeout for simulation deadlines, not context.WithTimeout")
		}
	}
	deadline := s.ToReal(s.Now().Add(d))
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	values, setCause := context.WithCancelCause(context.WithoutCancel(ctx))
	c := &timeoutContext{values: values, setCause: setCause, parent: ctx, deadline: deadline, done: make(chan struct{}), callbacks: map[uint64]func(){}}
	c.syncParent()
	cancel := func() { c.cancel(context.Canceled) }
	if d <= 0 {
		c.cancel(context.DeadlineExceeded)
		return c, cancel
	}
	qenv.Go(ctx, "valkey.timeout", func(tctx context.Context) {
		qenv.Daemon(tctx)
		defer c.cancel(context.Canceled)
		if !qenv.WaitFor(tctx, func() bool { return c.Err() != nil }, d) {
			c.cancel(context.DeadlineExceeded)
		}
	})
	return c, cancel
}

type timeoutKey struct{}

// The owned Done channel differs from the cause context's Done channel,
// forcing standard context children to use AfterFunc and our actual Err.
// A separate WithCancelCause preserves custom causes without inheriting its
// Canceled error for a virtual deadline.
type timeoutContext struct {
	values, parent context.Context
	setCause       context.CancelCauseFunc
	deadline       time.Time
	done           chan struct{}
	mu             sync.Mutex
	err            error
	next           uint64
	callbacks      map[uint64]func()
}

func (c *timeoutContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c *timeoutContext) Value(key any) any {
	if _, ok := key.(timeoutKey); ok {
		return c
	}
	return c.values.Value(key)
}
func (c *timeoutContext) syncParent() {
	if err := contextError(c.parent); err != nil {
		cause := context.Cause(c.parent)
		if cause == nil {
			cause = err
		}
		c.cancelCause(err, cause)
	}
}
func (c *timeoutContext) Done() <-chan struct{} { c.syncParent(); return c.done }
func (c *timeoutContext) Err() error            { c.syncParent(); c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *timeoutContext) cancel(err error)      { c.cancelCause(err, err) }
func (c *timeoutContext) cancelCause(err, cause error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	c.setCause(cause)
	close(c.done)
	callbacks := c.callbacks
	c.callbacks = nil
	c.mu.Unlock()
	for _, f := range callbacks {
		f()
	}
}

// AfterFunc lets standard context children link cancellation without a real
// goroutine polling this virtual context's Done channel.
func (c *timeoutContext) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		go f() //qenv:ok context.AfterFunc requires async dispatch here to avoid child propagation-lock deadlock
		return func() bool { return false }
	}
	c.next++
	id := c.next
	c.callbacks[id] = f
	c.mu.Unlock()
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, ok := c.callbacks[id]; !ok {
			return false
		}
		delete(c.callbacks, id)
		return true
	}
}

// A standard context child of a value-wrapped virtual context may propagate
// cancellation on a runtime goroutine. The adapter observes the owner too so
// virtual scheduling cannot outrun that propagation.
func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			if owner, ok := ctx.Value(timeoutKey{}).(*timeoutContext); ok {
				if _, direct := ctx.(*timeoutContext); !direct && owner.Err() == nil {
					panic("simvalkey: a wall-clock child deadline fired before its virtual deadline; use WithTimeout")
				}
			}
		}
		return err
	}
	if _, direct := ctx.(*timeoutContext); !direct {
		if owner, ok := ctx.Value(timeoutKey{}).(*timeoutContext); ok {
			return owner.Err()
		}
	}
	return nil
}
