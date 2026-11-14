// Package limit is RoundTrips' fixture: a limiter on a shared store,
// whose Admit reads the count and then reserves against it, two round
// trips one model action takes in one step.
package limit

import (
	"context"
	"errors"
	"sync"

	"example.com/roundtripfixture/audit"
)

// Store is the shared state the limiter's gateways use.
type Store interface {
	Get(ctx context.Context, key string) (int, error)
	Reserve(ctx context.Context, key string, n int) error
	Release(ctx context.Context, key string, n int) error
	Down() bool
}

// Limiter admits at most Max calls per key.
type Limiter struct {
	S   Store
	Max int
	Log audit.Log
}

var errFull = errors.New("full")

// Admit reads the count, then reserves: another gateway's reservation
// between them goes over Max, which no connect trace interleaves.
func (l *Limiter) Admit(ctx context.Context, key string) error {
	used, err := l.S.Get(ctx, key)
	if err != nil {
		return err
	}
	if used >= l.Max {
		return errFull
	}
	return l.reserve(ctx, key)
}

func (l *Limiter) reserve(ctx context.Context, key string) error { return l.S.Reserve(ctx, key, 1) }

// Done releases a call: one round trip.
func (l *Limiter) Done(ctx context.Context, key string) error {
	if l.S.Down() { // no context: not a round trip
		return nil
	}
	l.Log.Note(ctx, key) // the module's own package: followed, not counted
	return l.S.Release(ctx, key, 1)
}

// Audit reads the count twice.
func (l *Limiter) Audit(ctx context.Context) error {
	if _, err := l.S.Get(ctx, "a"); err != nil {
		return err
	}
	_, err := l.S.Get(ctx, "b")
	return err
}

// Mem is the store in memory; its methods make no round trips of their own.
type Mem struct {
	mu sync.Mutex
	n  map[string]int
}

func (m *Mem) Get(_ context.Context, key string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n[key], nil
}

func (m *Mem) Reserve(_ context.Context, key string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n == nil {
		m.n = map[string]int{}
	}
	m.n[key] += n
	return nil
}

func (m *Mem) Release(ctx context.Context, key string, n int) error { return m.Reserve(ctx, key, -n) }

func (m *Mem) Down() bool { return false }
