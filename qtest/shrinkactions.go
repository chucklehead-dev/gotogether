package qtest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Resetter returns the system an ActionShrink reruns to its initial
// state: its data cleared, no work pending (qshrink.Resetter, whose
// contract ShrinkActions checks). Only a fresh environment per rerun
// (Reset nil, Rerun building it) is safe against leaked state.
type Resetter = qshrink.Resetter

// ActionShrink is how ShrinkActions reruns a sequence of actions it
// did not drive (an external explorer's, a Go client's, a replayed
// session's) and judges each rerun.
type ActionShrink[A any] struct {
	// Binding is the binding each rerun's steps are checked against.
	Binding string
	// Rerun does actions on the system, from its initial state, and
	// returns the steps the run recorded (the system's and the
	// driver's). An action it cannot do returns ErrDiverged: the rerun
	// counts for nothing. Another error is a failed rerun; three in a row
	// end the shrink with it.
	Rerun func(ctx context.Context, actions []A) ([]qtrace.Step, error)
	// Reset returns the system to its initial state before each rerun:
	// nil only if Rerun builds a fresh environment itself.
	Reset Resetter
	// Keep is the failure each smaller sequence must still show; default
	// SameFailure of the original's first failing rerun. An expectation
	// a passing run meets (Conforms, Finishes) is refused.
	Keep Expectation
	// Calibration sets the reruns: the original's baseline, each
	// candidate's, the budget.
	Calibration Calibration
	// Opts are the checks' options (Constants, Invariants, Linearize,
	// Driver, ...), as Validate takes them.
	Opts []Option
}

// ShrunkActions is what ShrinkActions found: the smallest sequence that
// still fails as the original did, and what the reruns showed.
type ShrunkActions[A any] struct {
	// Actions is the shrunk sequence, a subsequence of the original.
	Actions []A
	// Run is a rerun of Actions that showed the failure, checked as
	// Validate checks steps: its report, step log and, under
	// GOTOGETHER_EVIDENCE, its manifest (with actions.json and
	// shrink.json among its artifacts).
	Run *Run
	// Original is the original sequence's length.
	Original int
	// Reruns are every rerun made; Reproduced those that showed the
	// failure, NotReproduced those that ran and did not (a pass, another
	// failure), Undecided those with no verdict for it (a lost tail, an
	// invariant not judged), Diverged those whose Rerun returned
	// ErrDiverged, Errors those that failed otherwise.
	Reruns, Reproduced, NotReproduced, Undecided, Diverged, Errors int
	// Baseline is the original's rate (Calibration.Baseline reruns), and
	// PerCandidate the reruns a candidate got before it counted as
	// passing.
	Baseline     Rate
	PerCandidate int
	// Rate is Actions' own, rerun Calibration.Baseline times after the
	// shrink.
	Rate Rate
	// Minimal: every sequence of Actions with one action fewer was rerun
	// PerCandidate times without reproducing the failure (1-minimal, each
	// removal with chance at least 1−α), Actions' own rate was measured
	// after the shrink (Rate, Calibration.Baseline reruns) and is one the
	// original's allows, and a last rerun of no actions showed nothing
	// left behind.
	// False when the budget or time ran out (Actions is the smallest so
	// far, or its rate and the last rerun were not made), a one-removal
	// never ran (each rerun diverged) or Actions' reruns after the shrink
	// had no verdict; NotMinimal says which. A Budget keeps
	// Calibration.Baseline+1 reruns for the rate and the last rerun, so a
	// budget that runs out in the search still measures them.
	Minimal    bool
	NotMinimal string
	keep       Expectation
}

