package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/validate/model"
)

// simulate runs the binding's model on its own: model.Simulate (the
// one engine for the model alone, qtest.Model's too) with the flags'
// options, its report printed (and, with -json and the other machine
// reports, projected from native results). By default quint samples; with -verify
// N, quint verify checks every trace of up to N steps; with -graph, the
// Go evaluator explores the state graph within one search bound
// (simulate.search, -states and -depth), the possibles' too, with
// deadlocks, the actions never enabled, the shortest counterexamples and
// each eventual val judged as cycles. The section's possibles are
// searched by the evaluator, what it did not find sampled by quint.
func simulate(ctx context.Context, args []string) (int, error) {
	var mr machineReports
	log := &simLog{}
	simCapture = log
	defer func() { simCapture = nil }()
	request, err := simulationRequest(args, &mr, log)
	if err != nil {
		return mr.write(log.document(), os.Stdout, 2, err)
	}
	// Every binding reports, in the order the roster finishes them: a
	// failure, an error or an unfinished share does not stop the others.
	// The exit is 1 if one failed, else 2 if one has no verdict.
	worst, attempted := 0, 0
	var unpassed []string
	for rep, analysisErr := range model.SimulateMany(ctx, request.bindings, request.options) {
		// Each attempted binding owns its display capture and existing report/2.
		// No later result replaces an earlier binding or forms an aggregate verdict.
		log = &simLog{quiet: mr.json}
		if rep != nil {
			log.binding = rep.Binding
		} else if be, ok := errors.AsType[*model.BindingError](analysisErr); ok {
			log.binding = be.Binding
		}
		simCapture = log
		attempted++
		code := 2
		if rep != nil {
			log.result = rep.Doc()
			simPrint(rep.Text)
			code = rep.Code
		} else if analysisErr == nil {
			analysisErr = fmt.Errorf("simulate %s: analysis returned no report", log.binding)
		}
		doc := log.document()
		code, writeErr := mr.write(doc, os.Stdout, code, analysisErr)
		if mr.undelivered || len(request.bindings) == 1 {
			// A report that cannot be delivered stops the roster; one
			// binding's error is the command's, as it always was.
			if writeErr != nil || code != 0 {
				return code, writeErr
			}
		} else if writeErr != nil {
			fmt.Fprintf(os.Stderr, "gotogether: simulate %s: %v\n", log.binding, writeErr)
		}
		if code != 0 {
			label := doc.Verdict
			if doc.Unfinished != "" {
				label = "unfinished"
			}
			unpassed = append(unpassed, fmt.Sprintf("%s (%s)", log.binding, label))
			if worst != 1 {
				worst = code
			}
		}
	}
	if len(request.bindings) > 1 && worst != 0 {
		// The summary, not an error: an error would exit 2 over a failure's 1.
		fmt.Fprintf(os.Stderr, "gotogether: simulate: %d of %d bindings did not pass: %s\n", len(unpassed), attempted, strings.Join(unpassed, ", "))
	}
	return worst, nil
}

type simulationPlan struct {
	bindings []string
	options  model.SimulateOptions
}

