package session

import (
	"strings"
	"testing"
)

// A run's reference names its test (a subtest's path too) and the -run
// pattern that runs it alone; anything else says what it wants.
func TestTest(t *testing.T) {
	for ref, want := range map[string][2]string{
		"TestNoDedupeCountsRetries#1:seed=1": {"TestNoDedupeCountsRetries", "^TestNoDedupeCountsRetries$"},
		"TestShop/claim#2:schedule=0:1,3:2":  {"TestShop/claim", "^TestShop$/^claim$"},
	} {
		name, pattern, err := Test(ref)
		if err != nil || name != want[0] || pattern != want[1] {
			t.Errorf("Test(%q) = %q, %q, %v; want %q, %q", ref, name, pattern, err, want[0], want[1])
		}
	}
	if _, _, err := Test("steps.jsonl"); err == nil || !strings.Contains(err.Error(), "TestX#2:seed=17") {
		t.Errorf("not a run's reference: %v", err)
	}
}

// A package whose test names no run fails to start, with the binary's
// output.
func TestStartFails(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a test binary")
	}
	_, err := Start(".", "TestNoSuchTest#1:seed=1")
	if err == nil || !strings.Contains(err.Error(), "the test binary ended") {
		t.Errorf("a session of no test: %v", err)
	}
}
