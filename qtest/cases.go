package qtest

import (
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A CaseCheck is what a package that makes runs its own way (qhegel's
// test cases) needs of qtest's Options: the binding's checker for the
// options' instance, and how each run records.
type CaseCheck struct {
	// Checker checks a case's steps (nil when no binding is given: the
	// cases are not checked).
	Checker *validate.Checker
	// Recorder are the options for each case's recorder (RecorderOptions).
	Recorder []qobs.Option
	// Global installs each case's recorder globally while it runs
	// (Global), for goroutines no run can follow.
	Global bool
	// SkipIfEmpty is a System's SkipIfEmpty (System.CheckCases): skip the
	// test when its nominal run records nothing, an unwoven build.
	SkipIfEmpty bool
}

// CheckCases resolves opts for runs a workflow makes itself and checks
// one at a time in the order they were recorded (qhegel's Hegel cases),
// against the binding at bindingPath ("": not checked at all). An option
// such a check ignores fails t, saying why, as every workflow's does.
func CheckCases(t testing.TB, workflow, bindingPath string, opts ...Option) CaseCheck {
	t.Helper()
	c := config(t, opts)
	c.refuse(t, workflow, casesIgnore)
	if bindingPath == "" {
		c.refuse(t, workflow, uncheckedIgnore)
		return CaseCheck{Recorder: c.Recorder, Global: c.Global}
	}
	return CaseCheck{Checker: c.checker(t, bindingPath), Recorder: c.Recorder, Global: c.Global}
}

// CheckCases is CheckCases for the system's runs made another way
// (qhegel.Shrink's search): its binding, its Opts, which may skip a build
// that records nothing (Judge), refusing what its explorations ignore.
func (s System) CheckCases(t testing.TB, workflow string) CaseCheck {
	t.Helper()
	c := config(t, s.opts())
	c.refuse(t, workflow, exploreIgnores)
	return CaseCheck{Checker: c.checker(t, s.Binding), Recorder: c.Recorder, Global: c.Global, SkipIfEmpty: c.SkipIfEmpty}
}

// checker is the binding's checker with the options' constants,
// invariants and quint options, its modules in Dir/quint.
func (c settings) checker(t testing.TB, bindingPath string) *validate.Checker {
	t.Helper()
	o := c.Options
	o.Dir = filepath.Join(c.Dir, "quint")
	ch := checkerFor(t, bindingPath, o, c)
	cc := *ch
	cc.Options = o
	return &cc
}

// Judge runs the system once on ch and checks the run against the model
// as an exploration checks its runs (sharing their verdicts), outside any
// exploration: no number, no evidence, no corpus, no claims. It is for a
// package that drives runs itself (qhegel.Shrink's replays, its first
// nominal run). A run that could not run to its end has its error in Err
// and no Report. As in an exploration, options an exploration ignores, a
// step that escaped the run (recorded from a goroutine started with go)
// and a Rates key that covers nothing fail t; a run that records nothing
// is judged as it is (an empty run), not skipped: the caller decides
// (CaseCheck.SkipIfEmpty).
func (s System) Judge(t testing.TB, ch qenv.Chooser) *Run {
	t.Helper()
	c := config(t, s.opts())
	c.refuse(t, "System.Judge", exploreIgnores)
	release := hooks.Strict()
	mark, _ := hooks.Escaped(0)
	r := runOne(c, len(qcover.Blocks()) > 0, ch, s.Run)
	release()
	if n, msgs := hooks.Escaped(mark); n > mark {
		t.Fatalf("gotogether: %d steps were recorded from outside the run judged (a test in parallel may have recorded some); the first:\n%s", n-mark, msgs[0])
	}
	if r.Err == nil { // a run that did not run declared nothing
		unknownKinds(t, "System.Rates key", keysOf(s.Rates.Kinds), []*Run{r})
	}
	if r.Err != nil {
		return r
	}
	o := c.Options
	o.Dir, o.NoITF = filepath.Join(c.Dir, "judge", "quint"), true
	cc, inst, _ := instanceChecker(t, s.Binding, o, c)
	judge(t, cc, inst, validate.SearchOptions{}, []*Run{r}, func(*Run) string { return "the run judged" })
	return r
}
