package qtest

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// fakeApp is a system in plain Go whose actions are letters (indices
// 0..7): bugA shows (the invariant noBugA fails) where a and when say,
// bugB (a step the model cannot take) where b says. Its failures are
// steps it records, judged by fakeJudge without quint.
type fakeApp struct {
	a, b    func(set uint) bool
	p       float64 // the chance bugA shows where a holds (1: always)
	rng     *rand.Rand
	diverge func(set uint) bool // the rerun cannot do these actions
	errs    int                 // the next errs reruns fail
	// poller: each rerun starts a poller Reset does not stop, recording a
	// poll in every later rerun; leakState: Reset leaves a count of
	// reruns, and the second shows bugB instead; shutdown: each rerun's
	// fresh server records its drain and a settle, under a recorder of
	// its own, at the time it ends (no leak: the environment's own steps)
	poller, leakState, shutdown bool
	runs, pollers, boots        int
	// slowAt: the rerun that boots the slowAt-th time sleeps slow
	slowAt int
	slow   time.Duration
}

type fakeReset struct{ app *fakeApp }

func (r fakeReset) Reset(context.Context) error {
	if !r.app.leakState {
		r.app.runs = 0
	}
	return nil
}

func (f *fakeApp) rerun(_ context.Context, idx []int) (steps []qtrace.Step, err error) {
	var set uint
	add := func(action string, args map[string]any) {
		steps = append(steps, qtrace.Step{Recorder: "app", Seq: uint64(len(steps) + 1), Action: action, Args: args})
	}
	for range f.pollers {
		add("poll", nil)
	}
	if f.poller {
		f.pollers++
	}
	f.boots++
	if f.boots == f.slowAt {
		time.Sleep(f.slow)
	}
	if f.shutdown {
		server := fmt.Sprintf("server-%d", f.boots)
		defer func() {
			steps = append(steps, qtrace.Step{Recorder: server, Seq: 1, Action: "drained", Time: time.Now()},
				qtrace.Step{Recorder: server, Seq: 2, Settled: true, Time: time.Now()})
		}()
	}
	for _, i := range idx {
		set |= 1 << i
		add("do", map[string]any{"letter": string(rune('a' + i))})
	}
	if f.errs > 0 && len(idx) > 0 {
		f.errs--
		return nil, errors.New("the browser crashed")
	}
	if f.diverge != nil && f.diverge(set) {
		return nil, fmt.Errorf("cancel: no job to cancel: %w", ErrDiverged)
	}
	if len(idx) > 0 {
		f.runs++
	}
	switch {
	case f.leakState && f.runs > 1 && len(idx) > 0:
		add("bugB", nil)
	case f.b != nil && f.b(set):
		add("bugB", nil)
	case f.a != nil && f.a(set) && (f.p >= 1 || f.rng.Float64() < f.p):
		add("bugA", map[string]any{"run": f.runs}) // arguments a failure's identity ignores
	}
	return steps, nil
}

// fakeJudge judges fakeApp's steps as the binding would: bugB a
// disabled step, bugA a violation of noBugA.
func fakeJudge(_ context.Context, steps []qtrace.Step) (*validate.Report, []qtrace.Step, error) {
	res := &validate.Result{Actor: "app", Conforms: validate.Pass}
	inv := validate.InvariantResult{Name: "noBugA", Verdict: validate.Pass}
	for i := range steps {
		switch steps[i].Action {
		case "bugB":
			res.Conforms, res.Failure = validate.Fail, &validate.Failure{Kind: "disabled", Index: i, Call: "bugB", Step: &steps[i]}
		case "bugA":
			inv.Verdict, inv.Failure = validate.Fail, &validate.Failure{Kind: "invariant", Index: i, Step: &steps[i]}
		}
	}
	if res.Conforms == validate.Pass {
		res.Invariants = []validate.InvariantResult{inv}
	}
	return &validate.Report{Actors: []*validate.Result{res}}, steps, nil
}

