// Package counter is the fixture of gotogether invariants: a counter
// annotated with the properties of model/counter.qnt it enforces.
package counter

import "errors"

// ErrFull is Inc's refusal at the cap.
var ErrFull = errors.New("counter: at its cap")

// A Counter counts up to Cap.
type Counter struct {
	N, Cap int
}

// Inc adds one, refusing at the cap.
//
//quint:enforces withinCap
func (c *Counter) Inc() error {
	if c.N >= c.Cap {
		return ErrFull
	}
	c.N++
	return nil
}

// Reset starts again from zero.
func (c *Counter) Reset() {
	//quint:enforces counterBase.nonNegative
	c.N = 0
}
