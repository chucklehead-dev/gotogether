package counters

import (
	"encoding/json"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// callsPerKey is the most calls of action any one key had in a run of x
// (a call's completed step; its invocation record aside).
func callsPerKey(x *qtest.Exploration, action string) int {
	most := 0
	for _, r := range x.Runs {
		n := map[any]int{}
		for _, s := range r.Steps {
			if s.Action == action && !s.Invocation {
				n[s.Args["key"]]++
				most = max(most, n[s.Args["key"]])
			}
		}
	}
	return most
}

// rewound reports whether every run of x recorded a rewind
// (gristRewound), judged or not (a run whose validation stopped at an
// earlier rejection does not take it).
func rewound(x *qtest.Exploration) bool {
	for _, r := range x.Runs {
		seen := false
		for _, s := range r.Steps {
			seen = seen || s.Action == "gristRewound"
		}
		if !seen {
			return false
		}
	}
	return len(x.Runs) > 0
}

// repeatedPaymentAfterRewind checks captured history independently of where
// model validation first stops. Every run must rewind without an operator
// resume; at least one canonical actor trace must complete a payment under the
// same full immutable key and trigger identity before and after that rewind.
func repeatedPaymentAfterRewind(x *qtest.Exploration) bool {
	witness := false
	for _, run := range x.Runs {
		traces, issues := qtrace.Reconstruct(run.Steps)
		for _, issue := range issues {
			if issue.Severity == "error" {
				return false
			}
		}
		seenRewind := false
		for _, trace := range traces {
			if trace.Actor != "counters/t1" {
				continue
			}
			before, applied := map[string]string{}, map[string]string{}
			for _, step := range trace.Steps {
				switch step.Action {
				case "gristResumed":
					return false
				case "gristRewound":
					var cps map[string]int64
					raw, _ := step.Args["cps"].(string)
					if json.Unmarshal([]byte(raw), &cps) != nil {
						return false
					}
					checkpoint, present := cps["payout"]
					if !present || checkpoint != 0 || step.Args["pos"] != int64(2) || step.Args["kept"] != int64(2) {
						return false // retirement retained; payout checkpoint lost
					}
					seenRewind = true
				case "bankPayServed":
					if !seenRewind && step.Outcome == "ok" {
						key, _ := step.Args["key"].(string)
						trigger, _ := step.Args["triggerId"].(string)
						applied[key] = trigger
					}
				case "bankPay":
					if step.Invocation {
						continue
					}
					key, _ := step.Args["key"].(string)
					trigger, _ := step.Args["triggerId"].(string)
					if key == "" || trigger == "" {
						return false
					}
					if seenRewind {
						witness = witness || before[key] == trigger && applied[key] == trigger
					} else {
						before[key] = trigger
					}
				}
			}
		}
		if !seenRewind {
			return false
		}
	}
	return witness
}

// Queries recorded after the actual rewind must carry that history's clock.
// Use capture order and source time independently of the order that the model
// reconstructs from those clocks; otherwise a stale clock could prove itself.
func queriesPastRewind(x *qtest.Exploration) bool {
	for _, run := range x.Runs {
		var boundary qtrace.Step
		seenRewind, seenQuery := false, false
		for _, step := range run.Steps {
			if step.Actor != "counters/t1" {
				continue
			}
			if step.Action == "gristRewound" {
				boundary, seenRewind = step, true
			}
			if seenRewind && step.Action == "showCounter" && !step.Invocation && !step.Time.Before(boundary.Time) {
				seenQuery = true
				readPos, ok := step.Args["readPos"].(int64)
				kept, _ := boundary.Args["pos"].(int64)
				if !ok {
					return false
				}
				required, anchored := boundary.Order["pos"], readPos <= kept
				for _, commit := range run.Steps {
					if commit.Actor != step.Actor || commit.Obs["head"] != readPos || commit.Time.Before(boundary.Time) || commit.Time.After(step.Time) {
						continue
					}
					var events []string
					raw, _ := commit.Obs["events"].(string)
					if json.Unmarshal([]byte(raw), &events) == nil && len(events) > 0 {
						// A query reading this event head follows the actual
						// append, even when its row was relayed later.
						required, anchored = max(required, commit.Order["pos"]), true
					}
				}
				if !anchored || step.Order["pos"] <= required {
					return false
				}
			}
		}
		if !seenRewind || !seenQuery {
			return false
		}
	}
	return len(x.Runs) > 0
}

func TestRepeatedPaymentWitnessUsesCanonicalHistory(t *testing.T) {
	for _, mode := range []string{"canonical", "out of recording order", "no rewind", "resumed", "different key", "different trigger", "checkpoint retained", "checkpoint unrecorded", "trigger lost", "not applied", "duplicate identity"} {
		t.Run(mode, func(t *testing.T) {
			payment := func(seq uint64) qtrace.Step {
				return qtrace.Step{Actor: "counters/t1", Recorder: "captured", Process: "node", Thread: "thread", Seq: seq, Action: "bankPay", Args: map[string]any{"key": "payout/2@immutable-event", "triggerId": "immutable-event"}}
			}
			site := payment(1)
			site.Action, site.Outcome = "bankPayServed", "ok"
			rewind := qtrace.Step{Actor: "counters/t1", Recorder: "captured", Process: "node", Thread: "thread", Seq: 3, Action: "gristRewound", Args: map[string]any{"pos": int64(2), "kept": int64(2), "cps": `{"payout":0}`}}
			steps := []qtrace.Step{site, payment(2), rewind, payment(4)}
			switch mode {
			case "out of recording order":
				steps[0], steps[3] = steps[3], steps[0]
			case "no rewind":
				steps[2].Action = "unrelated"
			case "resumed":
				steps[3].Seq = 5
				resume := rewind
				resume.Action, resume.Seq = "gristResumed", 4
				steps = append(steps, resume)
			case "different key":
				steps[3].Args["key"] = "payout/2@replacement-event"
			case "different trigger":
				steps[3].Args["triggerId"] = "replacement-event"
			case "checkpoint retained":
				steps[2].Args["cps"] = `{"payout":2}`
			case "checkpoint unrecorded":
				steps[2].Args["cps"] = `{}`
			case "trigger lost":
				steps[2].Args["pos"] = int64(1)
			case "not applied":
				steps[0].Outcome = "replayed"
			case "duplicate identity":
				steps[3].Seq = 2
				steps[3].Args["key"] = "another-record"
			}
			want := mode == "canonical" || mode == "out of recording order"
			got := repeatedPaymentAfterRewind(&qtest.Exploration{Runs: []*qtest.Run{{Steps: steps}}})
			if got != want {
				t.Fatalf("captured payment witness=%v, want %v", got, want)
			}
		})
	}
}

// TestRewindFailsClosed: GR10 slice 3's at-most-once acceptance. The
// store loses the payout checkpoint after the bank applied the payment,
// whose response is lost. A delayed, valid mail delivery supplies the next
// commit: the scripted failover drops that commit and the payout checkpoint,
// retaining the retirement trigger. No processor is disabled. Every node sees
// its log go back (gristRewound) and stops the tenant's processors
// (grist.ErrRewound), no operator resuming it here. The payout's trigger,
// back in the log after its checkpoint, is not paid again: one call of
// its key, and no run rejects the payout's call (its lost outcome,
// everyPayoutOutcomeLogged, is the tail's loss). Captured transitions conform,
// including a late completion; eventual outcomes remain unjudged while paused.
// With the fail-closed
// ablated (gristsim.Managed.FailOpen: the processors go on after the
// rewind, as a grist without ErrRewound would) the payout runs again
// unasked: two calls of one key (the bank, Keyed, replays the second),
// some run rejecting a processor step while the log is rewound (bankPay
// or welcome, whichever validation reaches first). The captured history
// independently proves the repeated payout completed after the rewind.
func TestRewindFailsClosed(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	runs := qtest.ExploreConfig{Seeds: 3, ShortSeeds: 2}
	runs.Chooser = func(seed uint64) qenv.Chooser {
		base := qenv.NewSeeded(seed)
		base.Rates = qenv.NoFaults
		return &qenv.Script{Base: base, Faults: []qenv.Scripted{
			{Kind: "rpc.mailer.delay", Name: "slow"},
			{Kind: "rpc.bank.response", Name: "lost"},
			{Kind: "store.counters.failover", Name: "loseTail", Follows: []int{1}},
		}}
	}
	cfg := payOnce
	cfg.Send = []string{"show-counter"} // no extra refusal-marker commit in the lost tail
	cfg.Store = &gristsim.Managed{Failover: &qenv.Failover{LoseTail: 1}}
	closed := system(exactly, cfg).WithRates(qenv.NoFaults).Explore(t, runs)
	closed.Expect(t, qtest.TransitionsConform("counters/t1"), qtest.None(qtest.Settles()), qtest.None(qtest.Rejects("bankPay")), qtest.Reaches("store.counters.failover").Faulted(), qtest.Reaches("rpc.mailer.delay").Faulted(), qtest.Reaches("rpc.bank.response").Faulted())
	if n := callsPerKey(closed, "bankPay"); n != 1 || !rewound(closed) {
		t.Errorf("fail closed: a payout key called %d times in a run (want once), every run rewound: %v", n, rewound(closed))
	}
	open := cfg
	open.Store = &gristsim.Managed{Failover: &qenv.Failover{LoseTail: 1}, FailOpen: true}
	ablated := system(exactly, open).WithRates(qenv.NoFaults).Explore(t, runs)
	if n := callsPerKey(ablated, "bankPay"); n != 2 {
		t.Errorf("fail open: a payout key called at most %d times in a run, want twice", n)
	}
	if !repeatedPaymentAfterRewind(ablated) {
		t.Error("fail open: no canonical trace proves a completed payment repeated under its original key and trigger identity after a rewind without resume")
	}
	if !queriesPastRewind(ablated) {
		t.Error("fail open: post-rewind queries did not retain the native history clock")
	}
	ablated.Expect(t, qtest.Some(qtest.Not(qtest.TransitionsConform("counters/t1"))), qtest.Some(qtest.AnyOf(qtest.Rejects("bankPay"), qtest.Rejects("welcome"))), qtest.Reaches("store.counters.failover").Faulted(), qtest.Reaches("rpc.mailer.delay").Faulted(), qtest.Reaches("rpc.bank.response").Faulted())
}
