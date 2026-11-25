package counter

import (
	"context"
	"math"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
)

// TestInc counts to the cap and is refused there.
//
//quint:checks withinCap
func TestInc(t *testing.T) {
	c := &Counter{Cap: 2}
	for range 2 {
		if err := c.Inc(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Inc(); err != ErrFull {
		t.Fatalf("at the cap: %v", err)
	}
}

func TestReset(t *testing.T) {
	t.Run("from the cap", func(t *testing.T) {
		c := &Counter{N: 2, Cap: 2}
		c.Reset(context.Background())
		if c.N < 0 { //quint:checks nonNegative, counter::withinCap
			t.Fatalf("negative: %d", c.N)
		}
	})
}

// bound is the counter bound to the model's inc (connect.Bind).
func bound() *connect.Binding[*Counter] {
	return connect.Bind(func(testing.TB) *Counter { return &Counter{Cap: 3} }).
		Action("inc", (*Counter).Inc)
}

// TestIncPastMaxInt goes past the model's noOverflow assumption: the
// real counter at math.MaxInt.
//
//quint:exercises noOverflow
func TestIncPastMaxInt(t *testing.T) {
	c := &Counter{N: math.MaxInt - 1, Cap: math.MaxInt}
	if err := c.Inc(); err != nil || c.N != math.MaxInt {
		t.Fatalf("to math.MaxInt: %v, %d", err, c.N)
	}
	if err := c.Inc(); err != ErrFull {
		t.Fatalf("past math.MaxInt: %v, %d", err, c.N)
	}
}
