package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// bankDriver builds testdata/bankdriver, an external driver with an
// overdraft bug, into a temporary directory.
func bankDriver(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bankdriver")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/bankdriver").CombinedOutput(); err != nil {
		t.Fatalf("building the driver: %v\n%s", err, out)
	}
	return bin
}

// gotogether shrink reruns an external driver per candidate, outside go
// test: a journey of six actions whose overdraft needs two shrinks to
// them, 1-minimal, though candidates that keep a cancel and drop its
// deposit diverge (the driver's stated exit status, never a pass). The
// reset runs before every rerun, the empty ones of the reset contract
// included. The result's manifest replays to the same verdict.
func TestShrinkCommand(t *testing.T) {
	skip.Quint(t)
	driver := bankDriver(t)
	dir := t.TempDir()
	journey := filepath.Join(dir, "journey.json")
	_ = os.WriteFile(journey, []byte(`[
		{"op": "deposit", "account": "bob", "amount": 2},
		{"op": "cancel", "account": "bob"},
		{"op": "deposit", "account": "alice", "amount": 3},
		{"op": "deposit", "account": "bob", "amount": 1},
		{"op": "withdraw", "account": "alice", "amount": 5},
		{"op": "deposit", "account": "alice", "amount": 1}
	]`), 0o644)
	resets := filepath.Join(dir, "resets")
	out := filepath.Join(dir, "shrunk")
	reset, _ := json.Marshal([]string{driver, "-reset", resets})
	var b bytes.Buffer
	code, err := shrinkTo(&b, []string{"-binding", "../../qtest/testdata/bank.yaml", "-actions", journey, "-out", out, "-reset", string(reset),
		"-diverged-exit", "3", "-baseline", "2", "-reruns", "1", "--", driver, "-actions", "{actions}", "-steps", "{steps}"})
	if err != nil || code != 0 {
		t.Fatalf("shrink: exit %d, %v:\n%s", code, err, b.String())
	}
	report := b.String()
	if !strings.Contains(report, "shrunk 6 actions to 2 (1-minimal), reproducing 2 of 2: [2 4]") || !strings.Contains(report, "diverged") {
		t.Errorf("the report:\n%s", report)
	}
	acts, _ := os.ReadFile(filepath.Join(out, "actions.json"))
	var shrunk []map[string]any
	if err := json.Unmarshal(acts, &shrunk); err != nil || len(shrunk) != 2 || shrunk[0]["op"] != "deposit" || shrunk[1]["amount"] != 5.0 {
		t.Errorf("actions.json: %s (%v)", acts, err)
	}
	var rec struct {
		Keep              string
		Reruns, Diverged  int
		Minimal           bool
		Original, Actions int
	}
	data, _ := os.ReadFile(filepath.Join(out, "shrink.json"))
	if err := json.Unmarshal(data, &rec); err != nil || !rec.Minimal || rec.Original != 6 || rec.Actions != 2 || rec.Diverged == 0 || rec.Keep != "the same failure (disabled withdraw)" {
		t.Errorf("shrink.json: %s (%v)", data, err)
	}
	if n, _ := os.ReadFile(resets); strings.Count(string(n), "reset\n") != rec.Reruns {
		t.Errorf("%d resets for %d reruns", strings.Count(string(n), "reset\n"), rec.Reruns)
	}
	m, err := validate.ReadManifest(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Driver == nil || m.Driver.Name != "gotogether shrink" || len(m.Artifacts) != 2 || m.Verdict.Verdict != validate.Fail {
		t.Errorf("manifest %+v (driver %+v)", m, m.Driver)
	}
	if code, out := vcapture(t, []string{"-replay", filepath.Join(out, "manifest.json"), "-dir", t.TempDir()}); code != 1 || !strings.Contains(out, "replay: the manifest's verdict, reproduced") {
		t.Errorf("replay: exit %d:\n%s", code, out)
	}
}

// The command refuses what it cannot shrink, naming the fix: no rerun
// command, a rerun that writes no step log, an original that does not
// fail.
func TestShrinkCommandRefusals(t *testing.T) {
	skip.Quint(t)
	driver := bankDriver(t)
	dir := t.TempDir()
	passing := filepath.Join(dir, "passing.json")
	_ = os.WriteFile(passing, []byte(`[{"op": "deposit", "account": "bob", "amount": 2}]`), 0o644)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-binding", "../../qtest/testdata/bank.yaml", "-actions", passing}, "the rerun's command after the flags"},
		{[]string{"-binding", "../../qtest/testdata/bank.yaml", "-actions", passing, "-baseline", "2", "--", driver, "-actions", "{actions}", "-steps", filepath.Join(dir, "elsewhere")},
			"wrote no step log at {steps}"},
		{[]string{"-binding", "../../qtest/testdata/bank.yaml", "-actions", passing, "-baseline", "2", "--", driver, "-actions", "{actions}", "-steps", "{steps}"},
			"the original's 1 actions do not reproduce a decided failure in 2 reruns"},
	} {
		var b bytes.Buffer
		code, err := shrinkTo(&b, append([]string{"-out", filepath.Join(t.TempDir(), "out")}, c.args...))
		if code != 2 || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: exit %d, %v; want %q", c.args, code, err, c.want)
		}
	}
}