// shrinkFake shrinks n letters on app with the fake judge, on t (a
// failTB's refusal ends the goroutine: the result is then nil).
func shrinkFake(t testing.TB, app *fakeApp, n int, keep Expectation, cal Calibration) *shrinkResult {
	if app.rng == nil {
		app.rng = rand.New(rand.NewPCG(1, 1))
	}
	if app.p == 0 {
		app.p = 1
	}
	s := &shrinker{t: t, n: n, rerun: app.rerun, reset: fakeReset{app}, judge: fakeJudge, keep: keep, cal: cal}
	var res *shrinkResult
	done := make(chan struct{})
	go func() { // a failTB's Fatalf ends it (runtime.Goexit)
		defer close(done)
		res = s.shrink()
	}()
	<-done
	if _, fake := t.(*failTB); res == nil && !fake {
		t.FailNow() // the refusal was logged; end the test on its own goroutine
	}
	return res
}

func maskOf(set []int) uint {
	var m uint
	for _, i := range set {
		m |= 1 << i
	}
	return m
}

// oneMinimal reports whether set fails by fails and every one-removal
// passes.
func oneMinimal(set uint, fails func(uint) bool) bool {
	if !fails(set) {
		return false
	}
	for s := set; s != 0; s &= s - 1 {
		if fails(set &^ (1 << bits.TrailingZeros(s))) {
			return false
		}
	}
	return true
}

// TestShrinkActionsAgainstBruteForce shrinks sequences of up to 8 letters
// (all 256 subsequences enumerable) against brute force: for a monotone
// failure the result is one of the inclusion-minimal failing sets (the
// minimum when it is unique) and every one-removal passes; for any
// failure it is 1-minimal; for a failure that shows at rate 1/2, the
// calibrated reruns reach a 1-minimal sequence of the true failure in
// at least 1−α of 200 shrinks.
func TestShrinkActionsAgainstBruteForce(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(7, 8))
	for trial := range 240 {
		n := 1 + trial%8
		full := uint(1)<<n - 1
		var fails func(uint) bool
		monotone := trial%2 == 0
		if monotone {
			var cores []uint
			for range 1 + rng.IntN(3) {
				cores = append(cores, 1+rng.UintN(full)) // not empty: no actions, no failure
			}
			fails = func(m uint) bool {
				for _, c := range cores {
					if m&c == c {
						return true
					}
				}
				return false
			}
		} else {
			table := map[uint]bool{full: true}
			for m := uint(1); m < full; m++ { // no actions, no failure: the Resetter's contract
				table[m] = rng.IntN(3) == 0
			}
			fails = func(m uint) bool { return table[m] }
		}
		if !fails(full) {
			continue
		}
		res := shrinkFake(t, &fakeApp{a: fails}, n, Expectation{}, Calibration{})
		got := maskOf(res.set)
		if !oneMinimal(got, fails) || !res.minimal || res.perCandidate != 2 || res.rate != (Rate{10, 10}) {
			t.Fatalf("trial %d: %v (minimal %v, %d per candidate, rate %s) is no 1-minimal failing set", trial, res.set, res.minimal, res.perCandidate, res.rate)
		}
		if !monotone {
			continue
		}
		var least []uint
		for m := uint(0); m <= full; m++ {
			if oneMinimal(m, fails) {
				least = append(least, m) // monotone: 1-minimal is inclusion-minimal
			}
		}
		if !slices.Contains(least, got) || len(least) == 1 && got != least[0] {
			t.Fatalf("trial %d: %b is none of brute force's inclusion-minimal failing sets %b", trial, got, least)
		}
	}

	// a flaky failure: a, c and f needed, shown half the time
	const trials, alpha = 1000, 0.05
	fails := func(m uint) bool { return m&0b100101 == 0b100101 }
	good := 0
	for trial := range trials {
		f := &failTB{TB: t}
		res := shrinkFake(f, &fakeApp{a: fails, p: 0.5, rng: rand.New(rand.NewPCG(uint64(trial), 3))}, 8, Expectation{}, Calibration{Alpha: alpha})
		if res != nil && oneMinimal(maskOf(res.set), fails) {
			good++
		}
	}
	t.Logf("a failure shown half the time: 1-minimal in %d of %d shrinks", good, trials)
	if float64(good) < (1-alpha)*trials {
		t.Fatalf("a failure shown half the time shrank to its 1-minimal sequence in %d of %d shrinks, want at least %v", good, trials, (1-alpha)*trials)
	}
}

