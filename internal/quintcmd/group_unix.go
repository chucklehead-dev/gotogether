//go:build unix

package quintcmd

import (
	"errors"
	"os/exec"
	"syscall"
)

// group makes cmd the leader of a process group of its own, and its
// Cancel kill the group: quint's evaluator is in it, as is anything else
// node starts. The group is killed while its leader is not yet reaped
// (Cancel runs before Wait returns), so its id cannot have been reused.
//
// A group of its own is out of the terminal's foreground group: a Ctrl-C
// reaches the test binary, not quint. On Linux the kernel then kills the
// leader (orphanGuard); a quint run is bounded, and the evaluator ends
// with its run.
func group(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	orphanGuard(cmd.SysProcAttr)
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil // gone already: what was to be stopped is
		}
		return err
	}
}
