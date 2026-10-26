package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/input"
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
	// invariants and constants, if set, override the binding's (-invariants;
	// a replay's, the manifest's instance)
	invariants []string
	constants  map[string]string
	search     validate.SearchOptions
	lostPrefix string // comma-separated recorders
	lostSuffix string
	files      []string
	// manifest, if set, is where to write the run's manifest, with the
	// driver, artifacts and name runInfo's fragment gives
	manifest, runInfo string
}

func runValidate(args []string) (int, error) {
	fset := commandFlags("validate")
	var v validateRun
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
	fset.StringVar(&v.manifest, "manifest", "", "write the run's manifest (gotogether.run/1) to this file: the tools, model, binding and inputs with their hashes, losses, the verdict and failing step; -replay judges it again")
	fset.StringVar(&v.runInfo, "run-info", "", "with -manifest: a JSON file of what gotogether did not see, written by the driver: {\"name\": ..., \"driver\": {\"name\", \"version\", \"seed\", \"origin\", \"args\": [...]}, \"artifacts\": [{\"name\", \"path\"}]} (paths relative to the file)")
	replay := fset.String("replay", "", "judge a run again from its manifest (validate -manifest's, or a test's under GOTOGETHER_EVIDENCE), refusing an input whose hash changed; exit as validate does, and 1 if the verdict is not the manifest's")
	fset.Parse(args)
	if *noITF {
		return 2, fmt.Errorf("validate: -noitf is the default now: drop it (-itf writes the ITF of conforming runs)")
	}
	if *replay != "" {
		return replayManifest(*replay, fset)
	}
	if v.runInfo != "" && v.manifest == "" {
		return 2, fmt.Errorf("validate: -run-info %s says what goes in a manifest: add -manifest out.json", v.runInfo)
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
// output goes, which quint, the backend (the manifest's by default), a
// new manifest of the replay. The others are the manifest's to say.
// Read-only.
var replayFlags = map[string]bool{"replay": true, "dir": true, "quint": true, "v": true, "backend": true, "manifest": true, "history": true, "itf": true}

// replayManifest judges the run of the manifest at path again: its files
// unchanged (refused otherwise, naming each), as it was judged, and
// reports whether the verdict is the manifest's.
func replayManifest(path string, fset *flag.FlagSet) (int, error) {
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
		fmt.Println("warning:", w)
	}
	get := func(name string) string { return fset.Lookup(name).Value.String() }
	v := validateRun{dir: get("dir"), quint: get("quint"), backend: get("backend"), verbose: get("v") == "true", withITF: get("itf") == "true",
		history: get("history"), manifest: get("manifest"), format: m.Check.Format, instance: m.Check.Instance, novars: m.Check.NoVars,
		requireSettle: m.Check.RequireSettle, search: m.Check.SearchOptions(), lostPrefix: strings.Join(m.Check.LostPrefix, ","),
		lostSuffix: strings.Join(m.Check.LostSuffix, ","), module: m.Model.Module, invariants: m.Model.Invariants, constants: m.Model.Constants}
	if v.backend == "" {
		v.backend = m.Tools.Backend
	}
	if v.format == "" {
		v.format = "auto"
	}
	if m.Model.Binding != nil {
		v.binding = m.Model.Binding.Path
	} else if m.Model.Spec != nil {
		v.spec = m.Model.Spec.Path
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
		fmt.Printf("note: the run was judged with quint %q, this replay with %q\n", m.Tools.Quint, q)
	}
	if g := validate.ModuleVersion(); g != m.Tools.Gotogether {
		fmt.Printf("note: the run was judged with gotogether %s, this replay with %s\n", m.Tools.Gotogether, g)
	}
	code, got, err := v.run()
	if err != nil {
		return code, err
	}
	if got.Same(m.Verdict) {
		fmt.Printf("replay: the manifest's verdict, reproduced: %s\n", got)
		return code, nil
	}
	fmt.Printf("replay: the verdict is not the manifest's (the inputs are the ones it hashed: the model's imports, the tools or the check changed)\n  manifest: %s\n  replay:   %s\n", m.Verdict, got)
	return 1, nil
}

// run judges the inputs, prints the report and the result line, and
// writes the manifest if asked: exit status 0 if every actor passes, 1
// if not.
func (v *validateRun) run() (int, validate.RunVerdict, error) {
	var none validate.RunVerdict
	rules, err := loadRules(v.conv)
	if err != nil {
		return 2, none, err
	}
	var b *binding.Binding
	if v.binding != "" {
		if b, err = binding.Load(v.binding); err != nil {
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
				fmt.Printf("input %s: OTLP/JSON spans, converted with %s: %d steps\n", f, v.conv, len(cs))
				steps = append(steps, cs...)
				reads = append(reads, read{f, "otlp", cs})
				continue
			}
		}
		in, err := input.Read(f, input.Format(v.format))
		if err != nil {
			return 2, none, err
		}
		fmt.Printf("input %s: %s\n", f, map[input.Format]string{input.OTLP: "OTLP/JSON spans", input.StepLog: "native step log",
			input.ITF: "model-level ITF", input.Unknown: "empty"}[in.Format])
		if in.Torn != nil {
			fmt.Printf("  warning: %v; its recorders' tails are lost after their last steps: eventual vals not judged, and a later step's failure that may depend on them is no verdict\n", in.Torn)
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
	if v.dir == "" { // one root for every actor's and input's modules, named in the output
		d, err := os.MkdirTemp("", "gotogether-validate-")
		if err != nil {
			return 2, none, err
		}
		v.dir = d
	}
	fmt.Printf("artifacts (generated modules, failing runs' ITF): %s\n", v.dir)
	opts := validate.Options{Quint: v.quint, Backend: v.backend, Dir: v.dir, NoITF: !v.withITF}
	ok := true
	var results []*validate.Result // every actor's and ITF's, for the result line
	// incomplete are the reasons of actors that pass and are not finished,
	// under -require-settle (or a complete binding): a prefix of a run
	// conforms, and would pass
	incomplete := map[*validate.Result]string{}
	// a finished run required (-require-settle, a complete binding)
	finish, finishWhy := v.requireSettle, "-require-settle"
	if !finish && b != nil && b.Complete {
		finish, finishWhy = true, "the binding is complete"
	}
	var c *validate.Checker
	if len(steps) > 0 || len(itfs) == 0 {
		if v.binding == "" {
			return 2, none, fmt.Errorf("recorded steps need -binding")
		}
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
		fmt.Print(rep.Summary())
		if v.verbose {
			for _, a := range rep.Actors {
				fmt.Println(a.Output)
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
			fmt.Printf("%s: %s (%s): %s\n", who, f.Reason, finishWhy, f.Why)
			ok = false
		}
		results = append(results, rep.Actors...)
	}
	for i, in := range itfs {
		o := opts
		if len(itfs) > 1 {
			o.Dir = fmt.Sprintf("%s/itf%d", o.Dir, i)
		}
		r, err := validate.ValidateITF(context.Background(), in.ITF, validate.ITFOptions{Options: o, Binding: b,
			Spec: v.spec, Module: v.module, Instance: v.instance, NoVarCheck: v.novars})
		if err != nil {
			return 2, none, fmt.Errorf("%s: %w", in.Path, err)
		}
		fmt.Print(r.Summary())
		if v.verbose {
			fmt.Println(r.Output)
		}
		ok = ok && r.OK()
		if finish && r.OK() {
			// an ITF trace is the model's states alone: no settle, no calls
			incomplete[r] = "no verdict (ITF trace)"
			fmt.Printf("%s: no verdict (ITF trace) (%s): a finished run can't be judged from an ITF trace, which records no settle: validate the recorded steps\n", in.Path, finishWhy)
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
		} else if m, err = itfManifest(opts, b, v.spec, v.module); err != nil {
			return 2, none, err
		}
		m.Verdict = verdict
		m.Check.RequireSettle, m.Check.Format, m.Check.Instance, m.Check.NoVars = v.requireSettle, v.format, v.instance, v.novars
		m.Check.LostPrefix, m.Check.LostSuffix = fields(v.lostPrefix), fields(v.lostSuffix)
		if v.conv != "" {
			if err := m.AddConvert(v.conv); err != nil {
				return 2, none, err
			}
		}
		for _, r := range reads {
			if err := m.AddInput(r.path, r.format, r.steps); err != nil {
				return 2, none, err
			}
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
		fmt.Printf("manifest: %s (gotogether validate -replay %s judges it again)\n", v.manifest, v.manifest)
	}
	if ok {
		fmt.Printf("RESULT: PASS (%s)\n", passSummary(results))
		return 0, verdict, nil
	}
	fmt.Printf("RESULT: FAIL (%s)\n", failSummary(results, incomplete))
	return 1, verdict, nil
}

// itfManifest is the manifest of ITF inputs checked with no checker: the
// binding, if given, else the spec named.
func itfManifest(o validate.Options, b *binding.Binding, spec, module string) (*validate.Manifest, error) {
	m := validate.NewManifest(o)
	m.Model.Invariants = []string{}
	if b != nil {
		if err := m.SetBinding(b); err != nil {
			return nil, err
		}
		return m, nil
	}
	m.Model.Module = module
	if spec != "" {
		if err := m.SetSpec(spec); err != nil {
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
