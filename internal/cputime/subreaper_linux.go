package main

import "syscall"

// becomeSubreaper makes this process the reaper of its orphaned
// descendants (PR_SET_CHILD_SUBREAPER), so their times reach it.
func becomeSubreaper() error {
	const prSetChildSubreaper = 36
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); e != 0 {
		return e
	}
	return nil
}
