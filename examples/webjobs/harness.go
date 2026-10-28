// Package harness runs webjobs under Bombadil and checks what it left
// against model/webjobs.qnt: the server's steps (recorded where it makes
// its effects) and the page's (package qdriver/bombadil with
// journey.Bombadil's mapping, from Bombadil's trace.jsonl), joined by their wall clocks on one host.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/journey"
	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	qbombadil "github.com/chucklehead-dev/gotogether/qdriver/bombadil"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A journey's directory holds what its run left, as Bombadil and the
// server wrote it:
//
//	trace.jsonl     Bombadil's trace (--output-path)
//	screenshots/    its screenshots (a recorded run checked in keeps two)
//	server.jsonl    the server's step log
const (
	TraceFile  = "trace.jsonl"
	ServerFile = "server.jsonl"
)

// Steps reads a journey's steps: the server's log and the page's,
// converted from Bombadil's trace. A torn last line of either is kept as
// a lost tail (a loss record) and returned as the *qtrace.TornTail error
// with the steps, which a caller may go on with.
func Steps(dir string) ([]qtrace.Step, error) {
	f, err := os.Open(filepath.Join(dir, ServerFile))
	if err != nil {
		return nil, err
	}
	server, serr := qtrace.ReadJSONL(f)
	f.Close()
	if _, torn := errors.AsType[*qtrace.TornTail](serr); serr != nil && !torn {
		return nil, fmt.Errorf("%s: %w", ServerFile, serr)
	}
	page, perr := qbombadil.LogFile(filepath.Join(dir, TraceFile), journey.Bombadil)
	if _, torn := errors.AsType[*qtrace.TornTail](perr); perr != nil && !torn {
		return nil, perr
	}
	return append(server, page.Steps()...), errors.Join(serr, perr)
}

// Live is a live run's configuration.
type Live struct {
	Bombadil  string        // the bombadil command (default: bombadil on PATH)
	Spec      string        // the specification (default bombadil/spec.ts)
	Dir       string        // the journey's directory, made by the run
	TimeLimit time.Duration // Bombadil's --time-limit (default 45s)
	// Seed draws the jobs' durations (webjobs.Config.Seed), MinWork and
	// MaxWork bound them (default 200ms to 600ms: several jobs a second)
	Seed             int64
	MinWork, MaxWork time.Duration
	// Reproduce, if set, is an earlier journey's directory: Bombadil
	// replays its actions (--reproduce) instead of exploring
	Reproduce string
	// Extra are more arguments for Bombadil (BOMBADIL_ARGS: an older
	// Chrome's --chrome-grant-permissions)
	Extra []string
	// Violation: the run may end in a violation of the spec's own
	// properties (Bombadil's exit 2), as a mutant's may; else that fails
	// the run (live's check)
	Violation bool
}

// LiveRun is what a live run did.
type LiveRun struct {
	// Driver is Bombadil: its version, the server's seed (Bombadil's own
	// choices are its trace's, which --reproduce replays), where it ran,
	// its arguments
	Driver validate.Driver
	Exit   int    // Bombadil's: 0 no violation, 2 a violation of the spec's properties, 1 an error
	Output string // Bombadil's output
	// Diverged: a --reproduce stopped where the page offered other
	// actions than the original run's (Bombadil's reproduction is best
	// effort: the page's timing is not replayed). Its trace is the
	// prefix it replayed.
	Diverged bool
}

