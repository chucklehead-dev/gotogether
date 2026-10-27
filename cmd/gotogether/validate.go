package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// validateRun is one validate command: what its flags ask for (a
// validate.Command), and how its report is printed (a replay's too).
type validateRun struct {
	binding, dir, backend, quint, format string
	spec, module, instance               string // an ITF input's
	conv, history                        string
	novars, verbose, withITF             bool
	requireSettle                        bool
	// invariants, if set, override the binding's (-invariants)
	invariants []string
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
	// (validate.Checker.MutateSteps), narrowed to mutateOps (comma-separated)
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
	return v.run()
}

// replayFlags are the flags -replay takes besides itself: where its
// output goes, which quint, the backend (only the manifest's: verdicts
// depend on it), a new manifest of the replay. The others are the
// manifest's to say.
// Read-only.
var replayFlags = map[string]bool{"replay": true, "dir": true, "quint": true, "v": true, "backend": true, "manifest": true, "history": true, "itf": true}

// replayManifest judges the run of the manifest at path again
// (validate.Replay): its files unchanged (refused otherwise, naming
// each), as it was judged, and reports whether the verdict is the
// manifest's.
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
	get := func(name string) string { return fset.Lookup(name).Value.String() }
	v := validateRun{out: out, verbose: get("v") == "true", history: get("history"), manifest: get("manifest"), withITF: get("itf") == "true"}
	r, err := validate.Replay(context.Background(), m, validate.Options{Quint: get("quint"), Backend: get("backend"), Dir: get("dir"), NoITF: get("itf") != "true"})
	if r == nil && err != nil {
		return 2, fmt.Errorf("validate -replay %s: %w", path, err)
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	code, err := v.report(r.Judged, err)
	if err != nil {
		return code, err
	}
	if r.Same {
		if r.Note != "" {
			fmt.Fprintln(out, "note:", r.Note)
		}
		fmt.Fprintf(out, "replay: the manifest's verdict, reproduced: %s\n", r.Verdict)
		return code, nil
	}
	fmt.Fprintf(out, "replay: the verdict is not the manifest's (the inputs are the ones it hashed: the model's imports, the tools or the check changed)\n  manifest: %s\n  replay:   %s\n  differs:  %s\n", r.Want, r.Verdict, r.Note)
	return 1, nil
}

// run judges the inputs (validate.Command), prints the report and the
// result line, and writes the manifest if asked: exit status 0 if every
// actor passes, 1 if not.
func (v *validateRun) run() (int, error) {
	cmd := validate.Command{Binding: v.binding, Files: v.files, Format: v.format, Convert: v.conv, Spec: v.spec, Module: v.module, Instance: v.instance,
		NoVars: v.novars, Invariants: v.invariants, Search: v.search, LostPrefix: fields(v.lostPrefix), LostSuffix: fields(v.lostSuffix),
		CompleteThrough: v.completeThrough, RequireSettle: v.requireSettle, RunInfo: v.runInfo,
		Options: validate.Options{Quint: v.quint, Backend: v.backend, Dir: v.dir, NoITF: !v.withITF}}
	j, err := cmd.Judge(context.Background())
	return v.report(j, err)
}

