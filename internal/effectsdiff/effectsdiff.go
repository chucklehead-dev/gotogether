// Package effectsdiff is the report of a step whose effects are not the
// model's (I/O specifications, DESIGN §112 and §115): one shape for
// validate's recorded runs and connect's model-driven ones, which export
// it under their own names (validate.EffectsMismatch,
// connect.EffectsMismatch), as qenv.Rates is choice.Rates.
package effectsdiff

import (
	"fmt"
	"strings"
)

// Match set: the made and expected effects compare as sets (binding's
// MatchSet; this package imports no binding).
const matchSet = "set"

// A Mismatch is why a step failed when its effects are not the model's:
// the effects the step made and the ones its action's effects def says
// it must make, and where they first differ.
type Mismatch struct {
	// Call is the def applied to the step's arguments
	// (payEffects("o1")), Match the binding's mode. For an Undeclared
	// mismatch, Call is the def the action would take (commitEffects).
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
	// Undeclared: the step's action has no effects def (none in the
	// model, and none opted out of), and the step made effects of a
	// mapped kind: the model states none, so none is the specification.
	Undeclared bool `json:"undeclared,omitempty"`
	// Mapping is where the effects' fields come from, for the fix
	// ("effects.kinds in checkout.binding.yaml", "the Binding's Effects");
	// Drop how to compare none of the action's effects
	// (`effects: "-" for commit in checkout.binding.yaml`,
	// `EffectsOf("commit", "-", "")`). Neither is reported data.
	Mapping string `json:"-"`
	Drop    string `json:"-"`
}

// Action is the step's action, for an Undeclared mismatch: its Call
// without the Effects suffix of the def it would take.
func (x *Mismatch) Action() string {
	def, _, _ := strings.Cut(x.Call, "(")
	return strings.TrimSuffix(def, "Effects")
}

// Brief is x in a line: the def, and the first difference or why the
// effects differ.
func (x *Mismatch) Brief() string {
	if x.Undeclared {
		s := fmt.Sprintf("%s made effects, and the model states none (it has no %s)", x.Action(), x.Call)
		if x.Index > 0 {
			s += fmt.Sprintf(": effect %d: %s, expected nothing", x.Index, x.Got)
		}
		return s
	}
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

// Headline is the failure's first line: the effects are not the
// model's def (validate's "at <record> (<step>) " precedes it, connect's
// "trace i step k <step>: ").
func (x *Mismatch) Headline() string {
	if x.Undeclared {
		return fmt.Sprintf("%s made effects, and the model states none (it has no %s)", x.Action(), x.Call)
	}
	return "its effects are not the model's " + x.Call
}

// Render is x as a report's lines, each prefixed with pfx.
func (x *Mismatch) Render(pfx string) string {
	var b strings.Builder
	list := func(vs []string) string { return "[" + strings.Join(vs, ", ") + "]" }
	if x.Match == matchSet {
		list = func(vs []string) string { return "Set(" + strings.Join(vs, ", ") + ")" }
	}
	mapping := x.Mapping
	if mapping == "" {
		mapping = "effects.kinds in the binding"
	}
	if x.Undeclared {
		a := x.Action()
		fmt.Fprintf(&b, "%seffects:  %s made effects, and the model states none (it has no %s):\n", pfx, a, x.Call)
		fmt.Fprintf(&b, "%s  made     %s", pfx, list(x.Made))
		if len(x.MadeAt) > 0 {
			fmt.Fprintf(&b, "   (%s)", strings.Join(x.MadeAt, ", "))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s  expected nothing (an action whose effects the model does not state makes none of a mapped kind)\n", pfx)
		drop := x.Drop
		if drop == "" {
			drop = fmt.Sprintf(`effects: "-" for %s`, a)
		}
		fmt.Fprintf(&b, "%s  Fix the code if %s sends nothing, or state what it sends in the model (def %s(...): List[Effect]; [] says it sends nothing); %s compares none of its effects. The effects' fields come from %s.\n", pfx, a, x.Call, drop, mapping)
		return b.String()
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
	def, _, _ := strings.Cut(x.Call, "(")
	fmt.Fprintf(&b, "%s  Fix the code's effect, or %s if the model is wrong; the effects' fields come from %s.\n", pfx, def, mapping)
	return b.String()
}
