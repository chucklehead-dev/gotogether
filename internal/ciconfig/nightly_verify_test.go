package ciconfig

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The verifier roster shares the job's unchanged two hours: the job's
// limit stays 120 minutes, the roster's -timeout is what is left of them
// from the job's recorded start (less the reports' upload), and no step
// raises it, so one cold proof cannot leave later bindings unvisited and
// no budget grew.
func TestNightlyVerifyRosterSharesTheUnchangedDeadline(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			Timeout int                          `yaml:"timeout-minutes"`
			Steps   []struct{ Name, Run string } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	job, ok := w.Jobs["verify"]
	if !ok || job.Timeout != 120 {
		t.Fatalf("verify job's limit changed: %+v", job.Timeout)
	}
	if len(job.Steps) == 0 || !strings.Contains(job.Steps[0].Run, `VERIFY_JOB_START=$(date +%s)`) {
		t.Fatal("the job's start must be recorded first, before setup spends any of its time")
	}
	var roster string
	for _, s := range job.Steps {
		if strings.Contains(s.Run, " simulate ") && strings.Contains(s.Run, "-verify 8") {
			roster = s.Run
		}
	}
	for _, want := range []string{
		`left=$(( VERIFY_JOB_START + 120*60 - 5*60 - $(date +%s) ))`,
		`if [ "$left" -le 0 ]`,
		`-verify 8 -timeout "${left}s" -report-dir`,
		`git ls-files -z 'examples/*binding*.yaml' 'examples/**/*binding*.yaml'`,
	} {
		if !strings.Contains(roster, want) {
			t.Fatalf("verify roster lost %q:\n%s", want, roster)
		}
	}
}
