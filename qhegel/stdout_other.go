//go:build !unix

package qhegel

import "os"

// redirectStdout assigns os.Stdout where there is no dup2: a goroutine
// printing meanwhile races with the assignment, and native writes are
// not captured.
func redirectStdout(w *os.File) (restore func(), err error) {
	old := os.Stdout
	os.Stdout = w
	return func() { os.Stdout = old }, nil
}
