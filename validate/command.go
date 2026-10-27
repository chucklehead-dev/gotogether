package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A Command is a check of recorded files as `gotogether validate` makes
// it, each field a flag of its: the inputs read (OTLP/JSON converted by
// the rules, step logs, ITF traces), the losses and watermarks the logs
// cannot show declared, the steps judged against the binding with the
// searches, the finish check, each ITF trace judged. The CLI is a printer
// of its Judged, and Replay judges a manifest with one, so a program
// checks a run as the CLI does, and its manifest is the CLI's.
type Command struct {
	// Binding is the binding file; needed for recorded steps, optional
	// for ITF written by gotogether.
	Binding string
	// Files are the inputs, in order.
	Files []string
	// Format is the inputs' format: "auto" (or ""), "otlp", "steps" or "itf".
	Format string
	// Convert is a rule file OTLP/JSON spans are converted with (-convert).
	Convert string
	// Spec, Module and Instance are an ITF input's model, when neither
	// the trace nor the binding names it; NoVars skips comparing the
	// model variables a trace carries.
	Spec, Module, Instance string
	NoVars                 bool
	// Invariants and Constants, if set, override the binding's.
	Invariants []string
	Constants  map[string]string
	// Search is the searches the steps are judged with (-linearize,
	// -resolve-pending).
	Search SearchOptions
	// LostPrefix and LostSuffix are recorders whose logs lost their first
	// or last steps, which the logs cannot show (-lost-prefix, -lost-suffix).
	LostPrefix, LostSuffix []string
	// CompleteThrough are watermarks the logs do not carry, "recorder=T"
	// each (T in RFC3339 or Unix nanoseconds; recorder "*" every one).
	CompleteThrough []string
	// RequireSettle judges the finish check (-require-settle); a binding
	// with complete: true asks for it too.
	RequireSettle bool
	// RunInfo is a JSON file of what gotogether did not see, for the
	// manifest (-run-info): {"name", "driver": {...}, "artifacts": [{"name", "path"}]}.
	RunInfo string
	// Options are quint's: Quint, Backend, Dir (default: a new temporary
	// directory, Judged.Temp), NoITF.
	Options Options

	// finishFixed: RequireSettle says whether the finish check is judged,
	// whatever the binding's complete says (a replay: as recorded)
	finishFixed bool
	// itfModels are a replay's ITF inputs' instances, by path: each
	// judged against the spec the manifest hashed for it
	itfModels map[string]*RunModel
}

// A JudgedInput is one input as a Command read it.
type JudgedInput struct {
	Path string
	// Format is what it held: input.OTLP, input.StepLog, input.ITF, or
	// input.Unknown for an empty file.
	Format input.Format
	// Converted: OTLP/JSON spans converted with the Command's rules.
	Converted bool
	// Steps are its steps as read (none for ITF).
	Steps []qtrace.Step
	// Torn is a step log whose last line was cut off.
	Torn *qtrace.TornTail
	file *input.File
}

// Judged is what a Command found: the inputs read, the steps' judgment,
// each ITF trace's result, the finish check and the run's verdict.
type Judged struct {
	Inputs []JudgedInput
	// Dir is where generated modules and failing runs' ITF went; Temp
	// that the Command made it (no Options.Dir): the CLI removes it when
	// the run passes.
	Dir  string
	Temp bool
	// Steps are every step input's, losses and watermarks marked: what
	// was judged.
	Steps []qtrace.Step
	// Checker judged them (nil: ITF inputs alone), and Judgment is its
	// verdict (nil: no step judged).
	Checker  *Checker
	Judgment *Judgment
	// ITF are the ITF inputs' results, in the inputs' order.
	ITF []*Result
	// Finish: the finish check was judged, FinishWhy because of what
	// ("-require-settle", or "the binding is complete"); Unfinished is the
	// steps' finish when they pass and are not finished (nil otherwise).
	Finish     bool
	FinishWhy  string
	Unfinished *Finish
	// Results are every actor's and ITF trace's; Incomplete the reasons
	// of those that pass and are not finished; OK whether the run passes
	// (every result, and the finish check if judged); Verdict the run's.
	Results    []*Result
	Incomplete map[*Result]string
	OK         bool
	Verdict    RunVerdict

	cmd     Command
	itfOpts map[string]ITFOptions // each ITF input's, for its instance in the manifest
}

