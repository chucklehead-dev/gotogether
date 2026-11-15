//go:build unix

package model

import (
	"runtime"
	"syscall"
)

// peakResident is the process's peak resident set in bytes, getrusage's
// high-water mark: kilobytes on Linux and the BSDs, bytes on Darwin.
func peakResident() int64 {
	var u syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &u) != nil {
		return 0
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return int64(u.Maxrss)
	}
	return int64(u.Maxrss) * 1024
}
