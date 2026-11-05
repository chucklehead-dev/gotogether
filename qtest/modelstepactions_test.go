package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/model"
)

func TestAllStepActionsReachableJudgment(t *testing.T) {
	seen := []model.ActionCoverage{{Action: "open", Transitions: 1, States: 1}}
	missing := []model.ActionCoverage{{Action: "open", Transitions: 1, States: 1}, {Action: "close"}}
	for _, tc := range []struct {
		name string
		rep  *model.Report
		want tri
		text string
	}{
		{"complete observed", &model.Report{Graph: &model.GraphResult{Complete: true, Actions: seen}}, yes, "witnessed within"},
		{"complete missing", &model.Report{Graph: &model.GraphResult{Complete: true, Actions: missing}}, no, "close"},
		{"depth missing", &model.Report{Graph: &model.GraphResult{Cut: "depth", Bound: model.GraphBound{Depth: 2}, Actions: missing}}, unknown, "within"},
		{"state limit missing", &model.Report{Graph: &model.GraphResult{Cut: "states", Bound: model.GraphBound{States: 3}, Actions: missing}}, unknown, "within"},
		{"unsupported missing", &model.Report{Graph: &model.GraphResult{Cut: "unsupported", Stopped: "operator", Actions: missing}}, unknown, "within"},
		{"partial observed", &model.Report{Graph: &model.GraphResult{Cut: "depth", Bound: model.GraphBound{Depth: 1}, Actions: seen}}, yes, "witnessed within"},
		{"empty inventory", &model.Report{Graph: &model.GraphResult{Complete: true}}, unknown, "no identified"},
		{"disagreement invalidates positive", &model.Report{EvaluatorBug: "native state absent", Graph: &model.GraphResult{Complete: true, Actions: seen}}, unknown, "disagreement"},
		{"model error invalidates positive", &model.Report{Graph: &model.GraphResult{Cut: "error", Stopped: "division by zero", Actions: seen}}, unknown, "model error"},
		{"no graph", &model.Report{}, unknown, "no state graph"},
		{"nil report", nil, unknown, "no state graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := AllStepActionsReachable()
			got, why := claim.judgeModel(tc.rep)
			if got != tc.want || !strings.Contains(why, tc.text) {
				t.Fatalf("judgment=%v reason=%q; want %v containing %q", got, why, tc.want, tc.text)
			}
		})
	}
	n := &modelNeeds{}
	if err := AllStepActionsReachable().needs(n); err != nil || !n.graph {
		t.Fatalf("claim did not request the shared graph: graph=%v error=%v", n.graph, err)
	}
}

func TestModelAllStepActionsReachable(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	binding, unchanged := modelOptionsFixture(t, "")
	defer unchanged()
	base := Model(t, binding)
	for _, tc := range []struct {
		name    string
		options ModelOptions
		failed  bool
		text    string
	}{
		{"complete", ModelOptions{}, false, "witnessed within"},
		{"partial positive", ModelOptions{Depth: 1}, false, "witnessed within"},
		{"disabled inc", ModelOptions{Constants: map[string]string{"MAX": "0"}}, true, "the model fails reaching every step action"},
		{"state limited", ModelOptions{States: 1}, true, "the model has no verdict on reaching every step action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configuredModelExpect(t, base.WithOptions(tc.options), AllStepActionsReachable())
			output := strings.Join(append(f.logs, f.errs...), "\n")
			if f.failed != tc.failed || !strings.Contains(output, tc.text) {
				t.Fatalf("failed=%v want %v; output:\n%s", f.failed, tc.failed, output)
			}
			if tc.name == "disabled inc" && !strings.Contains(output, "inc") {
				t.Fatalf("the unreachable action was not named: %s", output)
			}
		})
	}
	// The existing graph fixture has a permanently disabled named branch. A
	// shallow search cannot establish that absence, unlike a complete graph.
	f := configuredModelExpect(t, Model(t, "../validate/testdata/graph.yaml").WithOptions(ModelOptions{Depth: 1}), AllStepActionsReachable())
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "no verdict") {
		t.Fatalf("depth-limited absence became a proof or failure: %s", f.errs)
	}
}
