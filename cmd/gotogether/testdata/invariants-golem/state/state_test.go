package state

import "testing"

//quint:checks attempts.noLostAttempt, attempts.boundedAttempts
func TestRestore(t *testing.T) {
	if Restore(-1) != 0 {
		t.Fatal("lost")
	}
}

//quint:checks publication.noExpiredServe
func TestServe(t *testing.T) {
	if Serve(true) {
		t.Fatal("served expired")
	}
}