// Judge reads the inputs and judges them. An error is a tool's (an
// unreadable input, a binding that does not plan, quint failing), never
// a verdict; once the inputs are read the Judged it returns with it says
// what was read and where (Dir: kept, for what it holds).
func (cmd Command) Judge(ctx context.Context) (*Judged, error) {
	j := &Judged{cmd: cmd, Incomplete: map[*Result]string{}, itfOpts: map[string]ITFOptions{}}
	var rules *convert.Rules
	if cmd.Convert != "" {
		var err error
		if rules, err = convert.Load(cmd.Convert); err != nil {
			return nil, err
		}
	}
	var b *binding.Binding
	if cmd.Binding != "" {
		var err error
		if b, err = binding.Load(cmd.Binding); err != nil {
			return nil, err
		}
		if err := b.CheckSpec(); err != nil {
			return nil, err
		}
	}
	format := cmd.Format
	if format == "" {
		format = "auto"
	}
	var steps []qtrace.Step
	for _, f := range cmd.Files {
		if rules != nil {
			cs, ok, err := convertFile(f, rules)
			if err != nil {
				return nil, err
			}
			if ok {
				j.Inputs = append(j.Inputs, JudgedInput{Path: f, Format: input.OTLP, Converted: true, Steps: cs})
				steps = append(steps, cs...)
				continue
			}
		}
		in, err := input.Read(f, input.Format(format))
		if err != nil {
			return nil, err
		}
		ji := JudgedInput{Path: f, Format: in.Format, Steps: in.Steps, Torn: in.Torn, file: in}
		j.Inputs = append(j.Inputs, ji)
		if in.Format != input.ITF {
			steps = append(steps, in.Steps...)
		}
	}
	var err error
	if steps, err = markLost(steps, cmd.LostPrefix, true); err != nil {
		return nil, err
	}
	if steps, err = markLost(steps, cmd.LostSuffix, false); err != nil {
		return nil, err
	}
	if steps, err = markComplete(steps, cmd.CompleteThrough); err != nil {
		return nil, err
	}
	j.Steps = steps
	itfs := 0
	for _, in := range j.Inputs {
		if in.Format == input.ITF {
			itfs++
		}
	}
	needChecker := len(steps) > 0 || itfs == 0
	if needChecker && cmd.Binding == "" {
		return nil, fmt.Errorf("recorded steps need -binding")
	}
	opts := cmd.Options
	if opts.Dir == "" { // one root for every actor's and input's modules
		d, err := os.MkdirTemp("", "gotogether-validate-")
		if err != nil {
			return nil, err
		}
		opts.Dir, j.Temp = d, true
	}
	j.Dir = opts.Dir
	j.cmd.Options = opts
	// a finished run required (-require-settle, a complete binding)
	j.Finish, j.FinishWhy = cmd.RequireSettle, "-require-settle"
	if !j.Finish && !cmd.finishFixed && b != nil && b.Complete {
		j.Finish, j.FinishWhy = true, "the binding is complete"
	}
	if j.Finish && cmd.finishFixed && b != nil && b.Complete { // a replay of a complete binding's finish check, said as it was
		j.FinishWhy = "the binding is complete"
	}
	j.OK = true
	if needChecker {
		c, err := NewChecker(cmd.Binding, opts)
		if err != nil {
			return j, err
		}
		if err := Finishable(c.Binding); j.Finish && err != nil {
			return j, fmt.Errorf("validate %s: %s: %v", j.FinishWhy, cmd.Binding, err) // (a complete binding's lint says so already)
		}
		if cmd.Constants != nil {
			if c, err = c.WithConstants(cmd.Constants); err != nil {
				return j, err
			}
		}
		if cmd.Invariants != nil {
			if c, err = c.WithInvariants(cmd.Invariants...); err != nil {
				return j, err
			}
		}
		j.Checker = c
		// ordered by the search, checked, the Go evaluator's orders confirmed
		// by quint where the check fails, and marked
		jd, err := c.Judge(ctx, steps, cmd.Search)
		if err != nil {
			return j, err
		}
		j.Judgment = jd
		rep := jd.Report
		j.OK = rep.OK()
		// one judgment with qtest.Finishes; only a run that passes is
		// judged so, since one that fails says why already
		if f := rep.Finished(); j.Finish && rep.OK() && f.Verdict != Pass {
			for _, a := range rep.Actors {
				if f.Actor == "" || a.Actor == f.Actor {
					j.Incomplete[a] = f.Reason
				}
			}
			j.Unfinished = &f
			j.OK = false
		}
		j.Results = append(j.Results, rep.Actors...)
	}
	n := 0
	for _, in := range j.Inputs {
		if in.Format != input.ITF {
			continue
		}
		o := opts
		if itfs > 1 {
			o.Dir = fmt.Sprintf("%s/itf%d", o.Dir, n)
		}
		n++
		io := ITFOptions{Options: o, Binding: b, Spec: cmd.Spec, Module: cmd.Module, Instance: cmd.Instance, NoVarCheck: cmd.NoVars}
		if md := cmd.itfModels[in.Path]; md != nil { // a replay: the spec the manifest hashed, not the trace's old path
			io.Module, io.Instance = md.Module, md.Instance
			if md.Binding == nil && md.Spec != nil {
				io.Spec = md.Spec.Path
			}
		}
		r, err := ValidateITF(ctx, in.file.ITF, io)
		if err != nil {
			return j, fmt.Errorf("%s: %w", in.Path, err)
		}
		j.itfOpts[in.Path] = io
		j.ITF = append(j.ITF, r)
		j.OK = j.OK && r.OK()
		if j.Finish && r.OK() {
			// an ITF trace is the model's states alone: no settle, no calls
			j.Incomplete[r] = "no verdict (ITF trace)"
			j.OK = false
		}
		j.Results = append(j.Results, r)
	}
	j.Verdict = VerdictOf(j.Results, j.Incomplete)
	return j, nil
}

