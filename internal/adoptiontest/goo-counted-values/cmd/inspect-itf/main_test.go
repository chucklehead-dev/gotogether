package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One-unit rounding beyond float64's exact range must fail the native ITF gate.
func TestInspectRefusesRoundedNativeInteger(t *testing.T) {
	dir := t.TempDir()
	tracePath, resultsPath := filepath.Join(dir, "trace.json"), filepath.Join(dir, "observations.json")
	trace := `{"vars":["state","count","lastGroups"],"states":[{"state":"no_data","count":{"#bigint":"0"},"lastGroups":[]},{"state":"observed","count":{"#bigint":"9007199254740993"},"lastGroups":[{"#bigint":"9007199254740993"}],"mbt::actionTaken":"lookup","mbt::nondetPicks":{"total":{"tag":"Some","value":{"#bigint":"9007199254740993"}},"groups":{"tag":"Some","value":[{"#bigint":"9007199254740993"}]}}}]}`
	results := `[{"case":{"name":"above-float","total":"9007199254740993","groups":["9007199254740993"]},"result":{"count":"9007199254740993","groups":[{"g0":"value","count":"9007199254740993"}],"state":"observed"},"reads":2}]`
	if err := os.WriteFile(resultsPath, []byte(results), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, []byte(trace), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := inspect(tracePath, resultsPath)
	if err != nil || report.Observations[0].Count != "9007199254740993" {
		t.Fatalf("exact ITF rejected: %+v, %v", report, err)
	}
	rounded := strings.Replace(trace, `"count":{"#bigint":"9007199254740993"}`, `"count":{"#bigint":"9007199254740992"}`, 1)
	if rounded == trace {
		t.Fatal("test did not plant rounding")
	}
	if err := os.WriteFile(tracePath, []byte(rounded), 0600); err != nil {
		t.Fatal(err)
	}
	if report, err := inspect(tracePath, resultsPath); err == nil || report != nil || !strings.Contains(err.Error(), "differs from public") {
		t.Fatalf("rounded ITF accepted: %+v, %v", report, err)
	}
}
