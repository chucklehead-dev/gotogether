package validate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func witnessObserved(action string, phase int64, wanted ...int64) qtrace.Step {
	s := qtrace.Step{Action: action, Obs: map[string]any{"phase": phase}}
	if len(wanted) != 0 {
		s.Args = map[string]any{"wanted": wanted[0]}
	}
	return s
}

func witnessHistory(steps ...qtrace.Step) []qtrace.Step {
	out := append([]qtrace.Step(nil), steps...)
	out = append(out, qtrace.Step{Record: qtrace.Record{Settled: true}})
	for i := range out {
		out[i].Actor, out[i].Recorder, out[i].Seq = "probe", "capture", uint64(i+1)
	}
	return out
}

// witnessOracle enumerates the fixture's integer choices directly, without
// evaluator state merging, generated replay code, or the implementation search.
func witnessOracle(steps []qtrace.Step, positive, nonpositive bool) (conforms, satisfies bool) {
	type state struct {
		phase, chosen int64
		good          bool
	}
	states := []state{{good: true}}
	for _, step := range steps {
		if step.Settled {
			continue
		}
		var next []state
		for _, s := range states {
			var successors []state
			switch step.Action {
			case "pick", "anyPick", "guardedPick", "riskyPick", "donePick":
				if s.phase != 0 {
					continue
				}
				values := []int64{0, 1}
				if step.Action == "guardedPick" {
					values = []int64{1} // only this finite choice enables the action
				} else if step.Action == "riskyPick" || step.Action == "donePick" {
					values = []int64{-1, 1}
				}
				phase := int64(1)
				if step.Action == "donePick" {
					phase = 2
				}
				for _, value := range values {
					successors = append(successors, state{phase, value, s.good})
				}
			case "finish":
				if s.phase == 1 && s.chosen == step.Args["wanted"].(int64) {
					successors = append(successors, state{2, s.chosen, s.good})
				}
			case "reset":
				if s.phase == 2 {
					successors = append(successors, state{0, 0, s.good})
				}
			default:
				panic("unknown oracle action: " + step.Action)
			}
			for _, n := range successors {
				if n.phase != step.Obs["phase"].(int64) {
					continue
				}
				n.good = n.good && (!positive || n.chosen >= 0) && (!nonpositive || n.chosen <= 0)
				next = append(next, n)
			}
		}
		states = next
	}
	for _, s := range states {
		conforms = true
		satisfies = satisfies || s.good && s.phase == 2
	}
	return conforms, satisfies
}

func TestWitnessAgainstBruteForce(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/witness/binding.yaml", Options{Dir: t.TempDir(), Eval: "go", NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var histories [][]qtrace.Step
	var labels []string
	for _, action := range []string{"pick", "anyPick", "guardedPick", "riskyPick"} {
		for _, wanted := range []int64{-1, 0, 1, 2} {
			histories = append(histories, witnessHistory(witnessObserved(action, 1), witnessObserved("finish", 2, wanted)))
			labels = append(labels, fmt.Sprintf("%s then finish(%d)", action, wanted))
		}
	}
	// Repeated calls of the same choosing action need distinct pinned choices.
	histories = append(histories, witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 1), witnessObserved("reset", 0), witnessObserved("pick", 1), witnessObserved("finish", 2, 0)))
	labels = append(labels, "choose 1, reset, choose 0")
	histories = append(histories,
		witnessHistory(witnessObserved("pick", 2), witnessObserved("finish", 2, 1)),
		witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 1, 1)),
	)
	labels = append(labels, "wrong pick observation", "wrong finish observation")
	reports, errs := c.CheckBatch(t.Context(), histories, 0)
	for i, history := range histories {
		if errs[i] != nil {
			t.Errorf("%s: %v", labels[i], errs[i])
			continue
		}
		if len(reports[i].Actors) != 1 {
			t.Errorf("%s: missing actor result", labels[i])
			continue
		}
		conforms, good := witnessOracle(history, true, false)
		want := Fail
		if conforms {
			want = Pass
		}
		if reports[i].Actors[0].Conforms != want || reports[i].OK() != good {
			t.Errorf("%s: oracle conforms=%t jointly satisfies=%t; report:\n%s", labels[i], conforms, good, reports[i].Summary())
		}
		if !conforms && (reports[i].Actors[0].Choices == nil || !reports[i].Actors[0].Choices.Exhaustive) {
			t.Errorf("%s: negative conclusion lacks exhaustive model-choice search", labels[i])
		}
	}
}

