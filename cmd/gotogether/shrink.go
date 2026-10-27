package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// shrinkTo is the shrink command, its report written to out: a failing
// sequence of an external driver's actions shrunk outside go test
// (qshrink, as qtest.ShrinkActions shrinks one inside it). The driver
// is a command run per candidate, given the candidate's actions and
// where to write its step log; the steps it leaves are judged against
// the binding. Exit status: 0 shrunk to a 1-minimal sequence, 1 shrunk
// but not minimal (NotMinimal says why; the files are written), 2 a
// refusal (the reset contract broken, an original that does not
// reproduce, three failed reruns) or a tool error.
func shrinkTo(out io.Writer, args []string) (int, error) {
	fset := commandFlags("shrink")
	bpath := fset.String("binding", "", "the binding each rerun's steps are checked against (required)")
	actionsPath := fset.String("actions", "", "the failing sequence: a JSON array of the driver's actions, any JSON each, as qtest.ShrinkActions writes actions.json (required)")
	outDir := fset.String("out", "shrunk", "where the result goes: actions.json (the shrunk sequence), shrink.json (what the shrink did), steps.jsonl (a rerun of it that showed the failure) and manifest.json (gotogether validate -replay judges it again)")
	reset := fset.String("reset", "", "a command run before every rerun that returns the system to its initial state, no work pending: a program, or a JSON array of its argv (never a shell); none: each rerun builds a fresh environment itself (the only setup safe against leaked state)")
	diverged := fset.Int("diverged-exit", -1, "the rerun's exit status that says it could not do its actions (a button not shown, a job never submitted): such a rerun counts for nothing, never as a pass (qtest.ErrDiverged); -1: none. Any other status but 0 is a failed rerun")
	baseline := fset.Int("baseline", 0, "reruns of the original that measure its rate, and of the shrunk sequence after the shrink (Calibration.Baseline; default 10)")
	reruns := fset.Int("reruns", 0, "each candidate's reruns, whatever the rate (Calibration.Reruns; default from the rate: 1 for a deterministic driver is the cheapest)")
	need := fset.Int("need", 0, "reproductions that make a candidate fail (Calibration.Need; default 1)")
	alpha := fset.Float64("alpha", 0, "the chance a candidate whose failure shows at the original's rate is taken for one without it (Calibration.Alpha; default 0.05)")
	budget := fset.Int("budget", 0, "the most reruns the shrink makes (Calibration.Budget; 0: no limit)")
	limit := fset.Duration("time", 0, "the longest the shrink runs (Calibration.Time; 0: no limit)")
	var search validate.SearchOptions
	lin := fset.Bool("linearize", false, "check each rerun's steps in an order real time allows (validate -linearize)")
	fset.StringVar(&search.Key, "linearize-key", "", "linearize partitioned by this argument's value (validate -linearize-key)")
	fset.BoolVar(&search.ResolvePending, "resolve-pending", false, "search what each call left pending may have done (validate -resolve-pending)")
	invs := fset.String("invariants", "", "comma-separated model invariants to check instead of the binding's")
	dir := fset.String("dir", "", "where generated modules go (default: a temporary directory, removed at the end)")
	quint := fset.String("quint", "quint", "quint binary")
	backend := fset.String("backend", "", "quint test backend (default $QUINT_BACKEND or rust)")
	verbose := fset.Bool("v", false, "print each rerun's command and exit status")
	fset.Parse(args)
	argv := fset.Args()
	if *bpath == "" || *actionsPath == "" || len(argv) == 0 {
		return 2, fmt.Errorf("shrink needs -binding, -actions and the rerun's command after the flags: gotogether shrink -binding b.yaml -actions journey.json -- ./driver -play {actions} -steps {steps}")
	}
	search.Linearize = *lin || search.Key != ""
	data, err := os.ReadFile(*actionsPath)
	if err != nil {
		return 2, err
	}
	var original []json.RawMessage
	if err := json.Unmarshal(data, &original); err != nil {
		return 2, fmt.Errorf("shrink -actions %s: %w: want a JSON array of the driver's actions", *actionsPath, err)
	}
	if len(original) == 0 {
		return 2, fmt.Errorf("shrink -actions %s: no action in it: there is nothing to shrink", *actionsPath)
	}
	var resetArgv []string
	if *reset != "" {
		if resetArgv, err = argvOf(*reset); err != nil {
			return 2, fmt.Errorf("shrink -reset: %w", err)
		}
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return 2, err
	}
	work, err := os.MkdirTemp(*outDir, "reruns-")
	if err != nil {
		return 2, err
	}
	defer os.RemoveAll(work)
	quintDir := *dir
	if quintDir == "" {
		quintDir = filepath.Join(work, "quint")
	}
	opts := validate.Options{Quint: *quint, Backend: *backend, Dir: quintDir}
	c, err := validate.NewChecker(*bpath, opts)
	if err != nil {
		return 2, err
	}
	var invariants []string // nil: the binding's
	if *invs != "" {
		invariants = strings.Split(*invs, ",")
		if c, err = c.WithInvariants(invariants...); err != nil {
			return 2, err
		}
	}
	d := &driver{argv: argv, work: work, diverged: *diverged, verbose: *verbose, out: out}
	s := qshrink.Shrink{Actions: len(original),
		Rerun: func(ctx context.Context, idx []int) ([]qtrace.Step, error) {
			return d.rerun(ctx, pickRaw(original, idx))
		},
		Judge: func(ctx context.Context, steps []qtrace.Step) (*validate.Report, error) {
			j, err := c.Judge(ctx, steps, search)
			if err != nil {
				return nil, err
			}
			return j.Report, nil
		},
		Calibration: qshrink.Calibration{Baseline: *baseline, Reruns: *reruns, Need: *need, Alpha: *alpha, Budget: *budget, Time: *limit}}
	if resetArgv != nil {
		s.Reset = &resetCommand{argv: resetArgv, verbose: *verbose, out: out}
	}
	res, err := s.Run(context.Background())
	if err != nil {
		return 2, fmt.Errorf("shrink: %w", err)
	}
	fmt.Fprintf(out, "%s\n", res)
	// the shrunk sequence, what the shrink did, a rerun of it that showed
	// the failure, and its manifest: validate -replay judges it again
	shrunk := pickRaw(original, res.Set)
	acts := make([]any, len(shrunk))
	for i, a := range shrunk {
		acts[i] = a
	}
	arts, err := res.WriteArtifacts(*outDir, acts)
	if err != nil {
		return 2, err
	}
	stepsPath := filepath.Join(*outDir, "steps.jsonl")
	if err := writeSteps(stepsPath, res.Steps); err != nil {
		return 2, err
	}
	cmd := validate.Command{Binding: *bpath, Files: []string{stepsPath}, Invariants: invariants, Search: search, Options: opts}
	j, err := cmd.Judge(context.Background())
	if err != nil {
		return 2, fmt.Errorf("shrink: judging the shrunk sequence's rerun: %w", err)
	}
	m, err := j.Manifest()
	if err != nil {
		return 2, err
	}
	m.Name = "shrink"
	m.Driver = &validate.Driver{Name: "gotogether shrink", Args: append([]string{fmt.Sprintf("actions=%d of %d", len(shrunk), len(original)), "--"}, argv...)}
	for _, a := range arts {
		if err := m.AddArtifact(a.Name, a.Path); err != nil {
			return 2, err
		}
	}
	manifest := filepath.Join(*outDir, "manifest.json")
	if err := m.Write(manifest); err != nil {
		return 2, err
	}
	fmt.Fprintf(out, "shrunk actions: %s\nmanifest: %s (gotogether validate -replay %s judges it again: %s)\n", arts[0].Path, manifest, manifest, j.Verdict)
	if !res.Minimal {
		return 1, nil
	}
	return 0, nil
}

