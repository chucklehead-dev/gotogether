package gristtest_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// NoClientErrors holds of a run whose clients' calls were each answered
// at their first try a server took, and misses one with a call refused
// a try (the step's refused field, an int in process or an int64 read
// back), erring or left pending; qtest.Not(NoClientErrors()) its negation. A
// report with no client actor holds neither: it has no verdict for them
// (qtest.NoVerdict, whose claims qtest's TestSatisfies checks).
func TestNoClientErrors(t *testing.T) {
	call := func(args map[string]any, err string) binding.Call {
		return binding.Call{Action: "createCounter", Step: &qtrace.Step{Action: "createCounter", Args: args, Error: err}}
	}
	clients := func(cs []binding.Call, pending ...qtrace.Step) *validate.Report {
		return &validate.Report{Actors: []*validate.Result{{Actor: "t1/clients", Conforms: validate.Pass, Calls: cs, Pending: pending}}}
	}
	ok := clients([]binding.Call{call(map[string]any{"id": "c1"}, ""), call(map[string]any{"id": "c2"}, "")})
	for name, c := range map[string]struct {
		r          *validate.Report
		no, errors bool
	}{
		"answered":           {ok, true, false},
		"refused in process": {clients([]binding.Call{call(map[string]any{"id": "c1", "refused": 2}, "")}), false, true},
		"refused, read back": {clients([]binding.Call{call(map[string]any{"id": "c1", "refused": int64(1)}, "")}), false, true},
		"an error":           {clients([]binding.Call{call(nil, "boom")}), false, true},
		"pending":            {clients(nil, qtrace.Step{Action: "createCounter", Invocation: true}), false, true},
		"no client actor":    {&validate.Report{Actors: []*validate.Result{{Actor: "t1", Conforms: validate.Pass}}}, false, false},
	} {
		run := &qtest.Run{Report: c.r}
		if got := gristtest.NoClientErrors().Holds(run); got != c.no {
			t.Errorf("%s: NoClientErrors %v, want %v", name, got, c.no)
		}
		if got := qtest.Not(gristtest.NoClientErrors()).Holds(run); got != c.errors {
			t.Errorf("%s: Not(NoClientErrors) %v, want %v", name, got, c.errors)
		}
	}
}
