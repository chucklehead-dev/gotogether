package qshrink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/ddmin"
	"github.com/chucklehead-dev/gotogether/internal/stepdiff"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Resetter returns the system a shrink reruns to its initial state:
// its data cleared, no work pending. Only a fresh environment per rerun
// (Reset nil, Rerun building it) is safe against leaked state: a
// shrink's checks catch leaked work (steps a rerun of no actions records
// anew, a failure it shows), not state that changes later reruns without
// a step of its own (a counter Reset forgets). A shrink calls it before
// every rerun, and checks it first: a Reset then a rerun of no actions,
// twice, must record the same steps both times (up to recorders',
// actors', processes' and threads' names, and times). An environment's
// own steps (a fresh server's shutdown drain and settle) repeat and
// pass; a leak (a job the first left running, finishing in the second)
// does not. A Resetter that is a fmt.Stringer is named so in messages.
type Resetter interface {
	Reset(ctx context.Context) error
}

// A Shrink is a failing sequence of actions to shrink, known by index
// (0 to Actions-1: the action type is the caller's), and how to rerun
// and judge a subsequence of it.
type Shrink struct {
	// Actions is the original sequence's length.
	Actions int
	// Rerun does the actions at keep (indices of the original, in order)
	// on the system, from its initial state, and returns the steps the
	// run recorded (the system's and the driver's). An action it cannot
	// do returns ErrDiverged: the rerun counts for nothing. Another error
	// is a failed rerun; three in a row end the shrink with it.
	Rerun func(ctx context.Context, keep []int) ([]qtrace.Step, error)
	// Reset returns the system to its initial state before each rerun:
	// nil only if Rerun builds a fresh environment itself.
	Reset Resetter
	// Judge checks a rerun's steps against the model (a Checker's Judge,
	// with the searches the run needs).
	Judge func(ctx context.Context, steps []qtrace.Step) (*validate.Report, error)
	// Keep is the failure each smaller sequence must still show; nil:
	// SameFailure of the original's first rerun with a decided failure.
	Keep Keep
	// Calibration sets the reruns: the original's baseline, each
	// candidate's, the budget.
	Calibration Calibration
}

// Result is what a shrink found: the smallest sequence that still fails
// as the original did, and what the reruns showed.
type Result struct {
	// Set is the shrunk sequence, the original's indices it keeps, and
	// Steps a rerun of it that showed the failure.
	Set   []int
	Steps []qtrace.Step
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
	// Rate is Set's own, rerun Calibration.Baseline times after the
	// shrink.
	Rate Rate
	// Minimal: every sequence of Set with one action fewer was rerun
	// PerCandidate times without reproducing the failure (1-minimal, each
	// removal with chance at least 1−α), Set's own rate was measured
	// after the shrink (Rate) and is one the original's allows, and a
	// last rerun of no actions showed nothing left behind. False when the
	// budget or time ran out (Set is the smallest so far, or its rate and
	// the last rerun were not made), a one-removal never ran (each rerun
	// diverged) or Set's reruns after the shrink had no verdict;
	// NotMinimal says which. A Budget keeps Calibration.Baseline+1 reruns
	// for the rate and the last rerun, so a budget that runs out in the
	// search still measures them.
	Minimal    bool
	NotMinimal string
	// Keep is the failure kept: the one given, or the default.
	Keep Keep
}

// Run shrinks the sequence to a 1-minimal one that still fails the same
// way (ddmin): candidates are rerun on the system, from the state the
// Resetter leaves, and their steps judged. A failure that shows only
// sometimes is calibrated (Calibration): each candidate is rerun until it
// reproduces or enough reruns have not, and a rerun that did not run
// (ErrDiverged) counts for neither. A shrunk run that did not reproduce
// is never a pass: it is a sequence the failure was not seen in.
//
// It fails, naming the fix, when the Resetter leaves state behind (two
// reruns of no actions record different steps, or the original's first
// two reruns fail differently), when the original never reproduces, and
// when three reruns in a row fail. It stops early, not minimal, when the
// budget, the time or ctx's deadline is near.
func (s Shrink) Run(ctx context.Context) (res *Result, err error) {
	if s.Rerun == nil || s.Judge == nil {
		return nil, errors.New("a shrink needs a Rerun (which does a sequence of actions on the system and returns its steps) and a Judge")
	}
	sh := &shrinker{ctx: ctx, n: s.Actions, rerun: s.Rerun, reset: s.Reset, judge: s.Judge, keep: s.Keep, cal: s.Calibration}
	defer func() {
		switch p := recover().(type) {
		case nil:
		case fatal:
			res, err = nil, p.err
		default:
			panic(p)
		}
	}()
	return sh.shrink(), nil
}

