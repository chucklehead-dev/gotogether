package qtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/ddmin"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Resetter returns the system an ActionShrink reruns to its initial
// state: its data cleared, no work pending. Only a fresh environment per
// rerun (Reset nil, Rerun building it) is safe against leaked state:
// ShrinkActions' checks catch leaked work (steps a rerun of no actions
// records anew, a failure it shows), not state that changes later
// reruns without a step of its own (a counter Reset forgets). ShrinkActions calls it before
// every rerun, and checks it first: a Reset then a rerun of no actions,
// twice, must record the same steps both times (up to recorders',
// actors', processes' and threads' names, and times). An environment's
// own steps (a fresh server's shutdown drain and settle) repeat and
// pass; a leak (a job the first left running, finishing in the second)
// does not.
type Resetter interface {
	Reset(ctx context.Context) error
}

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
	o := c.Options
	o.Dir = filepath.Join(c.Dir, "quint")
	cc, inst, _ := instanceChecker(t, s.Binding, o, c)
	var cached int
	judge := func(ctx context.Context, steps []qtrace.Step) (*validate.Report, []qtrace.Step, error) {
		js, errs := cc.JudgeBatch(ctx, [][]qtrace.Step{steps}, c.searchOptions(), cachedCheck(cc, inst, &cached))
		if errs[0] != nil {
			return nil, nil, errs[0]
		}
		return js[0].Report, js[0].Steps, nil
	}
	var actions map[string]bool
	if cc.Model != nil {
		actions = map[string]bool{}
		for a := range cc.Model.Actions {
			actions[a] = true
		}
	}
	core := &shrinker{
		t: t, n: len(original), reset: s.Reset, judge: judge, keep: s.Keep, cal: s.Calibration, names: checkedNames(cc), actions: actions,
		rerun: func(ctx context.Context, idx []int) ([]qtrace.Step, error) { return s.Rerun(ctx, pick(original, idx)) },
	}
	res := core.shrink()
	out := &ShrunkActions[A]{Actions: pick(original, res.set), Original: len(original)}
	out.Reruns, out.Reproduced, out.NotReproduced, out.Undecided, out.Diverged, out.Errors = res.reruns, res.reproduced, res.notReproduced, res.undecided, res.diverged, res.errs
	out.Baseline, out.PerCandidate, out.Rate, out.Minimal, out.NotMinimal, out.keep = res.baseline, res.perCandidate, res.rate, res.minimal, res.notMinimal, res.keep
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
	out.Run = Validate(t, s.Binding, res.steps, opts...)
	t.Logf("gotogether: %s", out)
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

// shrinkRecord is shrink.json, a manifest's artifact: what the shrink did.
type shrinkRecord struct {
	Original      int    `json:"original"`
	Actions       int    `json:"actions"`
	Keep          string `json:"keep"`
	Reruns        int    `json:"reruns"`
	Reproduced    int    `json:"reproduced"`
	NotReproduced int    `json:"notReproduced"`
	Undecided     int    `json:"undecided"`
	Diverged      int    `json:"diverged"`
	Errors        int    `json:"errors"`
	Baseline      Rate   `json:"baseline"`
	PerCandidate  int    `json:"perCandidate"`
	Rate          Rate   `json:"rate"`
	Minimal       bool   `json:"minimal"`
	NotMinimal    string `json:"notMinimal,omitempty"`
}

