package qtest

import (
	"fmt"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// verbose reports whether GOTOGETHER_VERBOSE=1 asks for the whole
// reports where a passing test logs a short one, and the counts behind a
// summary's words.
func verbose() bool { return os.Getenv("GOTOGETHER_VERBOSE") == "1" }

// Brief is the run's first failure in one line, in the words a newcomer
// reads first: the step and why the model refuses it, the invariant and
// where it broke, or the run's reason ("" for a run that conforms). A
// test that catches what it expects logs it (an expected failure's
// example), where the whole report is GOTOGETHER_VERBOSE's: a newcomer
// never saw what a catch looks like in hello's output.
func (r *Run) Brief() string {
	if r == nil || r.Report == nil || r.Report.OK() {
		return ""
	}
	return briefOf(r.Report)
}

// briefOf is Brief of a report.
func briefOf(rep *validate.Report) string {
	for _, a := range rep.Actors {
		if a.OK() {
			continue
		}
		who := ""
		if len(rep.Actors) > 1 {
			who = "actor " + a.Actor + ": "
		}
		if f := a.Failure; f != nil && a.Conforms == validate.Fail {
			return who + failureBrief(f)
		}
		for _, inv := range a.Invariants {
			if inv.Verdict != validate.Fail {
				continue
			}
			if f := inv.Failure; f != nil && f.Index >= 0 {
				step := ""
				if f.Step != nil {
					step = ", " + f.Step.String()
				}
				return fmt.Sprintf("%sinvariant %s violated after step %d%s", who, inv.Name, f.Index+1, step)
			}
			return fmt.Sprintf("%sinvariant %s violated", who, inv.Name)
		}
		return who + a.Reason()
	}
	if rep.Unsettled != "" {
		return "the system did not settle: " + firstLineOf(rep.Unsettled)
	}
	return rep.Reason()
}

// failureBrief is a conformance failure in one line.
func failureBrief(f *validate.Failure) string {
	step := f.Call
	if f.Step != nil {
		step = f.Step.String()
	}
	switch f.Kind {
	case "disabled":
		return fmt.Sprintf("step %d, %s: %s", f.Index+1, step, whyBrief(f.Why))
	case "state":
		exp := ""
		if f.Expect != "" {
			exp = " (" + f.Expect + ")"
		}
		return fmt.Sprintf("after step %d, %s: the model's state does not match what was observed%s", f.Index+1, step, exp)
	case "error":
		return fmt.Sprintf("step %d, %s: quint could not evaluate it: %s", f.Index+1, step, firstLineOf(f.Quint))
	}
	return fmt.Sprintf("step %d, %s: %s", f.Index+1, step, f.Kind)
}

// whyBrief is why the model refuses a step, by its most specific false
// condition, with its values.
func whyBrief(x *validate.Explanation) string {
	switch {
	case x == nil || x.Unexplained != "":
		return "the model cannot take it from the state reached"
	case x.Reading != "" && len(x.Conditions) == 0:
		return fmt.Sprintf("the model's %s is disabled: %s", x.Action, x.Reading)
	case len(x.Conditions) == 0 && len(x.Branches) > 0:
		return fmt.Sprintf("the model's %s is disabled: none of its %d branches is enabled", x.Action, len(x.Branches))
	case len(x.Conditions) == 0:
		return fmt.Sprintf("the model's %s is disabled", x.Action)
	}
	c := x.Conditions[len(x.Conditions)-1]
	s := fmt.Sprintf("the model's %s is disabled: in %s: %s is false", x.Action, c.In, c.Expr)
	if len(c.Values) > 0 {
		vs := make([]string, len(c.Values))
		for i, v := range c.Values {
			vs[i] = v.Name + " = " + v.Value
		}
		s += " (where " + strings.Join(vs, ", ") + ")"
	}
	return s
}

func firstLineOf(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
