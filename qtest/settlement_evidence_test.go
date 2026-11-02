package qtest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func downSettlementSystem(down bool) System {
	return System{Binding: "../validate/testdata/funds.binding.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		ready := false
		p := s.Process("worker", false, func(ctx context.Context) {
			qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
			qobs.Record(ctx, "withdraw", nil, "amount", 10, "obs.balance", 0)
			ready = true
			if down {
				qenv.Sleep(ctx, time.Hour)
			}
		})
		if _, err := s.RunUntil(time.Second, func() bool { return ready }); err != nil {
			return err
		}
		if down {
			p.Crash()
		}
		return s.Settle(time.Second)
	}}
}

func TestDownSettlementRemainsRuntimeEvidence(t *testing.T) {
	RequireQuint(t)
	x := downSettlementSystem(true).Explore(t, ExploreConfig{Seeds: 1, Parallel: 1})
	x.Expect(t, None(Settles()))
	r := x.Runs[0]
	if r.Err != nil || r.Unsettled == "" || r.Report.Receipt().Status != receipt.Inconclusive {
		t.Fatalf("runtime unavailability became model failure: err=%v, report=%+v", r.Err, r.Report)
	}
	for _, step := range r.Steps {
		if step.Settled {
			t.Fatal("down process acquired a completion marker")
		}
	}
	found := false
	for _, a := range r.Report.Receipt().Actors {
		for _, check := range a.Checks {
			if check.Kind == "eventually" {
				found = true
				if check.Status != receipt.NotChecked {
					t.Fatalf("unavailable progress judged eventual obligation: %+v", check)
				}
			}
		}
	}
	if !found {
		t.Fatal("eventual obligation vanished from receipt")
	}
	data, err := json.Marshal(x.Evidence())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Executions []runprofile.Execution `json:"executions"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Executions) != 1 || decoded.Executions[0].Settlement == nil {
		t.Fatalf("typed settlement missing from JSON: %s", data)
	}
	u := decoded.Executions[0].Settlement
	if u.After != "1s" || len(u.DownProcesses) != 1 || u.DownProcesses[0] != (runprofile.ProcessIdentity{Name: "worker", Incarnation: 1, Index: 1}) {
		t.Fatalf("down process identity changed: %+v", u)
	}
	if !strings.Contains(x.Evidence().String(), `"worker"#1 (process 1)`) {
		t.Fatal("human evidence lost down process identity")
	}
	// A genuinely quiet, available system still exposes the real unfulfilled
	// obligation as a model defect; unavailable progress must not weaken it.
	downSettlementSystem(false).Explore(t, ExploreConfig{Seeds: 1, Parallel: 1}).Expect(t, All(Violates("eventually funded")))
}

func TestDownSettlementNavigationKeepsTypedRuntimeOutcome(t *testing.T) {
	RequireQuint(t)
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#1:seed=1")
	t.Setenv("GOTOGETHER_NAV", "start")
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	downSettlementSystem(true).Explore(t, ExploreConfig{Seeds: 1, Parallel: 1})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var node navproto.Node
	if err := json.Unmarshal(data, &node); err != nil {
		t.Fatal(err)
	}
	if node.Receipt.Status != receipt.Inconclusive || node.Execution == nil || node.Execution.Settlement == nil || len(node.Execution.Settlement.DownProcesses) != 1 {
		t.Fatalf("navigation lost runtime/model distinction: %s", data)
	}
}

func TestReturnedSettlementObservationIsIndependent(t *testing.T) {
	u := &qenv.Unsettled{After: time.Second, Tasks: []string{"work"}, DownProcesses: []qenv.ProcessIdentity{{Name: "same", Incarnation: 1, Index: 2}}}
	r := runOne(settings{}, false, qenv.Nominal{}, func(qenv.Chooser) error { return u })
	u.Tasks[0], u.DownProcesses[0].Name = "changed", "changed"
	first := r.execution("seed=1")
	if !r.hasExecution() || first.Settlement.Tasks[0] != "work" || first.Settlement.DownProcesses[0].Name != "same" {
		t.Fatal("returned error aliases retained runtime observation")
	}
	first.Settlement.DownProcesses[0].Index = 99
	if r.execution("seed=1").Settlement.DownProcesses[0].Index != 2 {
		t.Fatal("published execution aliases run metadata")
	}
	other := *r
	other.settlement = r.settlement.Clone()
	other.settlement.DownProcesses[0].Index++
	if sameRun(r, &other) == "" {
		t.Fatal("determinism ignored changed process identity")
	}
}

func TestSettlementIdentifiesOriginatingSimulationNotLastCall(t *testing.T) {
	RequireQuint(t)
	system := System{Binding: "../validate/testdata/funds.binding.yaml", Run: func(c qenv.Chooser) error {
		bad := qenv.New(c)
		defer bad.Close()
		ready := false
		worker := bad.Process("worker", false, func(ctx context.Context) {
			qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
			qobs.Record(ctx, "withdraw", nil, "amount", 10, "obs.balance", 0)
			ready = true
			qenv.Sleep(ctx, time.Hour)
		})
		if _, err := bad.RunUntil(time.Second, func() bool { return ready }); err != nil {
			return err
		}
		worker.Crash()
		unavailable := bad.Settle(time.Second)
		// Both simulations own a process named worker at creation index 1.
		// A later call on another Sim cannot identify the saved error's origin.
		good := qenv.New(c)
		defer good.Close()
		good.Process("worker", false, func(context.Context) {})
		if err := good.Run(time.Second); err != nil {
			return err
		}
		return unavailable
	}}
	x := system.Explore(t, ExploreConfig{Seeds: 1, Parallel: 1})
	x.Expect(t, None(Settles()))
	data, err := json.Marshal(x.Evidence())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Executions []runprofile.Execution `json:"executions"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Executions) != 1 {
		t.Fatalf("missing execution: %s", data)
	}
	e := decoded.Executions[0]
	if e.Settlement == nil || e.Settlement.Simulation != 1 || len(e.Simulations) < 3 || e.Simulations[len(e.Simulations)-1].Simulation != 2 {
		t.Fatalf("settlement guessed the last simulation: %s", data)
	}
	if len(e.Settlement.DownProcesses) != 1 || e.Settlement.DownProcesses[0] != (runprofile.ProcessIdentity{Name: "worker", Incarnation: 1, Index: 1}) {
		t.Fatalf("originating process identity changed: %+v", e.Settlement)
	}
	if !strings.Contains(e.String(), "; simulation 1") {
		t.Fatalf("originating simulation hidden from human evidence: %s", e.String())
	}
}
