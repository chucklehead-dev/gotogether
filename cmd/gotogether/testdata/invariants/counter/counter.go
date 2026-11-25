// Package counter is the fixture of gotogether invariants: a counter
// annotated with the properties of model/counter.qnt it enforces, and
// the actions it records.
package counter

import (
	"context"
	"errors"

	obs "github.com/chucklehead-dev/gotogether/qobs"
)

// ErrFull is Inc's refusal at the cap.
var ErrFull = errors.New("counter: at its cap")

// A Counter counts up to Cap.
type Counter struct {
	N, Cap int
}

// Inc adds one, refusing at the cap.
//
//quint:enforces withinCap
//quint:action action:inc
func (c *Counter) Inc() error {
	if c.N >= c.Cap {
		return ErrFull
	}
	c.N++
	return nil
}

// Reset starts again from zero, its step recorded through qobs imported
// under another name.
func (c *Counter) Reset(ctx context.Context) {
	//quint:enforces counterBase.nonNegative
	c.N = 0
	obs.Record(ctx, "reset", nil)
}

// Busy reports more than one caller inside the counter at once: the
// production check of the model's oneCaller assumption.
//
//quint:monitors oneCaller
func Busy(inside int) bool { return inside > 1 }
