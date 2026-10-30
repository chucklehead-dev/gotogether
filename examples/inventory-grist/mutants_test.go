package checkout

import (
	"bytes"
	"encoding/json"
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
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
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

// The local-only assertion in TestMutant must still pass: this defect is
// exposed by the system model. Its explanation must also survive composition
// assessment and the same JSON representation consumed by tools and the TUI.
func assertMissingReleaseComposition(t *testing.T, checks gristtest.Composition, native []qtrace.Step) {
	t.Helper()
	assessed, err := checks.Assess(t.Context(), native)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(assessed)
	if err != nil {
		t.Fatal(err)
	}
	var composition receipt.Composition
	if err := json.Unmarshal(raw, &composition); err != nil {
		t.Fatal(err)
	}
	if len(composition.Local) != 3 || len(composition.Peers) != 2 {
		t.Fatalf("composition omitted a local model or peer edge: %+v", composition)
	}
	for _, rows := range [][]receipt.Component{composition.Local, composition.Peers} {
		for _, row := range rows {
			if row.Status != receipt.Pass && !(row.Status == receipt.NotChecked && !row.Applicable) {
				t.Fatalf("missing release was incorrectly blamed on a local model or peer link: %+v", row)
			}
		}
	}
	if composition.Protocol.Status != receipt.Fail || len(composition.Protocol.Counterexample) == 0 || composition.Protocol.Judgment == nil {
		t.Fatalf("composition lost the system-model counterexample: %+v", composition.Protocol)
	}
	for _, actor := range composition.Protocol.Judgment.Actors {
		for _, d := range actor.Diagnostics {
			if d.Property != "allIntentsAnswered" {
				continue
			}
			if d.Actor != "protocol/t1" || d.Cause != "defect" {
				t.Fatalf("composition changed the diagnostic scope or cause: %+v", d)
			}
			if d.Status == "unavailable" && os.Getenv("QUINT_BACKEND") == "typescript" && d.Reason != "" {
				return // This backend has no exact state from a failing-test ITF.
			}
			if d.Status != "available" {
				t.Fatalf("composition lost diagnostic availability: %+v", d)
			}
			for _, item := range d.Items {
				if item.Kind == "cancellation" && item.Subject == "a" && item.Intent == "t1-c1-1" {
					steps, err := gristtest.ProtocolSteps(native, App, inventory.App, fulfillment.App)
					if err != nil {
						t.Fatal(err)
					}
					assertCancellationEvidence(t, item, native, steps)
					return
				}
			}
		}
	}
	t.Fatal("composition JSON lost the model-authored unanswered cancellation intent")
}

// The diagnostic must identify the actual accepted cancellation commit, not an
// arbitrary record sharing its order name or a fabricated missing peer call.
func assertCancellationEvidence(t *testing.T, item diagnostic.Item, native, projected []qtrace.Step) {
	t.Helper()
	if item.Evidence.Status != "resolved" || len(item.Evidence.Matches) != 1 {
		t.Fatalf("cancellation evidence was not uniquely resolved: %+v", item.Evidence)
	}
	match := item.Evidence.Matches[0]
	if match.Actor != "protocol/t1" || match.At <= 0 {
		t.Fatalf("evidence lost its checked actor or replay position: %+v", match)
	}
	var origin, captured *qtrace.Step
	for i := range native {
		s := &native[i]
		if s.Actor == "checkout/t1" && s.Recorder == grist.RecorderID("checkout", "t1") && s.Action == "cancel" && s.Args["id"] == item.Intent && s.Args["order"] == item.Subject {
			if origin != nil {
				t.Fatal("fixture has multiple originating cancellation commits")
			}
			origin = s
		}
	}
	for i := range projected {
		s := &projected[i]
		if s.Actor == "protocol/t1" && s.Action == "checkoutCancel" && s.Args["id"] == item.Intent && s.Args["order"] == item.Subject {
			captured = s
		}
	}
	if origin == nil || captured == nil {
		t.Fatalf("fixture lost native or projected cancellation: native=%+v projected=%+v", origin, captured)
	}
	if match.Record.Actor != captured.Actor || match.Record.Recorder != captured.Recorder || match.Record.Seq != captured.Seq {
		t.Fatalf("evidence did not identify the captured projected cancellation: got %+v, want %s %s seq %d", match.Record, captured.Actor, captured.Recorder, captured.Seq)
	}
	if match.Origin.Actor != origin.Actor || match.Origin.Recorder != origin.Recorder || match.Origin.Seq != origin.Seq {
		t.Fatalf("evidence lost the exact original commit identity: got %+v, want %s %s seq %d", match.Origin, origin.Actor, origin.Recorder, origin.Seq)
	}
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
		cfg.Phases = cfg.Phases[:1]
		cfg.Send = []string{"cancel"}
		e := gristsim.System(commits, App, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: quiet})
		e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		checks := compositionChecks(t)
		for _, r := range e.Runs {
			localChecks(t, r.Recorded, true)
			if count(r.Recorded, "inventory/t1", "reserve", "") != 1 || count(r.Recorded, "inventory/t1", "cancel", "") != 0 {
				t.Fatal("missing release did not leave a real held unit")
			}
			projected := saveProjection(t, r.Recorded)
			// Replay persisted evidence too: Source becomes a JSONL line, but the
			// explicit native identity remains in protocol.actor/recorder/seq.
			var encoded bytes.Buffer
			if err := qtrace.WriteJSONL(&encoded, projected); err != nil {
				t.Fatal(err)
			}
			roundtrip, err := qtrace.ReadJSONL(&encoded)
			if err != nil {
				t.Fatal(err)
			}
			for _, history := range [][]qtrace.Step{projected, roundtrip} {
				judged := qtest.Validate(t, protocol, history)
				judged.Expect(t, qtest.Violates(validate.EventuallyName("allIntentsAnswered")))
				explained := false
				for _, actor := range judged.Report.Actors {
					for _, ab := range actor.Absent {
						d := ab.Diagnostic
						if ab.Name == validate.EventuallyName("allIntentsAnswered") && d != nil && d.Status == "available" {
							for _, item := range d.Items {
								if actor.Actor == "protocol/t1" && item.Kind == "cancellation" && item.Subject == "a" && item.Intent == "t1-c1-1" {
									assertCancellationEvidence(t, item, r.Recorded, history)
									explained = true
								}
							}
						}
					}
				}
				if !explained {
					t.Fatal("missing release did not identify the model's unanswered cancellation intent")
				}
			}
			assertMissingReleaseComposition(t, checks, r.Recorded)
		}
	case "freshKey":
		cfg := config()
		cfg.Phases = nil
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
	cfg.Phases = nil
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
