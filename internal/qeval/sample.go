package qeval

import (
	"fmt"
	"math/rand/v2"
)

// Random simulation (ROADMAP Q1): `quint run`'s semantics in Go, one
// successor of an action drawn at random instead of every one. quint's
// generator draws each nondet's value and each any {}'s branch once and
// ends a trace where its draw disables the step; the sampler draws the
// same choices, each uniformly, but backtracks: a value or a branch that
// disables the action is put aside and another drawn, until one enables
// it or none is left. So a sample is always one of Successors (sound), and
// a step is disabled only when no choice enables it (complete): a trace
// ends at a deadlock of the model, never at an unlucky draw. Seeds cannot
// match quint's generator; parity is by meaning (sample_test.go): every
// trace the sampler makes replays in quint, every trace quint makes is a
// path of Successors, and on small models both reach the state graph's
// every state and no other.

// Sample is one transition of action name, applied to args, from s, its
// choices drawn by r: its state and its labels (Transitions'), and
// whether the action is enabled (false: no choice of it has a successor).
// An error is the evaluator's (Unsupported, EvalError), as Successors'.
func (m *Model) Sample(consts map[string]Value, s State, name string, r *rand.Rand, args ...Value) (t Transition, enabled bool, err error) {
	defer m.finishContext(&err)
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return Transition{}, false, fmt.Errorf("qeval: no action %s", name)
	}
	ev := m.evaluation(consts, s)
	ev.labels, ev.rng = true, r
	body, e, err := ev.bindArgs(d, args, nil, name)
	if err != nil {
		return Transition{}, false, err
	}
	succs, err := ev.action(body, e)
	if err != nil || len(succs) == 0 {
		return Transition{}, false, err
	}
	sc := succs[0]
	st := State{}
	for _, v := range m.Vars {
		x, ok := sc.a[v]
		if !ok {
			return Transition{}, false, unsupported(name, "leaves %s unassigned", v)
		}
		st[v] = x
	}
	evs := sc.lab.slice()
	for i := range evs {
		if evs[i].Action != "" {
			evs[i].Action = m.Display(evs[i].Action)
		}
	}
	return Transition{Events: evs, State: st, taken: sc.taken, takenKnown: true}, true, nil
}

// sampled is the first of n alternatives, drawn in a random order, whose
// successors (try's) are some: none when every alternative's are none.
// The order is drawn lazily: the first alternative costs one draw, and
// only a disabled one makes the permutation of the others.
func (ev *evaluation) sampled(n int, try func(i int) ([]succ, error)) ([]succ, error) {
	if n == 0 {
		return nil, nil
	}
	first := ev.rng.IntN(n)
	out, err := try(first)
	if err != nil || len(out) > 0 {
		return out, err
	}
	rest := make([]int, 0, n-1)
	for i := range n {
		if i != first {
			rest = append(rest, i)
		}
	}
	ev.rng.Shuffle(len(rest), func(a, b int) { rest[a], rest[b] = rest[b], rest[a] })
	for _, i := range rest {
		ev.checkpoint()
		out, err := try(i)
		if err != nil || len(out) > 0 {
			return out, err
		}
	}
	return nil, nil
}

// A SimOptions is a random simulation's (Simulate): quint run's --init,
// --step, --max-steps and --invariant.
type SimOptions struct {
	// Init and Step are the actions (default init and step).
	Init, Step string
	// Steps is the most steps a trace takes (quint's --max-steps): it has
	// up to Steps+1 states, fewer where the step is disabled (a deadlock
	// of the model ends the trace, as quint ends a sample).
	Steps int
	// Invariants are vals each state is judged by: the first false ends
	// the trace, its name the trace's Violated.
	Invariants []string
}

// A SimTrace is one trace of a simulation: its initial transition (from
// no state, with init's labels), then each step's, in order.
type SimTrace struct {
	Init  Transition
	Steps []Transition
	// Violated is the invariant false in the trace's last state, if any.
	Violated string
	// Deadlock: the step was disabled in the last state before Steps
	// were taken.
	Deadlock bool
}

// States are the trace's states, the initial one first.
func (t *SimTrace) States() []State {
	out := []State{t.Init.State}
	for _, s := range t.Steps {
		out = append(out, s.State)
	}
	return out
}

// Simulate is one random trace of the model (quint run's, one sample):
// init sampled from no state, then step, up to opt.Steps times, each
// state judged by opt.Invariants. A disabled init is an error, as in
// quint; a disabled step ends the trace (Deadlock).
func (m *Model) Simulate(consts map[string]Value, opt SimOptions, r *rand.Rand) (*SimTrace, error) {
	init, step := opt.Init, opt.Step
	if init == "" {
		init = "init"
	}
	if step == "" {
		step = "step"
	}
	t, ok, err := m.Sample(consts, nil, init, r)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("qeval: %s is disabled: the model has no initial state", m.Display(init))
	}
	tr := &SimTrace{Init: t}
	judge := func(s State) error {
		for _, inv := range opt.Invariants {
			v, err := m.Eval(consts, s, inv)
			if err != nil {
				return err
			}
			b, isBool := v.(Bool)
			if !isBool {
				return fmt.Errorf("qeval: invariant %s is %s, not a Boolean", inv, String(v))
			}
			if !b {
				tr.Violated = inv
				return nil
			}
		}
		return nil
	}
	if err := judge(t.State); err != nil || tr.Violated != "" {
		return tr, err
	}
	s := t.State
	for range opt.Steps {
		t, ok, err := m.Sample(consts, s, step, r)
		if err != nil {
			return tr, err
		}
		if !ok {
			tr.Deadlock = true
			return tr, nil
		}
		tr.Steps = append(tr.Steps, t)
		if err := judge(t.State); err != nil || tr.Violated != "" {
			return tr, err
		}
		s = t.State
	}
	return tr, nil
}

// ActionTaken is the action quint's --mbt names for transition t of
// action step from state from (mbt::actionTaken): the branch the last
// any {} on its path took (step = any { deposit, withdraw } takes
// deposit; a branch nested = any { inc(2), keep } its own inc or keep),
// its action's name or "" for an anonymous branch, as quint writes it,
// and step's own name where no any {} chose. A transition of a graph's
// path, which does not carry it, has it computed again from from
// (Transitions, matched by its labels and state).
func (m *Model) ActionTaken(consts map[string]Value, from State, step string, t Transition) (string, error) {
	if !t.takenKnown {
		ts, err := m.Transitions(consts, from, step)
		if err != nil {
			return "", err
		}
		want := string(appendEvents(nil, t.Events)) + m.Key(t.State)
		found := false
		for _, x := range ts {
			if string(appendEvents(nil, x.Events))+x.key == want {
				t, found = x, true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("qeval: the transition is none of %s's from its state", m.Display(step))
		}
	}
	if t.taken == nil {
		return m.Display(step), nil
	}
	return *t.taken, nil
}
