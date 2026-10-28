//go:build !unix

package quintcmd

import "os/exec"

// group does nothing where there are no unix process groups: Cancel
// kills quint's node alone (exec.CommandContext's default), and its
// evaluator may outlive a cancelled check.
func group(*exec.Cmd) {}
