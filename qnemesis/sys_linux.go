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

// orphanGuard has the kernel kill the child if the test binary dies
// before its cleanup runs (a panic, a timeout's SIGQUIT, a kill -9): a
// server left running would hold its port for the next run. The signal
// follows the thread that started the child, which the Go runtime does
// not end while the program runs (it ends only a thread locked to a
// goroutine that exits without unlocking, which Start never is).
func orphanGuard(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }

// waitStopped waits until every thread of pid is stopped: kill(2)
// returns before a SIGSTOP takes effect, and a pause recorded while the
// process still ran would precede steps it took after.
func waitStopped(ctx context.Context, pid int) error {
	for {
		tasks, _ := filepath.Glob("/proc/" + strconv.Itoa(pid) + "/task/*/stat")
		stopped := len(tasks) > 0
		for _, t := range tasks {
			b, err := os.ReadFile(t)
			if err != nil {
				continue // a thread that exited
			}
			// the state follows the command's closing parenthesis
			s := string(b)
			if i := strings.LastIndexByte(s, ')'); i < 0 || i+2 >= len(s) || (s[i+2] != 'T' && s[i+2] != 't' && s[i+2] != 'Z') {
				stopped = false
				break
			}
		}
		if stopped || len(tasks) == 0 {
			return nil // stopped, or gone
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
