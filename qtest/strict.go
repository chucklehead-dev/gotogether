package qtest

import (
	"os"
	"testing"
)

// strictEnv is the variable that turns skips into failures (Optional).
const strictEnv = "GOTOGETHER_STRICT"

// strict reports whether GOTOGETHER_STRICT=1: every toolkit skip fails.
func strict() bool { return os.Getenv(strictEnv) == "1" }

// Optional skips t for reason, as t.Skip does, unless GOTOGETHER_STRICT=1,
// which fails it instead (t.Fatal with reason, saying the variable is
// set). A CI job that installs everything its tests need sets it, so a
// test cannot pass there by skipping: a quint missing from PATH, a build
// not woven, a database the job forgot. For a test's own gated skips:
//
//	if os.Getenv("PG_URL") == "" {
//		qtest.Optional(t, "PG_URL not set: no database to check against")
//	}
//
// The toolkit's own skips go through it: RequireQuint, Mutate with no
// mutants woven, SkipIfEmpty's.
func Optional(t testing.TB, reason string) {
	t.Helper()
	if strict() {
		t.Fatal(reason + " (" + strictEnv + " is set, so a skip fails: give the job what the test needs, or unset " + strictEnv + " where the skip is legitimate)")
	}
	t.Skip(reason)
}
