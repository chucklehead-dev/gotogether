// Command cputime runs a test binary for go test -exec and appends the CPU
// time it cost, its children's included, to the file GOTOGETHER_CPU_TIMES
// names: one line per binary, "<package dir> <cpu seconds> <wall seconds>".
// ci/time-budget.sh compares those CPU seconds with ci/test-times.txt.
//
// CPU time, not wall time: CI runs every package of a module at once on a
// few cores, so a package's wall time is as much its neighbours' as its
// own (a package made parallel once slowed four others past their
// budgets). What a package's tests compute, in the test binary and in the
// quint, node and go processes it starts, does not change with what runs
// beside it.
//
//	go build -o "$RUNNER_TEMP/cputime" ./internal/cputime
//	GOTOGETHER_CPU_TIMES="$RUNNER_TEMP/cpu.txt" go test -exec "$RUNNER_TEMP/cputime" ./...
//	ci/time-budget.sh < "$RUNNER_TEMP/cpu.txt"
//
// The count is getrusage's RUSAGE_CHILDREN after the binary exits: the
// binary's own time and that of every descendant waited for, by the
// binary or by a process between them (the kernel adds a waited child's
// times, its own waited children's included, to its parent's). On Linux
// cputime is also the subreaper of the binary's descendants, so a process
// orphaned by its parent (a daemon a test left behind) is reparented to
// it and counted once it has exited. One still running when the binary
// exits is not counted.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cputime test-binary [args...] (as go test -exec; GOTOGETHER_CPU_TIMES names the file to append to)")
		os.Exit(2)
	}
	os.Exit(run(os.Args[1], os.Args[2:], os.Getenv("GOTOGETHER_CPU_TIMES")))
}

// run runs bin with args, appends its line to out (if out is set) and
// returns the exit code go test should see.
func run(bin string, args []string, out string) int {
	if err := becomeSubreaper(); err != nil {
		fmt.Fprintf(os.Stderr, "cputime: %v (orphaned descendants will not be counted)\n", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// go test signals the process it started (this one) on a timeout or an
	// interrupt: pass the signal on to the binary, which handles it.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		signal.Stop(sigs)
		fmt.Fprintf(os.Stderr, "cputime: %v\n", err)
		return 2
	}
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	wall := time.Since(start)
	signal.Stop(sigs)
	close(sigs)
	reapOrphans()
	if out != "" {
		if werr := appendLine(out, wall); werr != nil {
			fmt.Fprintf(os.Stderr, "cputime: %v\n", werr)
			if err == nil {
				return 2
			}
		}
	}
	if err == nil {
		return 0
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := cmd.ProcessState.ExitCode(); code > 0 {
		return code
	}
	fmt.Fprintf(os.Stderr, "cputime: %v\n", err)
	return 2
}

// reapOrphans waits for every descendant reparented to this process that
// has already exited, so that its time is in RUSAGE_CHILDREN.
func reapOrphans() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return
		}
	}
}

// appendLine appends "<dir> <cpu> <wall>" to out. One short write with
// O_APPEND, so the binaries go test runs at once do not interleave lines.
func appendLine(out string, wall time.Duration) error {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru); err != nil {
		return fmt.Errorf("getrusage: %w", err)
	}
	cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s %.1f %.1f\n", dir, cpu.Seconds(), wall.Seconds())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