// fatal is a refusal ending a shrink: Run returns its error.
type fatal struct{ err error }

func (s *shrinker) fatalf(format string, args ...any) {
	panic(fatal{fmt.Errorf(format, args...)})
}

// String summarizes the shrink, its sequence as indices.
func (r *Result) String() string {
	m := "1-minimal"
	if !r.Minimal {
		m = "not minimal: " + r.NotMinimal
	}
	return fmt.Sprintf("shrunk %d actions to %d (%s), reproducing %s: %v; %d reruns (%d reproduced, %d not, %d undecided, %d diverged, %d errors), the original's rate %s, %d per candidate",
		r.Original, len(r.Set), m, r.Rate, r.Set, r.Reruns, r.Reproduced, r.NotReproduced, r.Undecided, r.Diverged, r.Errors, r.Baseline, r.PerCandidate)
}

// record is shrink.json, a manifest's artifact: what the shrink did.
type record struct {
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

// WriteArtifacts writes actions.json (actions, the shrunk sequence, as
// JSON, or each as Go prints it when they do not marshal) and
// shrink.json (what the shrink did) into dir, for the minimal run's
// manifest (validate.Manifest.Artifacts), and returns them named
// "actions" and "shrink".
func (r *Result) WriteArtifacts(dir string, actions []any) ([]validate.Artifact, error) {
	acts, err := json.MarshalIndent(actions, "", "  ")
	if err != nil {
		printed := make([]string, len(actions))
		for i, a := range actions {
			printed[i] = fmt.Sprintf("%+v", a)
		}
		acts, _ = json.MarshalIndent(printed, "", "  ")
	}
	keep := ""
	if r.Keep != nil {
		keep = r.Keep.String()
	}
	rec, err := json.MarshalIndent(record{Original: r.Original, Actions: len(actions), Keep: keep, Reruns: r.Reruns, Reproduced: r.Reproduced,
		NotReproduced: r.NotReproduced, Undecided: r.Undecided, Diverged: r.Diverged, Errors: r.Errors, Baseline: r.Baseline, PerCandidate: r.PerCandidate, Rate: r.Rate,
		Minimal: r.Minimal, NotMinimal: r.NotMinimal}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

// shrinker is a Shrink running.
type shrinker struct {
	ctx   context.Context
	n     int
	rerun func(ctx context.Context, idx []int) ([]qtrace.Step, error)
	reset Resetter
	judge func(ctx context.Context, steps []qtrace.Step) (*validate.Report, error)
	keep  Keep
	cal   Calibration
	// reserve is how many reruns of the budget out keeps back: the shrunk
	// sequence's rate and the last empty rerun's, while ddmin searches
	reserve int
	start   time.Time
	// empty are the steps a rerun of no actions records (the
	// environment's own), which every later one must repeat
	empty                                                        []qtrace.Step
	reruns, reproduced, notReproduced, undecided, diverged, errs int
	inARow                                                       int           // failed reruns in a row
	longest                                                      time.Duration // the longest rerun, for the context's deadline
}

// attempt is one rerun's outcome.
type attempt struct {
	ran   bool  // it ran and was judged: not diverged, not an error
	v     Shown // Keep's value of it (Unknown before Keep is known)
	steps []qtrace.Step
	rep   *validate.Report
}

// out says why the shrink must stop ("": it need not): its budget, its
// time, or the context's deadline near (two of the longest reruns away:
// a test's, qtest.ShrinkActions gives it).
func (s *shrinker) out() string {
	switch {
	case s.cal.Budget > 0 && s.reruns >= s.cal.Budget-s.reserve:
		return fmt.Sprintf("its budget of %d reruns ran out (raise Calibration.Budget)", s.cal.Budget)
	case s.cal.Time > 0 && time.Since(s.start) >= s.cal.Time:
		return fmt.Sprintf("its time of %v ran out (raise Calibration.Time)", s.cal.Time)
	}
	if dl, ok := s.ctx.Deadline(); ok && time.Until(dl) < 2*s.longest+5*time.Second {
		return "the deadline is near (a test's: raise go test's -timeout; or set Calibration.Time)"
	}
	return ""
}

// once resets the system and reruns idx, judging what it recorded, and
// counts it by Keep's value (once Keep is known). Three failed reruns in
// a row end the shrink.
func (s *shrinker) once(ctx context.Context, idx []int) attempt {
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
		rep, err = s.judge(ctx, steps)
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
			s.fatalf("three reruns in a row failed, the last of %d actions with: %v. Fix the rerun (return ErrDiverged for actions it cannot do)", len(idx), err)
		}
		return attempt{}
	}
	s.inARow = 0
	a := attempt{ran: true, v: Unknown, steps: steps, rep: rep}
	if s.keep != nil {
		a.v = s.keep.Shows(rep)
		s.count(a.v)
	}
	return a
}

// count counts a judged rerun by Keep's value of it: a rerun with no
// verdict for the failure (a lost tail, an invariant not judged) is
// neither reproduced nor not.
func (s *shrinker) count(v Shown) {
	switch v {
	case Yes:
		s.reproduced++
	case No:
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
	if n, ok := s.reset.(fmt.Stringer); ok {
		return n.String()
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
		if !a.ran || a.v == Unknown {
			continue
		}
		ran++
		if a.v == Yes {
			if len(idx) == 0 {
				s.fatalf("a rerun of no actions reproduced %s, after reruns of the original did not show it there: "+
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

// emptyRerun resets and reruns no actions, and ends the shrink when it records
// other steps than the first such rerun (when, says which) or, Keep
// known, reproduces the failure: work a rerun left that Reset did not
// clear (a job finishing later, a poller), shown by the next.
func (s *shrinker) emptyRerun(ctx context.Context, when string) {
	s.reruns++
	err := s.resetErr(ctx)
	var steps []qtrace.Step
	if err == nil {
		steps, err = s.rerun(ctx, nil)
	}
	if err != nil {
		s.fatalf("a Reset, then a Rerun of no actions (%s), failed: %v. Rerun must take an empty sequence: "+
			"such reruns check that the Resetter leaves nothing behind", when, err)
	}
	if s.empty == nil {
		s.empty = append([]qtrace.Step{}, appSteps(steps)...) // non-nil: the first is taken
		return
	}
	if d := stepdiff.Diff(s.empty, appSteps(steps)); d != "" {
		s.fatalf("after Reset (%s), a Rerun of no actions (%s) recorded other steps than the first (%s): the Resetter "+
			"left work or state behind that it saw. Reset must return the system to its initial state, with nothing pending", s.resetter(), when, d)
	}
	if s.keep == nil {
		return
	}
	rep, err := s.judge(ctx, steps)
	if err != nil {
		s.fatalf("checking a Rerun of no actions (%s): %v", when, err)
	}
	if s.keep.Shows(rep) == Yes {
		s.fatalf("a Rerun of no actions (%s) reproduced %s: "+
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
func (s *shrinker) shrink() *Result {
	ctx := s.ctx
	s.start = time.Now()
	cal, err := s.cal.withDefaults()
	if err != nil {
		s.fatalf("%v", err)
	}
	s.cal = cal
	keepGiven := s.keep != nil
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
				s.fatalf("the original's reruns fail two ways (%s; then %s): either the system fails both ways, "+
					"and Keep must name the one to shrink (qtest.Violates(...), qtest.Rejects(...); gotogether shrink keeps the first failure alone: "+
					"make its reruns fail one way), or Reset (%s) leaves state one rerun sees and the next does not, and must clear it", a, b, s.resetter())
			}
		}
		if len(first) > 0 {
			s.keep = SameFailure(first[0])
		}
	}
	res := &Result{Original: s.n}
	ranAny := false
	for _, a := range base {
		if !a.ran {
			continue
		}
		ranAny = true
		if s.keep == nil {
			continue
		}
		v := s.keep.Shows(a.rep)
		if !keepGiven {
			s.count(v) // counted now Keep is known
		}
		if v == Unknown {
			continue
		}
		res.Baseline.Of++
		if v == Yes {
			res.Baseline.Reproduced++
			if res.Steps == nil {
				res.Steps = a.steps
			}
		}
	}
	switch {
	case !ranAny:
		s.fatalf("none of the original's %d reruns ran (%d diverged, %d failed): fix the Rerun", cal.Baseline, s.diverged, s.errs)
	case res.Baseline.Reproduced == 0:
		want := "a decided failure"
		if s.keep != nil {
			want = s.keep.String()
		}
		s.fatalf("the original's %d actions do not reproduce %s in %d reruns (%d without a verdict): calibrate the rerun "+
			"(make it deterministic: the same actions, timings and data each time, every step collected; or raise Calibration.Baseline for a rare failure)",
			s.n, want, cal.Baseline, s.undecided)
	}
	s.emptyRerun(ctx, "after the original's reruns")
	n := cal.Reruns
	if n == 0 {
		n = rerunsFor(rateBound(res.Baseline, cal.Alpha), cal.Need, cal.Alpha)
	}
	res.PerCandidate = n
	found := map[string][]qtrace.Step{fmt.Sprint(all): res.Steps}
	// the search leaves the budget enough for what Minimal also needs
	s.reserve = cal.Baseline + 1
	d := ddmin.Minimize(s.n, func(idx []int) ddmin.Outcome { return s.candidate(ctx, idx, n, found) },
		func() bool { return s.out() != "" })
	res.Set = d.Set
	res.Steps = found[fmt.Sprint(d.Set)]
	res.Minimal = d.Minimal
	switch {
	case d.Stopped:
		res.NotMinimal = s.out()
	case !d.Minimal:
		res.NotMinimal = "a sequence with one action fewer was never decided (each rerun diverged, failed or had no verdict for the failure), so it is not known to pass"
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
		if !a.ran || a.v == Unknown {
			continue
		}
		res.Rate.Of++
		if a.v == Yes {
			res.Rate.Reproduced++
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
	case !res.Minimal:
	case !measured || why != "":
		res.Minimal = false
		res.NotMinimal = why + ", before the shrunk sequence's own rate was measured and a last rerun of no actions checked for leftovers"
	case res.Rate.Of == 0:
		res.Minimal = false
		res.NotMinimal = fmt.Sprintf("the shrunk sequence's %d reruns after the shrink each diverged, failed or had no verdict for the failure: its rate is not known", cal.Baseline)
	}
	// a shrunk sequence that reproduces far less than the original showed
	// its failure in the shrink by state an earlier rerun left, or by
	// chance: not a minimal sequence of the failure
	if _, high := rateBounds(res.Rate, cal.Alpha); res.Rate.Of > 0 && (res.Rate.Reproduced == 0 || high < rateBound(res.Baseline, cal.Alpha)) {
		res.Minimal = false
		res.NotMinimal = fmt.Sprintf("the shrunk sequence reproduced %s after the shrink, the original %s: what it showed in the shrink came from state "+
			"an earlier rerun left (a Reset that leaks state: build a fresh environment per rerun, Reset nil) or from chance", res.Rate, res.Baseline)
	}
	res.Reruns, res.Reproduced, res.NotReproduced, res.Undecided, res.Diverged, res.Errors = s.reruns, s.reproduced, s.notReproduced, s.undecided, s.diverged, s.errs
	res.Keep = s.keep
	return res
}
