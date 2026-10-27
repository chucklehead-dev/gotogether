package qtest

import (
	"encoding/json"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The evidence view the CLI reads (internal/evidence.View) is Evidence's
// JSON: every field it reads decodes from WriteJSON's (a renamed tag
// would read as zero), and a manifest's evidence is ManifestEvidence's.
func TestEvidenceView(t *testing.T) {
	t.Parallel()
	view := func(e *Evidence) *evidence.View {
		t.Helper()
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var v evidence.View
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		return &v
	}
	e := &Evidence{Format: EvidenceFormat, Role: RoleFaithful, Package: "p", Import: "x/p", Test: "TestA#1", Spec: "m.qnt:m", Runs: 3, Conform: 2,
		FailingBy: map[string]int{"violates inv": 1}, Model: &ModelCoverage{Taken: map[string]int{"put": 2}, Never: []string{"get"}},
		Faults:  &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "rpc.db", Sites: []string{"a.go:1"}, Runs: 2, Injected: []int{3, 1, 2}}}},
		Exhaust: &Extent{Complete: true}}
	v := view(e)
	p := v.Faults.Points[0]
	if v.Format != e.Format || v.Role != e.Role || v.Package != "p" || v.Import != "x/p" || v.Test != e.Test || v.Spec != e.Spec || v.Runs != 3 || v.Conform != 2 ||
		v.FailingBy["violates inv"] != 1 || v.Model.Taken["put"] != 2 || v.Model.Never[0] != "get" || !v.Exhaust.Complete ||
		p.Kind != "rpc.db" || p.Sites[0] != "a.go:1" || p.Runs != 2 || evidence.Faulted(p.Injected) != e.Faults.Points[0].Faulted() {
		t.Errorf("the view of %+v is %+v", e, v)
	}
	for _, verdict := range []validate.RunVerdict{{Verdict: validate.Pass}, {Verdict: validate.Fail, Reason: "violates inv"},
		{Verdict: validate.NotEvaluated, Reason: "no verdict (lost tail)"}, {Verdict: validate.NotEvaluated, Reason: "incomplete (no settle)"}} {
		m := &validate.Manifest{Name: "run", Package: "p", Import: "x/p", Verdict: verdict, Model: validate.RunModel{Spec: &validate.File{Path: "m.qnt"}, Module: "m"}}
		want, _ := json.Marshal(view(ManifestEvidence(m)))
		got, _ := json.Marshal(evidence.OfManifest(m))
		if string(got) != string(want) {
			t.Errorf("%s: the manifest's evidence is %s, qtest's %s", verdict, got, want)
		}
	}
}
