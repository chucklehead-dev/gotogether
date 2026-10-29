package gristtest_test

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func protocolPair() (shop, bank *grist.App, steps []qtrace.Step) {
	shop, bank = pair()
	grist.NewCommand(shop, "place", func(r *grist.Reads, c pay) ([]grist.Event, error) { return nil, nil }, grist.Unconditioned("the fixture reads nothing"))
	key := "shop/charge/t1/1"
	steps = []qtrace.Step{
		{Action: "place", Actor: "shop/t1", Process: "shop", Recorder: grist.RecorderID("shop", "t1"), Seq: 1,
			Args: map[string]any{"id": "r1", "order": "a"}, Obs: map[string]any{"head": int64(2), "events": `["ordered","ordered"]`}},
		{Action: "pay", Actor: "bank/t1", Process: "bank", Recorder: grist.RecorderID("bank", "t1"), Seq: 1,
			Args: map[string]any{"id": key, "order": "a"}, Obs: map[string]any{"head": int64(1), "events": `["paid"]`}},
		{Action: "bankPay", Actor: "shop/t1", Process: "shop", Recorder: "caller", Seq: 1,
			Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": key, "trigger": int64(1)}},
		{Action: "settled", Recorder: "caller", Seq: 2, Record: qtrace.Record{Settled: true}},
	}
	return
}

// A pending caller is still evidence of a peer commit's cause. Its
// trigger inside a multi-event batch depends on the whole committed
// batch. Arrival order and unsynchronised service clocks do not order it.
func TestProtocolStepsCausality(t *testing.T) {
	shop, bank, steps := protocolPair()
	want, err := gristtest.ProtocolSteps(steps, shop, bank)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 3 || want[0].Action != "shopPlace" || want[1].Action != "bankPay" || !want[2].Settled ||
		want[1].Obs["protocol.parentApp"] != "shop" || want[1].Obs["protocol.parentID"] != "r1" ||
		want[1].Obs["protocol.parentPosition"] != int64(2) || want[1].Obs["protocol.trigger"] != int64(1) {
		t.Fatalf("projection: %+v; want place -> pay, cause shop/r1, and settle", want)
	}
	var log bytes.Buffer
	if err := qtrace.WriteJSONL(&log, want); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := qtrace.ReadJSONL(&log)
	if err != nil || roundTrip[1].Obs["protocol.recorder"] != grist.RecorderID("bank", "t1") || roundTrip[1].Obs["protocol.seq"] != "1" {
		t.Fatalf("projection lost native identity through JSONL: %v, %v", roundTrip, err)
	}
	// Reverse every record and swap the Apps: neither is an ordering source.
	reversed := slices.Clone(steps)
	slices.Reverse(reversed)
	got, err := gristtest.ProtocolSteps(reversed, bank, shop)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("reversed records: %v, %v; want the same projection", got, err)
	}
	// The bank's clock says it committed before the triggering order.
	// Preserve an explicit global settle after both: the clocks are not
	// evidence for cross-service order, but the native call's trigger is.
	steps[0].Time, steps[1].Time, steps[3].Time = time.Unix(100, 0), time.Unix(1, 0), time.Unix(101, 0)
	got, err = gristtest.ProtocolSteps(steps, shop, bank)
	if err != nil || got[0].Action != "shopPlace" || got[1].Action != "bankPay" || !got[2].Settled {
		t.Fatalf("skewed clocks: %v, %v; want the causal order and settle", got, err)
	}
	got[0].Args["id"], got[0].Obs["head"] = "changed", int64(9)
	if steps[0].Args["id"] != "r1" || steps[0].Obs["head"] != int64(2) {
		t.Fatal("projection mutated the original records")
	}
	// A finite prefix must not acquire a settle by projection.
	prefix, err := gristtest.ProtocolSteps(steps[:3], shop, bank)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range prefix {
		if s.Settled {
			t.Fatal("projection invented a settle for an unfinished history")
		}
	}
}

