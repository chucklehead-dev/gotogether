package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

func TestPossibleResolvedGoalSemantics(t *testing.T) {
	p := possibleGoal("stock > 0", nil)
	if p.Kind != "predicate" || p.Predicate != "stock > 0" || len(p.Steps) != 0 {
		t.Fatalf("predicate: %+v", p)
	}
	sp, err := binding.ParseStepPattern(`withdraw(result = "refused", amount = 9007199254740993)`)
	if err != nil {
		t.Fatal(err)
	}
	g := possibleGoal("", []binding.StepPattern{sp})
	if g.Kind != "action_sequence" || len(g.Steps) != 1 || g.Steps[0].Action != "withdraw" || g.Steps[0].Args[1].Value != "9007199254740993" {
		t.Fatalf("sequence: %+v", g)
	}
	sp.Params[0] = "changed"
	sp.Values[0] = "changed"
	if g.Steps[0].Args[0].Name != "result" || g.Steps[0].Args[0].Value != `"refused"` {
		t.Fatal("goal aliases source pattern")
	}
	for _, tc := range []struct {
		goal PossibleGoal
		path []string
		want string
	}{{p, nil, "predicate holds initially"}, {p, []string{"deposit(amount = 5)"}, "predicate holds after deposit(amount = 5)"}, {g, []string{`withdraw(result = "refused")`}, `action sequence executed by withdraw(result = "refused")`}} {
		r := PossibleResult{Found: true, Goal: tc.goal, Path: tc.path}
		if got := r.Verdict(); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
}

func TestPossibleWitnessPreservesExactArgumentsAndAttribution(t *testing.T) {
	value := qeval.Str(strings.Repeat("long", 100))
	event := qeval.Event{Action: "deposit", Params: []string{"memo"}, Args: []qeval.Value{value}}
	w := possibleWitness(event, true)
	if w.Kind != "action" || w.Action != "deposit" || len(w.Args) != 1 || w.Args[0].Name != "memo" || w.Args[0].Value != qeval.String(value) {
		t.Fatalf("witness: %+v", w)
	}
	if strings.Contains(event.String(), string(value)) {
		t.Fatal("test requires event's abbreviated display")
	}
	unknown := possibleWitness(event, false)
	if unknown.Kind != "model_step" || unknown.Action != "" || len(unknown.Args) != 0 {
		t.Fatalf("invented attribution: %+v", unknown)
	}
}

func TestPossibleGoalSurvivesEvaluatorUnavailable(t *testing.T) {
	cs := []*possibleCheck{{p: binding.Possible{Name: "accept"}, when: "ready", quintFound: true}}
	s := &sim{rep: &Report{}}
	_, err := s.reportPossibles("app.yaml", cs, nil, errors.New("unsupported expression"), 3, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := s.rep.Possibles[0]
	if v.Goal.Kind != "predicate" || v.Goal.Predicate != "ready" || !v.Found || v.Eval != nil || v.EvalErr == "" {
		t.Fatalf("lost goal/evidence: %+v", v)
	}
	if !strings.Contains(v.Verdict, "predicate witnessed") || strings.Contains(v.Verdict, "initial") {
		t.Fatalf("unobserved initial state: %s", v.Verdict)
	}
}
