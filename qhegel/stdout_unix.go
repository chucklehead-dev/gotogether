//go:build unix

package qhegel

import (
	"os"
	"syscall"
)

// redirectStdout points file descriptor 1 at w (dup2), so every write to
// standard output, os.Stdout's and native code's, goes to w, and returns
// what points it back. The os.Stdout variable is not touched.
func redirectStdout(w *os.File) (restore func(), err error) {
	saved, err := syscall.Dup(1)
	if err != nil {
		return nil, err
	}
	if err := dup2(int(w.Fd()), 1); err != nil {
		syscall.Close(saved)
		return nil, err
	}
	return func() {
		_ = dup2(saved, 1)
		syscall.Close(saved)
	}, nil
}
