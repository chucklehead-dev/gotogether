//go:build unix && !linux

package quintcmd

import "syscall"

// orphanGuard does nothing outside Linux, which alone has a parent-death
// signal.
func orphanGuard(*syscall.SysProcAttr) {}
