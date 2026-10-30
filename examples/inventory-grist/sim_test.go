package checkout

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/fulfillment"
	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/inventory"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

const commits = "checkout.binding.yaml"
const protocol = "protocol.binding.yaml"

func config() gristsim.Config {
	return gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 1, Send: []string{"cancel", "confirm"}, Alphabet: []string{"a", "b"},
		Phases: []gristsim.Phase{reservationPhase("a", "b")},
		Peers:  []*grist.App{inventory.App, fulfillment.App}, Check: true}
}

// reservationPhase requires both accepted placement and the inventory owner's
// observed hold before a customer decision can exercise cancellation/confirmation.
func reservationPhase(orders ...string) gristsim.Phase {
	p := gristsim.Phase{Name: "reserve inventory", Timeout: 10 * time.Second, Faults: gristsim.FaultsDisabled}
	for _, order := range orders {
		p.Calls = append(p.Calls, gristsim.Call{Name: "place", Args: Order{Order: order}})
		p.Await = append(p.Await, gristsim.Observation{Call: gristsim.Call{App: inventory.App, Name: "allocation", Args: Order{Order: order}}, Result: inventory.Allocation{Units: 1, Status: "held"}})
	}
	return p
}

func decisionPhase(name, command, status string) gristsim.Phase {
	return gristsim.Phase{Name: name, Timeout: 10 * time.Second, Faults: gristsim.FaultsDisabled,
		Calls: []gristsim.Call{{Name: command, Args: Order{Order: "a"}}},
		Await: []gristsim.Observation{{Call: gristsim.Call{App: inventory.App, Name: "allocation", Args: Order{Order: "a"}}, Result: inventory.Allocation{Units: 1, Status: status}}}}
}

func localChecks(t *testing.T, steps []qtrace.Step, inventoryOK bool) {
	t.Helper()
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
	if inventoryOK {
		qtest.Validate(t, "inventory/inventory.binding.yaml", steps).Expect(t, qtest.Conforms(), qtest.Settles())
	}
	// A run need not start a shipment. Empty fulfillment is checked by the
	// protocol's completion property, not an empty local actor selection.
	for _, s := range steps {
		if s.Actor == "fulfillment/t1" {
			qtest.Validate(t, "fulfillment/fulfillment.binding.yaml", steps).Expect(t, qtest.Conforms(), qtest.Settles())
			break
		}
	}
	gristtest.Linked(t, App, inventory.App, steps)
	// There may be no authorization, and hence no downstream calls to link.
	for _, s := range steps {
		if s.Actor == "inventory/t1" && s.Action == "fulfillmentStart" {
			gristtest.Linked(t, inventory.App, fulfillment.App, steps)
			break
		}
	}
}
func protocolCheck(t *testing.T, steps []qtrace.Step, limit int) gristtest.ProtocolSearch {
	t.Helper()
	checker, err := validate.NewChecker(protocol, validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	result, err := gristtest.CheckProtocolOrders(t.Context(), steps, limit, func(ctx context.Context, order []qtrace.Step) (bool, error) {
		report, err := checker.Check(ctx, order)
		if err != nil {
			return false, err
		}
		return report.OK(), nil
	}, App, inventory.App, fulfillment.App)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Counterexample) > 0 {
		qtest.Validate(t, protocol, result.Counterexample).Expect(t, qtest.Conforms())
	}
	t.Logf("protocol: %d causal orderings checked, exhaustive=%v", result.Checked, result.Exhaustive)
	return result
}

