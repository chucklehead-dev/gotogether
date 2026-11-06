package qtest

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wire "github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func faultOrderEvidenceFixture(name string, complete bool) *FaultOrder {
	first, second := qenv.Scripted{Kind: "rpc.a", Option: 1, After: 1}, qenv.Scripted{Kind: "rpc.b", Option: 2, After: 2}
	x := &Exploration{test: "TestOrders/" + name, id: 1, profile: &runprofile.Profile{ID: name, Chooser: "script", Seeds: &runprofile.Seeds{Count: 2}, Rates: &runprofile.Rates{Kinds: map[string]float64{"rpc": .1}}}}
	order := &FaultOrder{First: first, Second: second, Budget: 2, Exploration: x}
	x.faultOrder = order
	for _, seed := range []uint64{7, 8} {
		choices := []qenv.Choice{{Kind: "fault.rpc.a", N: 2, Value: 1, Step: 1}}
		remaining := []qenv.Scripted{second}
		if complete {
			choices = append(choices, qenv.Choice{Kind: "fault.rpc.b", N: 3, Value: 2, Step: 2})
			remaining = nil
		}
		x.Runs = append(x.Runs, &Run{origin: origin{kind: "seed", seed: seed}, scripted: true, Unscripted: remaining, Choices: choices,
			Report:      &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}, Composition: &receipt.Composition{History: "history", Local: []receipt.Component{{Target: "local", Applicable: true, Status: receipt.Pass}}}},
			simulations: []runprofile.Simulation{{Simulation: 1, Runtime: "deterministic", Scheduler: &runprofile.Scheduler{MaxSteps: 100}}}})
	}
	return order
}

func TestFaultOrderEvidenceRetainsNativeExtent(t *testing.T) {
	a, b := faultOrderEvidenceFixture("reached", true), faultOrderEvidenceFixture("bounded", false)
	skipped := &FaultOrder{First: b.First, Second: b.Second, Budget: 2}
	e := (&FaultOrderResult{Orders: []*FaultOrder{a, skipped, b}, test: "TestOrders"}).Evidence()
	if e.Test != "TestOrders" || e.Runs != 4 || e.Conform != 4 || len(e.FaultOrders) != 3 || e.Sample != nil || e.PCT != nil {
		t.Fatalf("aggregate: %+v", e)
	}
	got := e.FaultOrders
	if got[0].Status != "witnessed" || got[0].Witness != a.Exploration.ref()+":seed=7" || got[1].Status != "unreached" || len(got[1].Attempts) != 0 || got[2].Status != "unreached" || got[2].Witness != "" {
		t.Fatalf("coverage lost: %+v", got)
	}
	if got[0].Budget != 2 || len(got[0].Attempts) != 2 || *got[0].Attempts[1].Seed != 8 || got[0].Attempts[1].Run != a.Exploration.ref()+":seed=8" {
		t.Fatalf("actual identities: %+v", got[0])
	}
	if len(e.Profiles) != 2 || len(e.Executions) != 4 || len(e.Compositions) != 4 || e.Executions[0].Run == e.Executions[2].Run || e.Compositions[2].Run != b.Exploration.ref()+":seed=7" {
		t.Fatalf("child metadata lost: %+v", e)
	}
	if a.Exploration.Evidence().Sample != nil {
		t.Fatal("directed attempts gained an invented statistical bound")
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := e.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), `{"format":`) && !strings.HasPrefix(string(raw), "{\n  \"format\":") {
		t.Fatal("streaming format key moved")
	}
	view, err := wire.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	native, err := ReadEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.FaultOrders, native.FaultOrders) || !reflect.DeepEqual(view.FaultOrders, e.FaultOrders) {
		t.Fatal("wire view changed directed coverage")
	}
	for _, want := range []string{"witnessed", "unreached", "bounded absence, not impossibility", "seed budget 2", a.Exploration.ref() + ":seed=7"} {
		if !strings.Contains(e.String(), want) {
			t.Fatalf("text lost %q: %s", want, e)
		}
	}
	*got[0].Attempts[0].Seed = 999
	e.Profiles[0].Rates.Kinds["rpc"] = 0
	e.Executions[0].Simulations[0].Scheduler.MaxSteps = 0
	e.Compositions[0].Local[0].Reason = "changed"
	if a.Exploration.Runs[0].origin.seed != 7 || a.Exploration.profile.Rates.Kinds["rpc"] != .1 || a.Exploration.Runs[0].simulations[0].Scheduler.MaxSteps != 100 || a.Exploration.Runs[0].Report.Composition.Local[0].Reason != "" {
		t.Fatal("evidence aliases execution data")
	}
}

func TestFaultOrderFailureEvidenceRetainsAttempts(t *testing.T) {
	order := faultOrderEvidenceFixture("failure", true)
	x := order.Exploration
	x.Runs = []*Run{{origin: origin{kind: "seed", seed: 7}, Err: errors.New("driver failed")}, nil}
	x.faultOrderOrigins = []origin{{kind: "seed", seed: 7}, {kind: "seed", seed: 8}}
	e := x.failedEvidence(x.faultOrderOrigins)
	if e.Conform != 0 || e.Unjudged != 2 || len(e.RunErrors) != 2 || len(e.FaultOrders) != 1 || e.FaultOrders[0].Status != "unreached" || len(e.FaultOrders[0].Attempts) != 2 || *e.FaultOrders[0].Attempts[1].Seed != 8 {
		t.Fatalf("failure became missing coverage: %+v", e)
	}
	aggregate := (&FaultOrderResult{Orders: []*FaultOrder{order}}).Evidence()
	if aggregate.Conform != 0 || aggregate.Unjudged != 2 || len(aggregate.RunErrors) != 2 {
		t.Fatalf("aggregate invented model proof: %+v", aggregate)
	}
}

