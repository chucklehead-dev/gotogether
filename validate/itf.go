package validate

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/itf"
)

// ITFOptions says what to check a model-level ITF trace against. Every field
// is optional when the trace was written by gotogether (its #meta names the
// spec, module, constants, defs and invariants); set them to validate a
// trace from elsewhere, e.g. `quint run --mbt`.
type ITFOptions struct {
	Options
	Binding    *binding.Binding // supplies spec, module, defs and invariants when set
	Spec       string           // the model (overrides meta and binding)
	Module     string           // the parameterised module
	Instance   string           // take the constants from this instance module of Spec (e.g. currentDesign)
	Invariants []string         // overrides
	// NoVarCheck turns off the comparison of model variables present in the
	// trace's states with the model's state after each step.
	NoVarCheck bool
}

// ValidateITF replays a model-level ITF trace in Quint: each state's
// mbt::actionTaken is called with its mbt::nondetPicks as arguments (picks
// are matched to parameters by name). After each step it checks
//   - gotogether::expect, the observed projection the binding rendered, and
//   - every model variable the state carries (equality), so a trace from
//     `quint run --mbt`, or one gotogether validated earlier, is checked state
//     by state;
//
// and it checks the invariants in every state.
func ValidateITF(ctx context.Context, tr *itf.Trace, opt ITFOptions) (*Result, error) {
	meta, spec, module, defs, invs, err := itfInstance(tr, opt)
	if err != nil {
		return nil, err
	}
	o, err := opt.Options.resolve()
	if err == nil && o.Dir != "" { // a directory of its own, as a check's (dirFor)
		o.Dir, err = checkDir(o.Dir)
	}
	if err != nil {
		return nil, err
	}
	m, err := binding.LoadModel(o.Quint, spec, module)
	if err != nil {
		return nil, err
	}
	isVar := map[string]bool{}
	for _, v := range m.Vars {
		isVar[v] = true
	}
	varCheck := func(st itf.State) []string {
		if opt.NoVarCheck {
			return nil
		}
		var names []string
		for k := range st.Vars {
			if isVar[k] {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		var out []string
		for _, k := range names {
			out = append(out, fmt.Sprintf("%s == %s", k, st.Vars[k].Quint()))
		}
		return out
	}
	if len(tr.States) == 0 {
		return nil, fmt.Errorf("empty trace")
	}
	// call is state idx's call: its action with its picks, its expect and
	// the variables it carries checked after it
	call := func(idx int, st itf.State) (binding.Call, error) {
		ps, ok := m.Actions[st.Action]
		if !ok {
			return binding.Call{}, fmt.Errorf("state %d: %q is not an action of %s", idx, st.Action, module)
		}
		c := binding.Call{Action: st.Action, Args: map[string]string{}, Note: fmt.Sprintf("state %d", idx)}
		if obs, ok := st.Meta["observed"].(string); ok {
			c.Note += ": " + obs
		}
		var vals []string
		for _, p := range ps {
			v, ok := st.Picks[p.Name]
			if !ok {
				return binding.Call{}, fmt.Errorf("state %d: %s has no nondet pick named %s (picks are matched to parameters by name)", idx, m.Signature(st.Action), p.Name)
			}
			c.Args[p.Name] = v.Quint()
			vals = append(vals, v.Quint())
		}
		c.Quint = st.Action
		if len(vals) > 0 {
			c.Quint += "(" + strings.Join(vals, ", ") + ")"
		}
		var checks []string
		if e, ok := st.Extra["gotogether::expect"]; ok && e.Kind == itf.Str {
			checks = append(checks, e.S)
		}
		checks = append(checks, varCheck(st)...)
		if len(checks) == 1 {
			c.Expect = checks[0]
		} else if len(checks) > 1 {
			c.Expect = "and { " + strings.Join(checks, ", ") + " }"
		}
		return c, nil
	}
	var calls []binding.Call
	for i, st := range tr.States[1:] {
		c, err := call(i+1, st)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	// state 0 is init's, or the action of a snapshot the trace starts from
	// (a trace of a log that lost its head: binding.Plan.Start)
	var start *binding.Call
	if a := tr.States[0].Action; a != "" && a != "init" {
		c, err := call(0, tr.States[0])
		if err != nil {
			return nil, err
		}
		start = &c
	}
	ms := replay.ModuleSpec{Spec: spec, Module: module, Defs: defs, Invariants: invs,
		Header: []string{fmt.Sprintf("replay of a model-level ITF trace, %d states", len(tr.States))}}
	if opt.Instance != "" {
		im, consts, err := binding.InstanceConstants(spec, opt.Instance)
		if err != nil {
			return nil, err
		}
		if im != module {
			return nil, fmt.Errorf("instance %s instantiates %s, not %s", opt.Instance, im, module)
		}
		ms.Constants = consts
		ms.Header = append(ms.Header, "constants of instance "+opt.Instance)
	} else {
		for _, k := range sortedKeys(meta.Constants) {
			ms.Constants = append(ms.Constants, fmt.Sprintf("%s = %s", k, meta.Constants[k]))
		}
	}
	if init := varCheck(tr.States[0]); len(init) > 0 && start == nil { // (a start's are its expect)
		ms.InitExpect = "and { " + strings.Join(init, ", ") + " }"
	}
	actor := meta.Actor
	if actor == "" {
		actor = "itf"
	}
	base := "replay_" + sanitize(actor)
	res := &Result{Actor: actor}
	return res, evaluate(ctx, ms, [][]binding.Call{calls}, []*binding.Call{start}, base, o, []*Result{res})[0]
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// itfInstance is the instance an ITF trace is replayed against: the
// spec, module, defs and invariants the binding gives, else the trace's
// own metadata, each overridden by opt; and the metadata, its constants.
func itfInstance(tr *itf.Trace, opt ITFOptions) (meta *binding.ITFMeta, spec, module, defs string, invs []string, err error) {
	meta, _ = binding.ReadITFMeta(tr.Meta)
	if meta == nil {
		meta = &binding.ITFMeta{}
	}
	spec, module, defs, invs = meta.Spec, meta.Module, meta.Defs, meta.Invariants
	if b := opt.Binding; b != nil {
		spec, module, defs, invs = b.SpecPath(), b.Module, b.Defs, b.Invariants
	}
	if opt.Spec != "" {
		spec = opt.Spec
	}
	if opt.Module != "" {
		module = opt.Module
	}
	if opt.Invariants != nil {
		invs = opt.Invariants
	}
	if spec == "" || module == "" {
		return nil, "", "", "", nil, fmt.Errorf("the trace does not say which model it is for: give a binding, or spec and module")
	}
	if !filepath.IsAbs(spec) {
		if abs, err := filepath.Abs(spec); err == nil {
			spec = abs
		}
	}
	if opt.Instance == "" && len(meta.Constants) == 0 && opt.Binding == nil {
		return nil, "", "", "", nil, fmt.Errorf("no constants for %s: validate against an instance module (Instance) or a gotogether-written trace", module)
	}
	return meta, spec, module, defs, invs, nil
}

// ITFModel is the instance ValidateITF replays tr against, for its
// input's manifest (Input.Model): the spec (the trace's own, the
// binding's, or opt's) hashed with its imports, the module, the
// invariants checked, and the constants (the trace's) or the instance
// module they come from.
func ITFModel(tr *itf.Trace, opt ITFOptions) (*RunModel, error) {
	meta, spec, module, _, invs, err := itfInstance(tr, opt)
	if err != nil {
		return nil, err
	}
	m := &RunModel{Module: module, Invariants: append([]string{}, invs...), Instance: opt.Instance}
	if opt.Instance == "" {
		m.Constants = maps.Clone(meta.Constants)
	}
	if b := opt.Binding; b != nil && b.Path != "" {
		if m.Binding, err = hashed(b.Path); err != nil {
			return nil, err
		}
	}
	if m.Spec, m.Imports, err = specFiles(spec); err != nil {
		return nil, err
	}
	return m, nil
}
