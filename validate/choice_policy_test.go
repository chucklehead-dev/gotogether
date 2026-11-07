package validate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestChoiceVerdictPolicyAndProvenance(t *testing.T) {
	skip.Quint(t)
	realQuint, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHOICE_REAL_QUINT", realQuint)
	workLog := filepath.Join(t.TempDir(), "work.log")
	t.Setenv("CHOICE_WORK_LOG", workLog)
	wrapper := filepath.Join(t.TempDir(), "quint")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nif [ \"$1\" != \"--version\" ]; then printf '%s\\n' \"$*\" >> \"$CHOICE_WORK_LOG\"; fi\nexec \"$CHOICE_REAL_QUINT\" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"go", "quint", "shadow"} {
		for _, batch := range []bool{false, true} {
			name := policy + "/Check"
			if batch {
				name += "Batch"
			}
			t.Run(name, func(t *testing.T) {
				for _, tc := range []struct {
					name, binding string
					steps         []qtrace.Step
					positive      bool
				}{
					{"rejected", "binding.yaml", witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 2)), false},
					{"joint conflict", "joint.yaml", witnessHistory(witnessObserved("donePick", 2)), false},
					{"forced invariant", "binding.yaml", witnessHistory(witnessObserved("riskyPick", 1), witnessObserved("finish", 2, -1)), false},
					{"confirmed", "binding.yaml", witnessHistory(witnessObserved("guardedPick", 1), witnessObserved("finish", 2, 1)), true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						c, err := NewChecker(t.Context(), "testdata/witness/"+tc.binding, Options{Quint: wrapper, Eval: policy, NoITF: true, Dir: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						if err := os.WriteFile(workLog, nil, 0600); err != nil {
							t.Fatal(err)
						}
						var report *Report
						if batch {
							reports, errs := c.CheckBatch(t.Context(), [][]qtrace.Step{tc.steps}, 1)
							report, err = reports[0], errs[0]
						} else {
							report, err = c.Check(t.Context(), tc.steps)
						}
						if err != nil || report == nil || len(report.Actors) != 1 {
							t.Fatalf("report: %v %v", report, err)
						}
						r := report.Actors[0]
						calls, err := os.ReadFile(workLog)
						if err != nil {
							t.Fatal(err)
						}
						wantReplay := tc.positive || (policy == "go" && tc.name != "rejected")
						if (len(calls) > 0) != wantReplay {
							t.Fatalf("unexpected Quint workload: want replay=%v, got %s", wantReplay, calls)
						}
						if r.Steps != len(tc.steps)-1 || len(r.Calls) != r.Steps {
							t.Fatalf("observed calls lost: steps=%d calls=%d", r.Steps, len(r.Calls))
						}
						if r.Choices == nil || !r.Choices.Exhaustive || r.Choices.Explored == 0 {
							t.Fatalf("bounded search evidence lost: %+v", r.Choices)
						}
						want, by := Fail, "evaluator"
						if tc.positive {
							want, by = Pass, "both"
							if r.Choices.Confirmation != "path_confirmed" {
								t.Fatal("positive path was not confirmed")
							}
						} else if policy == "go" && tc.name != "rejected" {
							if r.Choices.Confirmation != "path_confirmed" {
								t.Fatal("failing representative path lost its separate confirmation")
							}
						} else if policy != "go" {
							want = NotEvaluated
							if r.Choices.Confirmation != "not_performed" || !strings.Contains(r.Choices.Reason, "independent confirmation") {
								t.Fatalf("strict policy invented proof: %+v", r.Choices)
							}
							for _, inv := range r.Invariants {
								if inv.Verdict != NotEvaluated {
									t.Fatalf("strict negative property verdict: %+v", inv)
								}
							}
						}
						if got := report.Verdict().Verdict; got != want || r.JudgedBy() != by {
							t.Fatalf("verdict=%v/%s, want %v/%s\n%s", got, r.JudgedBy(), want, by, report.Summary())
						}
						got := report.Receipt()
						status := receipt.Fail
						if want == Pass {
							status = receipt.Pass
						} else if want == NotEvaluated {
							status = receipt.Inconclusive
						}
						if got.Status != status || got.Actors[0].JudgedBy != by {
							t.Fatalf("receipt changed scope: %+v", got)
						}
						data, err := json.Marshal(got)
						if err != nil || !strings.Contains(string(data), `"judgedBy":"`+by+`"`) {
							t.Fatalf("JSON provenance: %s %v", data, err)
						}
						m, err := NewManifest(RunRecord{Binding: c.Binding, Options: c.Options, Results: report.Actors})
						if err != nil {
							t.Fatal(err)
						}
						path := filepath.Join(t.TempDir(), "run.json")
						if err := m.Write(path); err != nil {
							t.Fatal(err)
						}
						m, err = ReadManifest(path)
						if err != nil || len(m.Verdict.Actors) != 1 || m.Verdict.Actors[0].JudgedBy != by || m.Verdict.Verdict != want {
							t.Fatalf("manifest provenance: %+v %v", m, err)
						}
					})
				}
			})
		}
	}
}

func TestChoiceExhaustionKeepsCountsAndProvenance(t *testing.T) {
	skip.Quint(t)
	for _, policy := range []string{"go", "quint", "shadow"} {
		c, err := NewChecker(t.Context(), "testdata/witness/binding.yaml", Options{Eval: policy, ChoiceLimit: 1, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		report, err := c.Check(t.Context(), witnessHistory(witnessObserved("pick", 1), witnessObserved("finish", 2, 1)))
		c.Close()
		if err != nil || len(report.Actors) != 1 {
			t.Fatalf("%s: %v %v", policy, report, err)
		}
		r := report.Actors[0]
		if report.Verdict().Verdict != NotEvaluated || r.Steps != 2 || r.JudgedBy() != "evaluator" || r.Choices.Status != "exhausted" || r.Choices.Exhaustive || r.Choices.Limit != 1 {
			t.Fatalf("%s: exhaustion lost its scope: %+v", policy, r)
		}
	}
}
