//go:build linux

package raftkv

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
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

// stopGroup freezes the node's process group (SIGSTOP) and returns once
// its leader is stopped (/proc's state T), or after a second: a frozen
// node records nothing, so a settle recorded after it stands.
func stopGroup(cmd *exec.Cmd) error {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGSTOP); err != nil {
		return err
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return err
		}
		// pid (comm) state ...: the state follows the last ')'
		if i := bytes.LastIndexByte(b, ')'); i >= 0 && i+2 < len(b) && (b[i+2] == 'T' || b[i+2] == 't') {
			return nil
		}
	}
	return fmt.Errorf("node %d not stopped within a second of SIGSTOP", pid)
}
