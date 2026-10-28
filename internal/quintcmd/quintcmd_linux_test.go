package quintcmd_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// procStat is a process's command, state, parent and process group, from
// /proc; ok is false once it is gone.
func procStat(pid int) (comm string, state byte, ppid, pgrp int, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, 0, 0, false
	}
	s := string(b)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return "", 0, 0, 0, false
	}
	f := strings.Fields(s[end+1:])
	if len(f) < 3 {
		return "", 0, 0, 0, false
	}
	ppid, _ = strconv.Atoi(f[1])
	pgrp, _ = strconv.Atoi(f[2])
	return s[open+1 : end], f[0][0], ppid, pgrp, true
}

// evaluator is the pid of a quint_evaluator that quint, started by this
// test's process (leader's pid), started: one in leader's group, or a
// descendant of it.
func evaluator(leader int) int {
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		comm, _, ppid, pgrp, ok := procStat(pid)
		if !ok || !strings.HasPrefix(comm, "quint_evaluato") { // comm is cut at 15 bytes
			continue
		}
		if pgrp == leader || ppid == leader {
			return pid
		}
	}
	return 0
}

// A cancelled quint leaves no evaluator running: exec.CommandContext
// alone killed node and left quint_evaluator, CPU-bound, under init.
func TestCancelKillsTheEvaluator(t *testing.T) {
	skip.Quint(t)
	home, _ := os.UserHomeDir()
	if m, _ := filepath.Glob(filepath.Join(home, ".quint", "rust-evaluator-*")); len(m) == 0 {
		skip.Optional(t, "quint's Rust evaluator is not installed (ci/install-quint.sh)")
	}
	spec, err := filepath.Abs("testdata/spin.qnt")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := quintcmd.Command(ctx, "", filepath.Dir(spec), "run", filepath.Base(spec), "--backend", "rust",
		"--max-samples", "100000000", "--max-steps", "100", "--verbosity", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	leader := cmd.Process.Pid
	var ev int
	for deadline := time.Now().Add(60 * time.Second); ev == 0; {
		select {
		case err := <-done:
			t.Fatalf("quint ended before its evaluator was seen: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("no quint_evaluator started within 60s")
		}
		time.Sleep(50 * time.Millisecond)
		ev = evaluator(leader)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * quintcmd.WaitDelay):
		t.Fatal("the cancelled quint's Wait did not return")
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		_, state, ppid, _, ok := procStat(ev)
		if !ok || state == 'Z' || state == 'X' {
			return // killed (a zombie until init reaps it)
		}
		if time.Now().After(deadline) {
			// kill it here, so the failing test does not leave it either
			if p, err := os.FindProcess(ev); err == nil {
				_ = p.Kill()
			}
			t.Fatalf("quint_evaluator %d still runs 5s after its quint was cancelled (parent %d, state %c): "+
				"quint must run in a process group of its own, killed whole on cancel", ev, ppid, state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
