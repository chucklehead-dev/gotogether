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
	"github.com/chucklehead-dev/gotogether/validate/cli"
)

// validateRun is one validate command: what its flags ask for (a
// cli.Check), and how its report is printed (a replay's too).
type validateRun struct {
	choiceLimit                          int
	binding, dir, backend, quint, format string
	spec, module, instance               string // an ITF input's
	conv, history                        string
	compiled                             string // -compiled: a compiled model, judged with no quint
	novars, verbose, withITF             bool
	requireFinish                        bool
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
	// machine are the machine-readable reports asked for (-json, -report,
	// GOTOGETHER_REPORT_DIR), and doc the one they render, filled as the
	// text report is printed
	machine machineReports
	doc     *validate.Document
}

func runValidate(args []string) (int, error) { return validateTo(os.Stdout, args) }

// validateTo is the validate command, its report written to out.
func validateTo(out io.Writer, args []string) (int, error) {
	fset := commandFlags("validate")
	v := validateRun{out: out, doc: validate.NewDocument("validate")}
	v.machine.flags(fset)
	fset.StringVar(&v.binding, "binding", "", "binding file (optional for ITF written by gotogether)")
	fset.StringVar(&v.dir, "dir", "", "where to write generated modules and ITF (default: a new temp dir, printed)")
	fset.StringVar(&v.backend, "backend", "", "quint test backend (default $QUINT_BACKEND or rust)")
	fset.StringVar(&v.quint, "quint", "quint", "quint binary")
	fset.StringVar(&v.compiled, "compiled", "", "judge the steps by this compiled model (gotogether compile's) with the Go evaluator alone: no quint or Node; a run it cannot decide is no verdict (needs quint), and no pass is sampled by quint. Refused when stale (another binding, a changed source, another gotogether's evaluation module)")
	fset.StringVar(&v.format, "format", "auto", "input format: auto, otlp, steps (native step log), requests (a driver's request log, package qdriver) or itf")
	fset.StringVar(&v.spec, "spec", "", "ITF input: the model, if neither the trace nor a binding names it")
	fset.StringVar(&v.module, "module", "", "ITF input: the parameterised module")
	fset.StringVar(&v.instance, "instance", "", "ITF input: replay against this instance module (e.g. currentDesign), e.g. for quint run --mbt traces")
	fset.BoolVar(&v.novars, "novars", false, "ITF input: do not compare model variables carried by the trace")
	fset.BoolVar(&v.verbose, "v", false, "verbose: each failing record's place in the replay and its recorder's seq besides its id, every example of the actions enabled instead, the model's whole state, and quint's output")
	fset.StringVar(&v.conv, "convert", "", "convert OTLP/JSON spans with this rule file (see gotogether convert)")
	invs := fset.String("invariants", "", "comma-separated model invariants to check instead of the binding's")
	fset.BoolVar(&v.withITF, "itf", false, "also write the ITF of conforming runs, the model's state at every step (slow for models with a large state); failing runs always get theirs")
	fset.IntVar(&v.choiceLimit, "choice-limit", 0, "expanded states per actor for finite internal model choices (default 20000); exhaustion gives no verdict")
	noITF := fset.Bool("noitf", false, "gone: no ITF for conforming runs is the default")
	lin := fset.Bool("linearize", false, "check each actor's steps in an order real time allows, searched for one the model accepts (a client history: steps recorded when calls returned)")
	fset.StringVar(&v.search.Key, "linearize-key", "", "linearize each actor's history partitioned by this argument's value (independent keys), each partition alone")
	fset.IntVar(&v.search.Budget, "linearize-budget", 0, "orders a linearization checks per actor or partition (default 200)")
	fset.DurationVar(&v.search.Skew, "skew", 0, "how far apart recorders' clocks may be when real time orders a linearization's steps (default 1ms); implies -linearize")
	fset.BoolVar(&v.search.ResolvePending, "resolve-pending", false, "search what each call left pending (invoked, never completed) may have done")
	fset.StringVar(&v.history, "history", "", "write the history drawn as SVG to this file (lanes of calls, the order checked numbered, the failing step red, each call's ref on hover)")
	fset.StringVar(&v.lostPrefix, "lost-prefix", "", "comma-separated recorders whose logs lost their first steps, which the log cannot show (no seqs): judged as a lost prefix (qtrace.MarkLost)")
	settle := fset.Bool("require-settle", false, "deprecated: -require-finish (the run settled is one part of what it checks)")
	fset.BoolVar(&v.requireFinish, "require-finish", false, "exit 1 unless the run is finished, as qtest.Finishes judges it: it settled (its trace's last record a settle), every eventual val held there, and no call was left pending (one -resolve-pending places included: its caller never got the answer); a lost tail is no verdict (exit 1). The binding needs an eventual val, the journey's end; the default for a binding with complete: true. An ITF trace records no settle, so it cannot be judged finished (exit 1)")
	fset.StringVar(&v.lostSuffix, "lost-suffix", "", "comma-separated recorders whose logs lost their last steps, which the log cannot show (a killed process's buffer): their tails judged lost after their last steps (qtrace.MarkLost)")
	fset.Var(&v.completeThrough, "complete-through", "recorder=T: the recorder's steps recorded at or before T (RFC3339, or Unix nanoseconds) are all in the input, as an external collector knows what it has ingested (a ClickHouse poll, a log shipper's acknowledgement); *=T states it of every recorder. Repeatable. "+
		"A bounded response due by T is then judged though the run never settled (absent: a FAIL), one due after it is uncollected (no verdict), and a settle it does not reach settles nothing. A log with no watermark is taken whole (qtrace.MarkComplete)")
	fset.StringVar(&v.manifest, "manifest", "", "write the run's manifest (gotogether.run/2) to this file: the tools, model, binding and inputs with their hashes, losses, the verdict and failing step; -replay judges it again")
	fset.StringVar(&v.runInfo, "run-info", "", "with -manifest: a JSON file of what gotogether did not see, written by the driver: {\"name\": ..., \"driver\": {\"name\", \"version\", \"seed\", \"origin\", \"args\": [...]}, \"artifacts\": [{\"name\", \"path\"}]} (paths relative to the file)")
	fset.BoolVar(&v.mutateSteps, "mutate-steps", false, "after a pass, judge the steps' observation mutants (a step dropped, duplicated, swapped with its recorder's next, an argument or observation corrupted, retimed past its neighbour, moved to another actor) as the steps were judged, and print a table of them by op, killed and survived: a survivor changed something no check reads (qtest.MutateSteps)")
	fset.StringVar(&v.mutateOps, "mutate-ops", "", "with -mutate-steps: comma-separated ops to make (drop, dup, swap, corrupt, retime, reactor, gap; default all but gap, whose mutants are only ever undecided)")
	fset.IntVar(&v.mutateLimit, "mutate-limit", 0, "with -mutate-steps: at most this many mutants of each op, spread over the log (default: every one)")
	replay := fset.String("replay", "", "judge a run again from its manifest (validate -manifest's, or a test's under GOTOGETHER_EVIDENCE), refusing an input whose hash changed; exit as validate does, and 1 if the verdict is not the manifest's")
	fset.Parse(args)
	if err := v.machine.resolve(&v.out); err != nil {
		return 2, err
	}
	code, err := v.command(fset, *settle, *noITF, *replay, *invs, *lin)
	return v.machine.write(v.doc, out, code, err)
}

