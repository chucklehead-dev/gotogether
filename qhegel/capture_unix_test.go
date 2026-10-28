//go:build unix

package qhegel

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// Hegel's output is captured at file descriptor 1, os.Stdout's writes
// and native ones alike, without assigning os.Stdout: the assignment
// raced with a goroutine printing (the race detector named find.go's
// `os.Stdout = w` against fmt.Fprint's read; CI runs this under -race),
// and a native write went past it.
func TestCaptureStdoutAtTheDescriptor(t *testing.T) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { //qenv:ok a printer beside the capture, outside any simulation
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				fmt.Fprint(os.Stdout, "") // reads os.Stdout
			}
		}
	}()
	defer func() { close(done); wg.Wait() }()
	for i := range 20 {
		c := captureStdout()
		if c == nil {
			t.Fatal("no capture")
		}
		fmt.Printf("hegel's %d;", i)
		_, _ = syscall.Write(1, []byte("native;"))
		out := c.stop()
		if !strings.Contains(out, fmt.Sprintf("hegel's %d;", i)) || !strings.Contains(out, "native;") {
			t.Fatalf("captured %q: want os.Stdout's write and the native one", out)
		}
	}
}
