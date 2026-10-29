package checkout

import (
	"context"
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
		Prologue: []gristsim.Call{{Name: "place", Args: Order{Order: "a"}}, {Name: "place", Args: Order{Order: "b"}}},
		Peers:    []*grist.App{inventory.App, fulfillment.App}, Check: true}
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

// TestFaithful uses public gristsim configuration, generated local bindings,
// peer links, and an independently written business protocol.
func TestFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	// Give the reservation phase time to run before customers decide. This
	// is workload pacing in virtual time, not an application timer.
	cfg.Gen = map[string]func(func(int) int) any{}
	for _, name := range cfg.Send {
		cfg.Gen[name] = func(pick func(int) int) any {
			qenv.Sleep(context.Background(), time.Second)
			return Order{Order: []string{"a", "b"}[pick(2)]}
		}
	}
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Some(qtest.Scenario(qtest.Takes("cancel").Outcome("ok"))), qtest.Some(qtest.Scenario(qtest.Takes("confirm").Outcome("ok"))))
	for _, r := range e.Runs {
		localChecks(t, r.Recorded, true)
		result := protocolCheck(t, r.Recorded, 256)
		if !result.Exhaustive {
			t.Fatalf("ordering limit exhausted after %d checks; trial cannot claim all orders passed", result.Checked)
		}
	}
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
func paced(pick func(int) int) any {
	qenv.Sleep(context.Background(), time.Second)
	return Order{Order: "a"}
}
func decisionConfig(cancelFirst bool) gristsim.Config {
	cfg := config()
	cfg.Clients = 1
	cfg.Ops = 2
	cfg.Prologue = []gristsim.Call{{Name: "place", Args: Order{Order: "a"}}}
	cfg.Send = []string{"confirm", "cancel"}
	cfg.Gen = map[string]func(func(int) int) any{"confirm": paced, "cancel": paced}
	if cancelFirst {
		cfg.Prologue = append(cfg.Prologue, gristsim.Call{Name: "cancel", Args: Order{Order: "a"}})
		cfg.Send = []string{"confirm"}
		cfg.Ops = 1
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
	cfg.Prologue = nil
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
	cfg.Prologue = append(cfg.Prologue, gristsim.Call{Name: "place", Args: Order{Order: "c"}})
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
	e := gristsim.System(commits, App, decisionConfig(true)).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(seed uint64) qenv.Chooser {
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