// The failure kept is the original's (SameFailure), not any failure: a
// smaller sequence that shows another bug is not a reproduction, and the
// shrink does not drift to it.
func TestShrinkActionsKeepsTheFailure(t *testing.T) {
	t.Parallel()
	// bugA needs a and c; bugB shows with b where c is absent
	app := &fakeApp{a: func(m uint) bool { return m&0b101 == 0b101 }, b: func(m uint) bool { return m&0b10 != 0 && m&0b100 == 0 }}
	res := shrinkFake(t, app, 8, Expectation{}, Calibration{})
	if maskOf(res.set) != 0b101 || !res.minimal || res.keep.String() != "the same failure (invariant noBugA)" {
		t.Fatalf("got %v (minimal %v, keeping %s), want [0 2]: bugA's sequence, not bugB's", res.set, res.minimal, res.keep)
	}
	if res.notReproduced == 0 {
		t.Fatal("no rerun showed bugB: the test does not test the drift")
	}
	// a Keep given is the claim kept
	res = shrinkFake(t, &fakeApp{a: app.a, b: app.b}, 8, Violates("noBugA"), Calibration{})
	if maskOf(res.set) != 0b101 {
		t.Fatalf("Keep Violates(noBugA): got %v, want [0 2]", res.set)
	}
}

// A rerun that diverged (ErrDiverged) is never a pass: a sequence whose
// one-removal only ever diverges is not known minimal.
func TestShrinkActionsDivergedIsNoPass(t *testing.T) {
	t.Parallel()
	// the failure needs c alone, but c without a cannot be done
	app := &fakeApp{a: func(m uint) bool { return m&0b100 != 0 }, diverge: func(m uint) bool { return m&0b100 != 0 && m&1 == 0 }}
	res := shrinkFake(t, app, 4, Expectation{}, Calibration{})
	if maskOf(res.set) != 0b101 || res.minimal || res.diverged == 0 || !strings.Contains(res.notMinimal, "never decided") {
		t.Fatalf("got %v minimal %v (%d diverged, %q), want [0 2], not minimal: [2] never ran", res.set, res.minimal, res.diverged, res.notMinimal)
	}
}

// A Resetter that leaves state behind is refused, naming it: two reruns
// of no actions that record different steps (a poller the first left
// running), or a second rerun of the original that fails otherwise than
// the first. An environment whose every run records its own shutdown is
// no leak.
func TestShrinkActionsLeakyResetter(t *testing.T) {
	t.Parallel()
	bug := func(m uint) bool { return m&1 != 0 }
	for _, c := range []struct {
		app  *fakeApp
		want string
	}{
		{&fakeApp{a: bug, poller: true}, "after Reset (qtest.fakeReset), a Rerun of no actions (the second) recorded other steps than the first (step 0: the second run recorded one more (poll())): the Resetter left work or state behind"},
		{&fakeApp{a: bug, leakState: true}, "the original's reruns fail two ways (invariant noBugA; then disabled bugB): either the system fails both ways, and Keep must name the one to shrink"},
	} {
		f := &failTB{TB: t}
		if res := shrinkFake(f, c.app, 4, Expectation{}, Calibration{}); res != nil || len(f.errs) != 1 || !strings.Contains(f.errs[0], c.want) {
			t.Errorf("errors %q, want %q", f.errs, c.want)
		}
	}
	// a fresh server per rerun records its drain and settle each time,
	// under its own recorder and at its own time: the same steps
	res := shrinkFake(t, &fakeApp{a: func(m uint) bool { return m&0b101 == 0b101 }, shutdown: true}, 4, Expectation{}, Calibration{})
	if maskOf(res.set) != 0b101 || !res.minimal {
		t.Fatalf("with a shutdown step: got %v minimal %v, want [0 2]", res.set, res.minimal)
	}
}

