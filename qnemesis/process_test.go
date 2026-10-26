//go:build unix

package qnemesis

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A crash kills the incarnation, reaps it and starts the next, which is
// a new process (its count starts again) with a step log of its own; the
// crash is recorded, and journaled with the incarnations.
func TestCrashRestartsTheNextIncarnation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a1 := addrOf(t, dir, "n1", 1)
	for want := "1"; want != "3"; want = strconv.Itoa(atoiOr(want) + 1) {
		if got, err := get(a1, "/inc", 5*time.Second); err != nil || got != want {
			t.Fatalf("GET /inc: %q, %v; want %s", got, err, want)
		}
	}
	rec, recPath := nemesisRec(t, dir)
	var journal bytes.Buffer
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Journal: &journal}
	if err := n.Run(context.Background(), Schedule{{Kind: "nemesis.crash.n1"}}); err != nil {
		t.Fatal(err)
	}
	if p.Incarnation() != 2 || !p.Alive() {
		t.Fatalf("after the crash: incarnation %d, alive %v; want 2, alive", p.Incarnation(), p.Alive())
	}
	if got, err := get(addrOf(t, dir, "n1", 2), "/inc", 5*time.Second); err != nil || got != "1" {
		t.Fatalf("the new incarnation's first GET /inc: %q, %v; want 1 (a fresh process)", got, err)
	}
	if _, err := get(a1, "/inc", 200*time.Millisecond); err == nil {
		t.Error("the killed incarnation still answers")
	}
	steps, err := readLog(t, recPath)
	if err != nil || len(steps) != 1 || steps[0].Action != "crash" || steps[0].Args["node"] != "n1" || steps[0].Recorder != "nemesis" {
		t.Fatalf("the nemesis recorded %+v (%v); want one crash(node n1) of recorder nemesis", steps, err)
	}
	j, err := ReadJournal(&journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Entries) != 2 || j.Entries[0].Kind != "nemesis.crash.n1" || j.Entries[0].Incarnation != 1 || !j.Entries[0].Recorded ||
		j.Entries[1].Kind != "nemesis.restart.n1" || j.Entries[1].Event != "end" || j.Entries[1].Incarnation != 2 {
		t.Fatalf("journal %+v; want the crash of incarnation 1, recorded, then the restart of 2", j.Entries)
	}
}

func atoiOr(s string) int { n, _ := strconv.Atoi(s); return n }

// The crash step is recorded after the process is reaped: every step the
// killed incarnation recorded precedes it in time, though it records as
// fast as it can; the restart step precedes the next incarnation's steps.
func TestCrashIsRecordedAfterTheLastStep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1", "QNEMESIS_TICK=0")
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	rec, recPath := nemesisRec(t, dir)
	rc := DefaultRecording()
	rc["restart"] = func(e Event) (string, []any) {
		return "restart", []any{"node", e.Names[0], "incarnation", e.Incarnation}
	}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Record: rc}
	if err := n.Run(context.Background(), Schedule{{Kind: "nemesis.crash.n1", At: 30 * time.Millisecond}}); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 2)
	time.Sleep(20 * time.Millisecond) // a few of the next incarnation's ticks
	_ = p.Close()
	ns, _ := readLog(t, recPath)
	if len(ns) != 2 || ns[0].Action != "crash" || ns[1].Action != "restart" || ns[1].Args["incarnation"] != int64(2) {
		t.Fatalf("nemesis steps %+v; want crash, then restart of incarnation 2", ns)
	}
	first, _ := readLog(t, filepath.Join(dir, "n1.1.jsonl"))
	if len(first) < 10 {
		t.Fatalf("incarnation 1 recorded %d steps; want its ticks", len(first))
	}
	for _, s := range first {
		if s.Lost == "" && !s.Time.Before(ns[0].Time) {
			t.Fatalf("incarnation 1's %s (seq %d) at %v is not before the crash at %v: the crash was recorded before the process was gone", s.Action, s.Seq, s.Time, ns[0].Time)
		}
	}
	second, _ := readLog(t, filepath.Join(dir, "n1.2.jsonl"))
	if len(second) == 0 || !ns[1].Time.Before(second[0].Time) {
		t.Fatalf("the restart at %v does not precede incarnation 2's first step (%v)", ns[1].Time, second)
	}
}

// A line torn by the crash is the killed incarnation's tail declared
// lost (S11): ReadJSONL keeps the steps before it and marks the loss, and
// StepsIn does not count it.
func TestTornTailDeclaredLost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := addrOf(t, dir, "n1", 1)
	if _, err := get(a, "/inc", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := get(a, "/tear", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	steps := StepsIn(filepath.Join(dir, "n1.*.jsonl"))
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Steps: steps}
	if err := n.Run(context.Background(), Schedule{{Kind: "nemesis.crash.n1", After: 2}}); err != nil {
		t.Fatal(err)
	}
	got, err := readLog(t, filepath.Join(dir, "n1.1.jsonl"))
	var torn *qtrace.TornTail
	if !errors.As(err, &torn) {
		t.Fatalf("reading the killed incarnation's log: %v; want a torn tail", err)
	}
	last := got[len(got)-1]
	if len(got) != 3 || got[1].Action != "inc" || last.Lost != "suffix" || last.Recorder != "n1-1" || last.LostAfter != 2 {
		t.Fatalf("steps %+v; want start, inc, and the tail after seq 2 declared lost", got)
	}
	addrOf(t, dir, "n1", 2)
	if c := steps(); c != 3 { // start, inc; incarnation 2's start
		t.Fatalf("StepsIn counts %d; want 3 (the torn line is no step)", c)
	}
}

