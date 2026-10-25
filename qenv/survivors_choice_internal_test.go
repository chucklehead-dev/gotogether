package qenv

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv/internal/choice"
)

// Nearest is the nearest name, an exact one first: a name at distance 0
// is kept against farther ones after it, and is near enough to suggest.
// (Misnamed asks it of names a Script cannot match exactly, such as a
// nemesis family, which Names lists.)
func TestNearestKeepsAnExactName(t *testing.T) {
	if n := choice.Nearest("rpc.pay", []string{"rpc.pay", "rpc.payx"}); n != "rpc.pay" {
		t.Errorf("nearest %q, want rpc.pay", n)
	}
	if n := choice.Nearest("rpc.pay", []string{"rpc.pa", "rpc.pay"}); n != "rpc.pay" {
		t.Errorf("nearest %q, want rpc.pay", n)
	}
	if n := choice.Nearest("rpc.pay", []string{"queue.ack"}); n != "" {
		t.Errorf("nearest %q, want none", n)
	}
}
