package reader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/casselc/goo/subjectfacts"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

type inputCase struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	YAML string `json:"yaml"`
}

// readObserved derives every obs.* value exclusively from the real API return.
// Input rejection is a domain observation, so Record receives nil driver error.
// File/JSON/recorder/tool failures are fatal harness failures, never rejections.
func readObserved(ctx context.Context, c inputCase) {
	d, err := subjectfacts.Read(strings.NewReader(c.YAML))
	fields, measures := 0, 0
	name, api, kind, key, binding, typ, dimension, total := "", "", "", "", "", "", "", ""
	fraction, numerator, denominator := "", "", ""
	populationTrue, whereTrue := false, false
	if d != nil {
		fields, measures = len(d.Source.Fields), len(d.Measures)
		name, api, kind = d.Name, d.APIVersion, d.Source.Kind
		key = strings.Join(d.Source.SubjectKey, ",")
		binding, typ, dimension, total = d.Source.Bindings["clickhouse"], d.Source.Fields["healthy"], d.Dimensions["health"], d.Measures["total"].Op
		m := d.Measures["fraction"]
		fraction, numerator, denominator = m.Op, m.Numerator, m.Denominator
		populationTrue = d.Population != nil && d.Population.Field == "healthy" && d.Population.Eq == true
		w := d.Measures["good"].Where
		whereTrue = w != nil && w.Field == "healthy" && w.Eq == true
	}
	qobs.Record(ctx, "read", nil, "caseID", c.ID,
		"obs.accepted", err == nil, "obs.hasError", err != nil, "obs.nilDefinition", d == nil,
		"obs.fieldCount", fields, "obs.measureCount", measures, "obs.name", name,
		"obs.apiVersion", api, "obs.kind", kind, "obs.subjectKey", key,
		"obs.binding", binding, "obs.healthyType", typ, "obs.dimension", dimension,
		"obs.totalOp", total, "obs.populationTrue", populationTrue, "obs.whereTrue", whereTrue,
		"obs.fractionOp", fraction, "obs.numerator", numerator, "obs.denominator", denominator)
}

func record(t *testing.T) *qtest.Run {
	t.Helper()
	qtest.RequireQuint(t)
	data, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []inputCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return qtest.Record(t, "reader.binding.yaml", func(rec *qobs.Recorder) {
		ctx := qobs.WithRecorder(t.Context(), rec)
		for _, c := range cases {
			readObserved(ctx, c)
		}
	})
}
func TestConforms(t *testing.T) { record(t).Expect(t, qtest.Conforms()) }
func TestDemoEvidence(t *testing.T) {
	directory := os.Getenv("GOTOGETHER_DEMO_DIR")
	if directory == "" {
		t.Skip("set GOTOGETHER_DEMO_DIR to record evidence")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTOGETHER_KEEP", directory)
	run := record(t)
	run.Expect(t, qtest.Conforms())
	data, err := os.ReadFile(run.StepLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "steps.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := run.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Bundle(filepath.Join(directory, "run")); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(filepath.Join(directory, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}