// writeArtifacts writes actions.json (the shrunk actions, as JSON, or
// each as Go prints it when they do not marshal) and shrink.json into
// dir/shrink<N>, N counting t's shrinks, for the minimal run's manifest:
// two shrinks in one directory (GOTOGETHER_KEEP's is the test's) would
// overwrite the files an earlier run's manifest hashed.
func (s *ShrunkActions[A]) writeArtifacts(t testing.TB, dir string) ([]validate.Artifact, error) {
	dir = filepath.Join(dir, fmt.Sprintf("shrink%d", nextShrink(t)))
	acts, err := json.MarshalIndent(s.Actions, "", "  ")
	if err != nil {
		printed := make([]string, len(s.Actions))
		for i, a := range s.Actions {
			printed[i] = fmt.Sprintf("%+v", a)
		}
		acts, _ = json.MarshalIndent(printed, "", "  ")
	}
	rec, err := json.MarshalIndent(shrinkRecord{Original: s.Original, Actions: len(s.Actions), Keep: s.keep.String(), Reruns: s.Reruns, Reproduced: s.Reproduced,
		NotReproduced: s.NotReproduced, Undecided: s.Undecided, Diverged: s.Diverged, Errors: s.Errors, Baseline: s.Baseline, PerCandidate: s.PerCandidate, Rate: s.Rate,
		Minimal: s.Minimal, NotMinimal: s.NotMinimal}, "", "  ")
	if err != nil {
		return nil, err
	}
	var arts []validate.Artifact
	for _, f := range []struct {
		name string
		data []byte
	}{{"actions", acts}, {"shrink", rec}} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		p := filepath.Join(dir, f.name+".json")
		if err := os.WriteFile(p, append(f.data, '\n'), 0o644); err != nil {
			return nil, err
		}
		arts = append(arts, validate.Artifact{Name: f.name, Path: p})
	}
	return arts, nil
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

// shrinker is ShrinkActions without its actions' type: a sequence is the
// indices of the original's actions it keeps. judge checks a rerun's
// steps (a test's fake judges without quint).
type shrinker struct {
	t     testing.TB
	n     int
	rerun func(ctx context.Context, idx []int) ([]qtrace.Step, error)
	reset Resetter
	judge func(ctx context.Context, steps []qtrace.Step) (*validate.Report, []qtrace.Step, error)
	keep  Expectation
	cal   Calibration
	// reserve is how many reruns of the budget out keeps back: the shrunk
	// sequence's rate and the last empty rerun's, while ddmin searches
	reserve int
	// names are the binding's checked invariant names and actions the
	// model's (nil: not known), for Keep's: a misspelled one never holds
	names, actions map[string]bool

	start time.Time
	// empty are the steps a rerun of no actions records (the
	// environment's own), which every later one must repeat
	empty                                                        []qtrace.Step
	reruns, reproduced, notReproduced, undecided, diverged, errs int
	inARow                                                       int           // failed reruns in a row
	longest                                                      time.Duration // the longest rerun, for the test's deadline
}

// shrinkResult is a shrink's outcome, for ShrunkActions.
type shrinkResult struct {
	set                                                          []int
	steps                                                        []qtrace.Step // a rerun of set that reproduced
	reruns, reproduced, notReproduced, undecided, diverged, errs int
	baseline, rate                                               Rate
	perCandidate                                                 int
	minimal                                                      bool
	notMinimal                                                   string
	keep                                                         Expectation
}

// attempt is one rerun's outcome.
type attempt struct {
	ran   bool // it ran and was judged: not diverged, not an error
	v     tri  // Keep's value of it (unknown before Keep is known)
	steps []qtrace.Step
	rep   *validate.Report
}

// out says why the shrink must stop ("": it need not): its budget, its
// time, or the test's deadline near (two of the longest reruns away).
func (s *shrinker) out() string {
	switch {
	case s.cal.Budget > 0 && s.reruns >= s.cal.Budget-s.reserve:
		return fmt.Sprintf("its budget of %d reruns ran out (raise Calibration.Budget)", s.cal.Budget)
	case s.cal.Time > 0 && time.Since(s.start) >= s.cal.Time:
		return fmt.Sprintf("its time of %v ran out (raise Calibration.Time)", s.cal.Time)
	}
	if d, ok := s.t.(interface{ Deadline() (time.Time, bool) }); ok {
		if dl, ok := d.Deadline(); ok && time.Until(dl) < 2*s.longest+5*time.Second {
			return "the test's deadline is near (raise go test's -timeout, or set Calibration.Time)"
		}
	}
	return ""
}

