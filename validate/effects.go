package validate

import (
	"fmt"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/effectsdiff"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// An EffectsMismatch is why a step's expect failed when its effects are
// not the model's (I/O specifications, Failure.Effects): the effects the
// step made and the ones its action's effects def says it must make, and
// where they first differ. An addition to the verdict, which the expect's
// conjunct already decided (a state mismatch, claimed as Mismatch). It
// is connect's EffectsMismatch too: one report for both (DESIGN §115).
type EffectsMismatch = effectsdiff.Mismatch

// effects is the effects mismatch of plan's call i, failed after the
// step in state quintAfter (as quint reported it), or nil when the call
// compares no effects or its effects are the model's (the expect failed
// elsewhere).
func (c *Checker) effectsMismatch(get func() *evaluator, plan *binding.Plan, i int, quintAfter map[string]itf.Value) *EffectsMismatch {
	call := plan.Calls[i]
	ec := call.Effects
	if ec == nil {
		return nil
	}
	where := "the binding"
	if c.Binding.Path != "" {
		where = c.Binding.Path
	}
	x := &EffectsMismatch{Call: ec.Call, Match: ec.Match, Mapping: "effects.kinds in " + where}
	for _, e := range ec.From {
		at := fmt.Sprintf("%s at seq %d", e.Kind, e.Seq)
		x.MadeAt = append(x.MadeAt, at)
		if e.Late {
			x.Late = append(x.Late, at)
		}
	}
	x.Tries = ec.Tries
	e := get()
	if ec.Undeclared() {
		// the action states no effects, and the step made some of a
		// mapped kind (DESIGN §115's rule, validate's too): none is the
		// model's, whatever the evaluator would say
		x.Undeclared, x.Drop = true, fmt.Sprintf(`effects: "-" for %s in %s`, call.Action, where)
		for _, m := range ec.Made {
			if e.err == nil {
				if v, err := e.literal(m); err == nil {
					m = qeval.String(v)
				}
			}
			x.Made = append(x.Made, m)
		}
		x.Expected = []string{}
		x.Index, x.Field, x.Got, x.Want = binding.FirstDifference(x.Made, nil)
		return x
	}
	if e.err != nil {
		x.Made, x.Unexplained = ec.Made, "the Go evaluator has no evaluation module for this binding ("+firstLine(e.err.Error())+")"
		return x
	}
	// the effects made as the evaluator prints values, so that they
	// compare with the def's text for text
	for _, m := range ec.Made {
		v, err := e.literal(m)
		if err != nil {
			x.Made = append(x.Made, m)
			continue
		}
		x.Made = append(x.Made, qeval.String(v))
	}
	name := e.em.Effects[call.Action]
	if name == "" {
		x.Unexplained = "the evaluation module has no definition of " + ec.Def
		return x
	}
	consts, s, why := e.before(c, plan, i)
	if why != "" {
		x.Unexplained = why
		return x
	}
	as, err := e.callArgs(c, call)
	if err != nil {
		x.Unexplained = err.Error()
		return x
	}
	succ, err := e.m.Successors(consts, s, e.em.Actions[call.Action], as...)
	if err != nil || len(succ) == 0 {
		x.Unexplained = "the Go evaluator cannot take the step"
		return x
	}
	after := succ[0]
	if len(quintAfter) > 0 {
		if q, err := qeval.StateOf(e.m, itf.State{Vars: quintAfter}); err == nil {
			k := e.m.Key(q)
			for _, n := range succ {
				if e.m.Key(n) == k {
					after = n
					break
				}
			}
		}
	}
	var dargs []qeval.Value
	for _, p := range c.Model.Defs[ec.Def] {
		v, err := e.literal(call.Args[p])
		if err != nil {
			x.Unexplained = err.Error()
			return x
		}
		dargs = append(dargs, v)
	}
	v, err := e.m.Eval(consts, after, name, dargs...)
	if err != nil {
		x.Unexplained = err.Error()
		return x
	}
	switch l := v.(type) {
	case qeval.List:
		for _, el := range l {
			x.Expected = append(x.Expected, qeval.String(el))
		}
	case *qeval.Set:
		for _, el := range l.Elems {
			x.Expected = append(x.Expected, qeval.String(el))
		}
	default:
		x.Unexplained = fmt.Sprintf("%s is %s, no list of effects", ec.Call, qeval.String(v))
		return x
	}
	x.Index, x.Field, x.Got, x.Want = binding.FirstDifference(x.Made, x.Expected)
	if x.Index == 0 && len(x.Tries) == 0 && len(x.Late) == 0 {
		return nil // the effects are the model's: the expect failed elsewhere
	}
	return x
}
