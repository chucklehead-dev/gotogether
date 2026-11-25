package validate

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// An EffectsMismatch is why a step's expect failed when its effects are
// not the model's (I/O specifications, Failure.Effects): the effects the
// step made and the ones its action's effects def says it must make, and
// where they first differ. An addition to the verdict, which the expect's
// conjunct already decided (a state mismatch, claimed as Mismatch).
type EffectsMismatch struct {
	// Call is the def applied to the step's arguments
	// (payEffects("o1")), Match the binding's mode.
	Call  string `json:"call"`
	Match string `json:"match"`
	// Made are the step's effects as the model's values (normalized by
	// Match), MadeAt where each was recorded ("rpc.provider.request at
	// seq 29"); Expected the def's value in the state after the step,
	// when the Go evaluator could compute it.
	Made     []string `json:"made"`
	MadeAt   []string `json:"madeAt,omitempty"`
	Expected []string `json:"expected,omitempty"`
	// Index is the first effect that differs (1-based), Field its field
	// when both are one constructor's records, Got and Want its values.
	Index int    `json:"index,omitempty"`
	Field string `json:"field,omitempty"`
	Got   string `json:"got,omitempty"`
	Want  string `json:"want,omitempty"`
	// Tries are the retries under another key (match retried's Same).
	Tries []string `json:"tries,omitempty"`
	// Late are the effects made under the step's call after it completed.
	Late []string `json:"late,omitempty"`
	// Unexplained says why Expected is unknown.
	Unexplained string `json:"unexplained,omitempty"`
	// binding is the binding's file, for the fix
	binding string
}

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
	x := &EffectsMismatch{Call: ec.Call, Match: ec.Match, binding: c.Binding.Path}
	for _, e := range ec.From {
		at := fmt.Sprintf("%s at seq %d", e.Kind, e.Seq)
		x.MadeAt = append(x.MadeAt, at)
		if e.Late {
			x.Late = append(x.Late, at)
		}
	}
	x.Tries = ec.Tries
	e := get()
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

// Brief is x in a line: the def, and the first difference or why the
// effects differ.
func (x *EffectsMismatch) Brief() string {
	s := "its effects are not the model's " + x.Call
	switch {
	case x.Index > 0 && x.Field != "":
		s += fmt.Sprintf(": effect %d, field %s: %s, expected %s", x.Index, x.Field, x.Got, x.Want)
	case x.Index > 0:
		s += fmt.Sprintf(": effect %d: %s, expected %s", x.Index, x.Got, x.Want)
	case x.Unexplained != "":
		s += " (made " + strings.Join(x.Made, ", ") + ")"
	}
	if len(x.Tries) > 0 {
		s += "; " + x.Tries[0]
	}
	return s
}

// differs reports whether x says the effects are not the model's (or
// cannot say they are).
func (x *EffectsMismatch) differs() bool { return x != nil }

// render is x as a report's lines, each prefixed with pfx.
func (x *EffectsMismatch) render(pfx string) string {
	var b strings.Builder
	list := func(vs []string) string { return "[" + strings.Join(vs, ", ") + "]" }
	if x.Match == binding.MatchSet {
		list = func(vs []string) string { return "Set(" + strings.Join(vs, ", ") + ")" }
	}
	fmt.Fprintf(&b, "%seffects:  not the model's %s (match %s):\n", pfx, x.Call, x.Match)
	fmt.Fprintf(&b, "%s  made     %s", pfx, list(x.Made))
	if len(x.MadeAt) > 0 {
		fmt.Fprintf(&b, "   (%s)", strings.Join(x.MadeAt, ", "))
	}
	b.WriteString("\n")
	if x.Expected != nil || x.Unexplained == "" {
		fmt.Fprintf(&b, "%s  expected %s\n", pfx, list(x.Expected))
	} else {
		fmt.Fprintf(&b, "%s  expected: not computed (%s)\n", pfx, x.Unexplained)
	}
	switch {
	case x.Index > 0 && x.Field != "":
		fmt.Fprintf(&b, "%s  first difference: effect %d, field %s: %s, expected %s\n", pfx, x.Index, x.Field, x.Got, x.Want)
	case x.Index > 0:
		fmt.Fprintf(&b, "%s  first difference: effect %d: %s, expected %s\n", pfx, x.Index, x.Got, x.Want)
	}
	for _, t := range x.Tries {
		fmt.Fprintf(&b, "%s  %s: a retry must send what the first try sent; a fresh key per try lets the receiver apply both\n", pfx, t)
	}
	for _, l := range x.Late {
		fmt.Fprintf(&b, "%s  %s was made after the step completed (a goroutine it started): if the design sends later, state it as an owed effect and judge it at its site\n", pfx, l)
	}
	where := "the binding"
	if x.binding != "" {
		where = x.binding
	}
	def, _, _ := strings.Cut(x.Call, "(")
	fmt.Fprintf(&b, "%s  Fix the code's effect, or %s if the model is wrong; the effects' fields come from effects.kinds in %s.\n", pfx, def, where)
	return b.String()
}