// pickRaw is the actions at idx, in order.
func pickRaw(actions []json.RawMessage, idx []int) []json.RawMessage {
	out := make([]json.RawMessage, len(idx))
	for i, j := range idx {
		out[i] = actions[j]
	}
	return out
}

// argvOf is a command given as a flag: a JSON array of its argv, or a
// program alone (never split as a shell would).
func argvOf(s string) ([]string, error) {
	if !strings.HasPrefix(strings.TrimSpace(s), "[") {
		return []string{s}, nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(s), &argv); err != nil || len(argv) == 0 {
		return nil, fmt.Errorf("%q: want a program, or a JSON array of its argv (%v)", s, err)
	}
	return argv, nil
}

// driver runs the rerun command: each run is given a candidate's actions
// in a file and where to write its step log, as {actions} and {steps} in
// its arguments and GOTOGETHER_ACTIONS and GOTOGETHER_STEPS in its
// environment.
type driver struct {
	argv     []string
	work     string
	diverged int
	verbose  bool
	out      io.Writer
	n        int
}

// rerun runs the driver on actions and reads the steps it wrote: its
// exit status diverged is ErrDiverged, another but 0 an error.
func (d *driver) rerun(ctx context.Context, actions []json.RawMessage) ([]qtrace.Step, error) {
	d.n++
	if actions == nil {
		actions = []json.RawMessage{}
	}
	data, err := json.Marshal(actions)
	if err != nil {
		return nil, err
	}
	actionsPath := filepath.Join(d.work, fmt.Sprintf("actions%d.json", d.n))
	stepsPath := filepath.Join(d.work, fmt.Sprintf("steps%d.jsonl", d.n))
	if err := os.WriteFile(actionsPath, data, 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(actionsPath)
	defer os.Remove(stepsPath)
	argv := make([]string, len(d.argv))
	for i, a := range d.argv {
		argv[i] = strings.NewReplacer("{actions}", actionsPath, "{steps}", stepsPath).Replace(a)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "GOTOGETHER_ACTIONS="+actionsPath, "GOTOGETHER_STEPS="+stepsPath)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		return nil, fmt.Errorf("the rerun %s: %w", strings.Join(argv, " "), err)
	}
	if d.verbose {
		fmt.Fprintf(d.out, "rerun %d: %d actions: exit %d\n", d.n, len(actions), code)
	}
	switch {
	case d.diverged >= 0 && code == d.diverged:
		return nil, fmt.Errorf("the rerun exited %d (-diverged-exit): %w", code, qshrink.ErrDiverged)
	case code != 0:
		return nil, fmt.Errorf("the rerun %s exited %d: %s", strings.Join(argv, " "), code, tail(output.String()))
	}
	if _, err := os.Stat(stepsPath); err != nil {
		return nil, fmt.Errorf("the rerun %s wrote no step log at {steps} (%s): it must write the steps it recorded there (a native step log, or OTLP/JSON), even none", strings.Join(argv, " "), stepsPath)
	}
	in, err := input.Read(stepsPath, input.Unknown)
	if err != nil {
		return nil, err
	}
	if in.Format == input.ITF {
		return nil, fmt.Errorf("the rerun wrote an ITF trace at {steps}: want the steps it recorded (a native step log, or OTLP/JSON)")
	}
	return in.Steps, nil
}

// tail is the last lines of a command's output, for an error.
func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	if s = strings.Join(lines, "\n"); s == "" {
		return "(no output)"
	}
	return s
}

// resetCommand is a Resetter that runs a command.
type resetCommand struct {
	argv    []string
	verbose bool
	out     io.Writer
}

func (r *resetCommand) String() string { return "the command " + strings.Join(r.argv, " ") }

// Reset runs the command: its exit status 0 is a reset.
func (r *resetCommand) Reset(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, r.argv[0], r.argv[1:]...).CombinedOutput()
	if r.verbose {
		fmt.Fprintf(r.out, "reset: %v\n", err)
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(string(out)))
	}
	return nil
}

// writeSteps writes steps to path as a native step log.
func writeSteps(path string, steps []qtrace.Step) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(qtrace.WriteJSONL(f, steps), f.Close())
}
