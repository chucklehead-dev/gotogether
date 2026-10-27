package qnemesis

import (
	"bytes"
	"context"
	"fmt"
	"os"
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

// A Recorded target names what an incarnation recorded: its recorders'
// ids, its step logs (each recorder's last step there is where its tail
// was cut) and when it started (the cut, for a recorder whose log has
// none). A crash of one declares those recorders' tails lost
// (qtrace.MarkLost's records, on the nemesis's recorder): a failure that
// may depend on a step the incarnation took and never recorded is no
// verdict, not a FAIL. Process implements it.
type Recorded interface {
	RecordedBy(incarnation int) (recorders, logs []string, started time.Time)
}

// A Process is a program the nemesis runs, kills and restarts: Cmd makes
// the command of each incarnation, numbered from 1, so each can write a
// step log of its own (an incarnation's log ends where it was killed, and
// a torn last line is its tail declared lost: qtrace.ReadJSONL) and
// record its incarnation as its steps' process.
//
// Each incarnation leads a process group of its own; Kill sends SIGKILL
// to the group and reaps the leader, Pause and Resume send SIGSTOP and
// SIGCONT to it (Pause returns once every process of the group has
// stopped, on Linux). Close kills the current one: register it with
// t.Cleanup so no test leaves a server running.
//
// If the test binary itself is killed (kill -9, a CI timeout), no
// cleanup runs: on Linux the kernel kills the leader (Pdeathsig), not
// what the leader started. With PIDFile set, Start writes the group's id
// there and, at the next start, a later test's included, kills what an
// earlier incarnation left of its group first.
type Process struct {
	Name string
	Cmd  func(incarnation int) *exec.Cmd
	// Recorders are the ids (qobs.WithID) of the recorders an incarnation
	// writes, and Logs its step logs. Named, a crash declares their tails
	// lost (Recorded): the effect of a step the incarnation took but had
	// not yet recorded (a reply sent, then killed before the record,
	// which a step recorded on return, //quint:action's, always risks) is
	// then no verdict where another recorder saw it, rather than a FAIL.
	Recorders func(incarnation int) []string
	Logs      func(incarnation int) []string
	PIDFile   string

	mu      sync.Mutex
	cmd     *exec.Cmd
	inc     int
	started time.Time     // the current incarnation's start
	done    chan struct{} // closed once the current incarnation is reaped
	exit    error         // its exit, once done
	killed  bool          // the current incarnation was killed (Kill), not gone by itself
	closed  bool
}

// RecordedBy implements Recorded.
func (p *Process) RecordedBy(incarnation int) (recorders, logs []string, started time.Time) {
	if p.Recorders != nil {
		recorders = p.Recorders(incarnation)
	}
	if p.Logs != nil {
		logs = p.Logs(incarnation)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if incarnation == p.inc {
		started = p.started
	}
	return recorders, logs, started
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
	p.sweep()
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qnemesis: start process %s (incarnation %d): %w", p.Name, p.inc+1, err)
	}
	p.inc++
	p.cmd, p.started, p.killed = cmd, started, false
	if p.PIDFile != "" {
		pid := cmd.Process.Pid
		if err := os.WriteFile(p.PIDFile, []byte(fmt.Sprintf("%d %d\n", pid, startTime(pid))), 0o644); err != nil {
			return fmt.Errorf("qnemesis: process %s: its PIDFile: %w", p.Name, err)
		}
	}
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

// sweep kills what an incarnation left of its group, as PIDFile names
// it: one a killed test binary could not clean up. p.mu held.
func (p *Process) sweep() {
	if p.PIDFile == "" {
		return
	}
	b, err := os.ReadFile(p.PIDFile)
	if err != nil {
		return
	}
	var pgid int
	var start uint64
	if n, _ := fmt.Sscan(string(b), &pgid, &start); n == 2 && pgid > 1 {
		sweep(pgid, start)
	}
	_ = os.Remove(p.PIDFile)
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
	if cmd != nil && p.running() {
		p.killed = true
	}
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

// Pause implements Target: SIGSTOP to the incarnation's group. It
// returns once every process of the group has stopped (on Linux;
// elsewhere once the signal is sent).
//
// A process that is not running is not paused: the error says whether
// it never started, was killed, or exited by itself (a server that could
// not bind its address, a panic), and no fault is made of it, so a
// Nemesis journals and records none.
func (p *Process) Pause(ctx context.Context) error {
	pid, err := p.signal(true)
	if err != nil {
		return err
	}
	return waitStopped(ctx, pid)
}

// Resume implements Target: SIGCONT to the incarnation's group; an
// error, as Pause's, if it is not running.
func (p *Process) Resume(context.Context) error { _, err := p.signal(false); return err }

// signal pauses (SIGSTOP) or resumes (SIGCONT) the incarnation's group,
// returning the group's id.
func (p *Process) signal(pause bool) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, what := sigCont, "resume"
	if pause {
		s, what = sigStop, "pause"
	}
	if !p.running() {
		return 0, p.notRunning(what)
	}
	pid := p.cmd.Process.Pid
	if err := signalGroup(pid, s); err != nil {
		return 0, fmt.Errorf("qnemesis: %s process %s: %w", what, p.Name, err)
	}
	return pid, nil
}

// notRunning says why a process that is not running cannot be paused or
// resumed; p.mu held.
func (p *Process) notRunning(what string) error {
	switch {
	case p.cmd == nil:
		return fmt.Errorf("qnemesis: process %s is not running: start it before pausing or resuming it", p.Name)
	case p.killed:
		return fmt.Errorf("qnemesis: process %s is not running (incarnation %d was killed): start it before pausing or resuming it", p.Name, p.inc)
	}
	exit := "exit status 0"
	if p.exit != nil {
		exit = p.exit.Error()
	}
	return fmt.Errorf("qnemesis: process %s is not running: incarnation %d exited by itself (%s), so there is nothing to %s; "+
		"its output says why (an address in use, a panic): fix that, or Start it again first", p.Name, p.inc, exit, what)
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
	err := p.Kill(ctx)
	if p.PIDFile != "" && err == nil {
		_ = os.Remove(p.PIDFile) // nothing left to sweep
	}
	return err
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