// compositionChecks compiles each explicitly selected model once. Every
// assessment judges the same native history; it never reruns a peer separately.
func compositionChecks(t *testing.T) gristtest.Composition {
	t.Helper()
	checker := func(binding string) *validate.Checker {
		t.Helper()
		c, err := validate.NewChecker(binding, validate.Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	return gristtest.Composition{
		Models: []gristtest.LocalModel{
			{App: App, Checker: checker(commits)},
			{App: inventory.App, Checker: checker("inventory/inventory.binding.yaml")},
			{App: fulfillment.App, Checker: checker("fulfillment/fulfillment.binding.yaml")},
		},
		Protocol: &gristtest.ProtocolModel{Checker: checker(protocol), Limit: 256},
	}
}

// TestFaithful uses public gristsim configuration, generated local bindings,
// peer links, and an independently written business protocol.
func TestFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	// Readiness is observed through inventory's public query, without healing
	// faults or pacing generated arguments with an assumed completion delay.
	cfg.Gen = map[string]func(func(int) int) any{}
	for _, name := range cfg.Send {
		cfg.Gen[name] = func(pick func(int) int) any { return Order{Order: []string{"a", "b"}[pick(2)]} }
	}
	checks := compositionChecks(t)
	sys := checks.System(gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults))
	e := sys.Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4})
	// Conforms includes the registered composition checks, including an
	// exhaustive protocol search. Navigation reevaluates the same assessor.
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Some(qtest.Scenario(qtest.Takes("cancel").Outcome("ok"))), qtest.Some(qtest.Scenario(qtest.Takes("confirm").Outcome("ok"))))
	for _, r := range e.Runs {
		assertHeldPrerequisites(t, r)
		composition := r.Report.Composition
		if composition == nil || len(composition.Local) != 3 || len(composition.Peers) != 2 {
			t.Fatalf("composition omitted a service or declared peer edge: %+v", composition)
		}
		if !composition.Protocol.Configured || composition.Protocol.Quantifier != "all_causal_orders" || !composition.Protocol.Exhaustive || composition.Protocol.Checked == 0 || composition.Protocol.Limit != 256 {
			t.Fatalf("protocol receipt lacks the completed bounded search: %+v", composition.Protocol)
		}
		t.Logf("%s", composition.String())
	}
}

// The successful prerequisite is an actual read through inventory's public
// API. Its phase receipt and native query record must agree before any random
// customer intent starts; the independent inventory model checks its answer.
func assertHeldPrerequisites(t *testing.T, r *qtest.Run) {
	t.Helper()
	observed := 0
	for _, p := range r.Phases {
		if p.Name != "reserve inventory" {
			continue
		}
		if p.Status != "completed" || p.Faults != string(gristsim.FaultsDisabled) {
			t.Fatalf("reservation prerequisites did not complete: %+v", p)
		}
		for _, req := range p.Requirements {
			if req.App != "inventory" || req.Action != "allocation" {
				continue
			}
			var got inventory.Allocation
			if err := json.Unmarshal(req.Observed, &got); err != nil || req.Status != "satisfied" || req.Attempts < 1 || got != (inventory.Allocation{Units: 1, Status: "held"}) {
				t.Fatalf("phase did not retain the actual held allocation: %+v (decoded %+v, %v)", req, got, err)
			}
			observed++
		}
	}
	if observed != 2 {
		t.Fatalf("wanted one held observation for each prepared order, got %d", observed)
	}
	held := map[string]bool{}
	for _, s := range r.Recorded {
		if s.Actor == "inventory/t1" && s.Action == "allocation" && s.Obs["status"] == "held" {
			if order, ok := s.Args["order"].(string); ok {
				held[order] = true
			}
		}
		caller, _ := s.Args["caller"].(string)
		if s.Invocation && s.Actor == "checkout/t1/clients" && strings.HasPrefix(caller, "client") && (s.Action == "cancel" || s.Action == "confirm") {
			if !held["a"] || !held["b"] {
				t.Fatal("customer decision ran before inventory's observed holds")
			}
			return
		}
	}
	t.Fatal("fixture recorded no customer cancellation or confirmation")
}

// phases selects a fixed workload, leaving the simulation schedule seeded.
type phases struct {
	qenv.Chooser
	op int
}