// RunLive starts webjobs on 127.0.0.1 (a free port), recording its steps
// to the journey's server.jsonl, runs Bombadil on it headless with the
// spec, then stops the server (no request is taken after), waits for
// every job to end and records that the server drained and settled. A
// Bombadil exit of 0 or 2 is a run (2: the spec's own properties found a
// violation), and so is a reproduction that diverged (Diverged); any
// other is an error.
func RunLive(ctx context.Context, l Live) (*LiveRun, error) {
	if l.Bombadil == "" {
		l.Bombadil = "bombadil"
	}
	if l.Spec == "" {
		l.Spec = filepath.Join("bombadil", "spec.ts")
	}
	if l.TimeLimit == 0 {
		l.TimeLimit = 45 * time.Second
	}
	if l.MaxWork == 0 {
		l.MinWork, l.MaxWork = 200*time.Millisecond, 600*time.Millisecond
	}
	spec, err := filepath.Abs(l.Spec)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return nil, err
	}
	sink, err := qobs.FileSink(filepath.Join(l.Dir, ServerFile), qobs.PerStep)
	if err != nil {
		return nil, err
	}
	rec := qobs.NewRecorder(sink, qobs.WithID("server"), qobs.WithActor("webjobs"), qobs.WithProcess("webjobs"))
	app := webjobs.New(webjobs.Config{Recorder: rec, Seed: l.Seed, MinWork: l.MinWork, MaxWork: l.MaxWork})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: app}
	go srv.Serve(ln)
	// new submissions stop 2s before Bombadil's time limit is up, counted
	// from now (its start, and the browser's, take some of it), so the
	// journey ends with every job shown ended (a job runs at most
	// MaxWork, the page polls every 100ms)
	until := time.Now().Add(l.TimeLimit - 2*time.Second)
	origin := fmt.Sprintf("http://%s/?until=%d", ln.Addr(), until.UnixMilli())

	out := filepath.Join(l.Dir, "bombadil")
	args := []string{"browser", "test", "--headless", "--no-sandbox", "--output-path", out}
	if l.Reproduce != "" {
		args = append(args, "--reproduce", l.Reproduce)
	} else {
		args = append(args, "--time-limit", fmt.Sprintf("%ds", int(l.TimeLimit.Seconds())))
	}
	args = append(append(args, l.Extra...), origin, spec)
	cmd := exec.CommandContext(ctx, l.Bombadil, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()

	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shut)
	app.Drain()
	if err := sink.Close(); err != nil {
		return nil, err
	}
	// the arguments as a reader runs them again: from the module's
	// directory, the journey's own directory named <journey>
	shown := []string{"bombadil"}
	for _, a := range args {
		switch {
		case a == spec:
			a = "bombadil/spec.ts"
		case strings.HasPrefix(a, l.Dir):
			a = "<journey>" + strings.TrimPrefix(a, l.Dir)
		case l.Reproduce != "" && a == l.Reproduce:
			a = "<reproduced journey>"
		}
		shown = append(shown, a)
	}
	run := &LiveRun{Output: buf.String(), Driver: validate.Driver{Name: "bombadil", Version: version(l.Bombadil),
		Seed: fmt.Sprintf("server %d", l.Seed), Origin: hostname(), Args: shown}}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exit) && exit.ExitCode() == 2:
		run.Exit = 2
	case errors.As(runErr, &exit) && l.Reproduce != "" && strings.Contains(buf.String(), "diverged"):
		// the page answered otherwise than when Bombadil chose (a job
		// done sooner or later): its trace is the prefix it replayed
		run.Exit, run.Diverged = exit.ExitCode(), true
	default:
		return run, fmt.Errorf("bombadil %s: %v\n%s", strings.Join(args, " "), runErr, buf.String())
	}
	// the journey's directory holds Bombadil's trace and screenshots
	// beside the server's log, as a recorded run's does
	for _, name := range []string{TraceFile, "screenshots"} {
		if err := os.Rename(filepath.Join(out, name), filepath.Join(l.Dir, name)); err != nil {
			return run, err
		}
	}
	return run, os.WriteFile(filepath.Join(l.Dir, "bombadil.log"), buf.Bytes(), 0o644)
}

func version(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "bombadil ")
}

func hostname() string {
	if u := os.Getenv("GITHUB_SERVER_URL"); u != "" && os.Getenv("GITHUB_RUN_ID") != "" {
		return u + "/" + os.Getenv("GITHUB_REPOSITORY") + "/actions/runs/" + os.Getenv("GITHUB_RUN_ID")
	}
	h, _ := os.Hostname()
	return h
}

// RunInfo is what a journey's run-info.json says, for validate
// -manifest -run-info: what gotogether did not see (the driver, and its
// records of the run as artifacts, relative to the file).
type RunInfo struct {
	Name      string              `json:"name"`
	Driver    validate.Driver     `json:"driver"`
	Artifacts []validate.Artifact `json:"artifacts"`
}

// WriteRunInfo writes dir's run-info.json: the driver, and the trace and
// screenshots the directory holds as its artifacts.
func WriteRunInfo(dir, name string, d validate.Driver) (*RunInfo, error) {
	ri := &RunInfo{Name: name, Driver: d, Artifacts: []validate.Artifact{{Name: "bombadil trace", Path: TraceFile}}}
	shots, err := filepath.Glob(filepath.Join(dir, "screenshots", "*"))
	if err != nil {
		return nil, err
	}
	for _, p := range shots {
		ri.Artifacts = append(ri.Artifacts, validate.Artifact{Name: "screenshot", Path: filepath.Join("screenshots", filepath.Base(p))})
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ri); err != nil {
		return nil, err
	}
	return ri, os.WriteFile(filepath.Join(dir, "run-info.json"), b.Bytes(), 0o644)
}

// ReadRunInfo reads dir's run-info.json.
func ReadRunInfo(dir string) (*RunInfo, error) {
	b, err := os.ReadFile(filepath.Join(dir, "run-info.json"))
	if err != nil {
		return nil, err
	}
	var ri RunInfo
	return &ri, json.Unmarshal(b, &ri)
}
