//go:build unix

package qnemesis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// StepsIn counts the system's steps only: the nemesis's own log, which
// a pattern may match (logs/n*.jsonl matches logs/nemesis.jsonl), and
// loss records are left out by recorder id, so a fault's After waits for
// the system's steps, as a simulation's script does.
func TestStepsInLeavesOutTheNemesis(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	p.Recorders = func(inc int) []string { return []string{"n1-" + itoa(inc)} }
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	rec, _ := nemesisRec(t, dir)
	steps := StepsIn(filepath.Join(dir, "n*.jsonl")) // nemesis.jsonl too
	rc := Recording{"crash": func(Event) (string, []any) { return "crash", nil }, "restart": func(Event) (string, []any) { return "restart", nil }}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Record: rc, Steps: steps}
	done := make(chan error, 1)
	// the first crash after incarnation 1's start; the second after 3
	// system steps: 1's start, 2's start, and an inc of 2's
	go func() {
		done <- n.Run(ctx, Schedule{{Kind: "nemesis.crash.n1", After: 1}, {Kind: "nemesis.crash.n1", After: 3}})
	}()
	a2 := addrOf(t, dir, "n1", 2)
	time.Sleep(200 * time.Millisecond)
	if inc := p.Incarnation(); inc != 2 {
		t.Fatalf("incarnation %d at 2 system steps: the nemesis's own steps were counted", inc)
	}
	if c := steps(); c != 2 {
		t.Fatalf("StepsIn counts %d; want 2 (the two starts)", c)
	}
	if _, err := get(a2, "/inc", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p.Incarnation() != 3 {
		t.Fatalf("incarnation %d; want 3", p.Incarnation())
	}
}

// A pause is recorded once every process of the group has stopped, not
// the leader alone: a worker the leader started, recording as fast as it
// can, records nothing between the pause's step and the resume's.
func TestPauseStopsTheWholeGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1", "QNEMESIS_GRANDCHILD=ticker")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	worker := filepath.Join(dir, "n1.1.worker.jsonl")
	for StepsIn(worker)() < 100 {
		time.Sleep(time.Millisecond)
	}
	rec, recPath := nemesisRec(t, dir)
	rc := Recording{"pause": func(Event) (string, []any) { return "pause", nil }, "resume": func(Event) (string, []any) { return "resume", nil }}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: rec, Record: rc}
	var s Schedule
	for i := range 5 {
		f := Fault{Kind: "nemesis.pause.n1", For: 20 * time.Millisecond}
		if i > 0 {
			f.Follows = []int{i - 1} // after the last one's resume
		}
		s = append(s, f)
	}
	if err := n.Run(ctx, s); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	ns, _ := readLog(t, recPath)
	ticks, _ := readLog(t, worker)
	if len(ns) != 10 {
		var got []string
		for _, s := range ns {
			got = append(got, s.Action+"@"+s.Time.Format("05.000"))
		}
		t.Fatalf("nemesis steps %v; want 5 pauses and resumes", got)
	}
	for k := 0; k < len(ns); k += 2 {
		for _, tk := range ticks {
			if tk.Lost == "" && tk.Time.After(ns[k].Time) && tk.Time.Before(ns[k+1].Time) {
				t.Fatalf("the worker's tick (seq %d) at %v, while paused from %v to %v: the pause was recorded before the group stopped", tk.Seq, tk.Time, ns[k].Time, ns[k+1].Time)
			}
		}
	}
}

// What a killed test binary leaves of a group (its leader killed by the
// kernel, its worker alive, reparented) is swept at the next start of a
// Process with the same PIDFile.
func TestPIDFileSweepsWhatAKilledTestLeft(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "n1.pgid")
	p := child(t, dir, "n1", "QNEMESIS_GRANDCHILD=sleep")
	p.PIDFile = pidfile
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	worker := pidIn(t, filepath.Join(dir, "n1.1.grandchild"))
	// the test binary's death, as the leader sees it: Pdeathsig kills it
	// alone
	p.mu.Lock()
	leader := p.cmd.Process.Pid
	p.mu.Unlock()
	if err := syscall.Kill(leader, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for p.Alive() {
		time.Sleep(time.Millisecond)
	}
	if err := syscall.Kill(worker, 0); err != nil {
		t.Fatalf("the worker died with its leader (%v): nothing to sweep", err)
	}
	// the next test's Process, knowing only the file
	q := child(t, dir, "n1")
	q.PIDFile = pidfile
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	gone(t, worker, "the worker an earlier group left")
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Errorf("Close left the PIDFile: %v", err)
	}
}

// A crash ends a pause: no resume is called or recorded for an
// incarnation never paused, by the pause's For or by Stop.
func TestCrashEndsAPause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, s := range []Schedule{
		{{Kind: "nemesis.pause.a"}, {Kind: "nemesis.crash.a", For: time.Hour}},
		{{Kind: "nemesis.pause.a", For: 100 * time.Millisecond}, {Kind: "nemesis.crash.a", At: 20 * time.Millisecond}},
	} {
		n, c, mem := fakes()
		rc := DefaultRecording()
		rc["resume"] = func(Event) (string, []any) { return "resume", nil }
		n.Record = rc
		done := make(chan error, 1)
		go func() { done <- n.Run(ctx, s) }()
		if s[1].For > 0 {
			for n.Targets["a"].Alive() {
				time.Sleep(time.Millisecond)
			}
		}
		if s[1].For == 0 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if err := n.Stop(ctx); err != nil {
			t.Fatalf("%v: Stop: %v", s, err)
		}
		if s[1].For > 0 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		got := strings.Join(c.names(), ", ")
		if strings.Contains(got, "resume") || !strings.HasSuffix(got, "start a") {
			t.Errorf("%v: calls %s; want no resume, a started", s, got)
		}
		for _, st := range mem.Steps() {
			if st.Action == "resume" {
				t.Errorf("%v: a resume recorded", s)
			}
		}
	}
}

// readyTarget is a Process whose Start returns once the incarnation is
// ready (its address written, after its first step): a Target that waits
// for readiness, as a real deployment's does.
type readyTarget struct {
	*Process
	ready func(inc int)
}

func (r readyTarget) Start(ctx context.Context) error {
	if err := r.Process.Start(ctx); err != nil {
		return err
	}
	r.ready(r.Incarnation())
	return nil
}

// A restart is recorded before Start: with a Start that returns once the
// incarnation has recorded its first step, the restart's step still
// precedes it.
func TestRestartIsRecordedBeforeTheStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	rec, recPath := nemesisRec(t, dir)
	rc := Recording{"restart": func(Event) (string, []any) { return "restart", nil }}
	target := readyTarget{p, func(inc int) { addrOf(t, dir, "n1", inc) }}
	n := &Nemesis{Targets: map[string]Target{"n1": target}, Rec: rec, Record: rc}
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.crash.n1"}}); err != nil {
		t.Fatal(err)
	}
	ns, _ := readLog(t, recPath)
	second, _ := readLog(t, filepath.Join(dir, "n1.2.jsonl"))
	if len(ns) != 1 || len(second) == 0 || !ns[0].Time.Before(second[0].Time) {
		t.Fatalf("the restart (%v) does not precede incarnation 2's first step (%v)", ns, second)
	}
}
