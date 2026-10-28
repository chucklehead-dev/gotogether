package navproto

import (
	"strings"
	"testing"
)

// A navigator speaks its protocol only; a session of a later one is
// refused with the upgrade, an earlier one with the rebuild, and one
// naming none is read as the current.
func TestCheckNavProtocol(t *testing.T) {
	for _, p := range []string{"", Protocol} {
		if err := CheckProtocol(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := CheckProtocol("gotogether.nav/2"); err == nil || !strings.Contains(err.Error(), "later version") || !strings.Contains(err.Error(), "gotogether-nav@") {
		t.Errorf("a later protocol: %v", err)
	}
	if err := CheckProtocol("gotogether.nav/0"); err == nil || !strings.Contains(err.Error(), "older version") || !strings.Contains(err.Error(), "rebuild the test binary") {
		t.Errorf("an earlier protocol: %v", err)
	}
}
