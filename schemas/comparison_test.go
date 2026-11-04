package schemas_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
)

func comparisonDocument() *validate.Document {
	d := validate.NewDocument("model-diff")
	d.Verdict, d.Exit = "fail", 1
	graph := validate.DocComparisonGraph{Complete: true, States: 2, Starts: 1, Transitions: 2, Depth: 1, Bound: validate.DocGraphBound{States: 20}}
	d.Comparison = &validate.DocComparison{
		Scope: "finite_observation_traces", Basis: "complete_go_graph_product", Status: "different", Observe: []string{"phase"}, PairLimit: 100,
		Before: graph, After: graph,
		Added: validate.DocComparisonDirection{Status: "witness", Explored: 2, Witness: &validate.DocComparisonWitness{
			Observed: [][]validate.NamedValue{{{Name: "phase", Value: `"idle"`}}, {{Name: "phase", Value: `"acked"`}}},
			Source: &validate.DocComparisonPath{Steps: []validate.DocComparisonStep{
				{Call: "init", State: []validate.NamedValue{{Name: "phase", Value: `"idle"`}, {Name: "stamp", Value: "9007199254740993"}}},
				{Call: "ack", State: []validate.NamedValue{{Name: "phase", Value: `"acked"`}, {Name: "stamp", Value: "9007199254740993"}}},
			}, Replay: `init.expect(phase == "idle").then(step).expect(phase == "acked")`, Sampled: []int{1}},
			Native: "confirmed", ExclusionBasis: "complete_target_graph",
		}},
		Removed: validate.DocComparisonDirection{Status: "included", Explored: 3},
	}
	return d
}

// Comparison reports keep exact observed values, native source replay and graph
// exclusion separate on the existing report format, for agent and CI consumers.
func TestComparisonReportValidates(t *testing.T) {
	d := comparisonDocument()
	ps, err := schemas.Check(schemas.Report(), d.JSON())
	if err != nil || len(ps) != 0 {
		t.Fatalf("comparison report: %v %v\n%s", err, ps, d.JSON())
	}
	var decoded validate.Document
	if err := json.Unmarshal(d.JSON(), &decoded); err != nil {
		t.Fatal(err)
	}
	w := decoded.Comparison.Added.Witness
	if w.Native != "confirmed" || w.ExclusionBasis != "complete_target_graph" || w.Source.Steps[0].State[1].Value != "9007199254740993" {
		t.Fatalf("comparison evidence lost: %+v", w)
	}
	// Failure to replay the source does not become target rejection evidence.
	d.Verdict, d.Exit = "no verdict", 2
	d.Comparison.Status = "inconclusive"
	d.Comparison.Added.Witness.Native = "unconfirmed"
	d.Comparison.Added.Witness.NativeReason = "native replay did not confirm the observed sequence"
	if ps, err := schemas.Check(schemas.Report(), d.JSON()); err != nil || len(ps) != 0 {
		t.Fatalf("unconfirmed source schema: %v %v", err, ps)
	}
}

func TestComparisonReportRetainsIncompleteScope(t *testing.T) {
	d := comparisonDocument()
	d.Verdict, d.Exit = "no verdict", 2
	c := d.Comparison
	c.Status = "inconclusive"
	c.Before.Complete, c.Before.Cut = false, "depth"
	c.Before.Bound.Depth = 2
	c.Added = validate.DocComparisonDirection{Status: "inconclusive", Reason: "before graph is incomplete"}
	c.Removed = validate.DocComparisonDirection{Status: "bounded", Reason: "comparison pair budget reached", Explored: 100}
	if ps, err := schemas.Check(schemas.Report(), d.JSON()); err != nil || len(ps) != 0 {
		t.Fatalf("incomplete comparison schema: %v %v", err, ps)
	}
	if !strings.Contains(string(d.JSON()), `"complete": false`) || !strings.Contains(string(d.JSON()), `"cut": "depth"`) || strings.Contains(string(d.JSON()), `"witness"`) {
		t.Fatalf("incomplete comparison lost scope or invented witness: %s", d.JSON())
	}
}

func TestComparisonReportRejectsUnstructuredEvidence(t *testing.T) {
	for _, mutate := range []func(*validate.Document){
		func(d *validate.Document) { d.Comparison.Scope = "refinement" },
		func(d *validate.Document) { d.Comparison.Added.Status = "probably included" },
		func(d *validate.Document) { d.Comparison.Added.Witness.Native = "target rejected" },
		func(d *validate.Document) { d.Comparison.Added.Witness.ExclusionBasis = "native sample failed" },
	} {
		d := comparisonDocument()
		mutate(d)
		if ps, err := schemas.Check(schemas.Report(), d.JSON()); err != nil || len(ps) == 0 {
			t.Fatalf("unsupported comparison evidence accepted: %v %v\n%s", err, ps, d.JSON())
		}
	}
}

func TestComparisonReportPortableReplay(t *testing.T) {
	d := comparisonDocument()
	d.Artifacts = "."
	d.Comparison.Added.Witness.Replay = &validate.DocComparisonReplay{Module: validate.File{Path: "added/replay.qnt", SHA256: "module-hash"},
		Sources: []validate.File{{Path: "added/sources/model.qnt", SHA256: "source-hash"}},
		Main:    "simulate", Run: "gtg_comparison_witness", Seed: "2",
		Args: []string{"test", "added/replay.qnt", "--main", "simulate", "--match", "^gtg_comparison_witness$", "--seed", "2", "--max-samples", "1"}}
	if ps, err := schemas.Check(schemas.Report(), d.JSON()); err != nil || len(ps) != 0 {
		t.Fatalf("portable replay schema: %v %v", err, ps)
	}
	var decoded validate.Document
	if err := json.Unmarshal(d.JSON(), &decoded); err != nil {
		t.Fatal(err)
	}
	r := decoded.Comparison.Added.Witness.Replay
	if decoded.Artifacts != "." || r.Module.Path != "added/replay.qnt" || r.Sources[0].SHA256 != "source-hash" || r.Args[1] != r.Module.Path || r.Seed != "2" {
		t.Fatalf("portable source evidence lost: %+v", r)
	}
	bad := strings.Replace(string(d.JSON()), `"args": [`, `"extra": true, "args": [`, 1)
	if ps, err := schemas.Check(schemas.Report(), []byte(bad)); err == nil && len(ps) == 0 {
		t.Fatal("unknown replay metadata accepted")
	}
}