// The shrink refuses what it cannot do, naming the fix: an original that
// never reproduces, a Keep a passing run meets, three failed reruns in a
// row.
func TestShrinkActionsRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		app  *fakeApp
		keep Expectation
		want string
	}{
		{&fakeApp{a: func(uint) bool { return false }}, Expectation{}, "the original's 4 actions do not reproduce a decided failure in 10 reruns (0 without a verdict): calibrate the rerun"},
		{&fakeApp{a: func(uint) bool { return false }}, Violates("noBugA"), "do not reproduce a violation of noBugA (the run conforming) in 10 reruns (0 without a verdict): calibrate the rerun"},
		{&fakeApp{a: func(m uint) bool { return m != 0 }}, Conforms(), "Keep conformance, with every invariant holding describes a passing run"},
		{&fakeApp{a: func(m uint) bool { return m != 0 }}, Finishes(), "describes a passing run"},
		{&fakeApp{a: func(m uint) bool { return m != 0 }, errs: 3}, Expectation{}, "three reruns in a row failed, the last of 4 actions with: the browser crashed"},
	} {
		f := &failTB{TB: t}
		if res := shrinkFake(f, c.app, 4, c.keep, Calibration{}); res != nil || len(f.errs) != 1 || !strings.Contains(f.errs[0], c.want) {
			t.Errorf("errors %q, want %q", f.errs, c.want)
		}
	}
	// two failed reruns, then one that runs: no abort
	if res := shrinkFake(t, &fakeApp{a: func(m uint) bool { return m&1 != 0 }, errs: 2}, 4, Expectation{}, Calibration{}); res.errs != 2 || maskOf(res.set) != 1 {
		t.Errorf("got %v with %d errors, want [0] with 2", res.set, res.errs)
	}
}

// A budget that runs out ends the shrink with the smallest sequence so
// far, not minimal, saying why.
func TestShrinkActionsBudget(t *testing.T) {
	t.Parallel()
	res := shrinkFake(t, &fakeApp{a: func(m uint) bool { return m&0b10000001 == 0b10000001 }}, 8, Expectation{}, Calibration{Budget: 14})
	if res.minimal || !strings.Contains(res.notMinimal, "budget of 14 reruns ran out") || res.reruns != 14 || !oneOf(res.set) {
		t.Fatalf("got %v minimal %v after %d reruns (%q)", res.set, res.minimal, res.reruns, res.notMinimal)
	}
}

