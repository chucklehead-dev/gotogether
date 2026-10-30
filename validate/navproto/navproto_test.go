package navproto

import (
	"strings"
	"testing"
)

// A navigator speaks its protocol only; a session of a later one is
// refused with the upgrade, an earlier one with the rebuild, and an unnamed protocol is refused.
func TestCheckNavProtocol(t *testing.T) {
	for _, p := range []string{Protocol} {
		if err := CheckProtocol(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := CheckProtocol("gotogether.nav/3"); err == nil || !strings.Contains(err.Error(), "later version") || !strings.Contains(err.Error(), "gotogether-nav@") {
		t.Errorf("a later protocol: %v", err)
	}
	if err := CheckProtocol("gotogether.nav/1"); err == nil || !strings.Contains(err.Error(), "older version") || !strings.Contains(err.Error(), "rebuild the test binary") {
		t.Errorf("an earlier protocol: %v", err)
	}
}

func TestProtocolRequiresIdentity(t *testing.T) {
	if err := CheckProtocol(""); err == nil {
		t.Fatal("unnamed protocol accepted")
	}
}