func simulationRequest(args []string, mr *machineReports, log *simLog) (*simulationPlan, error) {
	fset := commandFlags("simulate")
	mr.flags(fset)
	var paths repeated
	fset.Var(&paths, "binding", "binding file: its spec, module, constants, defs, invariants and simulate section; repeat for sequential independent reports")
	steps := fset.Int("steps", 0, "steps per trace (default: the binding's simulate.steps, else 30)")
	samples := fset.Int("samples", 0, "traces per invariant (default: the binding's simulate.samples, else 1000)")
	seed := fset.String("seed", "1", "quint run --seed: fixed, so the verdicts repeat")
	consts := fset.String("const", "", "constants to set, NAME=expr,... (over the binding's and its simulate section's)")
	invs := fset.String("invariants", "", "comma-separated invariants instead of the binding's (classified as the binding says)")
	verify := fset.Int("verify", 0, "check each invariant in every trace of up to this many steps with quint verify (Apalache), instead of sampling with quint run")
	states := fset.Int("states", 0, fmt.Sprintf("states the Go evaluator's searches visit at most, the possibles' and -graph's (default: the binding's simulate.search.states, else %d)", model.DefaultSearchStates))
	memory := fset.Int64("memory", model.DefaultGraphMemoryBytes>>20, "with -graph: retained graph storage estimate budget in MiB; excludes compilation, temporary evaluation allocations and process heap/RSS")
	graph := fset.Bool("graph", false, "explore the model's state graph with the Go evaluator instead of sampling with quint: every invariant in every reachable state "+
		"(within -states and -depth), deadlocks, actions never enabled, the shortest counterexamples, and each eventual val as cycles (a fair one keeping it false forever)")
	depth := fset.Int("depth", 0, "the longest path from init the Go evaluator's searches explore, the possibles' and -graph's (default: the binding's simulate.search.depth, else none)")
	out := fset.String("out", "", "with -graph: write a Quint module of the binding's instance with a run replaying each counterexample (quint test --match 'gtg_' replays them)")
	unfair := fset.String("unfair", "", "with -graph: comma-separated actions the liveness search does not oblige (default: the binding's simulate.unfair)")
	stops := fset.String("stops", "", "with -graph: comma-separated actions the liveness search takes in no cycle (default: the binding's simulate.stops)")
	quint := fset.String("quint", "quint", "quint binary")
	timeout := fset.Duration("timeout", 0, "end the whole run within this: the bindings share it fairly (each the same first share, those that run out retried with what the others leave); "+
		"one that does not finish is reported unfinished, with no verdict (exit 2)")
	fset.Parse(args)
	var text io.Writer = os.Stdout
	if err := mr.resolve(&text); err != nil {
		return nil, err
	}
	log.quiet = text != os.Stdout
	if len(paths) == 1 {
		log.binding = paths[0]
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("simulate needs -binding b.yaml")
	}
	if fset.NArg() != 0 {
		return nil, fmt.Errorf("simulate: unexpected argument %q; repeat -binding for each model", fset.Arg(0))
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			return nil, fmt.Errorf("simulate: -binding needs a nonempty path")
		}
		canonical, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = resolved
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if seen[canonical] {
			return nil, fmt.Errorf("simulate: duplicate binding %q", path)
		}
		seen[canonical] = true
	}
	if len(paths) > 1 {
		if mr.json || len(mr.files) != 0 {
			return nil, fmt.Errorf("simulate: multiple bindings have independent reports; use -report-dir DIR instead of -json or -report FORMAT=FILE")
		}
		if *out != "" {
			return nil, fmt.Errorf("simulate: -out names one graph artifact; run each binding separately when exporting counterexamples")
		}
		// Diagnose an unusable report destination before starting expensive work.
		if dir, label := mr.directory(); dir != "" {
			if err := prepareSimulationReports(dir); err != nil {
				return nil, fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	memoryGiven := false
	fset.Visit(func(f *flag.Flag) { memoryGiven = memoryGiven || f.Name == "memory" })
	if memoryGiven && !*graph {
		return nil, fmt.Errorf("simulate: -memory requires -graph")
	}
	memoryBytes, err := graphMemoryBytes(*memory)
	if err != nil {
		return nil, fmt.Errorf("simulate: %w", err)
	}
	if !*graph {
		memoryBytes = 0
	}
	opt := model.SimulateOptions{Quint: *quint, Seed: *seed, Steps: *steps, Samples: *samples, Constants: splitTop(*consts),
		Verify: *verify, Graph: *graph, States: *states, Depth: *depth, MemoryBytes: memoryBytes, Out: *out, Progress: os.Stderr}
	if *timeout < 0 {
		return nil, fmt.Errorf("simulate: -timeout %s: want a positive duration", *timeout)
	}
	if *timeout > 0 {
		opt.Deadline = time.Now().Add(*timeout)
	}
	if *invs != "" {
		opt.Invariants = splitList(*invs)
	}
	if *unfair != "" {
		opt.Unfair = splitList(*unfair)
	}
	if *stops != "" {
		opt.Stops = splitList(*stops)
	}
	return &simulationPlan{bindings: append([]string(nil), paths...), options: opt}, nil
}

func prepareSimulationReports(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".gotogether-report-probe-*")
	if err != nil {
		return err
	}
	closeErr := file.Close()
	removeErr := os.Remove(file.Name())
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func splitList(s string) []string {
	var out []string
	for x := range strings.SplitSeq(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// splitTop splits NAME=expr,NAME=expr at the commas outside brackets:
// Set(1, 2) is one value.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}