// A driver that misbehaves does not hang the shrink nor lose its paths:
// a child it leaves running with its output open (a server started in
// the background) once held the rerun forever; a rerun that runs past
// -rerun-timeout is killed and named; a driver that changes its directory
// is given absolute paths though -out is relative; a torn step log is
// counted and named, its rerun no verdict. -diverged-exit 0 is refused. Ctrl-C ends it, cleaning up. (Not
// parallel: it changes the directory and waitDelay, and signals the
// process.)
func TestShrinkCommandMisbehavingDriver(t *testing.T) {
	skip.Quint(t)
	driver := bankDriver(t)
	bind, err := filepath.Abs("../../qtest/testdata/bank.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	journey := filepath.Join(dir, "journey.json")
	_ = os.WriteFile(journey, []byte(`[{"op": "deposit", "account": "alice", "amount": 3}, {"op": "withdraw", "account": "alice", "amount": 5}]`), 0o644)
	t.Chdir(dir)
	old := waitDelay
	waitDelay = 200 * time.Millisecond
	defer func() { waitDelay = old }()
	shrink := func(flags []string, driverFlags ...string) (int, string, error) {
		var b bytes.Buffer
		args := append([]string{"-binding", bind, "-actions", journey, "-baseline", "2", "-reruns", "1"}, flags...)
		args = append(append(append(args, "--", driver), driverFlags...), "-actions", "{actions}", "-steps", "{steps}")
		code, err := shrinkTo(&b, args)
		return code, b.String(), err
	}
	done := make(chan struct{})
	go func() { //qenv:ok a watchdog: the shrink once never returned
		defer close(done)
		if code, out, err := shrink([]string{"-out", "bg"}, "-background"); code != 0 || err != nil || !strings.Contains(out, "(1-minimal)") {
			t.Errorf("a driver leaving a child: exit %d, %v:\n%s", code, err, out)
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a driver leaving a child holding its output hung the shrink")
	}
	if code, out, err := shrink([]string{"-out", "slow", "-rerun-timeout", "300ms"}, "-hang", "10s"); code != 2 || err == nil ||
		!strings.Contains(err.Error(), "timed out after 300ms (-rerun-timeout): it was killed") {
		t.Errorf("a rerun past -rerun-timeout: exit %d, %v:\n%s", code, err, out)
	}
	// relative -out, a driver that changes its directory
	if code, out, err := shrink([]string{"-out", "rel"}, "-cd", os.TempDir()); code != 0 || err != nil {
		t.Errorf("a driver changing its directory: exit %d, %v:\n%s", code, err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "rel", "actions.json")); err != nil {
		t.Errorf("relative -out: %v", err)
	}
	// each rerun of one action has a torn log: the one-removals never decide
	code, out, err := shrink([]string{"-out", "torn", "-v"}, "-tear-single")
	if code != 1 || err != nil || !strings.Contains(out, "reruns' step logs were torn (reruns ") || !strings.Contains(out, "its step log is torn: line 2") ||
		!strings.Contains(out, "not minimal: a sequence with one action fewer was never decided") {
		t.Errorf("torn reruns: exit %d, %v:\n%s", code, err, out)
	}
	// Ctrl-C ends the shrink, its reruns' files removed
	type result struct {
		code int
		err  error
	}
	interrupted := make(chan result)
	go func() { //qenv:ok the shrink, interrupted from the test
		code, _, err := shrink([]string{"-out", "int"}, "-hang", "1s")
		interrupted <- result{code, err}
	}()
	time.Sleep(1500 * time.Millisecond) // into the first rerun
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	r := <-interrupted
	left, _ := filepath.Glob(filepath.Join(dir, "int", "reruns-*"))
	if r.code != 2 || r.err == nil || !strings.Contains(r.err.Error(), "shrink: interrupted after") || len(left) > 0 {
		t.Errorf("an interrupted shrink: exit %d, %v, left %v", r.code, r.err, left)
	}
	if code, _, err := shrink([]string{"-out", "zero", "-diverged-exit", "0"}); code != 2 || err == nil || !strings.Contains(err.Error(), "exit status 0 is a rerun that did its actions") {
		t.Errorf("-diverged-exit 0: exit %d, %v", code, err)
	}
}