func TestFaultOrderEvidenceRejectsContradictoryCoverage(t *testing.T) {
	e := (&FaultOrderResult{Orders: []*FaultOrder{faultOrderEvidenceFixture("valid", true)}}).Evidence()
	e.FaultOrders[0].Witness = "not-an-attempt"
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := e.WriteJSON(path); err == nil {
		t.Fatal("contradictory witnessed goal written")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidence(path); err == nil {
		t.Fatal("contradictory witnessed goal read")
	}
	if _, err := wire.Read(path); err == nil {
		t.Fatal("CLI view accepted contradictory witness")
	}
}

func TestFaultOrderEvidenceDefaultNemesisSpec(t *testing.T) {
	order := faultOrderEvidenceFixture("nemesis", false)
	order.First = qenv.Scripted{Kind: "nemesis.crash.node", Option: -3, Name: "ignored"}
	order.Exploration.Runs[0].Choices = []qenv.Choice{{Kind: "fault.nemesis.crash", Name: "crash.node", N: 2, Value: 1}, {Kind: "fault.rpc.b", N: 3, Value: 2, Step: 2}}
	order.Exploration.Runs[0].Unscripted = nil
	e := order.Exploration.Evidence()
	if e.FaultOrders[0].Status != "witnessed" || e.FaultOrders[0].First.Option != 0 || e.FaultOrders[0].First.Name != "" {
		t.Fatalf("nemesis spec changed: %+v", e.FaultOrders)
	}
	path := filepath.Join(t.TempDir(), "nemesis.json")
	if err := e.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidence(path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.String(), "nemesis.crash.node=0") {
		t.Fatal("ignored nominal option reported as a nemesis goal")
	}
}

func TestFaultOrderAggregatePreservesReplayRole(t *testing.T) {
	faithful, replayed := faultOrderEvidenceFixture("fresh", true), faultOrderEvidenceFixture("replayed", true)
	replayed.Exploration.alone = &runSpec{}
	for _, orders := range [][]*FaultOrder{{replayed}, {faithful, replayed}, {replayed, faithful}} {
		e := (&FaultOrderResult{Orders: orders}).Evidence()
		if e.Role != RoleReplay || len(e.FaultOrders) != len(orders) {
			t.Fatalf("replay became faithful coverage: %+v", e)
		}
	}
}

// Saved child files are the CLI's input, not just a manually requested aggregate.
func TestFaultOrderAutomaticEvidenceMetadata(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", dir)
	faults := []qenv.Scripted{{Kind: "test.ordered.a", Option: 1}, {Kind: "test.ordered.b", Option: 1}}
	faultOrdersNativeSystem().FaultOrders(t, FaultOrderConfig{Faults: faults, Seeds: 2})
	files, witnessed, unreached := 0, 0, 0
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !wire.Is(path) {
			return nil
		}
		e, err := ReadEvidence(path)
		if err != nil {
			return err
		}
		view, err := wire.Read(path)
		if err != nil {
			return err
		}
		if e.Conform != 2 || len(e.FaultOrders) != 1 || e.FaultOrders[0].Budget != 2 || len(e.FaultOrders[0].Attempts) != 2 || len(e.Profiles) != 1 || !reflect.DeepEqual(e.FaultOrders, view.FaultOrders) {
			t.Fatalf("saved child lost extent: %+v", e)
		}
		switch e.FaultOrders[0].Status {
		case "witnessed":
			witnessed++
		case "unreached":
			unreached++
		default:
			t.Fatalf("invalid saved status: %+v", e.FaultOrders)
		}
		files++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != 2 || witnessed != 1 || unreached != 1 {
		t.Fatalf("saved files %d: witnessed%d unreached%d", files, witnessed, unreached)
	}
}

func TestFaultOrderEvidenceWriterRequiresCurrentHeader(t *testing.T) {
	for _, format := range []string{"gotogether.evidence/2", ""} {
		t.Run(format, func(t *testing.T) {
			e := (&FaultOrderResult{Orders: []*FaultOrder{faultOrderEvidenceFixture("header", false)}}).Evidence()
			e.Format = format
			path := filepath.Join(t.TempDir(), "evidence.json")
			if err := e.WriteJSON(path); err == nil {
				t.Fatalf("wrote directed coverage under format %q", format)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("refused writer created a file: %v", err)
			}
		})
	}
}

func TestFaultOrderEvidenceNamedSpecIgnoresOption(t *testing.T) {
	order := faultOrderEvidenceFixture("named", true)
	order.First.Name = "failed"
	order.First.Option = -3
	order.Exploration.Runs[0].Choices[0].Name = "failed"
	e := order.Exploration.Evidence()
	if e.FaultOrders[0].Status != "witnessed" || e.FaultOrders[0].First.Option != 0 || e.FaultOrders[0].First.Name != "failed" {
		t.Fatalf("named spec changed: %+v", e.FaultOrders)
	}
	path := filepath.Join(t.TempDir(), "named.json")
	if err := e.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidence(path); err != nil {
		t.Fatal(err)
	}
}