// once resets the system and reruns idx, judging what it recorded, and
// counts it by Keep's value (once Keep is known). Three failed reruns in
// a row end the test.
func (s *shrinker) once(ctx context.Context, idx []int) attempt {
	s.t.Helper()
	s.reruns++
	began := time.Now()
	var steps []qtrace.Step
	err := s.resetErr(ctx)
	if err == nil {
		steps, err = s.rerun(ctx, idx)
	}
	s.longest = max(s.longest, time.Since(began))
	var rep *validate.Report
	if err == nil {
		rep, _, err = s.judge(ctx, steps)
		if err != nil {
			err = fmt.Errorf("checking its steps: %w", err)
		}
	}
	switch {
	case errors.Is(err, ErrDiverged):
		s.diverged++
		s.inARow = 0
		return attempt{}
	case err != nil:
		s.errs++
		if s.inARow++; s.inARow >= 3 {
			s.t.Fatalf("gotogether: ShrinkActions: three reruns in a row failed, the last of %d actions with: %v. Fix the rerun (return ErrDiverged for actions it cannot do)", len(idx), err)
		}
		return attempt{}
	}
	s.inARow = 0
	a := attempt{ran: true, v: unknown, steps: steps, rep: rep}
	if s.keep.check != nil {
		a.v = s.keep.value(rep)
		s.count(a.v)
	}
	return a
}

// count counts a judged rerun by Keep's value of it: a rerun with no
// verdict for the failure (a lost tail, an invariant not judged) is
// neither reproduced nor not.
func (s *shrinker) count(v tri) {
	switch v {
	case yes:
		s.reproduced++
	case no:
		s.notReproduced++
	default:
		s.undecided++
	}
}

// resetter names the Resetter for a message.
func (s *shrinker) resetter() string {
	if s.reset == nil {
		return "none: Rerun builds its own environment"
	}
	return fmt.Sprintf("%T", s.reset)
}

func (s *shrinker) resetErr(ctx context.Context) error {
	if s.reset == nil {
		return nil
	}
	if err := s.reset.Reset(ctx); err != nil {
		return fmt.Errorf("Reset (%s): %w", s.resetter(), err)
	}
	return nil
}

// candidate reruns idx up to n times: it fails at its need-th
// reproduction, passes once the reruns left cannot reach it, and is
// unknown when too few reruns decided (each diverged or had no verdict
// for the failure, the budget out). found keeps a reproducing rerun's
// steps. The empty sequence failing contradicts the Resetter's contract.
func (s *shrinker) candidate(ctx context.Context, idx []int, n int, found map[string][]qtrace.Step) ddmin.Outcome {
	got, ran := 0, 0
	for tries := 0; tries < 2*n && ran < n; tries++ {
		if s.out() != "" {
			return ddmin.Unknown
		}
		a := s.once(ctx, idx)
		if !a.ran || a.v == unknown {
			continue
		}
		ran++
		if a.v == yes {
			if len(idx) == 0 {
				s.t.Fatalf("gotogether: ShrinkActions: a rerun of no actions reproduced %s, after reruns of the original did not show it there: "+
					"state or work the actions leave survives Reset (%s). Make Reset clear it, or build a fresh environment per rerun (Reset nil)", s.keep, s.resetter())
			}
			if got++; got >= s.cal.Need {
				found[fmt.Sprint(idx)] = a.steps
				return ddmin.Fails
			}
		}
		if got+(n-ran) < s.cal.Need {
			return ddmin.Passes
		}
	}
	if ran < n {
		return ddmin.Unknown
	}
	return ddmin.Passes
}

// appSteps are the steps of steps the system made: model steps and
// invocation records, not the settles, losses and watermarks the
// recording made.
func appSteps(steps []qtrace.Step) []qtrace.Step {
	var out []qtrace.Step
	for _, st := range steps {
		if st.Model() || st.Invocation {
			out = append(out, st)
		}
	}
	return out
}

