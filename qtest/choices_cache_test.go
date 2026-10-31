package qtest

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestChoiceCacheSeparatesEffectiveLimits(t *testing.T) {
	t.Parallel()
	steps := []qtrace.Step{{Recorder: "old", Seq: 1, Action: "pick"}}
	instance := fmt.Sprintf("%s/%p|", t.Name(), &steps)
	checker := &validate.Checker{Binding: &binding.Binding{}}
	for _, limit := range []int{1, validate.DefaultChoiceLimit} {
		checker.Options.ChoiceLimit = limit
		report := &validate.Report{Actors: []*validate.Result{{Conforms: validate.NotEvaluated, Choices: &validate.ChoiceSearch{Limit: limit}}}}
		verdicts.put(instance+checker.Key(steps), verdict{report, steps})
	}
	for _, limit := range []int{1, 0, validate.DefaultChoiceLimit} {
		checker.Options.ChoiceLimit = limit
		cached := 0
		reports, errs := cachedCheck(checker, instance, &cached)(t.Context(), [][]qtrace.Step{steps})
		want := limit
		if want == 0 {
			want = validate.DefaultChoiceLimit
		}
		if errs[0] != nil || cached != 1 || reports[0].Actors[0].Choices.Limit != want {
			t.Fatalf("limit %d reused another policy: cached=%d reports=%+v errors=%v", limit, cached, reports, errs)
		}
	}
}

func TestChoiceCacheRebasesAndClonesWitness(t *testing.T) {
	t.Parallel()
	from := []qtrace.Step{{Recorder: "old-a", Seq: 7, Action: "recordedAlias"}, {Recorder: "old-b", Seq: 3, Action: "other"}}
	to := []qtrace.Step{{Recorder: "new-a", Seq: 70, Action: "recordedAlias"}, {Recorder: "new-b", Seq: 30, Action: "other"}}
	original := &validate.Report{Actors: []*validate.Result{{Choices: &validate.ChoiceSearch{Witness: []validate.ChoiceStep{
		{Index: 0, Action: "modelAction", Recorder: "old-a", Seq: 7, Expression: "original"},
		{Index: 0, Action: "otherModelAction", Recorder: "old-b", Seq: 3},
	}}}}}
	if !choiceWitnessesRebasable(original, from) {
		t.Fatal("unique original identities should support cache reuse")
	}
	got := rebase(original, from, to)
	ws := got.Actors[0].Choices.Witness
	if ws[0].Recorder != "new-a" || ws[0].Seq != 70 || ws[1].Recorder != "new-b" || ws[1].Seq != 30 || ws[0].Action != "modelAction" {
		t.Fatalf("wrong rebased witness: %+v", ws)
	}
	ws[0].Expression = "changed"
	got.Actors[0].Choices.Status = "changed"
	if original.Actors[0].Choices.Status != "" || original.Actors[0].Choices.Witness[0].Expression != "original" || original.Actors[0].Choices.Witness[0].Recorder != "old-a" {
		t.Fatal("rebasing mutated cached choice metadata")
	}
	for _, steps := range [][]qtrace.Step{nil, {from[0], from[0]}} {
		if choiceWitnessesRebasable(original, steps) {
			t.Fatal("missing or ambiguous witness identity allowed cache reuse")
		}
	}
	zero := &validate.Report{Actors: []*validate.Result{{Choices: &validate.ChoiceSearch{Witness: []validate.ChoiceStep{{Recorder: "r"}}}}}}
	if choiceWitnessesRebasable(zero, []qtrace.Step{{Recorder: "r"}, {Recorder: "r"}}) {
		t.Fatal("ambiguous sequence-less witness allowed cache reuse")
	}
}
