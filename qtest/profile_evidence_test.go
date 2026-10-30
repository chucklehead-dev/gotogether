package qtest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestSweepEvidenceRetainsRowPoliciesAndNativeIdentities(t *testing.T) {
	makeRow := func(id int, rate float64) *Exploration {
		x := &Exploration{test: "TestRows/row", id: id, profile: &runprofile.Profile{ID: "row", Chooser: "seeded", Rates: &runprofile.Rates{Kinds: map[string]float64{"rpc": rate}}}}
		x.Runs = []*Run{{origin: origin{kind: "seed", seed: 1}, Report: &validate.Report{}, simulations: []runprofile.Simulation{{Simulation: 1, Runtime: "deterministic", Horizon: "1s", Scheduler: &runprofile.Scheduler{MaxSteps: id * 100}}}}}
		return x
	}
	a, b := makeRow(1, .1), makeRow(2, .9)
	e := (&SweepResult{Runs: []*Exploration{a, b}}).Evidence()
	if len(e.Profiles) != 2 || e.Profiles[0].Rates.Kinds["rpc"] != .1 || e.Profiles[1].Rates.Kinds["rpc"] != .9 {
		t.Fatalf("row policies collapsed: %+v", e.Profiles)
	}
	if len(e.Executions) != 2 || e.Executions[0].Run == e.Executions[1].Run || e.Executions[0].Run != a.ref()+":seed=1" {
		t.Fatalf("native identities lost: %+v", e.Executions)
	}
	e.Profiles[0].Rates.Kinds["rpc"] = 0
	e.Executions[0].Simulations[0].Scheduler.MaxSteps = 0
	if a.profile.Rates.Kinds["rpc"] != .1 || a.Runs[0].simulations[0].Scheduler.MaxSteps != 100 {
		t.Fatal("evidence mutation changed execution configuration")
	}
	bytes, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(bytes), `{"format":`) {
		t.Fatal("format must remain the first key for streaming discovery")
	}
	var view evidence.View
	if err := json.Unmarshal(bytes, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Profiles) != 2 || len(view.Executions) != 2 || view.Executions[1].Simulations[0].Scheduler.MaxSteps != 200 {
		t.Fatal("CLI view lost policies or observations")
	}
}
