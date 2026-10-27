package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// validateRun is one validate command: what its flags (or a manifest's
// replay) ask for.
type validateRun struct {
	binding, dir, backend, quint, format string
	spec, module, instance               string // an ITF input's
	conv, history                        string
	novars, verbose, withITF             bool
	requireSettle                        bool
	// finishFixed: requireSettle says whether the finish check is judged,
	// whatever the binding's complete says (a replay: as recorded)
	finishFixed bool
	// itfModels are a replay's ITF inputs' instances, by path: each
	// judged against the spec the manifest hashed for it
	itfModels map[string]*validate.RunModel
	// invariants and constants, if set, override the binding's (-invariants;
	// a replay's, the manifest's instance)
	invariants []string
	constants  map[string]string
	search     validate.SearchOptions
	lostPrefix string // comma-separated recorders
	lostSuffix string
	// completeThrough are -complete-through's watermarks, "recorder=T"
	// each (recorder "*": every one), as given
	completeThrough repeated
	files           []string
	// manifest, if set, is where to write the run's manifest, with the
	// driver, artifacts and name runInfo's fragment gives
	manifest, runInfo string
	// mutateSteps judges the steps' observation mutants after a pass
	// (qtest.JudgeStepMutants), narrowed to mutateOps (comma-separated)
	// and mutateLimit per op
	mutateSteps bool
	mutateOps   string
	mutateLimit int
	// out is where the report goes (os.Stdout; a test's buffer, so tests
	// run in parallel)
	out io.Writer
}

func runValidate(args []string) (int, error) { return validateTo(os.Stdout, args) }

