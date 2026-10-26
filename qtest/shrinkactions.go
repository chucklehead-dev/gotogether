package qtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/ddmin"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Resetter returns the system an ActionShrink reruns to its initial
// state: its data cleared, no work pending. ShrinkActions calls it before
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
	// failure, no verdict), Diverged those whose Rerun returned
	// ErrDiverged, Errors those that failed otherwise.
	Reruns, Reproduced, NotReproduced, Diverged, Errors int
	// Baseline is the original's rate (Calibration.Baseline reruns), and
	// PerCandidate the reruns a candidate got before it counted as
	// passing.
	Baseline     Rate
	PerCandidate int
	// Rate is Actions' own, rerun Calibration.Baseline times after the
	// shrink.
	Rate Rate
	// Minimal: every sequence of Actions with one action fewer was rerun
	// PerCandidate times without reproducing the failure (1-minimal).
	// False when the budget or time ran out (Actions is the smallest so
	// far) or a one-removal never ran (each rerun diverged); NotMinimal
	// says which.
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
	res.fill(&out.Reruns, &out.Reproduced, &out.NotReproduced, &out.Diverged, &out.Errors)
	out.Baseline, out.PerCandidate, out.Rate, out.Minimal, out.NotMinimal, out.keep = res.baseline, res.perCandidate, res.rate, res.minimal, res.notMinimal, res.keep
	// the minimal run, checked as Validate checks it, with what the
	// shrink did as its manifest's artifacts
	opts := append([]Option{}, s.Opts...)
	if c.driver == nil {
		opts = append(opts, Driver(validate.Driver{Name: "qtest.ShrinkActions", Args: []string{fmt.Sprintf("actions=%d of %d", len(out.Actions), out.Original)}}))
	}
	if arts, err := out.writeArtifacts(c.Dir); err != nil {
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
	return fmt.Sprintf("shrunk %d actions to %d (%s), reproducing %s: %v; %d reruns (%d reproduced, %d not, %d diverged, %d errors), the original's rate %s, %d per candidate",
		s.Original, len(s.Actions), m, s.Rate, s.Actions, s.Reruns, s.Reproduced, s.NotReproduced, s.Diverged, s.Errors, s.Baseline, s.PerCandidate)
}