func TestWitnessUsesOneJointPropertyPath(t *testing.T) {
	skip.Quint(t)
	history := witnessHistory(witnessObserved("donePick", 2))
	for _, tc := range []struct {
		binding               string
		positive, nonpositive bool
	}{
		{"binding.yaml", true, false},
		{"nonpositive.yaml", false, true},
		{"joint.yaml", true, true},
	} {
		t.Run(tc.binding, func(t *testing.T) {
			c, err := NewChecker("testdata/witness/"+tc.binding, Options{Dir: t.TempDir(), Eval: "shadow"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			report, err := c.Check(t.Context(), history)
			if err != nil {
				t.Fatal(err)
			}
			conforms, good := witnessOracle(history, tc.positive, tc.nonpositive)
			if !conforms || len(report.Actors) != 1 || report.Actors[0].Conforms != Pass || report.OK() != good {
				t.Fatalf("oracle conforms=%t jointly satisfies=%t; report:\n%s", conforms, good, report.Summary())
			}
			if tc.binding == "joint.yaml" {
				for _, property := range report.Actors[0].Invariants {
					if property.Name == "phaseBounded" && property.Verdict != Pass {
						t.Fatal("an unrelated always-true property was blamed for a joint conflict")
					}
					if property.Name == "safe" || property.Name == "nonPositive" {
						if property.Verdict != Fail || property.Failure == nil || !strings.Contains(property.Failure.Note, "joint") || !strings.Contains(property.Failure.Note, "safe") || !strings.Contains(property.Failure.Note, "nonPositive") {
							t.Fatalf("joint conflict misreported as individual universal violation: %+v", property)
						}
					}
				}
			}
		})
	}
}

func TestWitnessKeepsPropertyHistoryWhenStatesMerge(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/witness/merged-history.yaml", Options{Dir: t.TempDir(), Eval: "shadow"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Both hidden choices end at exactly phase2/chosen0 after forget. The
	// negative branch violated safe earlier, so final-state equality cannot
	// replace the property history of the positive whole-trace witness.
	history := witnessHistory(witnessObserved("pick", 1), witnessObserved("forget", 2))
	report, err := c.Check(t.Context(), history)
	if err != nil || !report.OK() {
		t.Fatalf("lost the invariant-safe history at a merged state: err=%v report=%v", err, report)
	}
	if report.Actors[0].Choices == nil || report.Actors[0].Choices.Confirmation != "path_confirmed" {
		t.Fatal("merged history lacked a confirmed selected path")
	}
	// A later guard can require the negative prefix even though forget erases
	// its state. The earlier violation must not disappear with that assignment.
	forced := witnessHistory(witnessObserved("pick", 1), witnessObserved("onlyNegative", 1), witnessObserved("forget", 2))
	bad, err := c.Check(t.Context(), forced)
	if err != nil || len(bad.Actors) != 1 || bad.Actors[0].Conforms != Pass || bad.OK() {
		t.Fatalf("erased an observed-path invariant violation: err=%v report=%v", err, bad)
	}
	found := false
	for _, property := range bad.Actors[0].Invariants {
		found = found || property.Name == "safe" && property.Verdict == Fail
	}
	if !found {
		t.Fatal("forced unsafe prefix did not retain the safe invariant failure")
	}
}

func TestWitnessBacktracksAcrossObservedSteps(t *testing.T) {
	skip.Quint(t)
	history := witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 1))
	for _, eval := range []string{"go", "quint", "shadow"} {
		t.Run(eval, func(t *testing.T) {
			c, err := NewChecker("testdata/witness/binding.yaml", Options{Dir: t.TempDir(), Eval: eval})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for range 3 {
				report, err := c.Check(t.Context(), history)
				if err != nil || !report.OK() {
					t.Fatalf("consistent whole-trace witness not replayed: err=%v report=%v", err, report)
				}
				choices := report.Actors[0].Choices
				if choices == nil || choices.Confirmation != "path_confirmed" || len(choices.Witness) < 2 || choices.Explored == 0 {
					t.Fatalf("passing path was not explicitly confirmed: %+v", choices)
				}
				seen := map[uint64]string{}
				for _, step := range choices.Witness {
					if step.Expression == "" {
						t.Fatal("witness omitted original-action replay expression")
					}
					if step.Recorder == "capture" {
						seen[step.Seq] = step.Action
					}
				}
				if seen[1] != "pick" || seen[2] != "finish" {
					t.Fatalf("witness lost observed record provenance: %+v", choices.Witness)
				}
				// Every machine-facing result must disclose the internal search;
				// an occasional randomly sampled pass is not this feature.
				data, err := json.Marshal(report.Actors[0].Doc())
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]json.RawMessage
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				if len(doc["choices"]) == 0 || string(doc["choices"]) == "null" {
					t.Fatalf("result concealed internal-choice search and witness: %s", data)
				}
			}
		})
	}
}

