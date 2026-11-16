package counter

import (
	"context"
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
