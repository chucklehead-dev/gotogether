package ciconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
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
			Timeout any                          `yaml:"timeout-minutes"`
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
		`mapfile -t bindings < <(ci/verify-roster.sh shared)`,
	} {
		if !strings.Contains(roster, want) {
			t.Fatalf("verify roster lost %q:\n%s", want, roster)
		}
	}
}

// The grist apps' slow proofs run in verify-alone, a job per group of
// ci/verify-alone.txt; every other tracked binding in the shared roster.
// A binding is in exactly one: none dropped by the split, none twice. Each
// group's limit is GitHub's at most, and its job keeps the bound, the
// shared deadline and every report.
func TestNightlyVerifyRostersPartitionTheBindings(t *testing.T) {
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).Output()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out)
	}
	tracked := strings.Fields(run("git", "-C", "../..", "ls-files", "examples/*binding*.yaml", "examples/**/*binding*.yaml"))
	slices.Sort(tracked)
	tracked = slices.Compact(tracked)
	shared := strings.Fields(run("../../ci/verify-roster.sh", "shared"))
	var matrix struct {
		Include []struct {
			Name     string `json:"name"`
			Minutes  int    `json:"minutes"`
			Bindings string `json:"bindings"`
		} `json:"include"`
	}
	if err := json.Unmarshal([]byte(run("../../ci/verify-roster.sh", "alone")), &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) == 0 {
		t.Fatal("no verify-alone groups")
	}
	seen := map[string]string{}
	for _, b := range shared {
		seen[b] = "shared"
	}
	names := map[string]bool{}
	for _, g := range matrix.Include {
		if g.Name == "" || names[g.Name] || g.Minutes <= 5 || g.Minutes > 360 || g.Bindings == "" {
			t.Fatalf("group %+v: a unique name, more than the upload's five minutes and at most GitHub's 360", g)
		}
		names[g.Name] = true
		for _, b := range strings.Fields(g.Bindings) {
			if where, dup := seen[b]; dup {
				t.Fatalf("%s is in %s and in group %s", b, where, g.Name)
			}
			seen[b] = g.Name
		}
	}
	for _, b := range tracked {
		if seen[b] == "" {
			t.Fatalf("%s is in neither roster", b)
		}
	}
	if len(seen) != len(tracked) {
		t.Fatalf("a roster names an untracked binding: %d named, %d tracked", len(seen), len(tracked))
	}
	for _, b := range []string{"examples/counters/counters.binding.yaml", "examples/library/library.binding.yaml", "examples/wallet-grist/wallet.binding.yaml"} {
		if seen[b] == "shared" {
			t.Fatalf("%s's proof does not fit the shared roster (docs/performance.md): it runs alone", b)
		}
	}

	b, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			Timeout  any `yaml:"timeout-minutes"`
			Needs    any `yaml:"needs"`
			Strategy struct {
				FailFast *bool `yaml:"fail-fast"`
				Matrix   any   `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct{ Name, Run string } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	job := w.Jobs["verify-alone"]
	// select gates it (a dispatch's jobs filter, nightly_dispatch_test.go);
	// the plan gives its rows
	needs, _ := job.Needs.([]any)
	if !slices.Equal(needs, []any{"select", "verify-alone-plan"}) || job.Timeout != "${{ matrix.minutes }}" || job.Strategy.FailFast == nil || *job.Strategy.FailFast ||
		job.Strategy.Matrix != "${{ fromJSON(needs.verify-alone-plan.outputs.matrix) }}" {
		t.Fatalf("verify-alone must run every group of the plan, each within its own limit: %+v", job)
	}
	if len(job.Steps) == 0 || !strings.Contains(job.Steps[0].Run, `VERIFY_JOB_START=$(date +%s)`) {
		t.Fatal("verify-alone must record its start first")
	}
	var roster string
	for _, s := range job.Steps {
		if strings.Contains(s.Run, " simulate ") {
			roster = s.Run
		}
	}
	for _, want := range []string{
		`left=$(( VERIFY_JOB_START + GROUP_MINUTES*60 - 5*60 - $(date +%s) ))`,
		`if [ "$left" -le 0 ]`,
		`-verify 8 -timeout "${left}s" -report-dir`,
	} {
		if !strings.Contains(roster, want) {
			t.Fatalf("verify-alone lost %q:\n%s", want, roster)
		}
	}
	plan := w.Jobs["verify-alone-plan"]
	if len(plan.Steps) < 2 || !strings.Contains(plan.Steps[1].Run, "ci/verify-roster.sh alone") {
		t.Fatal("verify-alone-plan must take its groups from ci/verify-roster.sh")
	}
}