// Minimal holds only with the shrunk sequence's own rate measured and
// the last rerun of no actions made: a budget just big enough for the
// search (5 reruns before it, 16 in it) keeps back Baseline+1 reruns
// for them, ending the search early, not minimal, but with the rate
// measured; three more give the search its 16 and a minimal shrink. A
// time that runs out in the search's last rerun leaves Minimal false,
// naming it.
func TestShrinkActionsMinimalNeedsItsRate(t *testing.T) {
	t.Parallel()
	needs := func(m uint) bool { return m&0b10000001 == 0b10000001 }
	res := shrinkFake(t, &fakeApp{a: needs}, 8, Expectation{}, Calibration{Budget: 21, Baseline: 2, Reruns: 1})
	if res.minimal || !strings.Contains(res.notMinimal, "budget of 21 reruns ran out") || res.rate != (Rate{2, 2}) || res.reruns != 21 || !oneOf(res.set) {
		t.Errorf("a budget the search alone needs: %v minimal %v, rate %s, %d reruns (%q)", res.set, res.minimal, res.rate, res.reruns, res.notMinimal)
	}
	res = shrinkFake(t, &fakeApp{a: needs}, 8, Expectation{}, Calibration{Budget: 24, Baseline: 2, Reruns: 1})
	if !res.minimal || maskOf(res.set) != 0b10000001 || res.rate != (Rate{2, 2}) || res.reruns != 24 {
		t.Errorf("a budget for the search, the rate and the last rerun: %v minimal %v, rate %s, %d reruns (%q)", res.set, res.minimal, res.rate, res.reruns, res.notMinimal)
	}
	res = shrinkFake(t, &fakeApp{a: needs, slowAt: 21, slow: 600 * time.Millisecond}, 8, Expectation{}, Calibration{Time: 300 * time.Millisecond, Baseline: 2, Reruns: 1})
	if res.minimal || !strings.Contains(res.notMinimal, "time of 300ms ran out") || !strings.Contains(res.notMinimal, "before the shrunk sequence's own rate was measured") ||
		res.rate.Of != 0 || maskOf(res.set) != 0b10000001 {
		t.Errorf("a time out after the search: %v minimal %v, rate %s, %d reruns (%q)", res.set, res.minimal, res.rate, res.reruns, res.notMinimal)
	}
}

// oneOf: a set that still holds the failure's two letters.
func oneOf(set []int) bool { return maskOf(set)&0b10000001 == 0b10000001 }

// pressApp types letters: each a step of letters.qnt's press, recorded
// by the driver.
type pressApp struct{ resets int }

func (p *pressApp) Reset(context.Context) error { p.resets++; return nil }

func (p *pressApp) Play(_ context.Context, letters []string) ([]qtrace.Step, error) {
	steps := make([]qtrace.Step, len(letters))
	for i, l := range letters {
		steps[i] = qtrace.Step{Recorder: "driver", Seq: uint64(i + 1), Action: "press", Args: map[string]any{"letter": l}}
	}
	return steps, nil
}

// ShrinkActions against the model: each rerun's steps checked by quint,
// the failure kept the original's (a step the model cannot take: d after
// b) or the one Keep names (noBug's violation, which only a conforming
// run shows), the minimal run's manifest written under
// GOTOGETHER_EVIDENCE with what the shrink did among its artifacts.
func TestShrinkActions(t *testing.T) {
	RequireQuint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", dir)
	journey := []string{"e", "a", "b", "f", "c", "d"}
	if testing.Short() {
		journey = []string{"b", "e", "d"} // each set a quint check: a second or two
	}
	app := &pressApp{}
	// a misspelled Keep is refused before any rerun
	f := &failTB{TB: t}
	func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			ShrinkActions(f, journey, ActionShrink[string]{Binding: "testdata/letters.yaml", Rerun: app.Play, Keep: Rejects("presss")})
		}()
		<-done
	}()
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], `presss is not an action of the model (did you mean press?; its actions: init, press, step); a misspelled name never matches`) || app.resets != 0 {
		t.Fatalf("errors %q", f.errs)
	}
	shrunk := ShrinkActions(t, journey, ActionShrink[string]{Binding: "testdata/letters.yaml", Rerun: app.Play, Reset: app, Calibration: Calibration{Reruns: 1}})
	if !slices.Equal(shrunk.Actions, []string{"b", "d"}) || !shrunk.Minimal || shrunk.Original != len(journey) || shrunk.Rate != (Rate{10, 10}) || app.resets != shrunk.Reruns {
		t.Fatalf("got %s (%d resets)", shrunk, app.resets)
	}
	shrunk.Expect(t, Rejects("press"), AtMostActions(2))
	f = &failTB{TB: t}
	func() {
		done := make(chan struct{})
		go func() { defer close(done); shrunk.Expect(f, AtMostActions(1)) }()
		<-done
	}()
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "want a sequence shrunk to at most 1 action(s), it has 2: shrunk "+fmt.Sprint(len(journey))+" actions to 2 (1-minimal)") {
		t.Errorf("errors %q", f.errs)
	}
	m, err := validate.ReadManifest(filepath.Join(dir, pkgDir(), t.Name(), "run1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range m.Artifacts {
		names = append(names, a.Name)
		if a.SHA256 == "" {
			t.Errorf("artifact %s not hashed", a.Name)
		}
	}
	if m.Format != validate.ManifestFormat || m.Driver == nil || m.Driver.Name != "qtest.ShrinkActions" || !slices.Equal(names, []string{"actions", "shrink"}) || m.Verdict.Verdict != validate.Fail {
		t.Errorf("manifest %+v (driver %+v, artifacts %v)", m, m.Driver, names)
	}
	data, err := os.ReadFile(m.Artifacts[0].Path)
	if err != nil || strings.Join(strings.Fields(string(data)), "") != `["b","d"]` {
		t.Errorf("actions.json: %q, %v", data, err)
	}

	if testing.Short() {
		return // a second shrink, a dozen more quint checks
	}
	violation := ShrinkActions(t, []string{"d", "e", "a", "b", "f", "c"}, ActionShrink[string]{Binding: "testdata/letters.yaml", Rerun: app.Play, Reset: app,
		Keep: Violates("noBug"), Calibration: Calibration{Reruns: 1}})
	if !slices.Equal(violation.Actions, []string{"a", "c"}) || !violation.Minimal {
		t.Fatalf("Keep Violates(noBug): got %s", violation)
	}
	violation.Expect(t, Violates("noBug"), AtMostActions(2))
}