// shrinkRecord is shrink.json, a manifest's artifact: what the shrink did.
type shrinkRecord struct {
	Original      int    `json:"original"`
	Actions       int    `json:"actions"`
	Keep          string `json:"keep"`
	Reruns        int    `json:"reruns"`
	Reproduced    int    `json:"reproduced"`
	NotReproduced int    `json:"notReproduced"`
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
// dir, for the minimal run's manifest.
func (s *ShrunkActions[A]) writeArtifacts(dir string) ([]validate.Artifact, error) {
	acts, err := json.MarshalIndent(s.Actions, "", "  ")
	if err != nil {
		printed := make([]string, len(s.Actions))
		for i, a := range s.Actions {
			printed[i] = fmt.Sprintf("%+v", a)
		}
		acts, _ = json.MarshalIndent(printed, "", "  ")
	}
	rec, err := json.MarshalIndent(shrinkRecord{Original: s.Original, Actions: len(s.Actions), Keep: s.keep.String(), Reruns: s.Reruns, Reproduced: s.Reproduced,
		NotReproduced: s.NotReproduced, Diverged: s.Diverged, Errors: s.Errors, Baseline: s.Baseline, PerCandidate: s.PerCandidate, Rate: s.Rate,
		Minimal: s.Minimal, NotMinimal: s.NotMinimal}, "", "  ")
	if err != nil {
		return nil, err
	}
	var arts []validate.Artifact
	for _, f := range []struct {
		name string
		data []byte
	}{{"actions", acts}, {"shrink", rec}} {
		p := filepath.Join(dir, f.name+".json")
		if err := os.WriteFile(p, append(f.data, '\n'), 0o644); err != nil {
			return nil, err
		}
		arts = append(arts, validate.Artifact{Name: f.name, Path: p})
	}
	return arts, nil
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
	// names are the binding's checked invariant names and actions the
	// model's (nil: not known), for Keep's: a misspelled one never holds
	names, actions map[string]bool

	start                                             time.Time
	reruns, reproduced, notReproduced, diverged, errs int
	inARow                                            int           // failed reruns in a row
	longest                                           time.Duration // the longest rerun, for the test's deadline
}

// shrinkResult is a shrink's outcome, for ShrunkActions.
type shrinkResult struct {
	set                                               []int
	steps                                             []qtrace.Step // a rerun of set that reproduced
	reruns, reproduced, notReproduced, diverged, errs int
	baseline, rate                                    Rate
	perCandidate                                      int
	minimal                                           bool
	notMinimal                                        string
	keep                                              Expectation
}

func (r *shrinkResult) fill(reruns, reproduced, notReproduced, diverged, errs *int) {
	*reruns, *reproduced, *notReproduced, *diverged, *errs = r.reruns, r.reproduced, r.notReproduced, r.diverged, r.errs
}

// attempt is one rerun's outcome.
type attempt struct {
	ran        bool // it ran and was judged: not diverged, not an error
	reproduced bool
	steps      []qtrace.Step // as recorded
	rep        *validate.Report
}

// out says why the shrink must stop ("": it need not): its budget, its
// time, or the test's deadline near (two of the longest reruns away).
func (s *shrinker) out() string {
	switch {
	case s.cal.Budget > 0 && s.reruns >= s.cal.Budget:
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

// once resets the system and reruns idx, judging what it recorded.
// Three failed reruns in a row end the test.
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
	// before the baseline names the failure (Keep's default), no rerun
	// reproduces: shrink recounts the baseline's
	a := attempt{ran: true, steps: steps, rep: rep, reproduced: s.keep.check != nil && s.keep.value(rep) == yes}
	if a.reproduced {
		s.reproduced++
	} else {
		s.notReproduced++
	}
	return a
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
// unknown when too few reruns ran (each diverged, the budget out).
// found keeps a reproducing rerun's steps.
func (s *shrinker) candidate(ctx context.Context, idx []int, n int, found map[string][]qtrace.Step) ddmin.Outcome {
	got, ran := 0, 0
	for tries := 0; tries < 2*n && ran < n; tries++ {
		if s.out() != "" {
			return ddmin.Unknown
		}
		a := s.once(ctx, idx)
		if !a.ran {
			continue
		}
		ran++
		if a.reproduced {
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
		if u := unknownNames([]Expectation{s.keep}, s.actions, s.names); len(u) > 0 {
			t.Fatalf("gotogether: ShrinkActions: Keep names %s: a misspelled name never matches", strings.Join(u, "; "))
		}
	}
	// the Resetter's contract: no actions, the same steps each time. An
	// environment's own steps (a server's shutdown drain, its settle)
	// repeat; what a Reset leaves behind (a job still running, a poller)
	// shows in the second rerun and not the first.
	var empty [2][]qtrace.Step
	for k := range empty {
		s.reruns++
		err = s.resetErr(ctx)
		if err == nil {
			empty[k], err = s.rerun(ctx, nil)
		}
		if err != nil {
			t.Fatalf("gotogether: ShrinkActions: a Reset, then a Rerun of no actions, failed: %v. Rerun must take an empty sequence: "+
				"two such reruns check that the Resetter leaves nothing behind", err)
		}
	}
	if d := sameRun(&Run{Steps: appSteps(empty[0])}, &Run{Steps: appSteps(empty[1])}); d != "" {
		t.Fatalf("gotogether: ShrinkActions: after Reset (%s), two Reruns of no actions recorded different steps (%s): the Resetter left work or state "+
			"behind that the second saw. Reset must return the system to its initial state, with nothing pending", s.resetter(), d)
	}
	// the baseline: the original's rate, and the failure to keep
	all := make([]int, s.n)
	for i := range all {
		all[i] = i
	}
	var base []attempt
	for range cal.Baseline {
		base = append(base, s.once(ctx, all))
	}
	if len(base) >= 2 && base[0].ran && base[1].ran && !base[0].rep.OK() && !base[1].rep.OK() {
		if a, b := signatureOf(base[0].rep), signatureOf(base[1].rep); a != b {
			t.Fatalf("gotogether: ShrinkActions: the original's first two reruns failed differently (%s; then %s): Reset (%s) leaves state one rerun "+
				"sees and the next does not. Make Reset clear it", a, b, s.resetter())
		}
	}
	if !keepGiven {
		for _, a := range base {
			if a.ran && !a.rep.OK() {
				s.keep = SameFailure(&Run{Report: a.rep})
				break
			}
		}
	}
	res := &shrinkResult{keep: s.keep}
	ranAny := false
	for _, a := range base {
		if !a.ran {
			continue
		}
		ranAny = true
		res.baseline.Of++
		if s.keep.check != nil && s.keep.value(a.rep) == yes {
			res.baseline.Reproduced++
			if res.steps == nil {
				res.steps = a.steps
			}
		}
	}
	// the counts made before Keep was known are recounted
	s.reproduced, s.notReproduced = res.baseline.Reproduced, s.notReproduced+s.reproduced-res.baseline.Reproduced
	switch {
	case !ranAny:
		t.Fatalf("gotogether: ShrinkActions: none of the original's %d reruns ran (%d diverged, %d failed): fix the Rerun", cal.Baseline, s.diverged, s.errs)
	case res.baseline.Reproduced == 0:
		want := "a failure"
		if s.keep.check != nil {
			want = s.keep.String()
		}
		t.Fatalf("gotogether: ShrinkActions: the original's %d actions do not reproduce %s in %d reruns: calibrate the rerun "+
			"(make it deterministic: the same actions, timings and data each time; or raise Calibration.Baseline for a rare failure)", s.n, want, res.baseline.Of)
	}
	n := cal.Reruns
	if n == 0 {
		n = rerunsFor(rateBound(res.baseline, cal.Alpha), cal.Need, cal.Alpha)
	}
	res.perCandidate = n
	found := map[string][]qtrace.Step{fmt.Sprint(all): res.steps}
	d := ddmin.Minimize(s.n, func(idx []int) ddmin.Outcome { return s.candidate(ctx, idx, n, found) },
		func() bool { return s.out() != "" })
	res.set = d.Set
	res.steps = found[fmt.Sprint(d.Set)]
	res.minimal = d.Minimal
	switch {
	case d.Stopped:
		res.notMinimal = s.out()
	case !d.Minimal:
		res.notMinimal = "a sequence with one action fewer never ran (each rerun diverged or failed), so it is not known to pass"
	}
	// the shrunk sequence's own rate, as far as the budget allows
	for range cal.Baseline {
		if s.out() != "" {
			break
		}
		a := s.once(ctx, d.Set)
		if !a.ran {
			continue
		}
		res.rate.Of++
		if a.reproduced {
			res.rate.Reproduced++
		}
	}
	res.reruns, res.reproduced, res.notReproduced, res.diverged, res.errs = s.reruns, s.reproduced, s.notReproduced, s.diverged, s.errs
	return res
}
