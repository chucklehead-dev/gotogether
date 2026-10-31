package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

const initialChoicesModel = `module initialChoices {
  type Tag = A(int) | B
  var phase: int
  var chosen: int
  action init = %s
  action advance = all { phase == 0, phase' = 1, chosen' = chosen }
  action finish(wanted: int): bool = all { phase == 1, chosen == wanted, phase' = 2, chosen' = chosen }
  action probe(wanted: int): bool = all { chosen == wanted, phase' = phase, chosen' = chosen }
  action forget = all { phase' = 2, chosen' = 0 }
  action step = any { advance, forget, { nondet wanted = (-2).to(2).oneOf() finish(wanted) }, { nondet wanted = (-2).to(2).oneOf() probe(wanted) } }
  val safe = chosen >= 0
  val nonPositive = chosen <= 0
  val phaseBounded = 0.to(2).contains(phase)
}`

const initialChoicesBoth = `any { all { phase' = 0, chosen' = -1 }, all { phase' = 0, chosen' = 1 } }`

func initialChoicesChecker(t *testing.T, init, properties string, o Options) *Checker {
	t.Helper()
	skip.Quint(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.qnt"), []byte(fmt.Sprintf(initialChoicesModel, init)), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := `spec: model.qnt
module: initialChoices
actions:
  advance:
    expect: 'phase == {{.obs.phase}}'
  finish:
    args: {wanted: '{{.wanted}}'}
    expect: 'phase == {{.obs.phase}}'
  probe:
    args: {wanted: '{{.wanted}}'}
    expect: 'phase == {{.obs.phase}}'
  forget:
    expect: 'phase == {{.obs.phase}}'
invariants: [` + properties + `]
simulate:
  skip: finite initialization acceptance model admits hidden unsafe states
`
	path := filepath.Join(dir, "binding.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	o.Dir = t.TempDir()
	c, err := NewChecker(path, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// initialChoicesOracle enumerates the two declared roots and retains property
// history on each complete path, independently of qeval and generated replay.
func initialChoicesOracle(history []qtrace.Step, positive, nonpositive bool) (conforms, satisfies bool) {
	type state struct {
		phase, chosen int64
		good          bool
	}
	good := func(chosen int64) bool { return (!positive || chosen >= 0) && (!nonpositive || chosen <= 0) }
	states := []state{{0, -1, good(-1)}, {0, 1, good(1)}}
	for _, step := range history {
		if step.Settled {
			continue
		}
		var next []state
		for _, s := range states {
			n := s
			switch step.Action {
			case "advance":
				if s.phase != 0 {
					continue
				}
				n.phase = 1
			case "finish":
				if s.phase != 1 || s.chosen != step.Args["wanted"].(int64) {
					continue
				}
				n.phase = 2
			case "probe":
				if s.chosen != step.Args["wanted"].(int64) {
					continue
				}
			case "forget":
				n.phase, n.chosen = 2, 0
			default:
				panic("unknown initial-choice oracle action: " + step.Action)
			}
			if n.phase == step.Obs["phase"].(int64) {
				n.good = s.good && good(n.chosen)
				next = append(next, n)
			}
		}
		states = next
	}
	for _, s := range states {
		conforms, satisfies = true, satisfies || s.good
	}
	return conforms, satisfies
}

func TestInitialChoicesAgainstBruteForce(t *testing.T) {
	c := initialChoicesChecker(t, initialChoicesBoth, "safe, phaseBounded", Options{Eval: "go", NoITF: true})
	var histories [][]qtrace.Step
	for _, wanted := range []int64{-2, -1, 0, 1, 2} {
		histories = append(histories,
			witnessHistory(witnessObserved("advance", 1), witnessObserved("finish", 2, wanted)),
			witnessHistory(witnessObserved("probe", 0, wanted), witnessObserved("forget", 2)),
		)
	}
	// Individually admissible observations cannot borrow different initial roots.
	histories = append(histories,
		witnessHistory(witnessObserved("probe", 0, -1), witnessObserved("probe", 0, 1)),
		witnessHistory(witnessObserved("advance", 2), witnessObserved("finish", 2, 1)),
		witnessHistory(witnessObserved("forget", 2)),
	)
	reports, errs := c.CheckBatch(t.Context(), histories, 0)
	for i, history := range histories {
		if errs[i] != nil {
			t.Errorf("history %d: %v", i, errs[i])
			continue
		}
		conforms, satisfies := initialChoicesOracle(history, true, false)
		want := Fail
		if conforms {
			want = Pass
		}
		if len(reports[i].Actors) != 1 || reports[i].Actors[0].Conforms != want || reports[i].OK() != satisfies {
			t.Errorf("history %d: oracle conforms=%t satisfies=%t, report:\n%s", i, conforms, satisfies, reports[i].Summary())
			continue
		}
		choices := reports[i].Actors[0].Choices
		if choices == nil || !choices.Exhaustive || conforms && choices.Confirmation != "path_confirmed" {
			t.Errorf("history %d lacks complete search/selected-path evidence: %+v", i, choices)
		}
	}
}

func TestInitialChoicesSelectNonFirstRootAcrossPolicies(t *testing.T) {
	for _, policy := range []string{"go", "quint", "shadow"} {
		t.Run(policy, func(t *testing.T) {
			init := initialChoicesBoth
			if policy == "quint" {
				init = `{ nondet value = Set(-1, 1).oneOf(); all { phase' = 0, chosen' = value } }`
			}
			c := initialChoicesChecker(t, init, "safe, phaseBounded", Options{Eval: policy})
			report, err := c.Check(t.Context(), witnessHistory(witnessObserved("advance", 1), witnessObserved("finish", 2, 1)))
			if err != nil || !report.OK() {
				t.Fatalf("later observation did not select the positive root: err=%v report=%v", err, report)
			}
			choices := report.Actors[0].Choices
			if choices == nil || choices.Initialization == nil || choices.Initialization.Action != "init" || choices.Initialization.Expression == "" || choices.Confirmation != "path_confirmed" {
				t.Fatalf("selected initialization was not explicitly confirmed: %+v", choices)
			}
			if policy == "quint" && !strings.Contains(choices.Initialization.Expression, "contains") {
				t.Fatalf("pinned initialization omitted its original finite domain: %s", choices.Initialization.Expression)
			}
		})
	}
}

func TestInitialChoicesPreserveInitialPropertyHistory(t *testing.T) {
	c := initialChoicesChecker(t, initialChoicesBoth, "safe, nonPositive, phaseBounded", Options{Eval: "shadow"})
	report, err := c.Check(t.Context(), witnessHistory(witnessObserved("forget", 2)))
	if err != nil || len(report.Actors) != 1 || report.Actors[0].Conforms != Pass || report.OK() {
		t.Fatalf("merged initial histories satisfied incompatible properties: err=%v report=%v", err, report)
	}
	conflicting := 0
	for _, property := range report.Actors[0].Invariants {
		if property.Name == "phaseBounded" && property.Verdict != Pass {
			t.Fatal("unrelated property was blamed for initial-state conflict")
		}
		if property.Name == "safe" || property.Name == "nonPositive" {
			conflicting++
			if property.Verdict != Fail || property.Failure == nil || !strings.Contains(property.Failure.Note, "joint") {
				t.Fatalf("initial conflict was not retained after state merge: %+v", property)
			}
		}
	}
	if conflicting != 2 {
		t.Fatalf("initial joint conflict omitted properties: %+v", report.Actors[0].Invariants)
	}
	positive, err := c.WithInvariants("safe", "phaseBounded")
	if err != nil {
		t.Fatal(err)
	}
	forced, err := positive.Check(t.Context(), witnessHistory(witnessObserved("probe", 0, -1), witnessObserved("forget", 2)))
	if err != nil || forced.OK() || len(forced.Actors) != 1 || forced.Actors[0].Conforms != Pass {
		t.Fatalf("forced initial violation disappeared: err=%v report=%v", err, forced)
	}
	found := false
	for _, property := range forced.Actors[0].Invariants {
		if property.Name == "safe" {
			found = true
			if property.Verdict != Fail || property.Failure == nil || property.Failure.Index != -1 {
				t.Fatalf("initial violation moved to a later observed step: %+v", property)
			}
		}
	}
	if !found {
		t.Fatal("forced initial violation omitted the safe property")
	}
}

// An actor with no observed calls exercises roots alone; ordinary reconstruction
// deliberately does not invent an actor from an empty input log.
func initialChoicesOnly(t *testing.T, c *Checker) *Result {
	t.Helper()
	tr := &qtrace.Trace{Actor: "probe"}
	p := &binding.Plan{Actor: "probe"}
	o, err := c.Options.resolve()
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.checkChoices(t.Context(), tr, p, newResult(c.Binding, tr, p), o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInitialChoicesRootBudgetAndDeduplication(t *testing.T) {
	for _, limit := range []int{1, 2} {
		c := initialChoicesChecker(t, initialChoicesBoth, "phaseBounded", Options{ChoiceLimit: limit})
		r := initialChoicesOnly(t, c)
		if limit == 1 {
			if r.Conforms != NotEvaluated || r.Choices == nil || r.Choices.Status != "exhausted" || r.Choices.Explored != 1 || r.Choices.Exhaustive || r.Choices.Confirmation != "not_performed" {
				t.Fatalf("initial roots exceeded their budget or became a verdict: %+v", r.Choices)
			}
		} else if r.Conforms != Pass || r.Choices == nil || r.Choices.Explored != 2 || r.Choices.Confirmation != "path_confirmed" {
			t.Fatalf("exact root budget did not admit both initial states: %+v", r.Choices)
		}
	}
	duplicate := `any { all { phase' = 0, chosen' = 1 }, all { phase' = 0, chosen' = 1 } }`
	c := initialChoicesChecker(t, duplicate, "safe, phaseBounded", Options{ChoiceLimit: 1})
	if r := initialChoicesOnly(t, c); r.Conforms != Pass || r.Choices == nil || r.Choices.Explored != 1 {
		t.Fatalf("equivalent initial roots consumed multiple slots: %+v", r.Choices)
	}
}

func TestInitialChoicesNoCallsStillChecksJointProperties(t *testing.T) {
	c := initialChoicesChecker(t, initialChoicesBoth, "safe, nonPositive, phaseBounded", Options{})
	r := initialChoicesOnly(t, c)
	if r.Conforms != Pass || passed(r) || r.Choices == nil || r.Choices.Confirmation != "path_confirmed" {
		t.Fatalf("initial-only conflicting properties passed: %+v", r)
	}
}

func TestInitialChoicesDisabledInitHasNoConfirmation(t *testing.T) {
	init := `{ nondet value = Set(-1, 1).oneOf(); all { value == 99, phase' = 0, chosen' = value } }`
	c := initialChoicesChecker(t, init, "phaseBounded", Options{})
	r := initialChoicesOnly(t, c)
	if r.Conforms != NotEvaluated || r.Choices == nil || r.Choices.Status != "unsupported" || r.Choices.Confirmation != "not_performed" {
		t.Fatalf("no admitted initialization became a confirmed result: %+v", r)
	}
}

func TestInitialChoicesUnrenderableInitializerHasNoVerdict(t *testing.T) {
	init := `{ nondet value = Set(-1, 1).oneOf(); all { phase' = 0, chosen' = match A(value) { A(v) => v | B => 0 } } }`
	c := initialChoicesChecker(t, init, "phaseBounded", Options{})
	r := initialChoicesOnly(t, c)
	if r.Conforms != NotEvaluated || r.Choices == nil || r.Choices.Status != "unsupported" || r.Choices.Confirmation != "not_performed" || r.Choices.Initialization != nil {
		t.Fatalf("unrenderable initializer became an inferred confirmed state: %+v", r.Choices)
	}
	for _, property := range r.Invariants {
		if property.Verdict == Fail {
			t.Fatalf("unsupported initialization became a property failure: %+v", property)
		}
	}
}
