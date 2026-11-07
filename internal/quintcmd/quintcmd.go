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
func Command(ctx context.Context, quint, dir string, args ...string) *Cmd {
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
	return &Cmd{commandState: (*commandState)(cmd), ctx: ctx}
}

// commandState promotes exec.Cmd configuration fields without exposing its
// methods, so every launch passes through admission.
type commandState exec.Cmd

// Cmd is a single-use Quint command. Like exec.Cmd, a successful Start must
// be followed by Wait. Constructing a command does not reserve a slot.
// On supported Unix systems, two managed commands may run across processes
// of the same user sharing os.UserCacheDir(). Other systems use a process-local bound.
// This does not bound orphan workers after an owner's abrupt death.
type Cmd struct {
	*commandState
	ctx     context.Context
	release func()
}

func (c *Cmd) raw() *exec.Cmd { return (*exec.Cmd)(c.commandState) }
func (c *Cmd) admit() error {
	if c.Process != nil {
		return nil
	} // exec reports repeated use itself.
	if !versionQuery(c.raw()) {
		if err := c.checkVersion(); err != nil {
			return err
		}
	}
	release, err := acquire(c.ctx)
	if err != nil {
		return err
	}
	c.release = release
	return nil
}
func (c *Cmd) free() {
	if c.release != nil {
		c.release()
		c.release = nil
	}
}

// Start waits for admission before launching Quint. Cancellation while waiting
// starts no process. A launch failure returns its permit immediately.
func (c *Cmd) Start() error {
	if err := c.admit(); err != nil {
		return err
	}
	err := c.raw().Start()
	if err != nil && c.Process == nil {
		c.free()
	}
	return err
}

// Wait releases admission after exec finishes waiting and collecting output.
func (c *Cmd) Wait() error {
	err := c.raw().Wait()
	c.free()
	return err
}

func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

func (c *Cmd) Output() ([]byte, error) {
	if err := c.admit(); err != nil {
		return nil, err
	}
	// Preserve exec's stderr capture and ExitError semantics.
	if c.Process != nil {
		return c.raw().Output()
	}
	defer c.free()
	return c.raw().Output()
}

func (c *Cmd) CombinedOutput() ([]byte, error) {
	if err := c.admit(); err != nil {
		return nil, err
	}
	if c.Process != nil {
		return c.raw().CombinedOutput()
	}
	defer c.free()
	return c.raw().CombinedOutput()
}
