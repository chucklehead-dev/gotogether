// Package quintcmd is the toolkit's one way to run quint: every quint
// invocation of the core goes through Command.
//
// quint is a node program that starts its Rust evaluator
// (quint_evaluator) as a child of its own. exec.CommandContext kills
// only the direct child, node, on cancel: the evaluator was left running
// under init, CPU-bound, after every cancelled or timed-out check (the
// eleventh review measured one at 72% of a core 11 s after its check
// had returned). Command runs quint as the leader of a process group of
// its own and kills the whole group on cancel.
package quintcmd

import (
	"context"
	"os/exec"
	"time"
)

// WaitDelay bounds how long a cancelled command's Wait waits for its
// output pipes after the kill: a process outside the group (none
// should be) holding them must not hang the check.
const WaitDelay = 5 * time.Second

// Command is the command running quint (the binary; "" is "quint") with
// args, in dir: argv, never a shell. ctx's cancellation kills quint and
// everything it started (its process group, on unix). The caller passes
// absolute paths, or paths relative to dir, the spec's directory.
func Command(ctx context.Context, quint, dir string, args ...string) *exec.Cmd {
	if quint == "" {
		quint = "quint"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, quint, args...)
	cmd.Dir = dir
	cmd.WaitDelay = WaitDelay
	group(cmd)
	return cmd
}