// command is the validate command after its flags are parsed.
func (v *validateRun) command(fset *flag.FlagSet, settle, noITF bool, replay, invs string, lin bool) (int, error) {
	out := v.out
	if settle {
		v.note("warning: -require-settle is deprecated: it checks the run finished (qtest.Finishes), of which settling is one part; say -require-finish")
		v.requireFinish = true
	}
	if noITF {
		return 2, fmt.Errorf("validate: -noitf is the default now: drop it (-itf writes the ITF of conforming runs)")
	}
	if replay != "" {
		return v.replayManifest(replay, fset)
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
	v.search.Linearize = lin || v.search.Key != "" || v.search.Skew > 0
	if v.compiled != "" && v.manifest != "" {
		return 2, fmt.Errorf("validate -compiled: -manifest records a judgment validate -replay makes again with quint: drop one of them")
	}
	if invs != "" {
		v.invariants = strings.Split(invs, ",")
	}
	_ = out
	v.files = fset.Args()
	if len(v.files) == 0 {
		return 2, needs("validate", "a binding and the FILEs to check (step logs, OTLP/JSON, ITF)")
	}
	return v.run()
}

// replayFlags are the flags -replay takes besides itself: where its
// output goes, which quint, the backend (only the manifest's: verdicts
// depend on it), a new manifest of the replay. The others are the
// manifest's to say.
// Read-only.
var replayFlags = map[string]bool{"replay": true, "dir": true, "quint": true, "v": true, "backend": true, "manifest": true, "history": true, "itf": true,
	"json": true, "report": true}

// replayManifest judges the run of the manifest at path again
// (cli.Replay): its files unchanged (refused otherwise, naming
// each), as it was judged, and reports whether the verdict is the
// manifest's.
func (v0 *validateRun) replayManifest(path string, fset *flag.FlagSet) (int, error) {
	var extra []string
	fset.Visit(func(f *flag.Flag) {
		if !replayFlags[f.Name] {
			extra = append(extra, "-"+f.Name)
		}
	})
	if len(extra) > 0 || fset.NArg() > 0 {
		return 2, fmt.Errorf("validate -replay %s: %s: the manifest says the inputs and how they were judged; drop them (-dir, -quint, -backend, -v, -history, -itf, -manifest, -json and -report go with -replay)",
			path, strings.Join(append(extra, fset.Args()...), " "))
	}
	m, err := validate.ReadManifest(path)
	if err != nil {
		return 2, err
	}
	get := func(name string) string { return fset.Lookup(name).Value.String() }
	v := v0
	v.verbose, v.history, v.manifest, v.withITF = get("v") == "true", get("history"), get("manifest"), get("itf") == "true"
	r, err := cli.Replay(context.Background(), m, validate.Options{Quint: get("quint"), Backend: get("backend"), Dir: get("dir"), NoITF: get("itf") != "true"})
	if r == nil && err != nil {
		return 2, fmt.Errorf("validate -replay %s: %w", path, err)
	}
	for _, w := range r.Warnings {
		v.note("warning: " + w)
	}
	for _, n := range r.Notes {
		v.note("note: " + n)
	}
	code, err := v.report(r.Got, err)
	if err != nil {
		return code, err
	}
	if r.Same {
		if r.Differs != "" {
			v.note("note: " + r.Differs)
		}
		v.note(fmt.Sprintf("replay: the manifest's verdict, reproduced: %s", r.Got.Verdict))
		return code, nil
	}
	v.note(fmt.Sprintf("replay: the verdict is not the manifest's (the inputs are the ones it hashed: the model's imports, the tools or the check changed)\n  manifest: %s\n  replay:   %s\n  differs:  %s", r.Want, r.Got.Verdict, r.Differs))
	v.doc.Verdict, v.doc.Reason = "fail", "the verdict is not the manifest's"
	return 1, nil
}

// run judges the inputs (cli.Check), prints the report and the
// result line, and writes the manifest if asked: exit status 0 if every
// actor passes, 1 if not.
func (v *validateRun) run() (int, error) {
	format := input.Format(v.format)
	if v.format == "auto" {
		format = input.Unknown
	}
	chk := cli.Check{Binding: v.binding, Files: v.files, Format: format, Convert: v.conv, Spec: v.spec, Module: v.module, Instance: v.instance,
		NoVars: v.novars, Invariants: v.invariants, Search: v.search, LostPrefix: fields(v.lostPrefix), LostSuffix: fields(v.lostSuffix),
		CompleteThrough: v.completeThrough, RequireFinish: v.requireFinish, RunInfo: v.runInfo, Compiled: v.compiled,
		Options: validate.Options{Quint: v.quint, Backend: v.backend, Dir: v.dir, NoITF: !v.withITF, ChoiceLimit: v.choiceLimit}}
	j, err := chk.Judge(context.Background())
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
func (v *validateRun) report(j *cli.Judged, jerr error) (code int, _ error) {
	if j == nil {
		return 2, jerr
	}
	defer func() {
		if code == 0 && !v.withITF {
			j.Close() // a temporary directory; -dir's is kept
		} else if j.Dir != "" {
			v.doc.Artifacts = j.Dir
		}
	}()
	v.doc.Binding = v.binding
	for _, in := range j.Inputs {
		di := validate.DocInput{Path: in.Path, Format: string(in.Format), Steps: len(in.Steps)}
		if in.Converted {
			fmt.Fprintf(v.out, "input %s: OTLP/JSON spans, converted with %s: %d steps\n", in.Path, v.conv, len(in.Steps))
			v.doc.Inputs = append(v.doc.Inputs, di)
			continue
		}
		fmt.Fprintf(v.out, "input %s: %s\n", in.Path, map[input.Format]string{input.OTLP: "OTLP/JSON spans", input.StepLog: "native step log", input.Requests: "a driver's request log",
			input.ITF: "model-level ITF", input.Unknown: "empty"}[in.Format])
		if in.Torn != nil {
			w := fmt.Sprintf("%v; its recorders' tails are lost after their last steps: eventual vals not judged, and a later step's failure that may depend on them is no verdict", in.Torn)
			fmt.Fprintf(v.out, "  warning: %s\n", w)
			di.Warnings = append(di.Warnings, w)
		}
		for _, w := range in.Warnings {
			fmt.Fprintf(v.out, "  warning: %s\n", w)
			di.Warnings = append(di.Warnings, w)
		}
		v.doc.Inputs = append(v.doc.Inputs, di)
	}
	if j.Dir == "" {
		return 2, jerr
	}
	if jerr != nil && j.Temporary() && emptyDir(j.Dir) {
		// refused before anything was written there (a stale compiled
		// model): a directory named and left empty said otherwise
		j.Close()
		return 2, jerr
	}
	switch {
	case j.Temporary() && v.withITF:
		fmt.Fprintf(v.out, "artifacts (generated modules, every run's ITF): %s (kept: -itf)\n", j.Dir)
	case j.Temporary():
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
		if v.verbose {
			fmt.Fprint(v.out, rep.VerboseSummary())
		} else {
			fmt.Fprint(v.out, rep.Summary())
		}
		v.doc.AddReport(rep)
		v.navLines(rep)
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
			v.note(fmt.Sprintf("%s: %s (%s): %s", who, f.Reason, finishWhy(j), f.Why))
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
		if v.verbose {
			fmt.Fprint(v.out, r.VerboseSummary())
			fmt.Fprintln(v.out, r.Output)
		} else {
			fmt.Fprint(v.out, r.Summary())
		}
		v.doc.AddReport(&validate.Report{Actors: []*validate.Result{r}})
		fmt.Fprint(v.out, (&validate.Report{Actors: []*validate.Result{r}}).Receipt().String())
		if j.Finish && r.OK() {
			v.note(fmt.Sprintf("%s: no verdict (ITF trace) (%s): a finished run can't be judged from an ITF trace, which records no settle: validate the recorded steps", in.Path, finishWhy(j)))
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
		v.note(fmt.Sprintf("manifest: %s (gotogether validate -replay %s judges it again)", v.manifest, v.manifest))
	}
	if j.OK {
		v.result("PASS", passSummary(j.Results))
		return 0, nil
	}
	// Only a defect is a FAIL (DESIGN §53): a run whose every failure has
	// no verdict (a lost tail, dropped steps, an uncollected window) says
	// so, though it exits 1 as a failure does, being no pass.
	word := "FAIL"
	if j.Verdict.Verdict == validate.NotEvaluated {
		word = "NO VERDICT"
	}
	v.doc.Reason = j.Verdict.Reason
	if v.doc.Reason == "" && j.Unfinished != nil {
		v.doc.Reason = j.Unfinished.Reason
	}
	v.result(word, failSummary(j.Results))
	return 1, nil
}

// note prints a line of the report that is neither an actor's nor the
// result, and keeps it for the machine-readable report.
func (v *validateRun) note(line string) {
	fmt.Fprintln(v.out, line)
	v.doc.Notes = append(v.doc.Notes, line)
}

// result prints the result line, "RESULT: <word> (<summary>)", and
// keeps it for the machine-readable report.
func (v *validateRun) result(word, summary string) {
	fmt.Fprintf(v.out, "RESULT: %s (%s)\n", word, summary)
	v.doc.Verdict, v.doc.Result = strings.ToLower(word), summary
}

// finishWhy is why the finish check was judged, as the report says it.
func finishWhy(j *cli.Judged) string {
	if j.FinishByBinding {
		return "the binding is complete"
	}
	return "-require-finish"
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

// navLines print, for each actor whose step the model rejected or
// mismatched, the nav command that opens the model in the state before
// that step (a step log's run, as recorded: one input, no search).
func (v *validateRun) navLines(rep *validate.Report) {
	if v.binding == "" || len(v.files) != 1 || v.search.Linearize || v.search.ResolvePending || v.conv != "" {
		return
	}
	for _, a := range rep.Actors {
		f := a.Failure
		if f == nil || a.Conforms != validate.Fail || f.Index < 0 || f.Kind != "disabled" && f.Kind != "state" {
			continue
		}
		actor := ""
		if a.Actor != "" {
			actor = fmt.Sprintf(" -actor '%s'", a.Actor)
		}
		cmd := fmt.Sprintf("gotogether nav -binding %s -run %s%s -at %d", v.binding, v.files[0], actor, f.Index)
		fmt.Fprintf(v.out, "navigate: %s   (the model before %s: its branches, and why the step is not one)\n", cmd, f.At())
		for i := range v.doc.Actors {
			if v.doc.Actors[i].Actor == a.Actor {
				v.doc.Actors[i].Nav = cmd
			}
		}
	}
}

// emptyDir reports whether dir holds nothing (or cannot be read).
func emptyDir(dir string) bool {
	es, err := os.ReadDir(dir)
	return err != nil || len(es) == 0
}
