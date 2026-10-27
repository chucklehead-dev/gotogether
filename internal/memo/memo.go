// Package memo caches values computed once per key, for the core's
// process-wide caches (parsed templates, typechecked models, compiled
// evaluators). Each cache was a sync.Map with its own type assertions,
// and most computed a missing key in every goroutine that asked for it
// at once: two parallel checks of one model typechecked it twice.
package memo

import "sync"

// A Map holds a value per key, each computed by the first caller that
// asks for it while callers asking for the same key wait for it. The
// zero Map is empty and ready to use; a Map must not be copied.
type Map[K comparable, V any] struct {
	m sync.Map // K -> *entry[V]
}

type entry[V any] struct {
	once sync.Once
	v    V
	err  error
}

// Once is k's value, computed by f the first time: f's result is kept
// whatever it is (an error a value holds included).
func (m *Map[K, V]) Once(k K, f func() V) V {
	e := m.entry(k)
	e.once.Do(func() { e.v = f() })
	return e.v
}

// Do is k's value, computed by f the first time it succeeds: an error is
// returned to the callers that waited for it, and the next call computes
// again (a missing file may be written, a broken model fixed).
func (m *Map[K, V]) Do(k K, f func() (V, error)) (V, error) {
	e := m.entry(k)
	e.once.Do(func() {
		e.v, e.err = f()
		if e.err != nil {
			m.m.CompareAndDelete(k, e)
		}
	})
	return e.v, e.err
}

func (m *Map[K, V]) entry(k K) *entry[V] {
	if e, ok := m.m.Load(k); ok {
		return e.(*entry[V])
	}
	e, _ := m.m.LoadOrStore(k, new(entry[V]))
	return e.(*entry[V])
}