// ShrinkActions shrinks a failing sequence of actions to a 1-minimal
// one that still fails the same way (ddmin): candidates are rerun on the
// system, from the state the Resetter leaves, and their steps checked
// against the binding. A failure that shows only sometimes is
// calibrated (Calibration): each candidate is rerun until it reproduces
// or enough reruns have not, and a rerun that did not run (ErrDiverged)
// counts for neither. A shrunk run that did not reproduce is never a
// pass: it is a sequence the failure was not seen in.
//
//	shrunk := qtest.ShrinkActions(t, journey, qtest.ActionShrink[Action]{
//		Binding: "webjobs.binding.yaml", Rerun: client.Play, Reset: server,
//	})
//	shrunk.Expect(t, qtest.Violates("finishedOnce"), qtest.AtMostActions(2))
//
// It fails t, naming the fix, when the Resetter leaves state behind (two
// reruns of no actions record different steps, or the original's first
// two reruns fail differently), when the original never reproduces, and
// when three reruns in a row fail.
func ShrinkActions[A any](t testing.TB, original []A, s ActionShrink[A]) *ShrunkActions[A] {
	t.Helper()
	c := config(t, s.Opts)
	c.refuse(t, "ShrinkActions", validateIgnores)
	if s.Rerun == nil || s.Binding == "" {
		t.Fatalf("gotogether: ShrinkActions needs a Binding and a Rerun (which does a sequence of actions on the system and returns its steps)")
	}
	if s.Keep.every {
		t.Fatalf("gotogether: ShrinkActions: Keep %s describes a passing run; Keep names the failure each smaller sequence must show "+
			"(Rejects, Mismatch, Violates, SameFailure: the default)", s.Keep)
	}
	o := c.Options
	o.Dir = filepath.Join(c.Dir, "quint")
	cc, inst, _ := instanceChecker(t, s.Binding, o, c)
	var keep qshrink.Keep
	if s.Keep.check != nil {
		var actions map[string]bool
		if cc.Model != nil {
			actions = map[string]bool{}
			for a := range cc.Model.Actions {
				actions[a] = true
			}
		}
		if u := unknownNames([]Expectation{s.Keep}, actions, checkedNames(cc), "the run"); len(u) > 0 {
			t.Fatalf("gotogether: ShrinkActions: Keep %s: %s; a misspelled name never matches", s.Keep, strings.Join(u, "; "))
		}
		keep = shrinkKeep{s.Keep}
	}
	var cached int
	judge := func(ctx context.Context, steps []qtrace.Step) (*validate.Report, error) {
		js, errs := cc.JudgeBatch(ctx, [][]qtrace.Step{steps}, c.searchOptions(), cachedCheck(cc, inst, &cached))
		if errs[0] != nil {
			return nil, errs[0]
		}
		return js[0].Report, nil
	}
	res, err := runShrink(t, qshrink.Shrink{Actions: len(original), Reset: s.Reset, Judge: judge, Keep: keep, Calibration: s.Calibration,
		Rerun: func(ctx context.Context, idx []int) ([]qtrace.Step, error) { return s.Rerun(ctx, pick(original, idx)) }})
	if err != nil {
		t.Fatalf("gotogether: ShrinkActions: %v", err)
	}
	out := shrunkOf(original, res, s.Keep)
	// the minimal run, checked as Validate checks it, with what the
	// shrink did as its manifest's artifacts
	opts := append([]Option{}, s.Opts...)
	if c.driver == nil {
		opts = append(opts, Driver(validate.Driver{Name: "qtest.ShrinkActions", Args: []string{fmt.Sprintf("actions=%d of %d", len(out.Actions), out.Original)}}))
	}
	if arts, err := out.writeArtifacts(t, c.Dir); err != nil {
		t.Logf("gotogether: ShrinkActions: artifacts: %v", err)
	} else {
		opts = append(opts, Artifacts(arts...))
	}
	out.Run = Validate(t, s.Binding, res.Steps, opts...)
	t.Logf("gotogether: %s", out)
	return out
}

// runShrink runs the shrink with t's context and deadline: it stops
// before the test's deadline, its time two of the longest reruns away.
func runShrink(t testing.TB, s qshrink.Shrink) (*qshrink.Result, error) {
	ctx := t.Context()
	if d, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		if dl, ok := d.Deadline(); ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, dl)
			defer cancel()
		}
	}
	return s.Run(ctx)
}

// shrunkOf is a shrink's result over original's actions, keeping keep
// (the zero Expectation: the default's, SameFailure).
func shrunkOf[A any](original []A, res *qshrink.Result, keep Expectation) *ShrunkActions[A] {
	out := &ShrunkActions[A]{Actions: pick(original, res.Set), Original: len(original)}
	out.Reruns, out.Reproduced, out.NotReproduced, out.Undecided, out.Diverged, out.Errors = res.Reruns, res.Reproduced, res.NotReproduced, res.Undecided, res.Diverged, res.Errors
	out.Baseline, out.PerCandidate, out.Rate, out.Minimal, out.NotMinimal = res.Baseline, res.PerCandidate, res.Rate, res.Minimal, res.NotMinimal
	out.keep = keep
	if keep.check == nil && res.Keep != nil {
		out.keep = keepExpectation(res.Keep)
	}
	return out
}