// Check the canonical extension against every permutation of four
// commits, both when requests arrive in trigger order and when delayed
// delivery reverses it. The oracle enumerates orders instead of sorting
// the graph by the implementation's ready-node algorithm.
func TestProtocolStepsAgainstBruteForce(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		shop, bank, sample := protocolPair()
		var steps []qtrace.Step
		for i := 0; i < 2; i++ {
			root := sample[0]
			root.Seq, root.Args, root.Obs = uint64(i+1), map[string]any{"id": fmt.Sprint("r", i+1)}, map[string]any{"head": int64(i + 1), "events": `["ordered"]`}
			steps = append(steps, root)
			trigger := i + 1
			if reversed {
				trigger = 2 - i
			}
			key := fmt.Sprintf("shop/charge/t1/%d", trigger)
			paid := sample[1]
			paid.Seq, paid.Args, paid.Obs = uint64(i+1), map[string]any{"id": key}, map[string]any{"head": int64(i + 1), "events": `["paid"]`}
			call := sample[2]
			call.Seq, call.Args = uint64(i+1), map[string]any{"key": key, "trigger": int64(trigger)}
			steps = append(steps, paid, call)
		}
		end := sample[3]
		end.Seq = 3
		steps = append(steps, end)
		projected, err := gristtest.ProtocolSteps(steps, shop, bank)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range projected {
			if !s.Settled {
				got = append(got, fmt.Sprintf("%s/%s", s.Obs["protocol.service"], s.Obs["protocol.seq"]))
			}
		}
		labels := []string{"bank/1", "bank/2", "shop/1", "shop/2"}
		edges := [][2]string{{"shop/1", "shop/2"}, {"bank/1", "bank/2"}}
		if reversed {
			edges = append(edges, [2]string{"shop/2", "bank/1"}, [2]string{"shop/1", "bank/2"})
		} else {
			edges = append(edges, [2]string{"shop/1", "bank/1"}, [2]string{"shop/2", "bank/2"})
		}
		var want []string
		var enumerate func([]string, []string)
		enumerate = func(prefix, remaining []string) {
			if len(remaining) == 0 {
				for _, edge := range edges {
					if slices.Index(prefix, edge[0]) >= slices.Index(prefix, edge[1]) {
						return
					}
				}
				if want == nil || strings.Join(prefix, ",") < strings.Join(want, ",") {
					want = slices.Clone(prefix)
				}
				return
			}
			for i, next := range remaining {
				rest := append(slices.Clone(remaining[:i]), remaining[i+1:]...)
				enumerate(append(slices.Clone(prefix), next), rest)
			}
		}
		enumerate(nil, labels)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reversed=%v: got %v, brute force %v", reversed, got, want)
		}
	}
}

// A late request for trigger 1 can commit after trigger 2's request.
// Causality imposes parent -> child, not a total order by trigger number.
func TestProtocolStepsLateRequest(t *testing.T) {
	shop, bank, steps := protocolPair()
	steps[1].Args["id"] = "shop/charge/t1/2"
	steps[2].Args["key"], steps[2].Args["trigger"] = "shop/charge/t1/2", int64(2)
	second := steps[1]
	second.Seq, second.Args, second.Obs = 2, map[string]any{"id": "shop/charge/t1/1"}, map[string]any{"head": int64(2), "events": `["paid"]`}
	call := steps[2]
	call.Seq, call.Args = 2, map[string]any{"key": "shop/charge/t1/1", "trigger": int64(1)}
	steps[3].Seq = 3
	steps = append(steps, second, call)
	got, err := gristtest.ProtocolSteps(steps, shop, bank)
	if err != nil || len(got) != 4 || got[1].Args["id"] != "shop/charge/t1/2" || got[2].Args["id"] != "shop/charge/t1/1" {
		t.Fatalf("late request: %v, %v; want both after place, trigger 2 before trigger 1 at bank", got, err)
	}
}

func TestProtocolStepsRefusesBadEvidence(t *testing.T) {
	for _, scenario := range []string{"gap", "missing parent", "ambiguous cause", "rewind", "cycle", "missing App", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			shop, bank, steps := protocolPair()
			apps := []*grist.App{shop, bank}
			var want string
			switch scenario {
			case "gap":
				steps[0].Seq = 2
				want = "lost-prefix"
			case "missing parent":
				steps = steps[1:]
				want = "missing trigger"
			case "ambiguous cause":
				call := steps[2]
				call.Seq, call.Args = 2, map[string]any{"key": "shop/charge/t1/1", "trigger": int64(2)}
				steps[3].Seq = 3
				steps = append(steps, call)
				want = "conflicting triggers"
			case "rewind":
				steps[0].Action = "gristRewound"
				want = "rewound"
			case "cycle":
				// A callback to place claims the bank's payment caused the
				// very order that caused that payment: inconsistent evidence.
				place := grist.NewCommand(shop, "again", func(r *grist.Reads, c pay) ([]grist.Event, error) { return nil, nil })
				grist.NewPeerCall(grist.NewPeer(bank, shop), place)
				steps[0].Action, steps[0].Args["id"] = "again", "bank/return/t1/1"
				steps = append(steps, qtrace.Step{Action: "shopAgain", Actor: "bank/t1", Recorder: "callback", Seq: 1,
					Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": "bank/return/t1/1", "trigger": int64(1)}})
				want = "causal cycle"
			case "missing App":
				apps = apps[:1]
				want = "include the App declared"
			case "empty":
				steps = nil
				want = "no committed commands"
			}
			_, err := gristtest.ProtocolSteps(steps, apps...)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: %v; want %q", scenario, err, want)
			}
		})
	}
}

// No matching call means no observed causal parent, even if the id looks
// like a perfectly formed effect key. Let the model reject that command;
// deriving a parent from its spelling would validate a forged transport.
func TestProtocolStepsUnlinked(t *testing.T) {
	shop, bank, steps := protocolPair()
	steps[1].Args["id"] = "shop/charge/t1/1/fresh"
	got, err := gristtest.ProtocolSteps(steps, shop, bank)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Action == "bankPay" && (s.Obs["protocol.parentApp"] != "" || s.Obs["protocol.parentID"] != "") {
			t.Fatalf("an unlinked commit acquired a parent: %v", s.Obs)
		}
	}
}
