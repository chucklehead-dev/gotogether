package validate

import (
	"strings"
	"testing"
)

// A confirmed whole-run witness does not silently choose the model navigator's
// start. Cursor navigation does not yet consume pinned initialization evidence.
func TestInitialChoicesNavigationReportsUnavailableState(t *testing.T) {
	c := initialChoicesChecker(t, initialChoicesBoth, "safe, phaseBounded", Options{Eval: "go"})
	steps := witnessHistory(witnessObserved("advance", 1), witnessObserved("finish", 2, 1))
	report, err := c.Check(t.Context(), steps)
	if err != nil || !report.OK() {
		t.Fatalf("whole-run validation did not select the legal positive root: %v, %+v", err, report)
	}
	choices := report.Actors[0].Choices
	if choices == nil || choices.Initialization == nil || choices.Confirmation != "path_confirmed" {
		t.Fatalf("validation omitted its confirmed initialization: %+v", choices)
	}
	for _, start := range []NavStart{{}, {Steps: steps, Actor: "probe", At: -1}} {
		navigator, err := c.Nav(start)
		if err == nil || navigator != nil || !strings.Contains(err.Error(), "init") || !strings.Contains(err.Error(), "successors") {
			t.Fatalf("ambiguous navigation start %+v returned %v, %v; want explicit unavailable initialization", start, navigator, err)
		}
	}
	if report.Actors[0].Choices.Confirmation != "path_confirmed" || !report.OK() {
		t.Fatal("cursor-state unavailability changed the independent whole-run judgment")
	}
}
