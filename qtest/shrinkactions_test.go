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
	const trials, alpha = 200, 0.05
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
	if maskOf(res.set) != 0b101 || res.minimal || res.diverged == 0 || !strings.Contains(res.notMinimal, "never ran") {
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
		{&fakeApp{a: bug, poller: true}, "after Reset (qtest.fakeReset), two Reruns of no actions recorded different steps (step 0: the second run recorded one more (poll())): the Resetter left work or state behind that the second saw"},
		{&fakeApp{a: bug, leakState: true}, "the original's first two reruns failed differently (invariant noBugA; then disabled bugB): Reset (qtest.fakeReset) leaves state"},
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
		{&fakeApp{a: func(uint) bool { return false }}, Expectation{}, "the original's 4 actions do not reproduce a failure in 10 reruns: calibrate the rerun"},
		{&fakeApp{a: func(uint) bool { return false }}, Violates("noBugA"), "do not reproduce a violation of noBugA (the run conforming) in 10 reruns: calibrate the rerun"},
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
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], `Keep names action "presss" (the model's: init, press, step): a misspelled name never matches`) || app.resets != 0 {
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
