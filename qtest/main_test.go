package qtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain runs the package's tests with a temporary directory of their
// own, removed when they end: a test's report copies its failing runs'
// files out of t.TempDir() into os.TempDir() (keepCopy, gotogether-runs-,
// gotogether-steered), as a user's must outlive the test, and the
// package's tests of failures (fake TBs) left a directory there every
// run. The evaluator's compile cache (gotogether-eval) is kept where it
// was: it is shared across runs by design, and recompiling it would
// cost every run. (A TMPDIR set here, before any test: t.Setenv refuses
// parallel tests.)
func TestMain(m *testing.M) {
	orig := os.TempDir()
	tmp, err := os.MkdirTemp("", "gotogether-qtest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Join(orig, "gotogether-eval"), 0o755); err == nil {
		_ = os.Symlink(filepath.Join(orig, "gotogether-eval"), filepath.Join(tmp, "gotogether-eval"))
	}
	os.Setenv("TMPDIR", tmp)
	code := m.Run()
	os.RemoveAll(tmp) // the link, not the cache it names
	os.Exit(code)
}
