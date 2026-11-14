package connect

import (
	"context"
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
	Name   string
	Traces int // traces with at least one state where it held
	States int // states where it held, over every trace
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
	r := &required{m: m}
	for _, name := range c.Require {
		d, ok := m.Decl(name)
		if !ok || d.Kind != "def" {
			return nil, fmt.Errorf("Config.Require names %s, which is no val of %s: name a boolean val of the model", name, c.Spec)
		}
		r.reached = append(r.reached, reached{Name: name})
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

// report is a line per required val, and the names of those never reached.
func (r *required) report(traces int) (lines []string, missed []string) {
	for _, x := range r.reached {
		lines = append(lines, fmt.Sprintf("required %s: reached in %d of %d traces (%d of %d states)", x.Name, x.Traces, traces, x.States, r.states))
		if x.Traces == 0 {
			missed = append(missed, x.Name)
		}
	}
	return lines, missed
}