// Manifest is the judgment's manifest (gotogether.run/1), as
// `gotogether validate -manifest` writes it: the tools, the instance, how
// it was judged (the finish check as judged, the declared losses and
// watermarks), each input hashed, the verdict, and the run-info's name,
// driver and artifacts. Its Name is the run-info's, else "": the CLI
// names it after its file.
func (j *Judged) Manifest() (*Manifest, error) {
	cmd := j.cmd
	var m *Manifest
	var err error
	if j.Checker != nil {
		if m, err = j.Checker.Manifest(j.Results, cmd.Search); err != nil {
			return nil, err
		}
	} else {
		// ITF inputs checked with no checker: the binding, if given; each
		// input's own instance is its Input.Model
		m = NewManifest(cmd.Options)
		m.Model.Invariants = []string{}
		if cmd.Binding != "" {
			b, err := binding.Load(cmd.Binding)
			if err != nil {
				return nil, err
			}
			if err := m.SetBinding(b); err != nil {
				return nil, err
			}
		}
	}
	m.Verdict = j.Verdict
	format := cmd.Format
	if format == "" {
		format = "auto"
	}
	// the check as judged: a complete binding's finish check too
	m.Check.RequireSettle, m.Check.Format, m.Check.NoVars = j.Finish, format, cmd.NoVars
	m.Check.LostPrefix, m.Check.LostSuffix = cmd.LostPrefix, cmd.LostSuffix
	m.Check.CompleteThrough = cmd.CompleteThrough
	if cmd.Convert != "" {
		if err := m.AddConvert(cmd.Convert); err != nil {
			return nil, err
		}
	}
	for _, in := range j.Inputs {
		if err := m.AddInput(in.Path, string(in.Format), in.Steps); err != nil {
			return nil, err
		}
		if io, ok := j.itfOpts[in.Path]; ok {
			if m.Inputs[len(m.Inputs)-1].Model, err = ITFModel(in.file.ITF, io); err != nil {
				return nil, err
			}
		}
	}
	if err := addRunInfo(m, cmd.RunInfo); err != nil {
		return nil, err
	}
	return m, nil
}

// runInfo is what a run-info file gives a manifest: what gotogether did
// not see.
type runInfo struct {
	Name      string     `json:"name"`
	Driver    *Driver    `json:"driver"`
	Artifacts []Artifact `json:"artifacts"`
}

