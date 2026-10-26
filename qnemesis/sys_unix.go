//go:build unix

package qnemesis

import (
	"errors"
	"os/exec"
	"syscall"
)

// groupOf makes cmd the leader of a process group of its own, so a crash
// kills what it started too (a shell's child, a server's workers): a
// signal to the leader alone would leave them running, holding its ports
// and files, and the restarted incarnation would find them.
func groupOf(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	orphanGuard(cmd.SysProcAttr)
}

// signalGroup signals the process group led by pid. A group already gone
// is no error: what was to be stopped is.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

const (
	sigKill = syscall.SIGKILL
	sigStop = syscall.SIGSTOP
	sigCont = syscall.SIGCONT
)
