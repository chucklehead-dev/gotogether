package state

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
)

// TestAttemptsModel is attempts' driver in publication's package: its
// Config names attempts' spec, so publication's actions table skips it.
func TestAttemptsModel(t *testing.T) {
	bound := connect.Bind(func(testing.TB) *int { return new(int) }).
		Handle("attempt", func(n *int, _ *connect.Step) error { *n = Restore(*n + 1); return nil }).
		Handle("keepN", func(*int, *connect.Step) error { return nil }).
		Skip("idle")
	bound.Run(t, connect.Config{Spec: "../model/attempts.qnt"})
}