// A paused process serves nothing, and serves again once resumed, by
// its For; a pause without one lasts until Stop, which also starts a
// process a crash left down.
func TestPauseResumeAndStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a := addrOf(t, dir, "n1", 1)
	rec, recPath := nemesisRec(t, dir)
	rc := Recording{
		"pause":   func(Event) (string, []any) { return "pause", nil },
		"resume":  func(Event) (string, []any) { return "resume", nil },
		"crash":   func(Event) (string, []any) { return "crash", nil },
		"restart": func(Event) (string, []any) { return "restart", nil },
	}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Record: rc}
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx, Schedule{{Kind: "nemesis.pause.n1", For: 600 * time.Millisecond}}) }()
	time.Sleep(100 * time.Millisecond)
	if _, err := get(a, "/inc", 200*time.Millisecond); err == nil {
		t.Fatal("a paused process answered")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// (the request that timed out may be served now: the kernel queued it)
	if _, err := get(a, "/inc", 5*time.Second); err != nil {
		t.Fatalf("after the pause's For: %v", err)
	}

	// a pause, then a crash of another kind, neither undone: Stop settles
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.pause.n1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := get(a, "/inc", 200*time.Millisecond); err == nil {
		t.Fatal("a paused process answered")
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := get(a, "/inc", 5*time.Second); err != nil {
		t.Fatalf("after Stop: %v", err)
	}
	// a crash down for an hour: Stop ends the Run and starts it
	go func() { done <- n.Run(ctx, Schedule{{Kind: "nemesis.crash.n1", For: time.Hour}}) }()
	for p.Alive() {
		time.Sleep(time.Millisecond)
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run ended by Stop: %v", err)
	}
	if !p.Alive() || p.Incarnation() != 2 {
		t.Fatalf("after Stop: alive %v, incarnation %d; want the next incarnation running", p.Alive(), p.Incarnation())
	}
	steps, _ := readLog(t, recPath)
	var got []string
	for _, s := range steps {
		got = append(got, s.Action)
	}
	if want := "pause resume pause resume crash restart"; strings.Join(got, " ") != want {
		t.Fatalf("nemesis steps %v; want %s", got, want)
	}
}

// A crash kills the incarnation's whole process group (a worker it
// started dies with it), and Close leaves nothing running.
func TestCrashKillsTheGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1", "QNEMESIS_GRANDCHILD=1")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	worker := pidIn(t, filepath.Join(dir, "n1.1.grandchild"))
	n := &Nemesis{Targets: map[string]Target{"n1": p}}
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.crash.n1"}}); err != nil {
		t.Fatal(err)
	}
	gone(t, worker, "incarnation 1's worker after the crash")
	addrOf(t, dir, "n1", 2)
	worker2 := pidIn(t, filepath.Join(dir, "n1.2.grandchild"))
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	gone(t, worker2, "incarnation 2's worker after Close")
	if p.Alive() {
		t.Fatal("alive after Close")
	}
	if err := p.Start(ctx); err == nil {
		t.Fatal("a closed process started")
	}
}

func pidIn(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			return atoiOr(string(b))
		}
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

// gone waits for pid to be no process (a zombie reparented to init is
// reaped by it soon after).
func gone(t *testing.T, pid int, what string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && bytes.Contains(b, []byte(") Z ")) {
			return // dead, awaiting its reaper
		}
	}
	t.Fatalf("%s (pid %d) is still running", what, pid)
}

// A pause is recorded once the process has stopped: the process records
// nothing between the pause's step and the resume's, though it records
// as fast as it can.
func TestPauseIsRecordedOnceStopped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1", "QNEMESIS_TICK=0")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	rec, recPath := nemesisRec(t, dir)
	rc := Recording{
		"pause":  func(Event) (string, []any) { return "pause", nil },
		"resume": func(Event) (string, []any) { return "resume", nil },
	}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Record: rc}
	s := Schedule{{Kind: "nemesis.pause.n1", For: 30 * time.Millisecond}, {Kind: "nemesis.pause.n1", Follows: []int{0}, For: 30 * time.Millisecond}}
	if err := n.Run(ctx, s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	_ = p.Close()
	ns, _ := readLog(t, recPath)
	if len(ns) != 4 {
		t.Fatalf("nemesis steps %v; want pause, resume twice", ns)
	}
	ticks, _ := readLog(t, filepath.Join(dir, "n1.1.jsonl"))
	for k := 0; k < 4; k += 2 {
		paused, resumed := ns[k].Time, ns[k+1].Time
		for _, s := range ticks {
			if s.Lost == "" && s.Time.After(paused) && s.Time.Before(resumed) {
				t.Fatalf("a %s (seq %d) at %v, while paused from %v to %v: the pause was recorded before the process stopped", s.Action, s.Seq, s.Time, paused, resumed)
			}
		}
	}
}