// validateTo is the validate command, its report written to out.
func validateTo(out io.Writer, args []string) (int, error) {
	fset := commandFlags("validate")
	v := validateRun{out: out}
	fset.StringVar(&v.binding, "binding", "", "binding file (optional for ITF written by gotogether)")
	fset.StringVar(&v.dir, "dir", "", "where to write generated modules and ITF (default: a new temp dir, printed)")
	fset.StringVar(&v.backend, "backend", "", "quint test backend (default $QUINT_BACKEND or rust)")
	fset.StringVar(&v.quint, "quint", "quint", "quint binary")
	fset.StringVar(&v.format, "format", "auto", "input format: auto, otlp, steps (native step log) or itf")
	fset.StringVar(&v.spec, "spec", "", "ITF input: the model, if neither the trace nor a binding names it")
	fset.StringVar(&v.module, "module", "", "ITF input: the parameterised module")
	fset.StringVar(&v.instance, "instance", "", "ITF input: replay against this instance module (e.g. currentDesign), e.g. for quint run --mbt traces")
	fset.BoolVar(&v.novars, "novars", false, "ITF input: do not compare model variables carried by the trace")
	fset.BoolVar(&v.verbose, "v", false, "print quint's output")
	fset.StringVar(&v.conv, "convert", "", "convert OTLP/JSON spans with this rule file (see gotogether convert)")
	invs := fset.String("invariants", "", "comma-separated model invariants to check instead of the binding's")
	fset.BoolVar(&v.withITF, "itf", false, "also write the ITF of conforming runs, the model's state at every step (slow for models with a large state); failing runs always get theirs")
	noITF := fset.Bool("noitf", false, "gone: no ITF for conforming runs is the default")
	lin := fset.Bool("linearize", false, "check each actor's steps in an order real time allows, searched for one the model accepts (a client history: steps recorded when calls returned)")
	fset.StringVar(&v.search.Key, "linearize-key", "", "linearize each actor's history partitioned by this argument's value (independent keys), each partition alone")
	fset.IntVar(&v.search.Budget, "linearize-budget", 0, "orders a linearization checks per actor or partition (default 200)")
	fset.BoolVar(&v.search.ResolvePending, "resolve-pending", false, "search what each call left pending (invoked, never completed) may have done")
	fset.StringVar(&v.history, "history", "", "write the history drawn as SVG to this file (lanes of calls, the order checked numbered, the failing step red, each call's ref on hover)")
	fset.StringVar(&v.lostPrefix, "lost-prefix", "", "comma-separated recorders whose logs lost their first steps, which the log cannot show (no seqs): judged as a lost prefix (qtrace.MarkLost)")
	fset.BoolVar(&v.requireSettle, "require-settle", false, "exit 1 unless the run is finished, as qtest.Finishes judges it: it settled (its trace's last record a settle), every eventual val held there, and no call was left pending (one -resolve-pending places included: its caller never got the answer); a lost tail is no verdict (exit 1). The binding needs an eventual val, the journey's end; the default for a binding with complete: true. An ITF trace records no settle, so it cannot be judged finished (exit 1)")
	fset.StringVar(&v.lostSuffix, "lost-suffix", "", "comma-separated recorders whose logs lost their last steps, which the log cannot show (a killed process's buffer): their tails judged lost after their last steps (qtrace.MarkLost)")
	fset.Var(&v.completeThrough, "complete-through", "recorder=T: the recorder's steps recorded at or before T (RFC3339, or Unix nanoseconds) are all in the input, as an external collector knows what it has ingested (a ClickHouse poll, a log shipper's acknowledgement); *=T states it of every recorder. Repeatable. "+
		"A bounded response due by T is then judged though the run never settled (absent: a FAIL), one due after it is uncollected (no verdict), and a settle it does not reach settles nothing. A log with no watermark is taken whole (qtrace.MarkComplete)")
	fset.StringVar(&v.manifest, "manifest", "", "write the run's manifest (gotogether.run/1) to this file: the tools, model, binding and inputs with their hashes, losses, the verdict and failing step; -replay judges it again")
	fset.StringVar(&v.runInfo, "run-info", "", "with -manifest: a JSON file of what gotogether did not see, written by the driver: {\"name\": ..., \"driver\": {\"name\", \"version\", \"seed\", \"origin\", \"args\": [...]}, \"artifacts\": [{\"name\", \"path\"}]} (paths relative to the file)")
	fset.BoolVar(&v.mutateSteps, "mutate-steps", false, "after a pass, judge the steps' observation mutants (a step dropped, duplicated, swapped with its recorder's next, an argument or observation corrupted, retimed past its neighbour, moved to another actor) as the steps were judged, and print a table of them by op, killed and survived: a survivor changed something no check reads (qtest.MutateSteps)")
	fset.StringVar(&v.mutateOps, "mutate-ops", "", "with -mutate-steps: comma-separated ops to make (drop, dup, swap, corrupt, retime, reactor, gap; default all but gap, whose mutants are only ever undecided)")
	fset.IntVar(&v.mutateLimit, "mutate-limit", 0, "with -mutate-steps: at most this many mutants of each op, spread over the log (default: every one)")
	replay := fset.String("replay", "", "judge a run again from its manifest (validate -manifest's, or a test's under GOTOGETHER_EVIDENCE), refusing an input whose hash changed; exit as validate does, and 1 if the verdict is not the manifest's")
	fset.Parse(args)
	if *noITF {
		return 2, fmt.Errorf("validate: -noitf is the default now: drop it (-itf writes the ITF of conforming runs)")
	}
	if *replay != "" {
		return replayManifest(out, *replay, fset)
	}
	if v.runInfo != "" && v.manifest == "" {
		return 2, fmt.Errorf("validate: -run-info %s says what goes in a manifest: add -manifest out.json", v.runInfo)
	}
	if (v.mutateOps != "" || v.mutateLimit != 0) && !v.mutateSteps {
		return 2, fmt.Errorf("validate: -mutate-ops and -mutate-limit narrow -mutate-steps: add -mutate-steps")
	}
	for _, op := range fields(v.mutateOps) {
		if !slices.Contains(qtrace.MutationOps, op) {
			return 2, fmt.Errorf("validate -mutate-ops: %q is no mutation; the ops are %s", op, strings.Join(qtrace.MutationOps, ", "))
		}
	}
	v.search.Linearize = *lin || v.search.Key != ""
	if *invs != "" {
		v.invariants = strings.Split(*invs, ",")
	}
	v.files = fset.Args()
	if len(v.files) == 0 {
		usage()
	}
	code, _, err := v.run()
	return code, err
}

