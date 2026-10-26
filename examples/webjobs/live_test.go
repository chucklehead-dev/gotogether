package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// bombadil is the bombadil command a live test runs (BOMBADIL, else
// bombadil on PATH), and the extra arguments BOMBADIL_ARGS gives it. A
// live test is no part of -short (it takes Bombadil's time limit); out
// of -short, a missing bombadil skips it, or fails it under
// GOTOGETHER_STRICT (the nightly installs it).
func bombadil(t *testing.T) (string, []string) {
	t.Helper()
	if testing.Short() {
		t.Skip("a live Bombadil run takes its time limit: not under -short")
	}
	bin := os.Getenv("BOMBADIL")
	if bin == "" {
		bin = "bombadil"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		qtest.Optional(t, "bombadil not on PATH (npm i -g @antithesishq/bombadil@0.7.9, and a Chrome): no live run; the recorded runs in testdata/runs are checked under -short")
	}
	return path, strings.Fields(os.Getenv("BOMBADIL_ARGS"))
}

// journey is a run checked: its directory, what Bombadil did, the run.
type journey struct {
	name string
	dir  string
	seed int64
	live *LiveRun
	run  *qtest.Run
}

// live runs webjobs (as this binary was built: a mutant's overlay or
// none) under Bombadil for 45s (WEBJOBS_TIME_LIMIT, else 3s with
// WEBJOBS_RECORD set: a run to check in), and checks the journey against
// the binding. The journey's directory is the test's, or one under
// WEBJOBS_JOURNEYS (kept).
func live(t *testing.T, name string, l Live) *journey {
	t.Helper()
	bin, extra := bombadil(t)
	l.Bombadil, l.Extra = bin, append(extra, l.Extra...)
	if l.Dir == "" {
		l.Dir = filepath.Join(t.TempDir(), name)
		if d := os.Getenv("WEBJOBS_JOURNEYS"); d != "" { // kept (CI uploads a failure's)
			l.Dir = filepath.Join(d, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()))
		}
	}
	if d, err := time.ParseDuration(os.Getenv("WEBJOBS_TIME_LIMIT")); err == nil && l.TimeLimit == 0 {
		l.TimeLimit = d
	} else if l.TimeLimit == 0 && os.Getenv("WEBJOBS_RECORD") != "" {
		l.TimeLimit = 3 * time.Second
	}
	if l.Seed == 0 {
		l.Seed = time.Now().UnixNano()
	}
	lr, err := RunLive(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("journey: bombadil %s exited %d; %s", lr.Driver.Version, lr.Exit, l.Dir)
	if _, err := WriteRunInfo(l.Dir, "webjobs "+name, lr.Driver); err != nil {
		t.Fatal(err)
	}
	j := &journey{name: name, dir: l.Dir, seed: l.Seed, live: lr}
	j.run = check(t, l.Dir)
	return j
}

// recorded checks the journey checked in as testdata/runs/<name>.
func recorded(t *testing.T, name string) *qtest.Run {
	t.Helper()
	return check(t, filepath.Join("testdata", "runs", name))
}

// check validates the journey in dir, its driver and artifacts in the
// run's manifest (written under GOTOGETHER_EVIDENCE).
func check(t *testing.T, dir string) *qtest.Run {
	t.Helper()
	steps, err := Steps(dir)
	if err != nil {
		t.Fatal(err)
	}
	ri, err := ReadRunInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	var arts []validate.Artifact
	for _, a := range ri.Artifacts {
		arts = append(arts, validate.Artifact{Name: a.Name, Path: filepath.Join(dir, a.Path)})
	}
	return qtest.Validate(t, "webjobs.binding.yaml", steps, qtest.Driver(ri.Driver), qtest.Artifacts(arts...))
}

