//go:build !unix

package qnemesis

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

func groupOf(*exec.Cmd) {}

// signalGroup fails: process groups and SIGSTOP are POSIX's.
func signalGroup(int, syscall.Signal) error {
	return errors.New("qnemesis: a Process needs a POSIX system (process groups, SIGSTOP); use a Container, or run on Linux or macOS")
}

const (
	sigKill = syscall.SIGKILL
	sigStop = syscall.Signal(19)
	sigCont = syscall.Signal(18)
)

func waitStopped(context.Context, int) error { return nil }

func startTime(int) uint64 { return 0 }

func sweep(int, uint64) {}
