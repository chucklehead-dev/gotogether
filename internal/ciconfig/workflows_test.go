package ciconfig

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every workflow parses as YAML and names jobs, each with steps or a
// reusable workflow: GitHub refuses a file that does not, and the refusal
// shows only as a run with no jobs.
func TestWorkflowsLoad(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found (%v): run from internal/ciconfig", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			Jobs map[string]struct {
				Steps []map[string]any `yaml:"steps"`
				Uses  string           `yaml:"uses"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(b, &w); err != nil {
			t.Errorf("%s: %v (quote a value holding \": \", e.g. a step's name)", filepath.Base(f), err)
			continue
		}
		if len(w.Jobs) == 0 {
			t.Errorf("%s: no jobs", filepath.Base(f))
		}
		for name, j := range w.Jobs {
			if len(j.Steps) == 0 && j.Uses == "" {
				t.Errorf("%s: job %s has no steps", filepath.Base(f), name)
			}
		}
	}
}
