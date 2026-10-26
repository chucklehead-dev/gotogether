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