// judgeWith is fakeJudge with its report changed by edit for the reruns
// of n letters.
func judgeWith(n int, edit func(*validate.Result)) func(context.Context, []qtrace.Step) (*validate.Report, []qtrace.Step, error) {
	return func(ctx context.Context, steps []qtrace.Step) (*validate.Report, []qtrace.Step, error) {
		rep, st, err := fakeJudge(ctx, steps)
		k := 0
		for _, s := range steps {
			if s.Action == "do" {
				k++
			}
		}
		if k == n {
			edit(rep.Actors[0])
		}
		return rep, st, err
	}
}

// A rerun with no verdict for the failure kept (a lost tail, an
// invariant left unjudged, an undecided actor) is no pass: a one-removal
// decided by such reruns alone leaves the result not minimal, though the
// failure needs one action and every rerun of it hides it.
func TestShrinkActionsUnknownIsNoPass(t *testing.T) {
	t.Parallel()
	for mode, edit := range map[string]func(*validate.Result){
		"lost tail": func(r *validate.Result) {
			r.Lost = []qtrace.Lost{{Recorder: "app"}}
			r.Invariants[0].Verdict, r.Invariants[0].Failure = validate.NotEvaluated, nil
		},
		"invariant unjudged": func(r *validate.Result) {
			r.Invariants[0].Verdict, r.Invariants[0].Failure = validate.NotEvaluated, nil
		},
		"undecided": func(r *validate.Result) {
			r.Conforms = validate.NotEvaluated
			r.Invariants[0].Verdict, r.Invariants[0].Failure = validate.NotEvaluated, nil
		},
	} {
		app := &fakeApp{a: func(m uint) bool { return m&1 != 0 }, p: 1, rng: rand.New(rand.NewPCG(1, 1))}
		s := &shrinker{t: t, n: 2, rerun: app.rerun, reset: fakeReset{app}, judge: judgeWith(1, edit)}
		res := s.shrink()
		if maskOf(res.set) != 0b11 || res.minimal || res.undecided == 0 || !strings.Contains(res.notMinimal, "never decided") {
			t.Errorf("%s: got %v minimal %v (%d undecided, %q), want [0 1], not minimal", mode, res.set, res.minimal, res.undecided, res.notMinimal)
		}
	}
}