func TestWitnessEventuallyUsesTheInvariantPath(t *testing.T) {
	skip.Quint(t)
	history := witnessHistory(witnessObserved("donePick", 2))
	for _, binding := range []string{"eventual-conflict.yaml", "eventual-joint.yaml"} {
		t.Run(binding, func(t *testing.T) {
			c, err := NewChecker("testdata/witness/"+binding, Options{Dir: t.TempDir(), Eval: "shadow"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			report, err := c.Check(t.Context(), history)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Actors) != 1 || report.Actors[0].Conforms != Pass || report.OK() {
				t.Fatalf("combined unrelated property witnesses: %s", report.Summary())
			}
			failed := false
			for _, property := range report.Actors[0].Invariants {
				failed = failed || property.Eventually && property.Verdict == Fail
			}
			if !failed || report.Actors[0].Choices == nil || !report.Actors[0].Choices.Exhaustive {
				t.Fatalf("missing exhaustive joint eventual failure: %s", report.Summary())
			}
		})
	}
}

func TestWitnessLimitIsNotARejection(t *testing.T) {
	skip.Quint(t)
	history := witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 1))
	c, err := NewChecker("testdata/witness/binding.yaml", Options{Dir: t.TempDir(), ChoiceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	report, err := c.Check(t.Context(), history)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actors) != 1 {
		t.Fatalf("missing actor: %s", report.Summary())
	}
	actor := report.Actors[0]
	if actor.Conforms != NotEvaluated || actor.Choices == nil || actor.Choices.Exhaustive || actor.Choices.Confirmation != "not_performed" || actor.Choices.Limit != 1 {
		t.Fatalf("exhausted search became a sampled verdict: %s choices=%+v", report.Summary(), actor.Choices)
	}
	if actor.Choices.Explored > actor.Choices.Limit {
		t.Fatalf("search exceeded its declared expansion limit: %+v", actor.Choices)
	}
	for _, inv := range actor.Invariants {
		if inv.Verdict == Fail {
			t.Fatalf("search exhaustion manufactured a property failure: %+v", inv)
		}
	}
}

func TestWitnessUnsupportedPinIsNotConfirmation(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/witness/unsupported.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	report, err := c.Check(t.Context(), witnessHistory(witnessObserved("pick", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actors) != 1 {
		t.Fatalf("missing actor: %s", report.Summary())
	}
	actor := report.Actors[0]
	if actor.Conforms != NotEvaluated || actor.Choices == nil || actor.Choices.Status != "unsupported" || actor.Choices.Confirmation != "not_performed" {
		t.Fatalf("unrenderable selected path became a sampled pass: %s choices=%+v", report.Summary(), actor.Choices)
	}
	for _, property := range actor.Invariants {
		if property.Verdict == Fail {
			t.Fatalf("unsupported pin manufactured a property failure: %+v", property)
		}
	}
}

func TestWitnessDoesNotPromoteAssumedOrLostInput(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/witness/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, input := range []string{"assumed", "lost"} {
		t.Run(input, func(t *testing.T) {
			history := witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 1))
			if input == "assumed" {
				history[0].Ambiguous = true
			} else {
				history = append(history, qtrace.Step{Action: "lost", Actor: "probe", Recorder: "capture", Record: qtrace.Record{Lost: "suffix", LostAfter: 2}})
			}
			report, err := c.Check(t.Context(), history)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Actors) != 1 {
				t.Fatalf("missing actor: %s", report.Summary())
			}
			actor := report.Actors[0]
			if actor.Conforms != NotEvaluated || actor.Choices == nil || actor.Choices.Status != "unsupported" || actor.Choices.Confirmation != "not_performed" {
				t.Fatalf("unobserved input gained a confirmed model path: %s", report.Summary())
			}
			for _, property := range actor.Invariants {
				if property.Verdict == Fail {
					t.Fatalf("unsupported evidence manufactured a property failure: %+v", property)
				}
			}
		})
	}
}
