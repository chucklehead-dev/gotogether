package binding_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestUsedFieldsPreservesEvidenceThroughSanitizing(t *testing.T) {
	skip.Quint(t)
	c, err := validate.NewChecker("../validate/testdata/diagnostic/binding.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Binding.DiagnosticEvidence = map[string]map[string]binding.EvidenceRule{"outstanding": {"cancellation": {Action: "request", Match: map[string]string{"id": "intent"}, Origin: &binding.EvidenceOrigin{Actor: `{{index .obs "native.actor"}}`, Recorder: `{{index .obs "native.recorder"}}`, Seq: `{{index .args "nativeSeq"}}`}}}}
	steps := []qtrace.Step{{Actor: "tenant-a", Recorder: "projection", Seq: 1, Process: "p", Action: "request", Args: map[string]any{"id": "c1", "nativeSeq": "71", "private": "remove"}, Obs: map[string]any{"native.actor": "backend/a", "native.recorder": "backend-r", "private": "remove"}}, {Actor: "tenant-a", Recorder: "projection", Seq: 2, Process: "p", Record: qtrace.Record{Settled: true}}}
	filtered := qtrace.Filter{Keep: c.Binding.UsedFields(c.Model)}.Apply(steps)
	if _, ok := filtered[0].Args["private"]; ok {
		t.Fatal("unneeded argument retained")
	}
	if _, ok := filtered[0].Obs["private"]; ok {
		t.Fatal("unneeded observation retained")
	}
	var origin any
	for _, capture := range [][]qtrace.Step{steps, filtered} {
		report, err := c.Check(t.Context(), capture)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Actors) != 1 || report.Actors[0].Conforms != validate.Pass || len(report.Actors[0].Absent) != 1 || report.Actors[0].Absent[0].Cause != validate.Defect {
			t.Fatalf("sanitizing changed completion judgment: %+v", report)
		}
		n, err := c.Nav(validate.NavStart{Steps: capture, Actor: "tenant-a", At: -1})
		if err != nil {
			t.Fatal(err)
		}
		node, err := n.Node()
		if err != nil {
			t.Fatal(err)
		}
		e := node.Diagnostics[0].Items[0].Evidence
		if e.Status != "resolved" || e.Matches[0].Origin.Actor != "backend/a" || e.Matches[0].Origin.Recorder != "backend-r" || e.Matches[0].Origin.Seq != 71 {
			t.Fatalf("sanitizing broke provenance: %+v", e)
		}
		if origin != nil && !reflect.DeepEqual(origin, e.Matches[0].Origin) {
			t.Fatal("native identity changed")
		}
		origin = e.Matches[0].Origin
	}
}

func TestUsedFieldsRetainsDynamicMapsConservatively(t *testing.T) {
	m := &binding.Model{Actions: map[string][]binding.Param{"request": {{Name: "id", Type: "str"}}, "other": {{Name: "id", Type: "str"}}}}
	for _, src := range []string{`{{index .obs .args.which}}`, `{{$o := .obs}}{{index $o "native.actor"}}`, `{{with .obs}}{{index . "native.actor"}}{{end}}`, `{{range .obs}}{{.}}{{end}}`, `{{`} {
		b := &binding.Binding{DiagnosticEvidence: map[string]map[string]binding.EvidenceRule{"outstanding": {"cancellation": {Action: "request", Origin: &binding.EvidenceOrigin{Actor: src, Recorder: "r", Seq: "1"}}}}}
		keep := b.UsedFields(m)
		if !slices.Contains(keep["request"], "obs.*") || slices.Contains(keep["other"], "obs.*") {
			t.Fatalf("dynamic map dependency not action-scoped for %s: %v", src, keep)
		}
		steps := []qtrace.Step{{Action: "request", Args: map[string]any{"id": "c1", "which": "native.actor"}, Obs: map[string]any{"native.actor": "backend/a", "unselected": "must retain dynamic map"}}}
		filtered := qtrace.Filter{Keep: keep}.Apply(steps)
		if !reflect.DeepEqual(filtered[0].Obs, steps[0].Obs) {
			t.Fatalf("dynamic map pruned: %#v", filtered[0].Obs)
		}
		if _, ok := steps[0].Obs["unselected"]; !ok {
			t.Fatal("input mutated")
		}
	}
}

func TestUsedFieldsReadsIndexedActionTemplates(t *testing.T) {
	m := &binding.Model{Actions: map[string][]binding.Param{"request": {{Name: "id", Type: "str"}}}}
	b := &binding.Binding{Actions: map[string]binding.Action{"request": {Args: map[string]string{"id": `{{index .args "requestID"}}`}, Expect: `pending.contains({{index .obs "seen.id"}})`}}}
	if got := b.UsedFields(m)["request"]; !reflect.DeepEqual(got, []string{"obs.seen.id", "requestID"}) {
		t.Fatalf("indexed dependencies: %v", got)
	}
}
