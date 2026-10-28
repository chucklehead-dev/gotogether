package validate

import (
	"strings"
	"testing"
)

// A navigator speaks its protocol and every earlier one; a session of a
// later one is refused with the fix, and one naming none (a binary from
// before the protocol had a name) is nav/1.
func TestCheckNavProtocol(t *testing.T) {
	for _, p := range []string{"", NavProtocol} {
		if err := CheckNavProtocol(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := CheckNavProtocol("gotogether.nav/2"); err == nil || !strings.Contains(err.Error(), "later version") || !strings.Contains(err.Error(), "gotogether-nav@") {
		t.Errorf("a later protocol: %v", err)
	}
}
