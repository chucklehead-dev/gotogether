package validate

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A mutant whose log Reconstruct refused is malformed, not killed: the
// recorder's checks stopped it, and it counts in no score. One that
// survives with its bounds unjudged is undecided.
func TestStepFateMalformed(t *testing.T) {
	t.Parallel()
	overlap := &Report{Actors: []*Result{{Actor: "A", Conforms: Fail,
		Issues: []qtrace.Issue{{Severity: "error", Kind: "overlap", Msg: "two incarnations of A overlap"}}}}}
	conforming := &Report{Actors: []*Result{{Actor: "A", Conforms: Pass}}}
	if fate, why := stepFate(conforming, overlap); fate != FateMalformed || !strings.Contains(why, "refused by Reconstruct: error overlap") {
		t.Errorf("an overlap: %s (%s)", fate, why)
	}
	// a survivor whose bounds went unjudged, where the original's were
	// judged, is undecided: its survival says nothing of them
	unjudged := &Report{Actors: []*Result{{Actor: "A", Conforms: Pass,
		Issues: []qtrace.Issue{{Severity: "warn", Kind: "within", Msg: "bounded-response obligations not judged: the steps' times go back"}}}}}
	if fate, _ := stepFate(conforming, unjudged); fate != FateUndecided {
		t.Errorf("bounds unjudged: %s", fate)
	}
	if fate, _ := stepFate(unjudged, unjudged); fate != FateSurvived {
		t.Errorf("bounds unjudged in the original too: %s", fate)
	}
	rep := &StepMutants{Mutants: []StepMutation{{Fate: FateKilled}, {Fate: FateMalformed}, {Fate: FateSurvived}}}
	if rep.Score() != 0.5 || len(rep.Malformed()) != 1 {
		t.Errorf("score %v, %d malformed", rep.Score(), len(rep.Malformed()))
	}
}
