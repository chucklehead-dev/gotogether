package qtest

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// Optional skips t for reason, as t.Skip does, unless GOTOGETHER_STRICT
// is 1 (or true), which fails it instead (t.Fatal with reason, saying the
// variable is set; any other value but 0 or false fails too, naming it). A
// CI job that installs everything its tests need sets it, so a test
// cannot pass there by skipping: a quint missing from PATH, a build not
// woven, a database the job forgot. For a test's own gated skips:
//
//	if os.Getenv("PG_URL") == "" {
//		qtest.Optional(t, "PG_URL not set: no database to check against")
//	}
//
// The toolkit's own skips go through it: RequireQuint, Mutate with no
// mutants woven, SkipIfEmpty's.
func Optional(t testing.TB, reason string) {
	t.Helper()
	skipping(t, reason)
}

// skipping is every qtest skip: it clears the evidence an earlier run of
// the test wrote, unless the test explored already in this process, so
// a reused GOTOGETHER_EVIDENCE directory does not show a test that skipped
// as one that ran (evidence -expect), then skips or fails (strict).
func skipping(t testing.TB, reason string) {
	t.Helper()
	explorations.mu.Lock()
	_, explored := explorations.n[t.Name()]
	explorations.mu.Unlock()
	if !explored {
		clearEvidence(t.Name())
	}
	skip.Optional(t, reason)
}
