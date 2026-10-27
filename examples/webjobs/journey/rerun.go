package journey

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Opts are the recorders' identity, the server's and the page's: one
// actor and one process, the app's.
var Opts = []qobs.Option{qobs.WithActor("webjobs"), qobs.WithProcess("webjobs")}

// A Server is a fresh webjobs for one play, so each rerun starts from
// nothing (no reset endpoint: a new server): Start serves it and returns
// its URL, and a stop that stops it taking requests, waits for every job
// to end (it records drained and a settle) and returns its steps.
type Server interface {
	Start(ctx context.Context) (base string, stop func() ([]qtrace.Step, error), err error)
}

// InProcess is webjobs in this process (as the binary was built: a
// mutant's overlay or none), behind httptest. Jobs run MinWork to
// MaxWork, drawn from Seed and their key.
type InProcess struct {
	Seed             int64
	MinWork, MaxWork time.Duration
}

// Start implements Server.
func (s InProcess) Start(ctx context.Context) (string, func() ([]qtrace.Step, error), error) {
	mem := &qobs.MemorySink{}
	app := webjobs.New(webjobs.Config{Recorder: qobs.NewRecorder(mem, append(Opts, qobs.WithID("server"))...), Seed: s.Seed,
		MinWork: s.MinWork, MaxWork: s.MaxWork})
	srv := httptest.NewServer(app)
	return srv.URL, func() ([]qtrace.Step, error) {
		srv.Close()
		app.Drain()
		return mem.Steps(), nil
	}, nil
}

// Program is webjobs as the program Bin (cmd/webjobs, built with a
// mutant's overlay), its step log in Dir.
type Program struct {
	Bin              string
	Dir              string
	Seed             int64
	MinWork, MaxWork time.Duration
}

// Start implements Server: the program on a free port, stopped with
// SIGINT (it drains and settles).
func (s Program) Start(ctx context.Context) (string, func() ([]qtrace.Step, error), error) {
	f, err := os.CreateTemp(s.Dir, "server-*.jsonl")
	if err != nil {
		return "", nil, err
	}
	log := f.Name()
	f.Close()
	args := []string{"-addr", "127.0.0.1:0", "-steps", log, "-seed", fmt.Sprint(s.Seed)}
	if s.MaxWork > 0 {
		args = append(args, "-min-work", s.MinWork.String(), "-max-work", s.MaxWork.String())
	}
	cmd := exec.Command(s.Bin, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	base, ok := strings.CutPrefix(strings.TrimSpace(line), "webjobs: ")
	if err != nil || !ok {
		cmd.Process.Kill()
		cmd.Wait()
		return "", nil, fmt.Errorf("%s printed %q (%v), not its address: %s", filepath.Base(s.Bin), line, err, stderr.String())
	}
	return strings.TrimSuffix(base, "/"), func() ([]qtrace.Step, error) {
		cmd.Process.Signal(os.Interrupt)
		if err := cmd.Wait(); err != nil {
			return nil, fmt.Errorf("%s: %v: %s", s.Bin, err, stderr.String())
		}
		f, err := os.Open(log)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return qtrace.ReadJSONL(f)
	}, nil
}

// Rerun plays actions as c against a fresh server s and returns both
// recorders' steps once the server drained: the run a shrinker reruns
// after removing actions. c's Base and Recorder are its own. A play that
// got stuck (ErrStuck) returns its steps with the error.
func Rerun(ctx context.Context, s Server, c Client, actions []Action) ([]qtrace.Step, error) {
	base, stop, err := s.Start(ctx)
	if err != nil {
		return nil, err
	}
	mem := &qobs.MemorySink{}
	c.Base, c.Recorder = base, qobs.NewRecorder(mem, append(Opts, qobs.WithID("ui"))...)
	playErr := c.Play(ctx, actions)
	server, stopErr := stop()
	if stopErr != nil || playErr != nil && !errors.Is(playErr, ErrStuck) {
		return nil, errors.Join(playErr, stopErr)
	}
	// with ErrStuck, the steps of the play up to its stuck await
	return append(server, mem.Steps()...), playErr
}
