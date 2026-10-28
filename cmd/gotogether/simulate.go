package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate/model"
)

// simulate runs the binding's model on its own: model.Simulate (the
// one engine for the model alone, qtest.Model's too) with the flags'
// options, its report printed (and, with -json and the other machine
// reports, read from its lines). By default quint samples; with -verify
// N, quint verify checks every trace of up to N steps; with -graph, the
// Go evaluator explores the state graph within one search bound
// (simulate.search, -states and -depth), the possibles' too, with
// deadlocks, the actions never enabled, the shortest counterexamples and
// each eventual val judged as cycles. The section's possibles are
// searched by the evaluator, what it did not find sampled by quint.
func simulate(args []string) (int, error) {
	var mr machineReports
	log := &simLog{}
	simCapture = log
	defer func() { simCapture = nil }()
	code, err := simulateCmd(args, &mr, log)
	return mr.write(log.document(), os.Stdout, code, err)
}

// simulateCmd is the simulate command, its machine-readable reports'
// flags in mr and its lines in log.
func simulateCmd(args []string, mr *machineReports, log *simLog) (int, error) {
	fset := commandFlags("simulate")
	mr.flags(fset)
	bpath := fset.String("binding", "", "binding file: its spec, module, constants, defs, invariants and simulate section")
	steps := fset.Int("steps", 0, "steps per trace (default: the binding's simulate.steps, else 30)")
	samples := fset.Int("samples", 0, "traces per invariant (default: the binding's simulate.samples, else 1000)")
	seed := fset.String("seed", "1", "quint run --seed: fixed, so the verdicts repeat")
	consts := fset.String("const", "", "constants to set, NAME=expr,... (over the binding's and its simulate section's)")
	invs := fset.String("invariants", "", "comma-separated invariants instead of the binding's (classified as the binding says)")
	verify := fset.Int("verify", 0, "check each invariant in every trace of up to this many steps with quint verify (Apalache), instead of sampling with quint run")
	states := fset.Int("states", 0, fmt.Sprintf("states the Go evaluator's searches visit at most, the possibles' and -graph's (default: the binding's simulate.search.states, else %d)", model.DefaultSearchStates))
	graph := fset.Bool("graph", false, "explore the model's state graph with the Go evaluator instead of sampling with quint: every invariant in every reachable state "+
		"(within -states and -depth), deadlocks, actions never enabled, the shortest counterexamples, and each eventual val as cycles (a fair one keeping it false forever)")
	depth := fset.Int("depth", 0, "the longest path from init the Go evaluator's searches explore, the possibles' and -graph's (default: the binding's simulate.search.depth, else none)")
	out := fset.String("out", "", "with -graph: write a Quint module of the binding's instance with a run replaying each counterexample (quint test --match 'gtg_' replays them)")
	unfair := fset.String("unfair", "", "with -graph: comma-separated actions the liveness search does not oblige (default: the binding's simulate.unfair)")
	stops := fset.String("stops", "", "with -graph: comma-separated actions the liveness search takes in no cycle (default: the binding's simulate.stops)")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	var text io.Writer = os.Stdout
	if err := mr.resolve(&text); err != nil {
		return 2, err
	}
	log.quiet, log.binding = text != os.Stdout, *bpath
	if *bpath == "" {
		return 2, fmt.Errorf("simulate needs -binding b.yaml")
	}
	opt := model.SimulateOptions{Quint: *quint, Seed: *seed, Steps: *steps, Samples: *samples, Constants: splitTop(*consts),
		Verify: *verify, Graph: *graph, States: *states, Depth: *depth, Out: *out}
	if *invs != "" {
		opt.Invariants = splitList(*invs)
	}
	if *unfair != "" {
		opt.Unfair = splitList(*unfair)
	}
	if *stops != "" {
		opt.Stops = splitList(*stops)
	}
	rep, err := model.Simulate(*bpath, opt)
	if err != nil {
		return 2, err
	}
	simPrint(rep.Text)
	return rep.Code, nil
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
