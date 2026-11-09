package qnemesis

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Pause returns once every thread of every process of the group is
// stopped, the leader's worker's too (a busy one, which the leader's
// threads stopping first does not stop).
func TestPauseReturnsWithTheGroupStopped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1", "QNEMESIS_GRANDCHILD=ticker")
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	addrOf(t, dir, "n1", 1)
	for StepsIn(filepath.Join(dir, "n1.1.worker.jsonl"))() < 100 {
		time.Sleep(time.Millisecond)
	}
	p.mu.Lock()
	pgid := p.cmd.Process.Pid
	p.mu.Unlock()
	if len(members(pgid)) < 2 {
		t.Fatalf("group %d has %d processes; want the leader and its worker", pgid, len(members(pgid)))
	}
	for i := range 100 {
		if err := p.Pause(ctx); err != nil {
			t.Fatal(err)
		}
		for pid := range members(pgid) {
			tasks, _ := filepath.Glob("/proc/" + strconv.Itoa(pid) + "/task/*/stat")
			for _, task := range tasks {
				if st, ok := readStat(task); ok && st.state != 'T' && st.state != 't' {
					t.Fatalf("pause %d: %s is in state %c after Pause returned", i, task, st.state)
				}
			}
		}
		if err := p.Resume(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// A slow first confirmation must not leave another real process running.
// The confirmation seam makes the ordering witness independent of /proc scan
// speed: the child must already have received SIGSTOP before we inspect it.
func TestPauseProcessesSignalsAllBeforeConfirmation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := t.Context()
	var ps []*Process
	var groups []int
	for _, name := range []string{"first", "second"} {
		p := child(t, dir, name, "QNEMESIS_GRANDCHILD=ticker")
		if err := p.Start(ctx); err != nil {
			t.Fatal(err)
		}
		addrOf(t, dir, name, 1)
		p.mu.Lock()
		groups = append(groups, p.cmd.Process.Pid)
		p.mu.Unlock()
		ps = append(ps, p)
	}
	first := true
	err := pauseProcesses(ctx, func(ctx context.Context, group int) error {
		if first {
			first = false
			// waitStopped confirms receipt of a signal already sent; with serial
			// dispatch, the unsignaled second child continues until this deadline.
			witness, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := waitStopped(witness, groups[1]); err != nil {
				t.Error("second group still running when first confirmation began")
				return err
			}
		}
		return waitStopped(ctx, group)
	}, ps...)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPauseProcessesStopsEveryGroupThread(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := t.Context()
	var ps []*Process
	var groups []int
	for _, name := range []string{"first", "second"} {
		p := child(t, dir, name, "QNEMESIS_GRANDCHILD=ticker")
		if err := p.Start(ctx); err != nil {
			t.Fatal(err)
		}
		addrOf(t, dir, name, 1)
		for StepsIn(filepath.Join(dir, name+".1.worker.jsonl"))() < 100 {
			time.Sleep(time.Millisecond)
		}
		p.mu.Lock()
		groups = append(groups, p.cmd.Process.Pid)
		p.mu.Unlock()
		ps = append(ps, p)
	}
	if err := PauseProcesses(ctx, ps...); err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if len(members(group)) < 2 {
			t.Fatal("child worker missing from group")
		}
		for pid := range members(group) {
			tasks, _ := filepath.Glob("/proc/" + strconv.Itoa(pid) + "/task/*/stat")
			for _, task := range tasks {
				if st, ok := readStat(task); !ok || (st.state != 'T' && st.state != 't') {
					t.Fatalf("%s not stopped after collective pause", task)
				}
			}
		}
	}
	for _, p := range ps {
		if err := p.Resume(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
