package qtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

// A binding's compiled model (gotogether compile, kept beside the binding
// at validate.CompiledFor) lets its tests run where quint is not
// installed: the checks are the Go evaluator's alone (validate's compiled
// checker), and a run it cannot decide is no verdict ("needs quint"),
// never guessed. GOTOGETHER_COMPILED says when it is used:
//   - unset: where the binding's quint is not on PATH and the compiled
//     model exists; quint, where it is, as before;
//   - "1": always, and a test fails where the binding has none (CI's
//     job that runs tests with no quint, so that a quint left on PATH does
//     not hide a missing artifact);
//   - "0": never.
//
// The artifact is refused when stale (another binding, a changed source,
// an evaluation module this gotogether writes otherwise): recompile it
// (gotogether compile -binding B), and keep it current in CI with
// gotogether compile -check where quint is installed.
const compiledEnv = "GOTOGETHER_COMPILED"

// compiledFor is the compiled model the binding's checks use, if any: its
// path and true, per GOTOGETHER_COMPILED. A test fails where "1" asks for
// one the binding lacks.
func compiledFor(t testing.TB, bindingPath string, o validate.Options) (string, bool) {
	t.Helper()
	path := validate.CompiledFor(bindingPath)
	_, statErr := os.Stat(path)
	switch mode := os.Getenv(compiledEnv); mode {
	case "0":
		return "", false
	case "1":
		if statErr != nil {
			t.Fatalf("gotogether: %s=1 checks every run by the binding's compiled model, and %s has none at %s (%v): compile it (gotogether compile -binding %s) where quint is installed",
				compiledEnv, bindingPath, path, statErr, bindingPath)
		}
		return path, true
	case "":
		if statErr != nil {
			return "", false
		}
		quint := o.Quint
		if quint == "" {
			quint = "quint"
		}
		if _, err := exec.LookPath(quint); err == nil {
			return "", false
		}
		return path, true
	default:
		t.Fatalf("gotogether: %s=%q: want 1 (always the compiled model), 0 (never) or unset (where quint is not installed)", compiledEnv, mode)
		return "", false
	}
}

// compiledCheckers are the compiled checkers built in this process, by
// binding and artifact (absolute), each built once.
var compiledCheckers sync.Map // process-wide: string -> *compiledEntry

type compiledEntry struct {
	once sync.Once
	c    *validate.Checker
	err  error
}

// compiledChecker is the binding's checker by its compiled model at path,
// built once per process and logged once per test binary: the run's
// verdicts are the Go evaluator's alone.
func compiledChecker(t testing.TB, bindingPath, path string, o validate.Options) *validate.Checker {
	t.Helper()
	ab, err := filepath.Abs(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	ap, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := compiledCheckers.LoadOrStore(ab+"|"+ap, &compiledEntry{})
	e := v.(*compiledEntry)
	first := false
	e.once.Do(func() {
		first = true
		e.c, e.err = validate.NewCompiledChecker(ab, ap, o)
	})
	if e.err != nil {
		t.Fatalf("gotogether: %v", e.err)
	}
	if first {
		t.Logf("gotogether: %s judged by its compiled model %s, the Go evaluator alone (no quint here, or %s=1); a run it cannot decide is no verdict",
			bindingPath, path, compiledEnv)
	}
	return withOptions(e.c, o)
}

// withOptions is ch with o for its options, keeping a compiled checker's
// quint (none: it never runs one).
func withOptions(ch *validate.Checker, o validate.Options) *validate.Checker {
	cc := *ch
	if ch.Compiled() != nil {
		o.Quint = ch.Options.Quint
	}
	cc.Options = o
	return &cc
}

// RequireModel skips the test unless the binding's runs can be judged
// here: quint is installed, or the binding has a compiled model
// (gotogether compile) for qtest to use instead (GOTOGETHER_COMPILED).
// Use it in place of RequireQuint where a test only records, validates or
// explores runs; RequireQuint stays where a test needs quint itself (the
// model's own checks, Model, simulate, connect's traces).
func RequireModel(t testing.TB, bindingPath string) {
	t.Helper()
	if _, ok := compiledFor(t, bindingPath, validate.Options{}); ok {
		return
	}
	if _, err := exec.LookPath("quint"); err != nil {
		skipping(t, fmt.Sprintf("quint not installed (npm i -g @informalsystems/quint), and %s has no compiled model at %s (gotogether compile -binding %s)",
			bindingPath, validate.CompiledFor(bindingPath), bindingPath))
	}
}
