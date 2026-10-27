// Package cli checks recorded files as `gotogether validate` does:
// the inputs read (OTLP/JSON converted by rules, step logs, ITF traces),
// the losses and watermarks the logs cannot show declared, the steps
// judged against the binding with the searches, the finish check, each
// ITF trace judged (Check), and a run's manifest judged again (Replay).
// The CLI is a printer of its Judged. A package of its own, so that
// validate, which qtest and every test binary link, does not bring the
// OTLP reader and the OpenTelemetry SDK with it.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Check is a check of recorded files as `gotogether validate` makes
// it, each field a flag of its. Replay judges a manifest with one, so a
// program checks a run as the CLI does, and its manifest is the CLI's.
type Check struct {
	// Binding is the binding file; needed for recorded steps, optional
	// for ITF written by gotogether.
	Binding string
	// Files are the inputs, in order.
	Files []string
	// Format is the inputs' format: input.Unknown ("") detects each
	// file's, else input.OTLP, input.StepLog or input.ITF.
	Format input.Format
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
	Search validate.SearchOptions
	// LostPrefix and LostSuffix are recorders whose logs lost their first
	// or last steps, which the logs cannot show (-lost-prefix, -lost-suffix).
	LostPrefix, LostSuffix []string
	// CompleteThrough are watermarks the logs do not carry, "recorder=T"
	// each (T in RFC3339 or Unix nanoseconds; recorder "*" every one).
	CompleteThrough []string
	// RequireFinish judges the finish check (-require-finish), as
	// qtest.Finishes does; a binding with complete: true asks for it too.
	RequireFinish bool
	// Name, Driver and Artifacts are what gotogether did not see, for the
	// manifest: the run's name, what drove it, its records (hashed when the
	// manifest is made). RunInfo is a JSON file of them (-run-info),
	// {"name", "driver": {...}, "artifacts": [{"name", "path"}]}, read
	// after them.
	Name      string
	Driver    *validate.Driver
	Artifacts []validate.Artifact
	RunInfo   string
	// Options are quint's: Quint, Backend, Dir (default: a new temporary
	// directory, which Judged.Close removes), NoITF.
	Options validate.Options

	// finishFixed: RequireFinish says whether the finish check is judged,
	// whatever the binding's complete says (a replay: as recorded)
	finishFixed bool
	// itfModels are a replay's ITF inputs' instances, by path: each
	// judged against the spec the manifest hashed for it
	itfModels map[string]*validate.RunModel
}

// An Input is one input as a Check read it.
type Input struct {
	Path string
	// Format is what it held: input.OTLP, input.StepLog, input.ITF, or
	// input.Unknown for an empty file.
	Format input.Format
	// Converted: OTLP/JSON spans converted with the Check's rules.
	Converted bool
	// Steps are its steps as read (none for ITF).
	Steps []qtrace.Step
	// Torn is a step log whose last line was cut off.
	Torn *qtrace.TornTail
	// Warnings are reading's (input.File.Warnings).
	Warnings []string
	file     *input.File
}

// Judged is what a Check found: the inputs read, the steps' judgment,
// each ITF trace's result, the finish check and the run's verdict.
type Judged struct {
	Inputs []Input
	// Dir is where generated modules and failing runs' ITF went. One the
	// Check made (no Options.Dir) is temporary: Close removes it, and the
	// caller decides when (the CLI keeps a failing run's).
	Dir string
	// Steps are every step input's, losses and watermarks marked: what
	// was judged.
	Steps []qtrace.Step
	// Checker judged them (nil: ITF inputs alone), and Judgment is its
	// verdict (nil: no step judged).
	Checker  *validate.Checker
	Judgment *validate.Judgment
	// ITF are the ITF inputs' results, in the inputs' order.
	ITF []*validate.Result
	// Finish: the finish check was judged, FinishByBinding because the
	// binding is complete rather than RequireFinish; Unfinished is the
	// steps' finish when they pass and are not finished (nil otherwise).
	Finish          bool
	FinishByBinding bool
	Unfinished      *validate.Finish
	// Results are every actor's and ITF trace's, an unfinished one's
	// Unfinished saying why (copies: a checker's results are shared); OK
	// whether the run passes (every result, and the finish check if
	// judged); Verdict the run's.
	Results []*validate.Result
	OK      bool
	Verdict validate.RunVerdict

	chk     Check
	temp    bool                           // Dir is the Check's to remove
	itfOpts map[string]validate.ITFOptions // each ITF input's, for its instance in the manifest
}

// Temporary reports whether Dir is a temporary directory the Check
// made, which Close removes.
func (j *Judged) Temporary() bool { return j.temp }

