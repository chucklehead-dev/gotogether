package model

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestNightlyManagedToolJobsPinNode keeps the nightly's identity checks
// eligible: every job that runs TestNativeManagedToolIdentity installs
// exactly nativeManagedNodeVersion, whose binary nativeManagedNodeHash names,
// and fetches Apalache (TestSimulateVerify, quint's first verify) before the
// check, which needs it present (verify-alone once ran the check alone and
// failed every group in its first minute).
func TestNightlyManagedToolJobsPinNode(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for name, job := range w.Jobs {
		check := -1
		var node []string
		for i, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "actions/setup-node@") {
				node = append(node, s.With["node-version"])
			}
			if at := strings.Index(s.Run, "TestNativeManagedToolIdentity"); at >= 0 {
				check = i
				if warm := strings.Index(s.Run, "TestSimulateVerify"); warm < 0 || warm > at {
					t.Errorf("nightly job %s checks the managed tool's identity before quint has fetched Apalache: run TestSimulateVerify first in that step", name)
				}
			}
		}
		if check < 0 {
			continue
		}
		checked++
		if len(node) != 1 || node[0] != nativeManagedNodeVersion {
			t.Errorf("nightly job %s checks the managed tool's identity but installs node %q: install exactly %s, the release nativeManagedNodeHash names (bump both together)", name, node, nativeManagedNodeVersion)
		}
	}
	if checked < 2 {
		t.Fatalf("found %d nightly jobs checking the managed tool's identity, want verify and verify-alone", checked)
	}
}
