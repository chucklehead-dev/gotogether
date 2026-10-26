package qnemesis

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A Target is something the nemesis crashes, restarts and pauses: a
// process it started (Process), a container (Container), or one of the
// caller's own (a VM, a pod) behind these five methods.
type Target interface {
	// Kill stops the target at once, as a power cut would (no handler
	// runs), and returns once it is gone: a crash is recorded after it,
	// so every step the target recorded precedes the crash's.
	Kill(ctx context.Context) error
	// Start starts the target's next incarnation.
	Start(ctx context.Context) error
	// Pause freezes the target without its knowing (SIGSTOP); Resume
	// thaws it.
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	// Alive reports whether the target is running (a paused one is).
	Alive() bool
}

// incarnated is a Target that numbers its incarnations, for the steps a
// crash and a restart record (Event.Incarnation).
type incarnated interface{ Incarnation() int }

// A Process is a program the nemesis runs, kills and restarts: Cmd makes
// the command of each incarnation, numbered from 1, so each can write a
// step log of its own (an incarnation's log ends where it was killed, and
// a torn last line is its tail declared lost: qtrace.ReadJSONL) and
// record its incarnation as its steps' process.
//
// Each incarnation leads a process group of its own; Kill sends SIGKILL
// to the group and reaps the leader, Pause and Resume send SIGSTOP and
// SIGCONT to it. On Linux the kernel also kills an incarnation whose
// parent dies (Pdeathsig); Close kills the current one: register it with
// t.Cleanup so no test leaves a server running.
type Process struct {
	Name string
	Cmd  func(incarnation int) *exec.Cmd

	mu     sync.Mutex
	cmd    *exec.Cmd
	inc    int
	done   chan struct{} // closed once the current incarnation is reaped
	exit   error         // its exit, once done
	closed bool
}

// Incarnation is the number of the incarnation last started (0: none).
func (p *Process) Incarnation() int { p.mu.Lock(); defer p.mu.Unlock(); return p.inc }

// Start implements Target: it starts the next incarnation, which must
// not be running.
func (p *Process) Start(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return fmt.Errorf("qnemesis: process %s is closed: a closed Process starts no incarnation", p.Name)
	case p.Cmd == nil:
		return fmt.Errorf("qnemesis: process %s has no Cmd: give it the command of each incarnation", p.Name)
	case p.running():
		return fmt.Errorf("qnemesis: process %s is running (incarnation %d): kill it before starting another", p.Name, p.inc)
	}
	cmd := p.Cmd(p.inc + 1)
	if cmd == nil {
		return fmt.Errorf("qnemesis: process %s: Cmd(%d) returned nil", p.Name, p.inc+1)
	}
	groupOf(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qnemesis: start process %s (incarnation %d): %w", p.Name, p.inc+1, err)
	}
	p.inc++
	p.cmd = cmd
	done := make(chan struct{})
	p.done = done
	go func() {
		// reaped here, whoever killed it: a zombie would keep Alive true
		err := cmd.Wait()
		p.mu.Lock()
		p.exit = err
		p.mu.Unlock()
		close(done)
	}()
	return nil
}

// running reports whether the current incarnation has not been reaped;
// p.mu held.
func (p *Process) running() bool {
	if p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Alive implements Target.
func (p *Process) Alive() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.running() }

// Exit is how the last incarnation ended (nil while it runs, or if it
// exited 0): a server that died by itself says so here.
func (p *Process) Exit() error { p.mu.Lock(); defer p.mu.Unlock(); return p.exit }

// Kill implements Target: SIGKILL to the incarnation's group, then wait
// until the leader is reaped. Killing a process that is not running
// kills what is left of its group, and is no error.
func (p *Process) Kill(ctx context.Context) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := signalGroup(cmd.Process.Pid, sigKill); err != nil {
		return fmt.Errorf("qnemesis: kill process %s: %w", p.Name, err)
	}
	// the leader by its pid too: were it outside the group, the group's
	// signal would miss it and Kill would wait for it forever
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("qnemesis: process %s killed, not reaped: %w", p.Name, ctx.Err())
	}
	return nil
}

// Pause implements Target: SIGSTOP to the incarnation's group.
// It returns once the group's leader has stopped (on Linux; elsewhere
// once the signal is sent).
func (p *Process) Pause(ctx context.Context) error {
	if err := p.signal(true); err != nil {
		return err
	}
	p.mu.Lock()
	pid := p.cmd.Process.Pid
	p.mu.Unlock()
	return waitStopped(ctx, pid)
}

// Resume implements Target: SIGCONT to the incarnation's group.
func (p *Process) Resume(context.Context) error { return p.signal(false) }

// signal pauses (SIGSTOP) or resumes (SIGCONT) the incarnation's group.
func (p *Process) signal(pause bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running() {
		return fmt.Errorf("qnemesis: process %s is not running: start it before pausing or resuming it", p.Name)
	}
	s, what := sigCont, "resume"
	if pause {
		s, what = sigStop, "pause"
	}
	if err := signalGroup(p.cmd.Process.Pid, s); err != nil {
		return fmt.Errorf("qnemesis: %s process %s: %w", what, p.Name, err)
	}
	return nil
}

// Close kills the current incarnation, if any, and starts no other: a
// t.Cleanup's, so a failed test leaves no server behind. It waits for
// the reaping up to 10 seconds.
func (p *Process) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.Kill(ctx)
}

// A Container is a container the nemesis kills, restarts and pauses
// through the docker CLI (Docker, default "docker": podman's is the
// same): `docker kill` then `docker wait` (a crash is recorded once the
// container has stopped), `docker start`, `docker pause` and `unpause`
// (the cgroup freezer: the processes stop as SIGSTOP stops them, unable
// to catch it). Its incarnations count from 1, the running container's.
type Container struct {
	Name   string
	Docker string

	mu  sync.Mutex
	inc int
}

func (c *Container) docker(ctx context.Context, args ...string) (string, error) {
	bin := c.Docker
	if bin == "" {
		bin = "docker"
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("qnemesis: %s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Incarnation is the container's incarnation: 1 until it is restarted.
func (c *Container) Incarnation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return max(c.inc, 1)
}

// Kill implements Target.
func (c *Container) Kill(ctx context.Context) error {
	if _, err := c.docker(ctx, "kill", "--signal", "KILL", c.Name); err != nil && c.Alive() {
		return err // not running is no error: what was to stop has
	}
	_, err := c.docker(ctx, "wait", c.Name)
	return err
}

// Start implements Target.
func (c *Container) Start(ctx context.Context) error {
	if _, err := c.docker(ctx, "start", c.Name); err != nil {
		return err
	}
	c.mu.Lock()
	c.inc = max(c.inc, 1) + 1
	c.mu.Unlock()
	return nil
}

// Pause implements Target.
func (c *Container) Pause(ctx context.Context) error {
	_, err := c.docker(ctx, "pause", c.Name)
	return err
}

// Resume implements Target.
func (c *Container) Resume(ctx context.Context) error {
	_, err := c.docker(ctx, "unpause", c.Name)
	return err
}

// Alive implements Target: the container is running (paused included).
func (c *Container) Alive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := c.docker(ctx, "inspect", "-f", "{{.State.Running}}", c.Name)
	return err == nil && out == "true"
}
