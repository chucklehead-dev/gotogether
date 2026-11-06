package ciconfig

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Tool acquisition must fail before missing tools can turn tape tests into
// skips. Keep the golden checks and actual media rendering mandatory.
func TestTapeBootstrapWorkflow(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Continue bool `yaml:"continue-on-error"`
			If       any  `yaml:"if"`
			Steps    []struct {
				Run      string `yaml:"run"`
				Timeout  int    `yaml:"timeout-minutes"`
				Continue bool   `yaml:"continue-on-error"`
				If       any    `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs["qnav-tapes"]
	if !ok || job.Continue || job.If != nil {
		t.Fatal("qnav-tapes must be a mandatory job")
	}
	install, golden, render := false, false, false
	installIndex, goldenIndex, renderIndex := -1, -1, -1
	for i, step := range job.Steps {
		switch run := strings.TrimSpace(step.Run); {
		case run == "../ci/install-tape-tools.sh":
			install = true
			installIndex = i
			if step.Timeout <= 0 || step.Timeout > 10 || step.Continue || step.If != nil {
				t.Error("tape bootstrap needs a finite step deadline and mandatory failure propagation")
			}
		case run == "go test -count=1 -run TestTapes -v ./tapes/":
			golden = true
			goldenIndex = i
			if step.Continue || step.If != nil {
				t.Error("tape golden checks must not be conditional or ignore failure")
			}
		case run == "tapes/render.sh":
			render = true
			renderIndex = i
			if step.Continue || step.If != nil {
				t.Error("tape rendering must not be conditional or ignore failure")
			}
		}
	}
	if !install || !golden || !render || installIndex >= goldenIndex || goldenIndex >= renderIndex {
		t.Fatal("qnav-tapes requires bounded tool bootstrap, golden checks and media rendering")
	}
}
