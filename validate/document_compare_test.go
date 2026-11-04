package validate

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

func comparisonForRendering() *Document {
	d := NewDocument("model-diff")
	d.Verdict, d.Exit = "fail", 1
	d.Comparison = &DocComparison{Scope: "finite_observation_traces", Basis: "complete_go_graph_product", Status: "different", Observe: []string{"phase"}, PairLimit: 100, WorkLimit: 1000,
		Before: DocComparisonGraph{Binding: "before.yaml", Complete: true}, After: DocComparisonGraph{Binding: "after.yaml", Complete: true},
		Added: DocComparisonDirection{Status: "witness", Explored: 2, Witness: &DocComparisonWitness{Native: "confirmed", ExclusionBasis: "complete_target_graph",
			Observed: [][]NamedValue{{{Name: "phase", Value: `"idle"`}}, {{Name: "phase", Value: `"acked"`}}},
			Source:   &DocComparisonPath{Replay: "exact source replay"}}},
		Removed: DocComparisonDirection{Status: "included", Explored: 3},
	}
	return d
}

func TestComparisonJUnitSeparatesConfirmedDifferenceFromUnknown(t *testing.T) {
	d := comparisonForRendering()
	var suite junitSuite
	if err := xml.Unmarshal(d.JUnit(), &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 2 || suite.Failures != 1 || suite.Skipped != 0 || suite.Cases[0].Name != "added" || suite.Cases[0].Failure == nil || !strings.Contains(suite.Cases[0].Failure.Body, `phase = "acked"`) || !strings.Contains(suite.Cases[0].Failure.Body, "source native replay: confirmed") {
		t.Fatalf("comparison JUnit lost actual evidence: %s", d.JUnit())
	}
	d.Verdict, d.Exit, d.Comparison.Status = "no verdict", 2, "inconclusive"
	d.Comparison.Added.Witness.Native = "unconfirmed"
	d.Comparison.Added.Witness.NativeReason = "native samples missed source path"
	d.Comparison.Removed = DocComparisonDirection{Status: "bounded", Cut: "work", Reason: "work budget"}
	if err := xml.Unmarshal(d.JUnit(), &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 2 || suite.Failures != 0 || suite.Skipped != 2 {
		t.Fatalf("unknown comparison appeared passing or failing in JUnit: %s", d.JUnit())
	}
}

func TestComparisonSARIFRetainsDirectionalEvidence(t *testing.T) {
	d := comparisonForRendering()
	read := func() []map[string]any {
		t.Helper()
		var sarif struct {
			Runs []struct {
				Results []map[string]any `json:"results"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(d.SARIF(), &sarif); err != nil {
			t.Fatal(err)
		}
		if len(sarif.Runs) != 1 {
			t.Fatalf("SARIF missing run: %s", d.SARIF())
		}
		return sarif.Runs[0].Results
	}
	results := read()
	if len(results) != 1 || results[0]["ruleId"] != "model-diff" || results[0]["level"] != "error" {
		t.Fatalf("confirmed comparison SARIF: %s", d.SARIF())
	}
	props := results[0]["properties"].(map[string]any)
	if props["direction"] != "added" || props["scope"] != "finite_observation_traces" || props["basis"] != "complete_go_graph_product" || props["evidence"].(map[string]any)["witness"].(map[string]any)["native"] != "confirmed" {
		t.Fatalf("SARIF evidence lost: %s", d.SARIF())
	}
	locations := results[0]["locations"].([]any)
	if locations[0].(map[string]any)["physicalLocation"].(map[string]any)["artifactLocation"].(map[string]any)["uri"] != "after.yaml" {
		t.Fatalf("added behavior does not identify its source binding: %s", d.SARIF())
	}
	d.Comparison.Added.Witness.Native = "not_run"
	d.Comparison.Status, d.Verdict, d.Exit = "inconclusive", "no verdict", 2
	results = read()
	if len(results) != 1 || results[0]["ruleId"] != "model-diff-inconclusive" || results[0]["level"] != "warning" {
		t.Fatalf("unconfirmed comparison disappeared or failed in SARIF: %s", d.SARIF())
	}
	d.Comparison.Added = DocComparisonDirection{Status: "included"}
	d.Comparison.Status, d.Verdict, d.Exit = "equivalent", "pass", 0
	if len(read()) != 0 {
		t.Fatalf("equivalence emitted false SARIF failure: %s", d.SARIF())
	}
}
