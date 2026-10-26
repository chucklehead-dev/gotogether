package qnemesis

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// orphanGuard has the kernel kill the incarnation's leader if the test
// binary dies before its cleanup runs (a panic, a timeout's SIGQUIT, a
// kill -9): a server left running would hold its port for the next run.
// It reaches the leader only: what the leader started stays, and
// Process.PIDFile's sweep kills it at the next start. The signal follows
// the thread that started the child, which the Go runtime does not end
// while the program runs (it ends only a thread locked to a goroutine
// that exits without unlocking, which Start never is).
func orphanGuard(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }

// stat is a process's (or a thread's) /proc stat fields: its state,
// process group and start time (clock ticks since boot).
type stat struct {
	state byte
	pgrp  int
	start uint64
}

func readStat(path string) (stat, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return stat{}, false
	}
	// the fields follow the command's closing parenthesis (the command
	// may contain spaces and parentheses)
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return stat{}, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return stat{}, false
	}
	pgrp, _ := strconv.Atoi(f[2])
	start, _ := strconv.ParseUint(f[19], 10, 64)
	return stat{state: f[0][0], pgrp: pgrp, start: start}, true
}

// members are the processes of process group pgid, with their stats.
func members(pgid int) map[int]stat {
	out := map[int]stat{}
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		if st, ok := readStat("/proc/" + d.Name() + "/stat"); ok && st.pgrp == pgid {
			out[pid] = st
		}
	}
	return out
}

// waitStopped waits until every thread of every process of group pgid is
// stopped: kill(2) returns before a SIGSTOP takes effect, and a pause
// recorded while a process of the group still ran (a worker the leader
// started) would precede steps it took after.
func waitStopped(ctx context.Context, pgid int) error {
	for {
		stopped := true
		for pid := range members(pgid) {
			tasks, _ := filepath.Glob("/proc/" + strconv.Itoa(pid) + "/task/*/stat")
			for _, t := range tasks {
				st, ok := readStat(t)
				if ok && st.state != 'T' && st.state != 't' && st.state != 'Z' && st.state != 'X' {
					stopped = false
					break
				}
			}
			if !stopped {
				break
			}
		}
		if stopped {
			return nil // stopped, or gone
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

// startTime is pid's start time, for a PIDFile: a pid reused by another
// process starts later.
func startTime(pid int) uint64 {
	st, _ := readStat("/proc/" + strconv.Itoa(pid) + "/stat")
	return st.start
}

// sweep kills what is left of a group an earlier test's PIDFile names:
// its leader, if it still runs (the same start time: not a reused pid),
// else the group's processes started after it. A leader of another start
// time is another program's group, left alone.
func sweep(pgid int, start uint64) {
	if st, ok := readStat("/proc/" + strconv.Itoa(pgid) + "/stat"); ok && st.start != start {
		return
	}
	for pid, st := range members(pgid) {
		if st.start >= start {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