func (c *phases) Unwrap() qenv.Chooser { return c.Chooser }
func (c *phases) Choose(kind string, n int) int {
	if kind == "gristsim.op" {
		v := c.op % n
		c.op++
		return v
	}
	return c.Chooser.Choose(kind, n)
}
func quiet(seed uint64) qenv.Chooser {
	b := qenv.NewSeeded(seed)
	b.Rates = qenv.NoFaults
	return &phases{Chooser: b}
}
func decisionConfig(cancelFirst bool) gristsim.Config {
	cfg := config()
	cfg.Clients, cfg.Ops = 1, 1
	cfg.Phases = []gristsim.Phase{reservationPhase("a")}
	cfg.Gen = map[string]func(func(int) int) any{
		"confirm": func(func(int) int) any { return Order{Order: "a"} },
		"cancel":  func(func(int) int) any { return Order{Order: "a"} },
	}
	if cancelFirst {
		p := decisionPhase("cancel held allocation", "cancel", "cancelled")
		p.Await[0].Result = inventory.Allocation{Status: "cancelled"}
		cfg.Phases = append(cfg.Phases, p)
		cfg.Send = []string{"confirm"}
	} else {
		cfg.Phases = append(cfg.Phases, decisionPhase("authorize held allocation", "confirm", "authorized"))
		cfg.Send = []string{"cancel"}
	}
	return cfg
}
func count(steps []qtrace.Step, actor, action, outcome string) int {
	n := 0
	for _, s := range steps {
		if s.Actor == actor && s.Action == action && s.Recorder == grist.RecorderID(strings.Split(actor, "/")[0], "t1") && (s.Outcome == outcome || outcome == "" && s.Outcome == "ok") {
			n++
		}
	}
	return n
}
func TestDecisionRaces(t *testing.T) {
	qtest.RequireQuint(t)
	for _, cancelFirst := range []bool{true, false} {
		name := "authorization-first"
		if cancelFirst {
			name = "cancellation-first"
		}
		t.Run(name, func(t *testing.T) {
			e := gristsim.System(commits, App, decisionConfig(cancelFirst)).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
			e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
			for _, r := range e.Runs {
				localChecks(t, r.Recorded, true)
				result := protocolCheck(t, r.Recorded, 256)
				if !result.Exhaustive {
					t.Fatal("inconclusive ordering limit")
				}
				if cancelFirst {
					if count(r.Recorded, "inventory/t1", "cancel", "") != 1 || count(r.Recorded, "inventory/t1", "authorize", "not-held") != 1 || count(r.Recorded, "fulfillment/t1", "start", "") != 0 {
						t.Fatal("did not witness accepted cancellation before rejected authorization")
					}
				} else {
					if count(r.Recorded, "inventory/t1", "authorize", "") != 1 || count(r.Recorded, "inventory/t1", "cancel", "too-late") != 1 || count(r.Recorded, "fulfillment/t1", "start", "") != 1 {
						t.Fatal("did not witness authorization, shipment and too-late cancellation")
					}
				}
			}
		})
	}
}

func TestReservationCrash(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	cfg.Phases = nil
	cfg.Send = []string{"place"}
	cfg.Clients = 1
	cfg.Ops = 1
	cfg.Alphabet = []string{"a"}
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(seed uint64) qenv.Chooser {
		return &qenv.Script{Base: quiet(seed), Faults: []qenv.Scripted{{Kind: "grist.reserve-stock.effect", Name: "crashAfter"}}}
	}})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("grist.reserve-stock.effect").Faulted())
	for _, r := range e.Runs {
		localChecks(t, r.Recorded, true)
		if count(r.Recorded, "inventory/t1", "reserve", "") != 1 {
			t.Fatal("crash retry allocated twice")
		}
		if !protocolCheck(t, r.Recorded, 32).Exhaustive {
			t.Fatal("inconclusive ordering check")
		}
	}
}

