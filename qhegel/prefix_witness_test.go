package qhegel

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func pinnedPrefixRun(negative bool) []qtrace.Step {
	step := func(action string, phase int64, wanted ...int64) qtrace.Step {
		s := qtrace.Step{Action: action, Obs: map[string]any{"phase": phase}}
		if len(wanted) > 0 {
			s.Args = map[string]any{"wanted": wanted[0]}
		}
		return s
	}
	// The first action can choose -1 or 1. The next action forces which
	// choice belonged to this complete run, including its earlier invariant.
	run := []qtrace.Step{step("riskyPick", 1), step("finish", 2, -1)}
	if !negative {
		run = []qtrace.Step{step("riskyPick", 1), step("finish", 2, 1), step("reset", 0), step("guardedPick", 1), step("finish", 2, 1)}
	}
	for i := range run {
		run[i].Recorder = "capture"
		run[i].Actor = "probe"
		run[i].Seq = uint64(i + 1)
	}
	return run
}

// A selected finite-choice path's invariant failure can belong to an earlier
// step without rejecting every execution of that observed prefix. A longer
// candidate can select the safe choice instead. Check and Batched's Prefetch
// must agree with native direct validation, while still caching complete runs.
func TestInvariantWitnessDoesNotRejectOtherPrefixExecutions(t *testing.T) {
	qtest.RequireQuint(t)
	for _, eval := range []string{"go", "quint", "shadow"} {
		for _, prefetch := range []bool{false, true} {
			t.Run(eval+map[bool]string{false: "/Check", true: "/Prefetch"}[prefetch], func(t *testing.T) {
				c, err := validate.NewChecker(t.Context(), "../validate/testdata/witness/binding.yaml", validate.Options{Dir: t.TempDir(), Eval: eval, NoITF: true})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				bad, good := pinnedPrefixRun(true), pinnedPrefixRun(false)
				report, err := c.Check(t.Context(), bad)
				if err != nil || report == nil || report.OK() {
					t.Fatalf("unsafe selected path: %v %v", report, err)
				}
				negativeStatus := receipt.Fail
				if eval != "go" {
					negativeStatus = receipt.Inconclusive
				}
				if len(report.Actors) != 1 || report.Actors[0].Choices == nil || report.Actors[0].JudgedBy() != "evaluator" {
					t.Fatalf("finite-choice conclusion lost its Go origin:\n%s", report.Summary())
				}
				r := report.Actors[0]
				if eval == "go" {
					if r.Conforms != validate.Pass || r.Choices.Confirmation != "path_confirmed" {
						t.Fatalf("unsafe path was not a confirmed conforming witness:\n%s", report.Summary())
					}
					found := false
					for _, inv := range r.Invariants {
						if inv.Name == "safe" && inv.Verdict == validate.Fail && inv.Failure != nil && inv.Failure.Step != nil && inv.Failure.Step.Seq == bad[0].Seq {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing invariant failure at shared first step:\n%s", report.Summary())
					}
				} else {
					if r.Conforms != validate.NotEvaluated || r.Choices.Confirmation != "not_performed" || !r.Choices.Exhaustive {
						t.Fatalf("strict policy turned Go-only absence into a verdict:\n%s", report.Summary())
					}
					for _, inv := range r.Invariants {
						if inv.Verdict != validate.NotEvaluated {
							t.Fatalf("strict policy retained an unconfirmed property verdict: %+v", inv)
						}
					}
				}
				report, err = c.Check(t.Context(), good)
				if err != nil || report == nil || !report.OK() {
					t.Fatalf("safe longer run must directly pass: %v %v", report, err)
				}
				cc := newCaseChecker(c)
				check := func(run []qtrace.Step) verdict {
					t.Helper()
					if prefetch {
						vs, err := cc.Prefetch(t.Context(), [][]qtrace.Step{run})
						if err != nil {
							t.Fatal(err)
						}
						return vs[0]
					}
					v, err := cc.Check(t.Context(), run)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				if v := check(bad); v.OK || v.Status != negativeStatus || v.Cached {
					t.Fatalf("lost native unsafe witness: %+v", v)
				}
				if v := check(good); !v.OK || v.Status != receipt.Pass {
					t.Fatalf("unsafe selected witness rejected safe longer run: %+v", v)
				}
				// Even an exhaustive finite-choice conformance rejection is not
				// used as a prefix certificate by this conservative cache.
				rejected := pinnedPrefixRun(true)
				rejected[1].Args = map[string]any{"wanted": int64(2)}
				report, err = c.Check(t.Context(), rejected)
				wantConforms := validate.Fail
				if eval != "go" {
					wantConforms = validate.NotEvaluated
				}
				if err != nil || report == nil || len(report.Actors) != 1 || report.Actors[0].Conforms != wantConforms || report.Actors[0].Choices == nil {
					t.Fatalf("expected native finite-choice conformance rejection: %v %v", report, err)
				}
				if v := check(rejected); v.OK || v.Status != negativeStatus || v.Cached {
					t.Fatalf("conformance rejection passed: %+v", v)
				}
				extended := append(append([]qtrace.Step(nil), rejected...), good[2])
				if v := check(extended); v.OK || v.Status != negativeStatus || v.Cached {
					t.Fatalf("finite-choice rejection became a prefix certificate: %+v", v)
				}
				for _, run := range []struct {
					steps  []qtrace.Step
					status receipt.Status
				}{{bad, negativeStatus}, {rejected, negativeStatus}, {good, receipt.Pass}} {
					if v := check(run.steps); v.OK != (run.status == receipt.Pass) || v.Status != run.status || !v.Cached {
						t.Fatalf("complete-run cache changed: %+v", v)
					}
				}
			})
		}
	}
}

// Retain the sound optimization for a deterministic transition that cannot
// occur, independently of what an extension does after that disabled step.
func TestDeterministicConformancePrefixStillCached(t *testing.T) {
	qtest.RequireQuint(t)
	for _, eval := range []string{"go", "quint"} {
		t.Run(eval, func(t *testing.T) {
			c, err := validate.NewChecker(t.Context(), "testdata/bank.yaml", validate.Options{Dir: t.TempDir(), Eval: eval, NoITF: true})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			bad := []qtrace.Step{{Action: "deposit", Recorder: "capture", Seq: 1, Args: map[string]any{"account": "alice", "amount": int64(0)}, Obs: map[string]any{"alice": int64(0), "bob": int64(0)}}}
			report, err := c.Check(t.Context(), bad)
			if err != nil || report == nil || len(report.Actors) != 1 || report.Actors[0].Conforms != validate.Fail || report.Actors[0].Choices != nil {
				t.Fatalf("expected choice-free conformance rejection: %v %v", report, err)
			}
			cc := newCaseChecker(c)
			if v, err := cc.Check(t.Context(), bad); err != nil || v.OK {
				t.Fatalf("disabled transition: %+v %v", v, err)
			}
			extended := append(append([]qtrace.Step(nil), bad...), qtrace.Step{Action: "deposit", Recorder: "capture", Seq: 2, Args: map[string]any{"account": "alice", "amount": int64(1)}, Obs: map[string]any{"alice": int64(1), "bob": int64(0)}})
			if v, err := cc.Check(t.Context(), extended); err != nil || v.OK || !v.Cached {
				t.Fatalf("sound transition prefix was not reused: %+v %v", v, err)
			}
		})
	}
}