// keep copies the journey into testdata/runs/<name> when WEBJOBS_RECORD
// is set: its trace, the server's log and two screenshots (its first
// answered submission's, and its failing step's or its last's), with a
// run-info.json naming them.
func (j *journey) keep(t *testing.T) {
	t.Helper()
	if os.Getenv("WEBJOBS_RECORD") == "" {
		return
	}
	to := filepath.Join("testdata", "runs", j.name)
	if err := os.RemoveAll(to); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(to, "screenshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{TraceFile, ServerFile} {
		copyFile(t, filepath.Join(j.dir, f), filepath.Join(to, f))
	}
	var shots []string
	var last string
	for _, s := range j.run.Steps {
		if s.Recorder != "ui" {
			continue
		}
		if s.Action == "submit" && len(shots) == 0 {
			shots = append(shots, screenshot(s.Ref))
		}
		last = screenshot(s.Ref)
	}
	if f := validate.VerdictOf(j.run.Report.Actors, nil).Failing; f != nil && f.Ref != "" {
		last = screenshot(f.Ref)
	}
	shots = append(shots, last)
	for _, s := range shots {
		if s != "" {
			copyFile(t, filepath.Join(j.dir, s), filepath.Join(to, s))
		}
	}
	if _, err := WriteRunInfo(to, "webjobs "+j.name, j.live.Driver); err != nil {
		t.Fatal(err)
	}
	t.Logf("journey: recorded into %s", to)
}

// screenshot is the screenshot a ref names ("" for none).
func screenshot(ref string) string {
	_, s, ok := strings.Cut(ref, " screenshot=")
	if !ok {
		return ""
	}
	return s
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// replays checks the journey in dir with the CLI as its driver's job
// would: gotogether validate with the binding's complete: true (a finish
// check), a manifest from the run-info, and then validate -replay of the
// manifest, which refuses an input that changed and exits 1 on another
// verdict.
func replays(t *testing.T, dir string) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := Steps(abs)
	if err != nil {
		t.Fatal(err)
	}
	// the page's steps as a step log: the inputs a manifest hashes
	page := filepath.Join(t.TempDir(), "page.jsonl")
	var ui []qtrace.Step
	for _, s := range steps {
		if s.Recorder == "ui" {
			ui = append(ui, s)
		}
	}
	f, err := os.Create(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(qtrace.WriteJSONL(f, ui), f.Close()); err != nil {
		t.Fatal(err)
	}
	binding, _ := filepath.Abs("webjobs.binding.yaml")
	manifest := filepath.Join(t.TempDir(), "run.json")
	gotogether(t, 0, "validate", "-binding", binding, "-dir", t.TempDir(), "-manifest", manifest,
		"-run-info", filepath.Join(abs, "run-info.json"), filepath.Join(abs, ServerFile), page)
	gotogether(t, 0, "validate", "-replay", manifest, "-dir", t.TempDir())
}

// gotogether runs the CLI from the repository's root module, wanting
// exit code want.
func gotogether(t *testing.T, want int, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "-C", "../..", "./cmd/gotogether"}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != want {
		t.Fatalf("gotogether %s: exit %d, want %d:\n%s", strings.Join(args, " "), code, want, out)
	}
	return string(out)
}

// webjobs as it is, explored by Bombadil for 45s: a finished journey
// (conforming, the server drained and settled, every click answered on
// the page), its manifest replayed; then Bombadil reproduces it
// (--reproduce) against a new server, which is checked again.
func TestLive(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is; a mutant's live run is TestMutant's")
	}
	qtest.RequireQuint(t)
	j := live(t, "faithful", Live{})
	j.run.Expect(t, qtest.Finishes())
	replays(t, j.dir)
	j.keep(t)
	t.Run("reproduce", func(t *testing.T) {
		// the same server seed: the same jobs' durations, so the page
		// answers as it did when Bombadil chose its actions
		r := live(t, "reproduced", Live{Reproduce: j.dir, Seed: j.seed})
		if r.live.Diverged {
			// best effort: what it replayed conforms, and is no journey
			// (its last click may have no answer)
			r.run.Expect(t, qtest.Conforms())
			t.Logf("journey: the reproduction diverged after %d of the run's %d steps (the page's timing is not replayed)", len(r.run.Steps), len(j.run.Steps))
			return
		}
		r.run.Expect(t, qtest.Finishes())
		t.Logf("journey: reproduced %d of the run's %d steps", len(r.run.Steps), len(j.run.Steps))
	})
}
