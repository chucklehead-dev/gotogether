//go:build !linux

package raftkv

import (
	"os/exec"
	"syscall"
)

// nodeSysProcAttr is the default elsewhere: the stdin pipe
// (-exit-with-parent) is what ends a node whose parent is gone.
func nodeSysProcAttr() *syscall.SysProcAttr { return nil }

func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

// stopGroup does nothing elsewhere: no SIGSTOP on every platform, and a
// late node step may then void the harness's settle.
func stopGroup(*exec.Cmd) error { return nil }