// Keep's default is the first baseline rerun with a decided failure: one
// whose tail was lost is neither the failure kept nor a second way of
// failing.
func TestShrinkActionsKeepFromDecided(t *testing.T) {
	t.Parallel()
	for _, lostAt := range []int{1, 2} {
		app := &fakeApp{a: func(m uint) bool { return m&1 != 0 }, p: 1, rng: rand.New(rand.NewPCG(1, 1))}
		full := 0
		judge := judgeWith(3, func(r *validate.Result) {
			if full++; full == lostAt {
				r.Lost = []qtrace.Lost{{Recorder: "app"}}
				r.Invariants[0].Verdict, r.Invariants[0].Failure = validate.NotEvaluated, nil
			}
		})
		s := &shrinker{t: t, n: 3, rerun: app.rerun, reset: fakeReset{app}, judge: judge}
		res := s.shrink()
		if res.keep.String() != "the same failure (invariant noBugA)" || maskOf(res.set) != 1 || res.baseline != (Rate{9, 9}) {
			t.Errorf("lost at %d: kept %s, got %v, baseline %s", lostAt, res.keep, res.set, res.baseline)
		}
	}
}

// leakApp is a system whose Reset clears nothing the actions leave: a
// run with a leaves a job that records bugA in the next run (job), every
// run records bugA (always), or bugA shows with a only for the first
// shows reruns (fading: state the reruns used up), or every run records
// bugA after the first after reruns (state the reruns built up).
type leakApp struct {
	job, always        bool
	shows, after, runs int
	pending            bool
}

func (l *leakApp) Reset(context.Context) error { return nil }

func (l *leakApp) rerun(_ context.Context, idx []int) ([]qtrace.Step, error) {
	l.runs++
	var steps []qtrace.Step
	add := func(a string) {
		steps = append(steps, qtrace.Step{Recorder: "app", Seq: uint64(len(steps) + 1), Action: a})
	}
	if l.pending {
		add("bugA")
		l.pending = false
	}
	var m uint
	for _, i := range idx {
		m |= 1 << i
		add("do")
	}
	switch {
	case l.always, l.after > 0 && l.runs > l.after:
		add("bugA")
	case l.job && m&0b111 == 0b111:
		add("bugA")
	case l.shows > 0 && m&1 != 0 && l.runs <= l.shows:
		add("bugA")
	}
	if l.job && m&1 != 0 {
		l.pending = true
	}
	return steps, nil
}

// A leak that only the actions make is caught by the reruns of no
// actions after the baseline (work left pending, or a failure the empty
// sequence shows), and a shrunk sequence that no longer reproduces after
// the shrink is not minimal: the failure it showed came from state.
func TestShrinkActionsLeaksOfTheActions(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		app  *leakApp
		n    int
		want string
	}{
		{&leakApp{job: true}, 6, "a Rerun of no actions (after the original's reruns) recorded other steps than the first (step 0: the second run recorded one more (bugA()))"},
		{&leakApp{always: true}, 6, "a Rerun of no actions (after the original's reruns) reproduced the same failure (invariant noBugA): the failure needs none of the actions"},
		// past the checks of no actions: two, the baseline's ten, one
		{&leakApp{after: 13, shows: 1000}, 2, "a rerun of no actions reproduced the same failure (invariant noBugA), after reruns of the original did not show it there"},
	} {
		f := &failTB{TB: t}
		var res *shrinkResult
		done := make(chan struct{})
		go func() {
			defer close(done)
			res = (&shrinker{t: f, n: c.n, rerun: c.app.rerun, reset: c.app, judge: fakeJudge}).shrink()
		}()
		<-done
		if res != nil || len(f.errs) != 1 || !strings.Contains(f.errs[0], c.want) {
			t.Errorf("errors %q, want %q", f.errs, c.want)
		}
	}
	// bugA with a during the first 20 reruns only: the shrink reaches [a],
	// which then reproduces 3 times of 10 (below what 10 of 10 allows), or
	// with 17 never again
	for shows, want := range map[int]string{20: "reproduced 3 of 10 after the shrink, the original 10 of 10", 17: "reproduced 0 of 10 after the shrink"} {
		l := &leakApp{shows: shows}
		res := (&shrinker{t: t, n: 4, rerun: l.rerun, reset: l, judge: fakeJudge}).shrink()
		if maskOf(res.set) != 1 || res.minimal || !strings.Contains(res.notMinimal, want) {
			t.Errorf("shows %d: got %v minimal %v rate %s (%q)", shows, res.set, res.minimal, res.rate, res.notMinimal)
		}
	}
}

