package binding

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A search's candidate, planned within its history, interns ids in the
// history's order and takes its constants from all the history's steps:
// two orders of one history name each id alike and check one instance.
func TestPlanWithin(t *testing.T) {
	b := &Binding{Spec: "x.qnt", Module: "m", Intern: map[string]string{"id": "actor"},
		Constants: map[string]string{"IDS": `{{ set "id" }}`, "N": `{{ count "inc" }}`},
		Actions:   map[string]Action{"inc": {Args: map[string]string{"id": "{{.id}}"}}}}
	inc := func(id string) qtrace.Step {
		return qtrace.Step{Action: "inc", Actor: "x", Process: "p", Args: map[string]any{"id": id}, Obs: map[string]any{}}
	}
	whole := &qtrace.Trace{Actor: "x", Steps: []qtrace.Step{inc("a"), inc("b"), inc("c")}, Processes: []string{"p"}}
	// a prefix of another order: c first, then a
	cand := &qtrace.Trace{Actor: "x", Steps: []qtrace.Step{inc("c"), inc("a")}, Processes: []string{"p"}}
	p, err := b.PlanWithin(nil, cand, whole)
	if err != nil {
		t.Fatal(err)
	}
	if p.Constants["IDS"] != "Set(1, 2, 3)" || p.Constants["N"] != "3" {
		t.Errorf("constants %v: want the history's, IDS = Set(1, 2, 3) and N = 3", p.Constants)
	}
	if got := p.Calls[0].Args["id"] + "," + p.Calls[1].Args["id"]; got != "3,1" {
		t.Errorf("ids %s: want c and a as the history names them, 3,1", got)
	}
	own, err := b.Plan(nil, cand)
	if err != nil {
		t.Fatal(err)
	}
	if own.Constants["IDS"] != "Set(1, 2)" || own.Calls[0].Args["id"] != "1" {
		t.Errorf("planned alone: %v, id %s; want its own, Set(1, 2) and c as 1", own.Constants, own.Calls[0].Args["id"])
	}
}
