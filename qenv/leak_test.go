package qenv_test

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain fails the package when tests leave goroutines in qenv behind: a
// task that never unwound, a sleeper Close did not end. A simulation must
// end every goroutine it started, or a long exploration accumulates them.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if leaked := leakedTasks(2 * time.Second); leaked != "" {
			fmt.Fprintf(os.Stderr, "qenv: goroutines left behind by the tests:\n%s\n", leaked)
			code = 1
		}
	}
	os.Exit(code)
}

// leakedTasks waits up to d for qenv's goroutines to end, and returns the
// stacks of those that did not.
func leakedTasks(d time.Duration) string {
	deadline := time.Now().Add(d) //qenv:ok waits in real time, after every simulation
	for {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var left []string
		for _, g := range strings.Split(string(buf), "\n\n") {
			if strings.Contains(g, "gotogether/qenv.") && !strings.Contains(g, "leakedTasks") {
				left = append(left, g)
			}
		}
		if len(left) == 0 {
			return ""
		}
		if time.Now().After(deadline) { //qenv:ok
			return strings.Join(left, "\n\n")
		}
		time.Sleep(10 * time.Millisecond) //qenv:ok
	}
}
