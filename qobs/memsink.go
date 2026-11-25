package qobs

import (
	"context"
	"sync"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// MemorySink keeps steps in memory: for tests, and for validating a run
// in-process without any OTel pipeline.
type MemorySink struct {
	mu sync.Mutex
	// The steps in recording order, by value, in chunks: head first, then
	// rest's. head grows by append up to memFirstChunk steps, so a short
	// run allocates as one slice would (nothing more); each of rest's is
	// made whole and filled to its capacity before the next is made. A step
	// is copied in once, and never again as the run grows: one slice's
	// growth copied every step recorded so far at each growth, about four
	// steps' bytes per step recorded at append's factor for long slices
	// (1.25), and its B/op in a benchmark moved with where b.N fell between
	// two growths.
	head []qtrace.Step
	rest [][]qtrace.Step
	n    int // steps in head and rest
}

// The chunks' sizes, in steps: head's length before the sink stops
// growing it, and the bounds of a later chunk's capacity, a quarter of the
// steps recorded so far. The last chunk's unused room is then at most a
// quarter of the steps the sink holds, and never more than memMaxChunk
// steps.
const (
	memFirstChunk = 256
	memMinChunk   = 256
	memMaxChunk   = 4096
)

// Record keeps a copy of s after the steps recorded before it.
func (m *MemorySink) Record(_ context.Context, s *qtrace.Step) {
	m.mu.Lock()
	switch k := len(m.rest) - 1; {
	case k >= 0 && len(m.rest[k]) < cap(m.rest[k]):
		m.rest[k] = append(m.rest[k], *s)
	case k < 0 && (len(m.head) < memFirstChunk || len(m.head) < cap(m.head)):
		// head grows by append, and once past memFirstChunk takes the
		// room append's last growth left
		m.head = append(m.head, *s)
	default:
		c := make([]qtrace.Step, 1, min(max(m.n/4, memMinChunk), memMaxChunk))
		c[0] = *s
		m.rest = append(m.rest, c)
	}
	m.n++
	m.mu.Unlock()
}

// Steps returns a copy of what was recorded, in the order it was.
//
// It concatenates the chunks into a slice of exactly the steps recorded,
// on every call, uncached: the copy is the caller's to keep or change (as
// it always was), so a cached concatenation would be copied again on each
// call anyway, at the cost of the concatenation itself, and held besides.
// The sink's callers ask once per run (its end, a navigator's node).
func (m *MemorySink) Steps() []qtrace.Step {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n == 0 {
		return nil
	}
	out := make([]qtrace.Step, 0, m.n)
	out = append(out, m.head...)
	for _, c := range m.rest {
		out = append(out, c...)
	}
	return out
}

// Reset forgets everything recorded.
func (m *MemorySink) Reset() { m.mu.Lock(); m.head, m.rest, m.n = nil, nil, 0; m.mu.Unlock() }