// emptyRerun resets and reruns no actions, and fails t when it records
// other steps than the first such rerun (when, says which) or, Keep
// known, reproduces the failure: work a rerun left that Reset did not
// clear (a job finishing later, a poller), shown by the next.
func (s *shrinker) emptyRerun(ctx context.Context, when string) {
	t := s.t
	t.Helper()
	s.reruns++
	err := s.resetErr(ctx)
	var steps []qtrace.Step
	if err == nil {
		steps, err = s.rerun(ctx, nil)
	}
	if err != nil {
		t.Fatalf("gotogether: ShrinkActions: a Reset, then a Rerun of no actions (%s), failed: %v. Rerun must take an empty sequence: "+
			"such reruns check that the Resetter leaves nothing behind", when, err)
	}
	if s.empty == nil {
		s.empty = append([]qtrace.Step{}, appSteps(steps)...) // non-nil: the first is taken
		return
	}
	if d := sameRun(&Run{Steps: s.empty}, &Run{Steps: appSteps(steps)}); d != "" {
		t.Fatalf("gotogether: ShrinkActions: after Reset (%s), a Rerun of no actions (%s) recorded other steps than the first (%s): the Resetter "+
			"left work or state behind that it saw. Reset must return the system to its initial state, with nothing pending", s.resetter(), when, d)
	}
	if s.keep.check == nil {
		return
	}
	rep, _, err := s.judge(ctx, steps)
	if err != nil {
		t.Fatalf("gotogether: ShrinkActions: checking a Rerun of no actions (%s): %v", when, err)
	}
	if s.keep.value(rep) == yes {
		t.Fatalf("gotogether: ShrinkActions: a Rerun of no actions (%s) reproduced %s: "+
			"the failure needs none of the actions (the environment fails alone: shrink nothing), or state the original's reruns left survives "+
			"Reset (%s): make Reset clear it, or build a fresh environment per rerun (Reset nil)", when, s.keep, s.resetter())
	}
}

// decided reports whether rep has a failure and a verdict for every
// part a failure is judged by (SameFailure's): the failure to keep.
func decided(rep *validate.Report) bool {
	return rep != nil && len(firstFailures(rep)) > 0 && !unjudgedFailure(rep)
}

