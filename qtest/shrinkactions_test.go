package qtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// letters is a rerun whose letters show noBugA's violation where a and
// c are both kept (a fake judge's: no quint), for the expectations
// ShrinkActions keeps.
func letters(_ context.Context, idx []int) ([]qtrace.Step, error) {
	var steps []qtrace.Step
	var m uint
	for _, i := range idx {
		m |= 1 << i
		steps = append(steps, qtrace.Step{Recorder: "app", Seq: uint64(len(steps) + 1), Action: "do"})
	}
	if m&0b101 == 0b101 {
		steps = append(steps, qtrace.Step{Recorder: "app", Seq: uint64(len(steps) + 1), Action: "bugA"})
	}
	return steps, nil
}

// lettersJudge judges letters' steps as a binding would: bugA a
// violation of noBugA.
func lettersJudge(_ context.Context, steps []qtrace.Step) (*validate.Report, error) {
	inv := validate.InvariantResult{Name: "noBugA", Verdict: validate.Pass}
	for i := range steps {
		if steps[i].Action == "bugA" {
			inv.Verdict, inv.Failure = validate.Fail, &validate.Failure{Kind: "invariant", Index: i, Step: &steps[i]}
		}
	}
	return &validate.Report{Actors: []*validate.Result{{Actor: "app", Conforms: validate.Pass, Invariants: []validate.InvariantResult{inv}}}}, nil
}

// An expectation is a shrink's Keep (shrinkKeep): Violates kept, its
// description in the refusal of an original that never shows it; the
// default's, SameFailure, comes back as an expectation of the result.
func TestShrinkKeepsExpectations(t *testing.T) {
	t.Parallel()
	res, err := qshrink.Shrink{Actions: 6, Rerun: letters, Judge: lettersJudge, Keep: shrinkKeep{Violates("noBugA")}}.Run(t.Context())
	if err != nil || !slices.Equal(res.Set, []int{0, 2}) || !res.Minimal {
		t.Fatalf("Keep Violates(noBugA): %v, %v", res, err)
	}
	none := func(ctx context.Context, idx []int) ([]qtrace.Step, error) { return letters(ctx, nil) }
	_, err = qshrink.Shrink{Actions: 4, Rerun: none, Judge: lettersJudge, Keep: shrinkKeep{Violates("noBugA")}}.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "do not reproduce a violation of noBugA (the run conforming) in 10 reruns (0 without a verdict): calibrate the rerun") {
		t.Errorf("an original that never shows it: %v", err)
	}
	res, err = qshrink.Shrink{Actions: 6, Rerun: letters, Judge: lettersJudge}.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := shrunkOf([]string{"a", "b", "c", "d", "e", "f"}, res, Expectation{})
	if !slices.Equal(out.Actions, []string{"a", "c"}) || out.keep.String() != "the same failure (invariant noBugA)" {
		t.Errorf("got %s keeping %s", out, out.keep)
	}
	rep, _ := lettersJudge(t.Context(), []qtrace.Step{{Action: "bugA"}})
	if !out.keep.Holds(&Run{Report: rep}) {
		t.Errorf("%s does not hold of the failure it kept", out.keep)
	}
}

// A Keep a passing run meets is refused before any rerun, naming the
// claims a Keep takes.
func TestShrinkActionsRefusesPassingKeeps(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		keep Expectation
		want string
	}{
		{Conforms(), "Keep conformance, with every invariant holding describes a passing run"},
		{Finishes(), "describes a passing run"},
	} {
		f := &failTB{TB: t}
		reruns := 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			ShrinkActions(f, []string{"a"}, ActionShrink[string]{Binding: "testdata/letters.yaml", Keep: c.keep,
				Rerun: func(context.Context, []string) ([]qtrace.Step, error) { reruns++; return nil, nil }})
		}()
		<-done
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], c.want) || reruns != 0 {
			t.Errorf("errors %q (%d reruns), want %q", f.errs, reruns, c.want)
		}
	}
}

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
	if !slices.Equal(shrunk.Actions, []string{"b", "d"}) || !shrunk.Minimal || shrunk.Original != len(journey) || shrunk.Rate != (Rate{Reproduced: 10, Of: 10}) || app.resets != shrunk.Reruns {
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

// Two shrinks in one test keep their own artifacts: the first's manifest
// hashes files the second does not overwrite.
func TestShrinkArtifactsPerShrink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var first string
	for _, c := range []struct {
		acts []string
		set  []int
	}{{[]string{"b", "d"}, []int{1, 3}}, {[]string{"a", "c", "e"}, []int{0, 2, 4}}} {
		arts, err := (&ShrunkActions[string]{Actions: c.acts, Original: 5}).writeArtifacts(t, dir, &qshrink.Result{Set: c.set, Original: 5})
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = arts[0].Path
		}
		// shrink.json says the set and its length, as actions.json has it
		var rec struct {
			Set     []int
			Actions int
		}
		data, _ := os.ReadFile(arts[1].Path)
		if err := json.Unmarshal(data, &rec); err != nil || !slices.Equal(rec.Set, c.set) || rec.Actions != len(c.acts) {
			t.Errorf("shrink.json: %s (%v)", data, err)
		}
	}
	if data, err := os.ReadFile(first); err != nil || strings.Join(strings.Fields(string(data)), "") != `["b","d"]` {
		t.Errorf("the first shrink's actions.json: %q, %v", data, err)
	}
}