// addRunInfo adds the name, driver and artifacts of the run-info file
// at path (none: "") to m, hashing each artifact.
func addRunInfo(m *Manifest, path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ri runInfo
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ri); err != nil {
		return fmt.Errorf("validate -run-info %s: %w: want {\"name\": ..., \"driver\": {\"name\", \"version\", \"seed\", \"origin\", \"args\"}, \"artifacts\": [{\"name\", \"path\"}]}", path, err)
	}
	m.Name, m.Driver = ri.Name, ri.Driver
	for _, a := range ri.Artifacts {
		p := a.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		if err := m.AddArtifact(a.Name, p); err != nil {
			return fmt.Errorf("validate -run-info %s: artifact %s: %w", path, a.Name, err)
		}
	}
	return nil
}

// convertFile converts an OTLP/JSON file; ok is false for other formats.
func convertFile(path string, rules *convert.Rules) ([]qtrace.Step, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if input.Sniff(data) != input.OTLP {
		return nil, false, nil
	}
	tds, err := otelio.ReadTracesData(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	steps, err := rules.Steps(tds)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return steps, true, nil
}

// markLost declares the losses of the recorders the steps cannot show
// (qtrace.MarkLost): a prefix up to each recorder's first seq, a suffix
// after its last.
func markLost(steps []qtrace.Step, recorders []string, prefix bool) ([]qtrace.Step, error) {
	if len(recorders) == 0 {
		return steps, nil
	}
	flag := map[bool]string{true: "-lost-prefix", false: "-lost-suffix"}[prefix]
	first, last := map[string]uint64{}, map[string]uint64{}
	var names []string
	for _, s := range steps {
		if s.Lost != "" {
			continue
		}
		if _, ok := last[s.Recorder]; !ok {
			names = append(names, s.Recorder)
			first[s.Recorder] = s.Seq
		}
		first[s.Recorder] = min(first[s.Recorder], s.Seq)
		last[s.Recorder] = max(last[s.Recorder], s.Seq)
	}
	for _, r := range recorders {
		r = strings.TrimSpace(r)
		if _, ok := last[r]; !ok {
			sort.Strings(names)
			return nil, fmt.Errorf("validate: %s %s: no step of recorder %q in the input: name one of its recorders (%s)", flag, strings.Join(recorders, ","), r, strings.Join(names, ", "))
		}
		l := qtrace.Lost{Recorder: r, Prefix: prefix, After: last[r]}
		if prefix {
			l.After = max(first[r], 1) - 1
		}
		steps = qtrace.MarkLost(steps, l)
	}
	return steps, nil
}

// markComplete states the watermarks the steps do not carry
// (qtrace.MarkComplete): each "recorder=T", T in RFC3339 or Unix
// nanoseconds, recorder "*" for every one.
func markComplete(steps []qtrace.Step, specs []string) ([]qtrace.Step, error) {
	if len(specs) == 0 {
		return steps, nil
	}
	recs := map[string]bool{}
	for _, s := range steps {
		if s.Model() || s.Invocation || s.Settled {
			recs[s.Recorder] = true
		}
	}
	for _, spec := range specs {
		rec, at, ok := strings.Cut(spec, "=")
		rec, at = strings.TrimSpace(rec), strings.TrimSpace(at)
		if !ok || rec == "" || at == "" {
			return nil, fmt.Errorf("validate -complete-through %q: want recorder=T (T in RFC3339 or Unix nanoseconds; *=T for every recorder), e.g. -complete-through 'srv=2026-10-26T12:00:00Z'", spec)
		}
		if rec != qtrace.AllRecorders && !recs[rec] {
			names := make([]string, 0, len(recs))
			for r := range recs {
				names = append(names, r)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("validate -complete-through %s: no step of recorder %q in the input: name one of its recorders (%s), or * for every one", spec, rec, strings.Join(names, ", "))
		}
		t, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			n, nerr := strconv.ParseInt(at, 10, 64)
			if nerr != nil || n <= 0 {
				return nil, fmt.Errorf("validate -complete-through %s: %q is no time: give RFC3339 (2026-10-26T12:00:00Z) or Unix nanoseconds", spec, at)
			}
			t = time.Unix(0, n)
		}
		steps = qtrace.MarkComplete(steps, rec, t)
	}
	return steps, nil
}