// shrink checks the Resetter's contract, calibrates on the original and
// shrinks it.
func (s *shrinker) shrink() *shrinkResult {
	t := s.t
	t.Helper()
	ctx := t.Context()
	s.start = time.Now()
	cal, err := s.cal.withDefaults()
	if err != nil {
		t.Fatalf("gotogether: ShrinkActions: %v", err)
	}
	s.cal = cal
	keepGiven := s.keep.check != nil
	if keepGiven {
		if s.keep.every {
			t.Fatalf("gotogether: ShrinkActions: Keep %s describes a passing run; Keep names the failure each smaller sequence must show "+
				"(Rejects, Mismatch, Violates, SameFailure: the default)", s.keep)
		}
		if u := unknownNames([]Expectation{s.keep}, s.actions, s.names, "the run"); len(u) > 0 {
			t.Fatalf("gotogether: ShrinkActions: Keep %s: %s; a misspelled name never matches", s.keep, strings.Join(u, "; "))
		}
	}
	// the Resetter's contract: no actions, the same steps each time. An
	// environment's own steps (a server's shutdown drain, its settle)
	// repeat; what a Reset leaves behind (a job still running, a poller)
	// shows in the second rerun and not the first. The check is repeated
	// after the baseline and at the end, when the actions' own leftovers
	// would show.
	s.emptyRerun(ctx, "the first")
	s.emptyRerun(ctx, "the second")
	// the baseline: the original's rate, and the failure to keep
	all := make([]int, s.n)
	for i := range all {
		all[i] = i
	}
	var base []attempt
	for range cal.Baseline {
		base = append(base, s.once(ctx, all))
	}
	if !keepGiven {
		// the first two reruns with a decided failure: a rerun with no
		// verdict for a part (a lost tail) says nothing of how it fails
		var first []*validate.Report
		for _, a := range base {
			if a.ran && decided(a.rep) {
				first = append(first, a.rep)
			}
		}
		if len(first) >= 2 {
			if a, b := signatureOf(first[0]), signatureOf(first[1]); a != b {
				t.Fatalf("gotogether: ShrinkActions: the original's reruns fail two ways (%s; then %s): either the system fails both ways, "+
					"and Keep must name the one to shrink (qtest.Violates(...), qtest.Rejects(...)), or Reset (%s) leaves state one rerun sees "+
					"and the next does not, and must clear it", a, b, s.resetter())
			}
		}
		if len(first) > 0 {
			s.keep = SameFailure(&Run{Report: first[0]})
		}
	}
	res := &shrinkResult{keep: s.keep}
	ranAny := false
	for _, a := range base {
		if !a.ran {
			continue
		}
		ranAny = true
		if s.keep.check == nil {
			continue
		}
		v := s.keep.value(a.rep)
		if !keepGiven {
			s.count(v) // counted now Keep is known
		}
		if v == unknown {
			continue
		}
		res.baseline.Of++
		if v == yes {
			res.baseline.Reproduced++
			if res.steps == nil {
				res.steps = a.steps
			}
		}
	}
	switch {
	case !ranAny:
		t.Fatalf("gotogether: ShrinkActions: none of the original's %d reruns ran (%d diverged, %d failed): fix the Rerun", cal.Baseline, s.diverged, s.errs)
	case res.baseline.Reproduced == 0:
		want := "a decided failure"
		if s.keep.check != nil {
			want = s.keep.String()
		}
		t.Fatalf("gotogether: ShrinkActions: the original's %d actions do not reproduce %s in %d reruns (%d without a verdict): calibrate the rerun "+
			"(make it deterministic: the same actions, timings and data each time, every step collected; or raise Calibration.Baseline for a rare failure)",
			s.n, want, cal.Baseline, s.undecided)
	}
	s.emptyRerun(ctx, "after the original's reruns")
	n := cal.Reruns
	if n == 0 {
		n = rerunsFor(rateBound(res.baseline, cal.Alpha), cal.Need, cal.Alpha)
	}
	res.perCandidate = n
	found := map[string][]qtrace.Step{fmt.Sprint(all): res.steps}
	// the search leaves the budget enough for what Minimal also needs
	s.reserve = cal.Baseline + 1
	d := ddmin.Minimize(s.n, func(idx []int) ddmin.Outcome { return s.candidate(ctx, idx, n, found) },
		func() bool { return s.out() != "" })
	res.set = d.Set
	res.steps = found[fmt.Sprint(d.Set)]
	res.minimal = d.Minimal
	switch {
	case d.Stopped:
		res.notMinimal = s.out()
	case !d.Minimal:
		res.notMinimal = "a sequence with one action fewer was never decided (each rerun diverged, failed or had no verdict for the failure), so it is not known to pass"
	}
	s.reserve = 0 // after s.out() said why the search stopped
	// the shrunk sequence's own rate, as far as the budget allows
	measured := true
	for range cal.Baseline {
		if s.out() != "" {
			measured = false
			break
		}
		a := s.once(ctx, d.Set)
		if !a.ran || a.v == unknown {
			continue
		}
		res.rate.Of++
		if a.v == yes {
			res.rate.Reproduced++
		}
	}
	why := s.out()
	if why == "" {
		s.emptyRerun(ctx, "at the end")
	}
	// Minimal says the shrunk sequence's own rate was measured and the
	// last empty rerun ran: a budget or time out before them (a time, or
	// the test's deadline: the budget keeps them back) leaves it unknown
	switch {
	case !res.minimal:
	case !measured || why != "":
		res.minimal = false
		res.notMinimal = why + ", before the shrunk sequence's own rate was measured and a last rerun of no actions checked for leftovers"
	case res.rate.Of == 0:
		res.minimal = false
		res.notMinimal = fmt.Sprintf("the shrunk sequence's %d reruns after the shrink each diverged, failed or had no verdict for the failure: its rate is not known", cal.Baseline)
	}
	// a shrunk sequence that reproduces far less than the original showed
	// its failure in the shrink by state an earlier rerun left, or by
	// chance: not a minimal sequence of the failure
	if _, high := rateBounds(res.rate, cal.Alpha); res.rate.Of > 0 && (res.rate.Reproduced == 0 || high < rateBound(res.baseline, cal.Alpha)) {
		res.minimal = false
		res.notMinimal = fmt.Sprintf("the shrunk sequence reproduced %s after the shrink, the original %s: what it showed in the shrink came from state "+
			"an earlier rerun left (a Reset that leaks state: build a fresh environment per rerun, Reset nil) or from chance", res.rate, res.baseline)
	}
	res.reruns, res.reproduced, res.notReproduced, res.undecided, res.diverged, res.errs = s.reruns, s.reproduced, s.notReproduced, s.undecided, s.diverged, s.errs
	return res
}
