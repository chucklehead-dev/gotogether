//go:build linux

package raftkv

import (
	"os/exec"
	"syscall"
)

// nodeSysProcAttr starts a node in a process group of its own, so a kill
// reaches whatever it starts, and with Pdeathsig: Linux kills it when
// the thread that started it exits. (Pdeathsig is the thread's, not the
// process's; the stdin pipe, -exit-with-parent, is what survives any
// parent's end.)
func nodeSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// killGroup is kill -9 of the node's process group.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
