// Package memo caches values computed once per key, for the core's
// process-wide caches (parsed templates, typechecked models, compiled
// evaluators). Each cache was a sync.Map with its own type assertions,
// and most computed a missing key in every goroutine that asked for it
// at once: two parallel checks of one model typechecked it twice.
package memo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// A Map holds a value per key, each computed by the first caller that
// asks for it while callers asking for the same key wait for it. The
// zero Map is empty and ready to use; a Map must not be copied.
type Map[K comparable, V any] struct {
	m sync.Map // K -> *entry[V]
}

type entry[V any] struct {
	started atomic.Bool
	done    chan struct{}
	v       V
	err     error
}

// Once is k's value, computed by f the first time: f's result is kept
// whatever it is (an error a value holds included).
func (m *Map[K, V]) Once(k K, f func() V) V {
	e := m.entry(k)
	if e.started.CompareAndSwap(false, true) {
		func() { defer close(e.done); e.v = f() }()
	} else {
		<-e.done
	}
	return e.v
}

// Do is k's value, computed by f the first time it succeeds. Errors are
// shared with waiting callers, and the next call computes again.
func (m *Map[K, V]) Do(k K, f func() (V, error)) (V, error) {
	e := m.entry(k)
	if e.started.CompareAndSwap(false, true) {
		m.compute(k, e, f)
	} else {
		<-e.done
	}
	return e.v, e.err
}

// DoContext is Do with caller-owned cancellation. A waiting caller can
// leave without cancelling another caller's computation. If that owner
// was cancelled, a still-live waiter retries with its own function and
// context rather than inheriting the owner's cancellation.
func (m *Map[K, V]) DoContext(ctx context.Context, k K, f func() (V, error)) (V, error) {
	for {
		if err := ctx.Err(); err != nil {
			var zero V
			return zero, err
		}
		e := m.entry(k)
		if e.started.CompareAndSwap(false, true) {
			m.compute(k, e, func() (V, error) {
				v, err := f()
				if err != nil && ctx.Err() != nil {
					return v, errors.Join(err, ctx.Err())
				}
				return v, err
			})
			if err := ctx.Err(); err != nil {
				var zero V
				return zero, err
			}
			return e.v, e.err
		}
		select {
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		case <-e.done:
			if err := ctx.Err(); err != nil {
				var zero V
				return zero, err
			}
			if errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded) {
				continue
			}
			return e.v, e.err
		}
	}
}

func (m *Map[K, V]) compute(k K, e *entry[V], f func() (V, error)) {
	defer close(e.done)
	e.v, e.err = f()
	if e.err != nil {
		m.m.CompareAndDelete(k, e)
	}
}

func (m *Map[K, V]) entry(k K) *entry[V] {
	if e, ok := m.m.Load(k); ok {
		return e.(*entry[V])
	}
	e, _ := m.m.LoadOrStore(k, &entry[V]{done: make(chan struct{})})
	return e.(*entry[V])
}