// A state mismatch is known by the observed fields that differ from the
// model's, and an actor's first failure is the earliest in the replay.
func TestSameFailureFieldsAndOrder(t *testing.T) {
	t.Parallel()
	mismatch := func(balance, count int64) *validate.Report {
		return &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "state", Index: 3,
			ExpectTemplate: "balance == {{.obs.balance}} and count == {{.obs.count}}",
			State:          map[string]itf.Value{"balance": itf.IntOf(10), "count": itf.IntOf(2)},
			Step:           &qtrace.Step{Action: "deposit", Obs: map[string]any{"balance": balance, "count": count}}}}}}
	}
	balance := SameFailure(&Run{Report: mismatch(11, 2)})
	if want := "the same failure (state deposit (obs.balance))"; balance.String() != want {
		t.Fatalf("got %q, want %q", balance, want)
	}
	if balance.value(mismatch(12, 2)) != yes || balance.value(mismatch(10, 3)) != no || balance.value(mismatch(11, 3)) != no {
		t.Error("a mismatch of another field matched, or the same field's did not")
	}
	// a conformance failure at call 5, an invariant failing at call 2
	both := &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Fail,
		Failure:    &validate.Failure{Kind: "disabled", Index: 5, Call: "bugB"},
		Invariants: []validate.InvariantResult{{Name: "noBugA", Verdict: validate.Fail, Failure: &validate.Failure{Index: 2}}}}}}
	if got := SameFailure(&Run{Report: both}).String(); got != "the same failure (invariant noBugA)" {
		t.Errorf("got %q, want the invariant, the earlier", got)
	}
	// a conforming actor with an invariant unjudged may have failed first
	unjudged := violatesOne("a", "noBugA")
	unjudged.Invariants = append(unjudged.Invariants, validate.InvariantResult{Name: "within 1s", Verdict: validate.NotEvaluated, Within: true})
	if v := SameFailure(&Run{Report: &validate.Report{Actors: []*validate.Result{violatesOne("a", "noBugA")}}}).value(&validate.Report{Actors: []*validate.Result{unjudged}}); v != unknown {
		t.Errorf("an unjudged within beside the failure: %v, want unknown", v)
	}
}

func violatesOne(actor, name string) *validate.Result {
	return &validate.Result{Actor: actor, Conforms: validate.Pass, Invariants: []validate.InvariantResult{{Name: name, Verdict: validate.Fail, Failure: &validate.Failure{Index: 1}}}}
}

// Two shrinks in one test keep their own artifacts: the first's manifest
// hashes files the second does not overwrite.
func TestShrinkArtifactsPerShrink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var first string
	for _, acts := range [][]string{{"b", "d"}, {"a", "c", "e"}} {
		arts, err := (&ShrunkActions[string]{Actions: acts, Original: 5}).writeArtifacts(t, dir)
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = arts[0].Path
		}
	}
	if data, err := os.ReadFile(first); err != nil || strings.Join(strings.Fields(string(data)), "") != `["b","d"]` {
		t.Errorf("the first shrink's actions.json: %q, %v", data, err)
	}
}