func TestCapacity(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	cfg.Clients = 1
	cfg.Ops = 1
	cfg.Send = []string{"place"}
	cfg.Alphabet = []string{"a"}
	cfg.Phases[0].Calls = append(cfg.Phases[0].Calls, gristsim.Call{Name: "place", Args: Order{Order: "c"}})
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	for _, r := range e.Runs {
		localChecks(t, r.Recorded, true)
		if count(r.Recorded, "inventory/t1", "reserve", "") != 2 || count(r.Recorded, "inventory/t1", "reserve", "full") != 1 {
			t.Fatal("third checkout did not reach inventory's capacity refusal")
		}
		if !protocolCheck(t, r.Recorded, 256).Exhaustive {
			t.Fatal("inconclusive ordering check")
		}
	}
}
func TestCancellationOvertakesReserve(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := decisionConfig(true)
	// This contrast intentionally cancels before observing a reservation. Keep
	// the scripted delayed request active: an observation must not heal it.
	cfg.Phases = []gristsim.Phase{{Name: "cancel before delayed reservation", Timeout: 10 * time.Second, Faults: gristsim.FaultsConfigured,
		Calls: []gristsim.Call{{Name: "place", Args: Order{Order: "a"}}, {Name: "cancel", Args: Order{Order: "a"}}},
		Await: []gristsim.Observation{{Call: gristsim.Call{App: inventory.App, Name: "allocation", Args: Order{Order: "a"}}, Result: inventory.Allocation{Status: "cancelled"}}}}}
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(seed uint64) qenv.Chooser {
		return &qenv.Script{Base: quiet(seed), Faults: []qenv.Scripted{{Kind: "rpc.inventory.delay", Name: "slow"}}}
	}})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.inventory.delay").Faulted())
	for _, r := range e.Runs {
		localChecks(t, r.Recorded, true)
		if count(r.Recorded, "inventory/t1", "cancel", "") != 1 || count(r.Recorded, "inventory/t1", "reserve", "cancelled") != 1 {
			t.Fatal("cancellation did not overtake the delayed reservation")
		}
		if !protocolCheck(t, r.Recorded, 256).Exhaustive {
			t.Fatal("inconclusive ordering check")
		}
	}
}

func TestAnswered(t *testing.T) {
	for _, refusal := range []error{inventory.ErrCancelled, inventory.ErrTooLate, inventory.ErrFull, inventory.ErrNotHeld} {
		if err := answered(grist.NotApplied(refusal)); err != nil {
			t.Fatalf("business answer retried: %v", err)
		}
	}
	for _, failure := range []error{nil, grist.ErrOutcomeUnknown, grist.ErrKeyReused, grist.Rejection("undeclared"), context.DeadlineExceeded} {
		if err := answered(failure); err != failure {
			t.Fatalf("non-business failure swallowed: %v -> %v", failure, err)
		}
	}
}

// A valid order name is insufficient: the payload must match the exact
// originating placement, even when both orders already exist.
func TestProtocolCorrelation(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	cfg.Clients = 1
	cfg.Ops = 1
	cfg.Send = []string{"place"}
	cfg.Alphabet = []string{"a"}
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	checker, err := validate.NewChecker(protocol, validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	for _, r := range e.Runs {
		steps, err := gristtest.ProtocolSteps(r.Recorded, App, inventory.App, fulfillment.App)
		if err != nil {
			t.Fatal(err)
		}
		qtest.Validate(t, protocol, steps).Expect(t, qtest.Conforms())
		at := -1
		for i, s := range steps {
			if s.Action == "inventoryReserve" {
				at = i
				break
			}
		}
		if at < 2 || steps[at].Args["order"] != "a" {
			t.Fatal("fixture needs both placements before the first allocation")
		}
		steps[at].Args["order"] = "b" // an existing order, but not this call's cause
		report, err := checker.Check(t.Context(), steps)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Actors) != 1 || report.Actors[0].Failure == nil || report.Actors[0].Failure.Index != at {
			t.Fatalf("payload/cause mismatch not rejected at its first step: %+v", report)
		}
	}
}