// Close removes Dir if the Check made it (no Options.Dir), with the
// generated modules and the ITF in it; a Dir the caller gave is its own,
// and kept. Call it when the files are no longer needed.
func (j *Judged) Close() error {
	if j == nil || !j.temp || j.Dir == "" {
		return nil
	}
	j.temp = false
	return os.RemoveAll(j.Dir)
}

// unfinished is r with why it is no finished run: a copy, since the
// checker's results are shared through its verdict cache.
func unfinished(r *validate.Result, why string) *validate.Result {
	c := *r
	c.Unfinished = why
	return &c
}

// Judge reads the inputs and judges them. An error is a tool's (an
// unreadable input, a binding that does not plan, quint failing), never
// a verdict; once the inputs are read the Judged it returns with it says
// what was read and where (Dir: kept, for what it holds; Close removes a
// temporary one).
func (chk Check) Judge(ctx context.Context) (*Judged, error) {
	j := &Judged{chk: chk, itfOpts: map[string]validate.ITFOptions{}}
	var rules *convert.Rules
	if chk.Convert != "" {
		var err error
		if rules, err = convert.Load(chk.Convert); err != nil {
			return nil, err
		}
	}
	var b *binding.Binding
	if chk.Binding != "" {
		var err error
		if b, err = binding.Load(chk.Binding); err != nil {
			return nil, err
		}
		if err := b.CheckSpec(); err != nil {
			return nil, err
		}
	}
	var steps []qtrace.Step
	for _, f := range chk.Files {
		if rules != nil {
			cs, ok, err := convertFile(f, rules)
			if err != nil {
				return nil, err
			}
			if ok {
				j.Inputs = append(j.Inputs, Input{Path: f, Format: input.OTLP, Converted: true, Steps: cs})
				steps = append(steps, cs...)
				continue
			}
		}
		in, err := input.Read(f, chk.Format)
		if err != nil {
			return nil, err
		}
		j.Inputs = append(j.Inputs, Input{Path: f, Format: in.Format, Steps: in.Steps, Torn: in.Torn, Warnings: in.Warnings, file: in})
		if in.Format != input.ITF {
			steps = append(steps, in.Steps...)
		}
	}
	var files []*input.File
	for _, in := range j.Inputs {
		files = append(files, in.file)
	}
	if err := input.Distinct(files...); err != nil {
		return nil, err
	}
	var err error
	if steps, err = markLost(steps, chk.LostPrefix, true); err != nil {
		return nil, err
	}
	if steps, err = markLost(steps, chk.LostSuffix, false); err != nil {
		return nil, err
	}
	if steps, err = markComplete(steps, chk.CompleteThrough); err != nil {
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
	if needChecker && chk.Binding == "" {
		return nil, fmt.Errorf("recorded steps need -binding")
	}
	opts := chk.Options
	if opts.Dir == "" { // one root for every actor's and input's modules
		d, err := os.MkdirTemp("", "gotogether-validate-")
		if err != nil {
			return nil, err
		}
		opts.Dir, j.temp = d, true
	}
	j.Dir = opts.Dir
	j.chk.Options = opts
	// a finished run required (-require-finish, a complete binding; a
	// replay's as recorded)
	complete := b != nil && b.Complete
	j.Finish = chk.RequireFinish || !chk.finishFixed && complete
	j.FinishByBinding = j.Finish && complete && (!chk.RequireFinish || chk.finishFixed)
	j.OK = true
	if needChecker {
		c, err := validate.NewChecker(chk.Binding, opts)
		if err != nil {
			return j, err
		}
		if err := validate.Finishable(c.Binding); j.Finish && err != nil {
			why := "-require-finish"
			if j.FinishByBinding {
				why = "the binding is complete"
			}
			return j, fmt.Errorf("validate %s: %s: %v", why, chk.Binding, err) // (a complete binding's lint says so already)
		}
		if chk.Constants != nil {
			if c, err = c.WithConstants(chk.Constants); err != nil {
				return j, err
			}
		}
		if chk.Invariants != nil {
			if c, err = c.WithInvariants(chk.Invariants...); err != nil {
				return j, err
			}
		}
		j.Checker = c
		// ordered by the search, checked, the Go evaluator's orders confirmed
		// by quint where the check fails, and marked
		jd, err := c.Judge(ctx, steps, chk.Search)
		if err != nil {
			return j, err
		}
		j.Judgment = jd
		rep := jd.Report
		j.OK = rep.OK()
		actors := rep.Actors
		// one judgment with qtest.Finishes; only a run that passes is
		// judged so, since one that fails says why already
		if f := rep.Finished(); j.Finish && rep.OK() && f.Verdict != validate.Pass {
			actors = make([]*validate.Result, len(rep.Actors))
			for i, a := range rep.Actors {
				actors[i] = a
				if f.Actor == "" || a.Actor == f.Actor {
					actors[i] = unfinished(a, f.Reason)
				}
			}
			j.Unfinished = &f
			j.OK = false
		}
		j.Results = append(j.Results, actors...)
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
		io := validate.ITFOptions{Options: o, Binding: b, Spec: chk.Spec, Module: chk.Module, Instance: chk.Instance, NoVarCheck: chk.NoVars}
		if md := chk.itfModels[in.Path]; md != nil { // a replay: the spec the manifest hashed, not the trace's old path
			io.Module, io.Instance = md.Module, md.Instance
			if md.Binding == nil && md.Spec != nil {
				io.Spec = md.Spec.Path
			}
		}
		r, err := validate.ValidateITF(ctx, in.file.ITF, io)
		if err != nil {
			return j, fmt.Errorf("%s: %w", in.Path, err)
		}
		j.itfOpts[in.Path] = io
		j.ITF = append(j.ITF, r)
		j.OK = j.OK && r.OK()
		if j.Finish && r.OK() {
			// an ITF trace is the model's states alone: no settle, no calls
			r = unfinished(r, "no verdict (ITF trace)")
			j.OK = false
		}
		j.Results = append(j.Results, r)
	}
	j.Verdict = (&validate.Report{Actors: j.Results}).Verdict()
	return j, nil
}

// Manifest is the judgment's manifest (gotogether.run/2), as
// `gotogether validate -manifest` writes it: the tools, the instance, how
// it was judged (the finish check as judged, the declared losses and
// watermarks), each input hashed, the verdict, and the Check's name,
// driver and artifacts (RunInfo's after them). Its Name is the Check's,
// else "": the CLI names it after its file.
func (j *Judged) Manifest() (*validate.Manifest, error) {
	chk := j.chk
	rec := validate.RunRecord{Options: chk.Options, Search: chk.Search, Results: j.Results, Convert: chk.Convert, Artifacts: chk.Artifacts}
	if j.Checker != nil {
		rec.Binding = j.Checker.Binding
	} else if chk.Binding != "" {
		// ITF inputs checked with no checker: the binding, if given; each
		// input's own instance is its Input.Model
		b, err := binding.Load(chk.Binding)
		if err != nil {
			return nil, err
		}
		rec.Binding = b
	}
	format := string(chk.Format)
	if format == "" {
		format = "auto"
	}
	// the check as judged: a complete binding's finish check too
	rec.Check = validate.RunCheck{RequireFinish: j.Finish, Format: format, NoVars: chk.NoVars, LostPrefix: chk.LostPrefix, LostSuffix: chk.LostSuffix,
		CompleteThrough: chk.CompleteThrough}
	for _, in := range j.Inputs {
		ri := validate.RunInput{Path: in.Path, Format: string(in.Format), Steps: in.Steps}
		if io, ok := j.itfOpts[in.Path]; ok {
			var err error
			if ri.Model, err = validate.ITFModel(in.file.ITF, io); err != nil {
				return nil, err
			}
		}
		rec.Inputs = append(rec.Inputs, ri)
	}
	ri, err := readRunInfo(chk.RunInfo)
	if err != nil {
		return nil, err
	}
	rec.Artifacts = append(rec.Artifacts, ri.Artifacts...)
	m, err := validate.NewManifest(rec)
	if err != nil {
		if chk.RunInfo != "" {
			err = fmt.Errorf("validate -run-info %s: %w", chk.RunInfo, err)
		}
		return nil, err
	}
	if m.Model.Binding == nil {
		m.Model.Invariants = []string{}
	}
	m.Name, m.Driver = chk.Name, chk.Driver
	if ri.Name != "" {
		m.Name = ri.Name
	}
	if ri.Driver != nil {
		m.Driver = ri.Driver
	}
	return m, nil
}

// runInfo is what a run-info file gives a manifest: what gotogether did
// not see.
type runInfo struct {
	Name      string              `json:"name"`
	Driver    *validate.Driver    `json:"driver"`
	Artifacts []validate.Artifact `json:"artifacts"`
}

// readRunInfo reads the run-info file at path (none: ""), its
// artifacts' paths resolved against its directory.
func readRunInfo(path string) (runInfo, error) {
	var ri runInfo
	if path == "" {
		return ri, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ri, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ri); err != nil {
		return ri, fmt.Errorf("validate -run-info %s: %w: want {\"name\": ..., \"driver\": {\"name\", \"version\", \"seed\", \"origin\", \"args\"}, \"artifacts\": [{\"name\", \"path\"}]}", path, err)
	}
	for i, a := range ri.Artifacts {
		if !filepath.IsAbs(a.Path) {
			ri.Artifacts[i].Path = filepath.Join(filepath.Dir(path), a.Path)
		}
	}
	return ri, nil
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
			names := slices.Sorted(maps.Keys(recs))
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