// String summarizes the shrink.
func (s *ShrunkActions[A]) String() string {
	m := "1-minimal"
	if !s.Minimal {
		m = "not minimal: " + s.NotMinimal
	}
	return fmt.Sprintf("shrunk %d actions to %d (%s), reproducing %s: %v; %d reruns (%d reproduced, %d not, %d undecided, %d diverged, %d errors), the original's rate %s, %d per candidate",
		s.Original, len(s.Actions), m, s.Rate, s.Actions, s.Reruns, s.Reproduced, s.NotReproduced, s.Undecided, s.Diverged, s.Errors, s.Baseline, s.PerCandidate)
}

// writeArtifacts writes actions.json (the shrunk actions, as JSON, or
// each as Go prints it when they do not marshal) and shrink.json into
// dir/shrink<N>, N counting t's shrinks, for the minimal run's manifest
// (qshrink.Result.WriteArtifacts): two shrinks in one directory
// (GOTOGETHER_KEEP's is the test's) would overwrite the files an earlier
// run's manifest hashed.
func (s *ShrunkActions[A]) writeArtifacts(t testing.TB, dir string) ([]validate.Artifact, error) {
	dir = filepath.Join(dir, fmt.Sprintf("shrink%d", nextShrink(t)))
	res := &qshrink.Result{Original: s.Original, Reruns: s.Reruns, Reproduced: s.Reproduced, NotReproduced: s.NotReproduced, Undecided: s.Undecided,
		Diverged: s.Diverged, Errors: s.Errors, Baseline: s.Baseline, PerCandidate: s.PerCandidate, Rate: s.Rate, Minimal: s.Minimal, NotMinimal: s.NotMinimal,
		Keep: shrinkKeep{s.keep}}
	acts := make([]any, len(s.Actions))
	for i, a := range s.Actions {
		acts[i] = a
	}
	return res.WriteArtifacts(dir, acts)
}

// shrinks counts each test's shrinks, for their artifacts' directories.
// Process-wide.
var shrinks struct {
	mu sync.Mutex
	n  map[string]int
}

// nextShrink is the number of t's shrink starting, from 1.
func nextShrink(t testing.TB) int {
	shrinks.mu.Lock()
	defer shrinks.mu.Unlock()
	if shrinks.n == nil {
		shrinks.n = map[string]int{}
	}
	shrinks.n[t.Name()]++
	return shrinks.n[t.Name()]
}

// A ShrinkClaim is what a test expects of a shrink (ShrunkActions.Expect):
// an Expectation of its minimal run, or a claim on the sequence
// (AtMostActions).
type ShrinkClaim interface {
	String() string
	// holdsShrunk reports whether the claim holds of a shrink to n
	// actions whose run is r, and if not, what it saw.
	holdsShrunk(n int, r *Run) (bool, string)
}

func (e Expectation) holdsShrunk(_ int, r *Run) (bool, string) { return e.Holds(r), "" }

// AtMostActions claims the shrink reached at most n actions.
func AtMostActions(n int) ShrinkClaim { return atMostActions(n) }

type atMostActions int

func (n atMostActions) String() string {
	return fmt.Sprintf("a sequence shrunk to at most %d action(s)", int(n))
}
func (n atMostActions) holdsShrunk(k int, _ *Run) (bool, string) {
	return k <= int(n), fmt.Sprintf("it has %d", k)
}

// Expect fails t unless every claim holds: each Expectation of the
// minimal run (as Run.Expect judges it, printing its report), each
// AtMostActions of the sequence.
func (s *ShrunkActions[A]) Expect(t testing.TB, claims ...ShrinkClaim) {
	t.Helper()
	for _, c := range claims {
		if e, ok := c.(Expectation); ok {
			s.Run.Expect(t, e)
			continue
		}
		if ok, saw := c.holdsShrunk(len(s.Actions), s.Run); !ok {
			t.Fatalf("gotogether: want %s, %s: %s", c, saw, s)
		}
	}
}

// pick is the actions at idx, in order.
func pick[A any](actions []A, idx []int) []A {
	out := make([]A, len(idx))
	for i, j := range idx {
		out[i] = actions[j]
	}
	return out
}