// report prints a judgment (j, and the error that ended it, if any):
// its inputs, where its artifacts are, each actor's and ITF trace's
// report, the finish check, the steps' observation mutants if asked, the
// manifest if asked, and the result line; exit status 0 if the run
// passes, 1 if not, 2 on a tool error.
//
// Without -dir the artifacts go to a temporary directory made once the
// inputs are read and the binding is known to be needed and given, and
// removed when the run passes (unless -itf asked for its ITF): one was
// left behind by every run, tool errors included. A failing run's, or a
// tool error's after it was made, is kept: the report names it.
func (v *validateRun) report(j *validate.Judged, jerr error) (code int, _ error) {
	if j == nil {
		return 2, jerr
	}
	defer func() {
		if j.Temp && code == 0 && !v.withITF {
			os.RemoveAll(j.Dir)
		}
	}()
	for _, in := range j.Inputs {
		if in.Converted {
			fmt.Fprintf(v.out, "input %s: OTLP/JSON spans, converted with %s: %d steps\n", in.Path, v.conv, len(in.Steps))
			continue
		}
		fmt.Fprintf(v.out, "input %s: %s\n", in.Path, map[input.Format]string{input.OTLP: "OTLP/JSON spans", input.StepLog: "native step log",
			input.ITF: "model-level ITF", input.Unknown: "empty"}[in.Format])
		if in.Torn != nil {
			fmt.Fprintf(v.out, "  warning: %v; its recorders' tails are lost after their last steps: eventual vals not judged, and a later step's failure that may depend on them is no verdict\n", in.Torn)
		}
	}
	if j.Dir == "" {
		return 2, jerr
	}
	switch {
	case j.Temp && v.withITF:
		fmt.Fprintf(v.out, "artifacts (generated modules, every run's ITF): %s (kept: -itf)\n", j.Dir)
	case j.Temp:
		fmt.Fprintf(v.out, "artifacts (generated modules, failing runs' ITF): %s (removed if the run passes; -dir keeps them)\n", j.Dir)
	default:
		fmt.Fprintf(v.out, "artifacts (generated modules, failing runs' ITF): %s\n", j.Dir)
	}
	if jd := j.Judgment; jd != nil {
		rep := jd.Report
		if v.history != "" {
			if err := os.WriteFile(v.history, rep.HistorySVG(j.Steps, jd.Steps), 0o644); err != nil {
				return 2, err
			}
		}
		fmt.Fprint(v.out, rep.Summary())
		if v.verbose {
			for _, a := range rep.Actors {
				fmt.Fprintln(v.out, a.Output)
			}
		}
		if f := j.Unfinished; f != nil {
			who := "the run"
			if f.Actor != "" {
				who = "actor " + f.Actor
			}
			fmt.Fprintf(v.out, "%s: %s (%s): %s\n", who, f.Reason, j.FinishWhy, f.Why)
		}
		if v.mutateSteps && jerr == nil {
			if err := v.mutate(j.Checker, j.Steps, rep.OK() && j.Unfinished == nil); err != nil {
				return 2, err
			}
		}
	}
	itfs := 0
	for _, in := range j.Inputs {
		if in.Format != input.ITF {
			continue
		}
		if itfs >= len(j.ITF) {
			break
		}
		r := j.ITF[itfs]
		itfs++
		fmt.Fprint(v.out, r.Summary())
		if v.verbose {
			fmt.Fprintln(v.out, r.Output)
		}
		if j.Finish && r.OK() {
			fmt.Fprintf(v.out, "%s: no verdict (ITF trace) (%s): a finished run can't be judged from an ITF trace, which records no settle: validate the recorded steps\n", in.Path, j.FinishWhy)
		}
	}
	if jerr != nil {
		return 2, jerr
	}
	if v.manifest != "" {
		m, err := j.Manifest()
		if err != nil {
			return 2, err
		}
		if m.Name == "" {
			m.Name = strings.TrimSuffix(filepath.Base(v.manifest), filepath.Ext(v.manifest))
		}
		if err := m.Write(v.manifest); err != nil {
			return 2, err
		}
		fmt.Fprintf(v.out, "manifest: %s (gotogether validate -replay %s judges it again)\n", v.manifest, v.manifest)
	}
	if j.OK {
		fmt.Fprintf(v.out, "RESULT: PASS (%s)\n", passSummary(j.Results))
		return 0, nil
	}
	// Only a defect is a FAIL (DESIGN §53): a run whose every failure has
	// no verdict (a lost tail, dropped steps, an uncollected window) says
	// so, though it exits 1 as a failure does, being no pass.
	word := "FAIL"
	if j.Verdict.Verdict == validate.NotEvaluated {
		word = "NO VERDICT"
	}
	fmt.Fprintf(v.out, "RESULT: %s (%s)\n", word, failSummary(j.Results, j.Incomplete))
	return 1, nil
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
	rep, err := c.MutateSteps(context.Background(), steps, v.search, nil, opts...)
	if err != nil {
		return fmt.Errorf("validate -mutate-steps: %w", err)
	}
	fmt.Fprint(v.out, rep)
	return nil
}

// repeated is a flag given any number of times, its values in order.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, " ") }

// Set adds a value.
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }
