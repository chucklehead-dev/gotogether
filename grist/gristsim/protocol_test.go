package gristsim_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Local conformance alone permits an effect that omits a peer call, or
// a catalog that permits a direct orders -> ledger hop. The independently
// written system model requires a posting to depend on a payment and
// every accepted order to be posted at the recorded settle.
func TestProtocolMissingAndPremature(t *testing.T) {
	qtest.RequireQuint(t)
	for _, mutant := range []string{"missing", "premature"} {
		t.Run(mutant, func(t *testing.T) {
			orders, bank, ledger := postingChainMutant(false, mutant)
			bindings := chainBindings(t, orders, bank, ledger)
			cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Check: true,
				Send: []string{"place"}, Alphabet: []string{"a"}, Ints: []int{1}, Peers: []*grist.App{bank, ledger}}
			sys := gristsim.System(bindings[0], orders, cfg).WithRates(qenv.NoFaults)
			sys.Mutant = mutant
			e := sys.Explore(t, qtest.ExploreConfig{Seeds: 2, Chooser: func(seed uint64) qenv.Chooser {
				base := qenv.NewSeeded(seed)
				base.Rates = qenv.NoFaults
				var faults []qenv.Scripted
				if mutant == "premature" {
					faults = []qenv.Scripted{{Kind: "rpc.bank.delay", Name: "slow"}}
				}
				return &qenv.Script{Base: base, Faults: faults}
			}})
			e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
			if mutant == "premature" {
				e.Expect(t, qtest.Reaches("rpc.bank.delay").Faulted())
			}
			for _, r := range e.Runs {
				qtest.Validate(t, bindings[1], r.Recorded).Expect(t, qtest.Conforms(), qtest.Settles())
				if mutant == "premature" {
					qtest.Validate(t, bindings[2], r.Recorded).Expect(t, qtest.Conforms(), qtest.Settles())
					if !postingBalance(r.Recorded, 1) {
						t.Fatal("premature fixture: want final totals to agree, exposing a transient protocol error")
					}
					// These are the simulation's true virtual commit times,
					// used only to witness this mutant, never to order the
					// system model's records across services.
					var paidAt, postedAt time.Time
					for _, s := range r.Recorded {
						if s.Actor == "bank/t1" && s.Action == "pay" {
							paidAt = s.Time
						}
						if s.Actor == "ledger/t1" && s.Action == "post" {
							postedAt = s.Time
						}
					}
					if paidAt.IsZero() || postedAt.IsZero() || !postedAt.Before(paidAt) {
						t.Fatal("premature fixture: posting did not precede its payment")
					}
				}
				projected, err := gristtest.ProtocolSteps(r.Recorded, orders, bank, ledger)
				if err != nil {
					t.Fatal(err)
				}
				claim := qtest.Violates(validate.EventuallyName("allPosted"))
				if mutant == "premature" {
					claim = qtest.Rejects("ledgerPost")
				}
				qtest.Validate(t, "testdata/protocol/chain.binding.yaml", projected).Expect(t, claim)
			}
		})
	}
}

// Isolate the protocol's duplicate-post guard from transport correlation:
// even a second posting with an apparently valid causal payment must be
// rejected. The faithful projection immediately beside it conforms.
func TestProtocolDuplicatePosting(t *testing.T) {
	qtest.RequireQuint(t)
	orders, bank, ledger := postingChain(false)
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(1)
	base.Rates = qenv.NoFaults
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, orders, gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1,
		Send: []string{"place"}, Ints: []int{1}, Peers: []*grist.App{bank, ledger}}); err != nil {
		t.Fatal(err)
	}
	projected, err := gristtest.ProtocolSteps(sink.Steps(), orders, bank, ledger)
	if err != nil {
		t.Fatal(err)
	}
	const binding = "testdata/protocol/chain.binding.yaml"
	qtest.Validate(t, binding, projected).Expect(t, qtest.Conforms(), qtest.Settles())
	duplicate := slices.Clone(projected)
	if len(duplicate) != 4 || duplicate[2].Action != "ledgerPost" || !duplicate[3].Settled {
		t.Fatalf("want place, pay, post, settle: %v", duplicate)
	}
	duplicate = append(duplicate[:3], duplicate[2], duplicate[3])
	duplicate[3].Seq, duplicate[4].Seq = 4, 5
	qtest.Validate(t, binding, duplicate).Expect(t, qtest.Rejects("ledgerPost"))
}

// A canonical extension can serialize independent work and hide a model's
// unjustified one-order-at-a-time restriction. The same real simulation
// history passes the business protocol under every causal ordering.
func TestProtocolOrdersConcurrentHistory(t *testing.T) {
	qtest.RequireQuint(t)
	orders, bank, ledger := postingChain(false)
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(1)
	base.Rates = qenv.NoFaults
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, orders, gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 1,
		Send: []string{"place"}, Ints: []int{1}, Peers: []*grist.App{bank, ledger}}); err != nil {
		t.Fatal(err)
	}
	native := sink.Steps()
	canonical, err := gristtest.ProtocolSteps(native, orders, bank, ledger)
	if err != nil {
		t.Fatal(err)
	}
	const serial = "testdata/protocol/serial.binding.yaml"
	qtest.Validate(t, serial, canonical).Expect(t, qtest.Conforms(), qtest.Settles())
	checker, err := validate.NewChecker(serial, validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	strict, err := gristtest.CheckProtocolOrders(t.Context(), native, 32, func(ctx context.Context, steps []qtrace.Step) (bool, error) {
		report, err := checker.Check(ctx, steps)
		if err != nil {
			return false, err
		}
		return report.OK(), nil
	}, orders, bank, ledger)
	if err != nil || strict.Exhaustive || strict.Checked < 2 || len(strict.Counterexample) == 0 {
		t.Fatalf("want a noncanonical counterexample: %+v, %v", strict, err)
	}
	qtest.Validate(t, serial, strict.Counterexample).Expect(t, qtest.Rejects("ordersPlace"))
	for _, s := range strict.Counterexample {
		if !s.Settled && (s.Source == "" || s.Obs["protocol.recorder"] == nil || s.Obs["protocol.seq"] == nil) {
			t.Fatalf("counterexample lost identity: %+v", s)
		}
	}
	faithful, err := validate.NewChecker("testdata/protocol/chain.binding.yaml", validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer faithful.Close()
	checked, err := gristtest.CheckProtocolOrders(t.Context(), native, 32, func(ctx context.Context, steps []qtrace.Step) (bool, error) {
		report, err := faithful.Check(ctx, steps)
		if err != nil {
			return false, err
		}
		return report.OK(), nil
	}, orders, bank, ledger)
	if err != nil || !checked.Exhaustive || checked.Checked != 5 || len(checked.Counterexample) != 0 {
		t.Fatalf("faithful concurrent protocol: %+v, %v", checked, err)
	}
	t.Logf("strict model failed ordering %d; faithful protocol passed all %d orderings", strict.Checked, checked.Checked)
}
