package quintcmd

import "syscall"

// orphanGuard has the kernel kill quint's node if the program running it
// dies without cancelling (a test binary's panic, a timeout, a Ctrl-C).
// The signal follows the thread that started the child, which the Go
// runtime does not end while the program runs.
func orphanGuard(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
