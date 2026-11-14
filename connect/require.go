package connect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
)

// reached is how often one of Config.Require's vals held in a run's
// traces: in how many traces (at any state) and in how many states.
type reached struct {
	Name    string
	Traces  int // traces with at least one state where it held
	States  int // states where it held, over every trace
	Witness int // the steps of its witness trace, replayed; -1 for none
}

// required evaluates Config.Require's vals in the states of a run's
// traces with the Go evaluator, on the model quint compiled from c.Spec
// (its c.Main, c.Init and c.StepName), and counts where each held.
type required struct {
	m       *qeval.Model
	reached []reached
	states  int // states seen, over every trace
}

// compileRequired compiles c's model for the vals of c.Require, or
// returns nil when it names none. A name that is no definition of the
// model is an error here, before any trace runs.
func compileRequired(ctx context.Context, c Config) (*required, error) {
	if len(c.Require) == 0 {
		return nil, nil
	}
	spec, err := filepath.Abs(c.Spec)
	if err != nil {
		return nil, err
	}
	out, err := os.CreateTemp(c.Dir, "require-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(out.Name())
	args := []string{"compile", "--target", "json", "--verbosity", "0"}
	if c.Main != "" {
		args = append(args, "--main", c.Main)
	}
	if c.Init != "" {
		args = append(args, "--init", c.Init)
	}
	if c.StepName != "" {
		args = append(args, "--step", c.StepName)
	}
	cmd := quintcmd.Command(ctx, c.Quint, filepath.Dir(spec), append(args, spec)...)
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	err = cmd.Run()
	out.Close()
	if err != nil {
		return nil, &QuintError{Args: args, Output: stderr.String(), Err: err}
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		return nil, err
	}
	m, err := qeval.Load(data)
	if err != nil {
		return nil, fmt.Errorf("Config.Require needs the Go evaluator, which cannot read %s: %w", c.Spec, err)
	}
	return newRequired(m, c.Spec, c.Require)
}

// newRequired counts names in m's states, refusing a name that is no
// definition of spec's model.
func newRequired(m *qeval.Model, spec string, names []string) (*required, error) {
	r := &required{m: m}
	for _, name := range names {
		d, ok := m.Decl(name)
		if !ok || d.Kind != "def" {
			return nil, fmt.Errorf("Config.Require names %s, which is no val of %s: name a boolean val of the model", name, spec)
		}
		r.reached = append(r.reached, reached{Name: name, Witness: -1})
	}
	return r, nil
}

// trace counts the states of tr where each required val holds.
func (r *required) trace(tr *itf.Trace) error {
	held := make([]bool, len(r.reached))
	for i, st := range tr.States {
		s, err := qeval.StateOf(r.m, st)
		if err != nil {
			return fmt.Errorf("Config.Require: state %d: %w", i, err)
		}
		r.states++
		for j := range r.reached {
			v, err := r.m.Eval(nil, s, r.reached[j].Name)
			if err != nil {
				return fmt.Errorf("Config.Require: evaluating %s in state %d: %w", r.reached[j].Name, i, err)
			}
			b, ok := v.(qeval.Bool)
			if !ok {
				return fmt.Errorf("Config.Require names %s, which is %s in state %d, not a boolean: name a boolean val of the model", r.reached[j].Name, qeval.String(v), i)
			}
			if b {
				r.reached[j].States++
				held[j] = true
			}
		}
	}
	for j, h := range held {
		if h {
			r.reached[j].Traces++
		}
	}
	return nil
}

// witnessed records that name's witness trace, of steps steps, ran.
func (r *required) witnessed(name string, steps int) {
	for i := range r.reached {
		if r.reached[i].Name == name {
			r.reached[i].Witness = steps
		}
	}
}

// report is a line per required val, and the names of those neither the
// samples nor a witness trace reached.
func (r *required) report(traces int) (lines []string, missed []string) {
	for _, x := range r.reached {
		line := fmt.Sprintf("required %s: reached in %d of %d traces (%d of %d states)", x.Name, x.Traces, traces, x.States, r.states)
		if x.Witness >= 0 {
			line += fmt.Sprintf(", and by a witness trace of %d steps", x.Witness)
		}
		lines = append(lines, line)
		if x.Traces == 0 && x.Witness < 0 {
			missed = append(missed, x.Name)
		}
	}
	return lines, missed
}

// A witness is the shortest trace the Go evaluator found to a state where
// a required val holds, replayed before the sampled traces: a state the
// samples may never reach, or reach once in thousands, is then always
// tested (ROADMAP N1's steering).
type witness struct {
	name  string
	trace *itf.Trace
}

// witnessStates bounds each search's states: a model whose required state
// lies past it is left to the samples (the report says so).
const witnessStates = 20000

// witnesses searches from c's initial states, by c's step, within
// c.MaxSteps (quint's default, 20, when unset), for the shortest path to
// a state where each required val holds, and returns each found as a
// trace in quint's --mbt form, with a note for each not found. A model
// the evaluator cannot run gets notes and no witnesses: the samples
// still count.
func (r *required) witnesses(c Config) (out []witness, notes []string) {
	init, step, depth := c.Init, c.StepName, c.MaxSteps
	if init == "" {
		init = "init"
	}
	if step == "" {
		step = "step"
	}
	if depth == 0 {
		depth = 20
	}
	starts, err := r.m.Successors(nil, nil, init)
	if err != nil {
		return nil, []string{fmt.Sprintf("no witness traces: the Go evaluator cannot run %s: %v", init, err)}
	}
	goals := make([]qeval.Goal, len(r.reached))
	for i, x := range r.reached {
		goals[i] = qeval.Goal{Holds: func(s qeval.State) (bool, error) {
			v, err := r.m.Eval(nil, s, x.Name)
			b, _ := v.(qeval.Bool)
			return bool(b), err
		}}
	}
	type best struct {
		start qeval.State
		path  []qeval.Transition
		found bool
		cut   string
	}
	bests := make([]best, len(goals))
	for _, st := range starts {
		rs, err := r.m.Reach(nil, st, step, goals, qeval.Bound{States: witnessStates, Depth: depth})
		if err != nil {
			return nil, []string{fmt.Sprintf("no witness traces: the Go evaluator cannot run %s: %v", step, err)}
		}
		for i, x := range rs {
			switch {
			case x.Found && (!bests[i].found || len(x.Path) < len(bests[i].path)):
				bests[i] = best{start: st, path: x.Path, found: true}
			case !x.Found && !bests[i].found && x.Cut != "":
				bests[i].cut = x.Cut
			}
		}
	}
	for i, b := range bests {
		name := r.reached[i].Name
		if !b.found {
			why := fmt.Sprintf("unreachable within %d steps", depth)
			if depth == 1 {
				why = "unreachable in one step"
			}
			if b.cut == "states" {
				why = fmt.Sprintf("not found in the first %d states", witnessStates)
			}
			notes = append(notes, fmt.Sprintf("required %s: no witness trace (%s)", name, why))
			continue
		}
		tr, err := r.traceOf(step, b.start, b.path)
		if err != nil {
			notes = append(notes, fmt.Sprintf("required %s: no witness trace: %v", name, err))
			continue
		}
		out = append(out, witness{name: name, trace: tr})
	}
	return out, notes
}

// traceOf is a path of the model as quint run --mbt writes a trace: each
// state's variables, the action taken (the named
// action step applied) and its nondeterministic picks.
func (r *required) traceOf(step string, start qeval.State, path []qeval.Transition) (*itf.Trace, error) {
	tr := &itf.Trace{Vars: []string{"mbt::actionTaken", "mbt::nondetPicks"}}
	for _, v := range r.m.Vars {
		tr.Vars = append(tr.Vars, v[strings.LastIndex(v, ":")+1:])
	}
	add := func(s qeval.State, action string, picks []qeval.Event) error {
		st := itf.State{Index: len(tr.States), Vars: map[string]itf.Value{}, Action: action, Picks: map[string]itf.Value{}, Extra: map[string]itf.Value{}}
		for _, v := range r.m.Vars {
			x, err := qeval.ToITF(s[v])
			if err != nil {
				return err
			}
			st.Vars[v[strings.LastIndex(v, ":")+1:]] = x
		}
		for _, p := range picks {
			x, err := qeval.ToITF(p.Value)
			if err != nil {
				return err
			}
			st.Picks[p.Pick] = x
		}
		tr.States = append(tr.States, st)
		return nil
	}
	if err := add(start, "init", nil); err != nil {
		return nil, err
	}
	for _, t := range path {
		// quint names the action step applied, not the actions that one
		// calls: outer, for a step that applies outer, which applies inner
		acts := t.Actions()
		if len(acts) > 0 && acts[0].Action == r.m.Display(step) {
			acts = acts[1:]
		}
		if len(acts) == 0 {
			return nil, errors.New("a transition the evaluator found applied no named action: name every action step can take")
		}
		if err := add(t.State, acts[0].Action, t.Picks()); err != nil {
			return nil, err
		}
	}
	return tr, nil
}