// replayFlags are the flags -replay takes besides itself: where its
// output goes, which quint, the backend (only the manifest's: verdicts
// depend on it), a new manifest of the replay. The others are the
// manifest's to say.
// Read-only.
var replayFlags = map[string]bool{"replay": true, "dir": true, "quint": true, "v": true, "backend": true, "manifest": true, "history": true, "itf": true}

// replayManifest judges the run of the manifest at path again: its files
// unchanged (refused otherwise, naming each), as it was judged, and
// reports whether the verdict is the manifest's.
func replayManifest(out io.Writer, path string, fset *flag.FlagSet) (int, error) {
	var extra []string
	fset.Visit(func(f *flag.Flag) {
		if !replayFlags[f.Name] {
			extra = append(extra, "-"+f.Name)
		}
	})
	if len(extra) > 0 || fset.NArg() > 0 {
		return 2, fmt.Errorf("validate -replay %s: %s: the manifest says the inputs and how they were judged; drop them (-dir, -quint, -backend, -v, -history, -itf and -manifest go with -replay)",
			path, strings.Join(append(extra, fset.Args()...), " "))
	}
	m, err := validate.ReadManifest(path)
	if err != nil {
		return 2, err
	}
	warns, err := m.Verify()
	if err != nil {
		return 2, fmt.Errorf("validate -replay %s: %w", path, err)
	}
	for _, w := range warns {
		fmt.Fprintln(out, "warning:", w)
	}
	get := func(name string) string { return fset.Lookup(name).Value.String() }
	if b := get("backend"); b != "" && b != m.Tools.Backend {
		return 2, fmt.Errorf("validate -replay %s -backend %s: the run was judged on backend %q (\"\": the default), and verdicts depend on it: drop -backend", path, b, m.Tools.Backend)
	}
	if env := os.Getenv("QUINT_BACKEND"); m.Tools.Backend == "" && env != "" {
		fmt.Fprintf(out, "note: QUINT_BACKEND=%s ignored: the run was judged on the default backend\n", env)
		os.Unsetenv("QUINT_BACKEND")
	}
	v := validateRun{out: out, dir: get("dir"), quint: get("quint"), backend: m.Tools.Backend, verbose: get("v") == "true", withITF: get("itf") == "true",
		history: get("history"), manifest: get("manifest"), format: m.Check.Format, novars: m.Check.NoVars,
		requireSettle: m.Check.RequireSettle, finishFixed: true, search: m.Check.SearchOptions(), lostPrefix: strings.Join(m.Check.LostPrefix, ","),
		lostSuffix: strings.Join(m.Check.LostSuffix, ","), completeThrough: m.Check.CompleteThrough, module: m.Model.Module, invariants: m.Model.Invariants, constants: m.Model.Constants}
	if v.format == "" {
		v.format = "auto"
	}
	if m.Model.Binding != nil {
		v.binding = m.Model.Binding.Path
	}
	v.itfModels = map[string]*validate.RunModel{}
	for _, in := range m.Inputs {
		if in.Model != nil {
			v.itfModels[in.Path] = in.Model
			if in.Model.Binding != nil && v.binding == "" {
				v.binding = in.Model.Binding.Path
			}
		}
	}
	if m.Model.Convert != nil {
		v.conv = m.Model.Convert.Path
	}
	for _, in := range m.Inputs {
		v.files = append(v.files, in.Path)
	}
	if len(v.files) == 0 {
		return 2, fmt.Errorf("validate -replay %s: the manifest names no input: it was written for a run with none, and there is nothing to judge", path)
	}
	if q := validate.QuintVersion(v.quint); q != m.Tools.Quint {
		fmt.Fprintf(out, "note: the run was judged with quint %q, this replay with %q\n", m.Tools.Quint, q)
	}
	if g := validate.ModuleVersion(); g != m.Tools.Gotogether {
		fmt.Fprintf(out, "note: the run was judged with gotogether %s, this replay with %s\n", m.Tools.Gotogether, g)
	}
	code, got, err := v.run()
	if err != nil {
		return code, err
	}
	same, note := m.Verdict.Same(got)
	if same {
		if note != "" {
			fmt.Fprintln(out, "note:", note)
		}
		fmt.Fprintf(out, "replay: the manifest's verdict, reproduced: %s\n", got)
		return code, nil
	}
	fmt.Fprintf(out, "replay: the verdict is not the manifest's (the inputs are the ones it hashed: the model's imports, the tools or the check changed)\n  manifest: %s\n  replay:   %s\n  differs:  %s\n", m.Verdict, got, note)
	return 1, nil
}

