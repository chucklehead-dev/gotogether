package checkout

import (
	"os"
	"path/filepath"
	"testing"

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

func saveProjection(t *testing.T, native []qtrace.Step) []qtrace.Step {
	t.Helper()
	steps, err := gristtest.ProtocolSteps(native, App, inventory.App, fulfillment.App)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("TRIAL_EVIDENCE")
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, qtest.Mutant()+".jsonl")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = qtrace.WriteJSONL(f, steps)
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Logf("protocol history: %s", path)
	}
	return steps
}
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("TestMutants builds the planted bugs")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "missingRelease":
		cfg := decisionConfig(true)
		cfg.Prologue = cfg.Prologue[:1]
		cfg.Send = []string{"cancel"}
		e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
		e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		for _, r := range e.Runs {
			localChecks(t, r.Recorded, true)
			if count(r.Recorded, "inventory/t1", "reserve", "") != 1 || count(r.Recorded, "inventory/t1", "cancel", "") != 0 {
				t.Fatal("missing release did not leave a real held unit")
			}
			qtest.Validate(t, protocol, saveProjection(t, r.Recorded)).Expect(t, qtest.Violates(validate.EventuallyName("allIntentsAnswered")))
		}
	case "freshKey":
		cfg := config()
		cfg.Prologue = nil
		cfg.Send = []string{"place"}
		cfg.Clients = 1
		cfg.Ops = 1
		cfg.Alphabet = []string{"a"}
		e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(seed uint64) qenv.Chooser {
			return &qenv.Script{Base: quiet(seed), Faults: []qenv.Scripted{{Kind: "rpc.inventory.response", Name: "lost"}}}
		}})
		e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.inventory.response").Faulted())
		for _, r := range e.Runs {
			qtest.Validate(t, "inventory/inventory.binding.yaml", r.Recorded).Expect(t, qtest.Conforms(), qtest.Settles())
			if count(r.Recorded, "inventory/t1", "reserve", "") != 2 {
				t.Fatal("retry did not allocate a second physical unit")
			}
			if len(gristtest.Links(App, inventory.App, r.Recorded)) == 0 {
				t.Fatal("fresh retry id was not detected by Links")
			}
			qtest.Validate(t, protocol, saveProjection(t, r.Recorded)).Expect(t, qtest.Rejects("inventoryReserve"))
		}
	case "authorizeCancelled":
		e := gristsim.System(commits, App, decisionConfig(true)).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
		e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		for _, r := range e.Runs {
			if count(r.Recorded, "inventory/t1", "cancel", "") != 1 || count(r.Recorded, "inventory/t1", "authorize", "") != 1 || count(r.Recorded, "fulfillment/t1", "start", "") != 1 {
				t.Fatal("did not witness shipment after an accepted cancellation")
			}
			// Native inventory sequence proves which durable decision won.
			var cancelSeq, authorizeSeq uint64
			for _, s := range r.Recorded {
				if s.Actor == "inventory/t1" && s.Recorder == grist.RecorderID("inventory", "t1") {
					if s.Action == "cancel" {
						cancelSeq = s.Seq
					}
					if s.Action == "authorize" {
						authorizeSeq = s.Seq
					}
				}
			}
			if cancelSeq >= authorizeSeq {
				t.Fatal("cancellation did not precede authorization")
			}
			qtest.Validate(t, "inventory/inventory.binding.yaml", r.Recorded).Expect(t, qtest.Rejects("authorize"))
			qtest.Validate(t, protocol, saveProjection(t, r.Recorded)).Expect(t, qtest.Rejects("inventoryAuthorize"))
		}
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}
func TestMutants(t *testing.T) { qtest.Mutants{Generated: gristtest.PeerMutants(App)}.Run(t) }
func TestInventoryMutants(t *testing.T) {
	qtest.Mutants{Dir: "inventory/testdata/mutants", Package: "inventory"}.Run(t)
}

// The exact same retry fault on the faithful app allocates one unit.
func TestLostReservationReply(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := config()
	cfg.Prologue = nil
	cfg.Send = []string{"place"}
	cfg.Clients = 1
	cfg.Ops = 1
	cfg.Alphabet = []string{"a"}
	e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(seed uint64) qenv.Chooser {
		return &qenv.Script{Base: quiet(seed), Faults: []qenv.Scripted{{Kind: "rpc.inventory.response", Name: "lost"}}}
	}})
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.inventory.response").Faulted())
	for _, r := range e.Runs {
		localChecks(t, r.Recorded, true)
		if count(r.Recorded, "inventory/t1", "reserve", "") != 1 {
			t.Fatal("stable-key retry allocated twice")
		}
		if !protocolCheck(t, r.Recorded, 32).Exhaustive {
			t.Fatal("inconclusive ordering check")
		}
	}
}
