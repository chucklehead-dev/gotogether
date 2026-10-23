package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The messages for the traps of explorations: every run failing at its
// first step names the constants, and faults a script never made are
// listed.
func TestTrapMessages(t *testing.T) {
	failAt := func(i int) *validate.Report {
		return &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail, Failure: &validate.Failure{Index: i}}}}
	}
	x := &Exploration{constants: map[string]string{"STOCK": "2"}, Runs: []*Run{{Report: failAt(0)}, {Report: failAt(0)}}}
	if m := x.atStart(); !strings.Contains(m, "all 2 runs fail at step 1") || !strings.Contains(m, "STOCK=2") {
		t.Errorf("at start: %q", m)
	}
	x.Runs[1].Report = failAt(3)
	if m := x.atStart(); m != "" {
		t.Errorf("a run failing later: %q", m)
	}
	x.Runs[0].Unscripted = []qenv.Scripted{{Kind: "rpc.api.request", Option: 1, After: 14}}
	if m := x.unscripted(); !strings.Contains(m, "in 1 of 2 runs scripted faults never happened") || !strings.Contains(m, "rpc.api.request=1 after 14 steps") {
		t.Errorf("unscripted: %q", m)
	}
}

// A scripted fault naming no declared kind, or an option its kind lacks,
// fails the exploration: it never fires, and the claim it was for once
// failed as if the code had survived it, with advice about rates.
func TestUnknownScriptedFault(t *testing.T) {
	qenv.DeclareFaults("rpc.scripted.response", "lost")
	f := &failTB{TB: t}
	x := &Exploration{Runs: []*Run{{scripted: true, Unscripted: []qenv.Scripted{
		{Kind: "rpc.scripted.respons", Option: 1, After: 2},
		{Kind: "rpc.scripted.response", Name: "lots", After: 2},
	}}}}
	x.unknownScripted(f)
	all := strings.Join(f.errs, "\n")
	for _, want := range []string{`its kind "rpc.scripted.respons" covers no fault kind or nemesis`, `rpc.scripted.response has no option "lots"; its options: lost`} {
		if !strings.Contains(all, want) {
			t.Errorf("errors lack %q:\n%s", want, all)
		}
	}
}