// run judges the inputs, prints the report and the result line, and
// writes the manifest if asked: exit status 0 if every actor passes, 1
// if not.
//
// Without -dir the artifacts go to a temporary directory made once the
// inputs are read and the binding is known to be needed and given, and
// removed when the run passes (unless -itf asked for its ITF): one was
// left behind by every run, tool errors included. A failing run's, or a
// tool error's after it was made, is kept: the report names it.
func (v *validateRun) run() (code int, _ validate.RunVerdict, _ error) {
	var none validate.RunVerdict
	temp := ""
	defer func() {
		if temp != "" && code == 0 && !v.withITF {
			os.RemoveAll(temp)
		}
	}()
	rules, err := loadRules(v.conv)
	if err != nil {
		return 2, none, err
	}
	var b *binding.Binding
	if v.binding != "" {
		if b, err = binding.Load(v.binding); err != nil {
			return 2, none, err
		}
		if err := b.CheckSpec(); err != nil {
			return 2, none, err
		}
	}
	type read struct {
		path, format string
		steps        []qtrace.Step // its steps, a manifest's count of them
	}
	var reads []read
	var steps []qtrace.Step
	var itfs []*input.File
	for _, f := range v.files {
		if rules != nil {
			cs, ok, err := convertFile(f, rules)
			if err != nil {
				return 2, none, err
			}
			if ok {
				fmt.Fprintf(v.out, "input %s: OTLP/JSON spans, converted with %s: %d steps\n", f, v.conv, len(cs))
				steps = append(steps, cs...)
				reads = append(reads, read{f, "otlp", cs})
				continue
			}
		}
		in, err := input.Read(f, input.Format(v.format))
		if err != nil {
			return 2, none, err
		}
		fmt.Fprintf(v.out, "input %s: %s\n", f, map[input.Format]string{input.OTLP: "OTLP/JSON spans", input.StepLog: "native step log",
			input.ITF: "model-level ITF", input.Unknown: "empty"}[in.Format])
		if in.Torn != nil {
			fmt.Fprintf(v.out, "  warning: %v; its recorders' tails are lost after their last steps: eventual vals not judged, and a later step's failure that may depend on them is no verdict\n", in.Torn)
		}
		if in.Format == input.ITF {
			itfs = append(itfs, in)
			reads = append(reads, read{f, "itf", nil})
		} else {
			steps = append(steps, in.Steps...)
			reads = append(reads, read{f, string(in.Format), in.Steps})
		}
	}
	if steps, err = markLost(steps, v.lostPrefix, true); err != nil {
		return 2, none, err
	}
	if steps, err = markLost(steps, v.lostSuffix, false); err != nil {
		return 2, none, err
	}
	if steps, err = markComplete(steps, v.completeThrough); err != nil {
		return 2, none, err
	}
	needChecker := len(steps) > 0 || len(itfs) == 0
	if needChecker && v.binding == "" {
		return 2, none, fmt.Errorf("recorded steps need -binding")
	}
	if v.dir == "" { // one root for every actor's and input's modules, named in the output
		d, err := os.MkdirTemp("", "gotogether-validate-")
		if err != nil {
			return 2, none, err
		}
		v.dir, temp = d, d
		fmt.Fprintf(v.out, "artifacts (generated modules, failing runs' ITF): %s (removed if the run passes; -dir keeps them)\n", v.dir)
	} else {
		fmt.Fprintf(v.out, "artifacts (generated modules, failing runs' ITF): %s\n", v.dir)
	}
	opts := validate.Options{Quint: v.quint, Backend: v.backend, Dir: v.dir, NoITF: !v.withITF}
	ok := true
	var results []*validate.Result // every actor's and ITF's, for the result line
	// incomplete are the reasons of actors that pass and are not finished,
	// under -require-settle (or a complete binding): a prefix of a run
	// conforms, and would pass
	incomplete := map[*validate.Result]string{}
	// a finished run required (-require-settle, a complete binding)
	finish, finishWhy := v.requireSettle, "-require-settle"
	if !finish && !v.finishFixed && b != nil && b.Complete {
		finish, finishWhy = true, "the binding is complete"
	}
	if finish && v.finishFixed && b != nil && b.Complete { // a replay of a complete binding's finish check, said as it was
		finishWhy = "the binding is complete"
	}
	var c *validate.Checker
	if needChecker {
		if c, err = validate.NewChecker(v.binding, opts); err != nil {
			return 2, none, err
		}
		if err := validate.Finishable(c.Binding); finish && err != nil {
			return 2, none, fmt.Errorf("validate %s: %s: %v", finishWhy, v.binding, err) // (a complete binding's lint says so already)
		}
		if v.constants != nil {
			if c, err = c.WithConstants(v.constants); err != nil {
				return 2, none, err
			}
		}
		if v.invariants != nil {
			if c, err = c.WithInvariants(v.invariants...); err != nil {
				return 2, none, err
			}
		}
		// ordered by the search, checked, the Go evaluator's orders confirmed
		// by quint where the check fails, and marked
		j, err := c.Judge(context.Background(), steps, v.search)
		if err != nil {
			return 2, none, err
		}
		rep := j.Report
		if v.history != "" {
			if err := os.WriteFile(v.history, rep.HistorySVG(steps, j.Steps), 0o644); err != nil {
				return 2, none, err
			}
		}
		fmt.Fprint(v.out, rep.Summary())
		if v.verbose {
			for _, a := range rep.Actors {
				fmt.Fprintln(v.out, a.Output)
			}
		}
		ok = ok && rep.OK()
		// one judgment with qtest.Finishes; only a run that passes is
		// judged so, since one that fails says why already
		if f := rep.Finished(); finish && rep.OK() && f.Verdict != validate.Pass {
			who := "the run"
			for _, a := range rep.Actors {
				if f.Actor == "" || a.Actor == f.Actor {
					incomplete[a] = f.Reason
				}
			}
			if f.Actor != "" {
				who = "actor " + f.Actor
			}
			fmt.Fprintf(v.out, "%s: %s (%s): %s\n", who, f.Reason, finishWhy, f.Why)
			ok = false
		}
		results = append(results, rep.Actors...)
		if v.mutateSteps {
			if err := v.mutate(c, steps, ok); err != nil {
				return 2, none, err
			}
		}
	}
	itfModels := map[string]*validate.RunModel{} // each ITF input's instance, for the manifest
	for i, in := range itfs {
		o := opts
		if len(itfs) > 1 {
			o.Dir = fmt.Sprintf("%s/itf%d", o.Dir, i)
		}
		io := validate.ITFOptions{Options: o, Binding: b, Spec: v.spec, Module: v.module, Instance: v.instance, NoVarCheck: v.novars}
		if md := v.itfModels[in.Path]; md != nil { // a replay: the spec the manifest hashed, not the trace's old path
			io.Module, io.Instance = md.Module, md.Instance
			if md.Binding == nil && md.Spec != nil {
				io.Spec = md.Spec.Path
			}
		}
		r, err := validate.ValidateITF(context.Background(), in.ITF, io)
		if err != nil {
			return 2, none, fmt.Errorf("%s: %w", in.Path, err)
		}
		if v.manifest != "" {
			if itfModels[in.Path], err = validate.ITFModel(in.ITF, io); err != nil {
				return 2, none, err
			}
		}
		fmt.Fprint(v.out, r.Summary())
		if v.verbose {
			fmt.Fprintln(v.out, r.Output)
		}
		ok = ok && r.OK()
		if finish && r.OK() {
			// an ITF trace is the model's states alone: no settle, no calls
			incomplete[r] = "no verdict (ITF trace)"
			fmt.Fprintf(v.out, "%s: no verdict (ITF trace) (%s): a finished run can't be judged from an ITF trace, which records no settle: validate the recorded steps\n", in.Path, finishWhy)
			ok = false
		}
		results = append(results, r)
	}
	verdict := validate.VerdictOf(results, incomplete)
	if v.manifest != "" {
		var m *validate.Manifest
		if c != nil {
			if m, err = c.Manifest(results, v.search); err != nil {
				return 2, none, err
			}
		} else if m, err = itfManifest(opts, b); err != nil {
			return 2, none, err
		}
		m.Verdict = verdict
		// the check as judged: a complete binding's finish check too
		m.Check.RequireSettle, m.Check.Format, m.Check.NoVars = finish, v.format, v.novars
		m.Check.LostPrefix, m.Check.LostSuffix = fields(v.lostPrefix), fields(v.lostSuffix)
		m.Check.CompleteThrough = v.completeThrough
		if v.conv != "" {
			if err := m.AddConvert(v.conv); err != nil {
				return 2, none, err
			}
		}
		for _, r := range reads {
			if err := m.AddInput(r.path, r.format, r.steps); err != nil {
				return 2, none, err
			}
			m.Inputs[len(m.Inputs)-1].Model = itfModels[r.path]
		}
		if err := addRunInfo(m, v.runInfo); err != nil {
			return 2, none, err
		}
		if m.Name == "" {
			m.Name = strings.TrimSuffix(filepath.Base(v.manifest), filepath.Ext(v.manifest))
		}
		if err := m.Write(v.manifest); err != nil {
			return 2, none, err
		}
		fmt.Fprintf(v.out, "manifest: %s (gotogether validate -replay %s judges it again)\n", v.manifest, v.manifest)
	}
	if ok {
		fmt.Fprintf(v.out, "RESULT: PASS (%s)\n", passSummary(results))
		return 0, verdict, nil
	}
	// Only a defect is a FAIL (DESIGN §53): a run whose every failure has
	// no verdict (a lost tail, dropped steps, an uncollected window) says
	// so, though it exits 1 as a failure does, being no pass.
	word := "FAIL"
	if verdict.Verdict == validate.NotEvaluated {
		word = "NO VERDICT"
	}
	fmt.Fprintf(v.out, "RESULT: %s (%s)\n", word, failSummary(results, incomplete))
	return 1, verdict, nil
}

