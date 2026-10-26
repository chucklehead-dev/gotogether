//go:build unix && !linux

package qnemesis

import (
	"context"
	"syscall"
)

// orphanGuard does nothing outside Linux, which alone has a parent-death
// signal: Process.Close (a t.Cleanup) is then the only guard.
func orphanGuard(*syscall.SysProcAttr) {}

// waitStopped returns at once outside Linux, where no /proc says a
// process is stopped: a pause is recorded once SIGSTOP is sent, and may
// precede a step the process takes before it stops.
func waitStopped(context.Context, int) error { return nil }

// startTime is unknown outside Linux.
func startTime(int) uint64 { return 0 }

// sweep does nothing outside Linux: without /proc a group's processes
// cannot be told from another program's that reused the id.
func sweep(int, uint64) {}
