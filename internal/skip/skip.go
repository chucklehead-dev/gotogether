// Package skip is the toolkit's one way to skip a test: a skip normally,
// a failure under GOTOGETHER_STRICT=1, where a CI job that installs what
// its tests need must not pass by skipping them. qtest.Optional and
// RequireQuint are it, for users; the core's own tests use it directly
// (qtest's tests cannot import qtest's helpers from other packages
// without a cycle).
package skip

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Env is the variable that turns skips into failures.
const Env = "GOTOGETHER_STRICT"

// Strict reports whether GOTOGETHER_STRICT asks for strict mode: "1" or
// "true" (any case) does, unset, "", "0" or "false" does not, and any other
// value is an error, so a misspelt setting is not silently lenient.
func Strict() (bool, error) {
	switch v := os.Getenv(Env); strings.ToLower(v) {
	case "1", "true":
		return true, nil
	case "", "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s=%q: set it to 1 (or true) to make skips fail, or unset it", Env, v)
	}
}

// Optional skips t for reason, or fails it under strict mode with the
// same reason and the fix.
func Optional(t testing.TB, reason string) {
	t.Helper()
	strict, err := Strict()
	switch {
	case err != nil:
		t.Fatal(reason + " (and " + err.Error() + ")")
	case strict:
		t.Fatal(reason + " (" + Env + " is set, so a skip fails: give the job what the test needs, or unset " + Env + " where the skip is legitimate)")
	}
	t.Skip(reason)
}

// Quint skips t, or fails it under strict mode, when the quint CLI is not
// on PATH.
func Quint(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		Optional(t, "quint not installed (npm i -g @informalsystems/quint)")
	}
}