// mutate judges and prints the observation mutants of steps, after the
// run passed (ok): a failing run's mutants fail too, and say nothing.
func (v *validateRun) mutate(c *validate.Checker, steps []qtrace.Step, ok bool) error {
	if !ok {
		fmt.Fprintln(v.out, "mutate-steps: none judged: the run does not pass, and a failing run's mutants fail too; fix the run or the model first")
		return nil
	}
	var opts []qtrace.MutationOption
	if ops := fields(v.mutateOps); len(ops) > 0 {
		opts = append(opts, qtrace.MutateOps(ops...))
	}
	if v.mutateLimit > 0 {
		opts = append(opts, qtrace.MutateLimit(v.mutateLimit))
	}
	rep, err := qtest.JudgeStepMutants(c, steps, v.search, opts...)
	if err != nil {
		return fmt.Errorf("validate -mutate-steps: %w", err)
	}
	fmt.Fprint(v.out, rep)
	return nil
}

// itfManifest is the manifest of ITF inputs checked with no checker:
// the binding, if given; each input's own instance is its Input.Model.
func itfManifest(o validate.Options, b *binding.Binding) (*validate.Manifest, error) {
	m := validate.NewManifest(o)
	m.Model.Invariants = []string{}
	if b != nil {
		if err := m.SetBinding(b); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// runInfo is what -run-info's file gives a manifest: what gotogether did
// not see.
type runInfo struct {
	Name      string              `json:"name"`
	Driver    *validate.Driver    `json:"driver"`
	Artifacts []validate.Artifact `json:"artifacts"`
}

// addRunInfo adds the name, driver and artifacts of the -run-info file
// at path (none: "") to m, hashing each artifact.
func addRunInfo(m *validate.Manifest, path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ri runInfo
	dec := json.NewDecoder(strings.NewReader(string(data)))
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

// repeated is a flag given any number of times, its values in order.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, " ") }

// Set adds a value.
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// markComplete states -complete-through's watermarks the steps do not
// carry (qtrace.MarkComplete): each "recorder=T", T in RFC3339 or Unix
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
