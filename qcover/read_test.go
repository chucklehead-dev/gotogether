package qcover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// ReadSummary reads a Summary's JSON, and the code coverage of an
// evidence.json; evidence without it says how to get it.
func TestReadSummary(t *testing.T) {
	dir := t.TempDir()
	s := &Summary{Runs: 3, Hit: map[int]int{1: 2}}
	bare, _ := json.Marshal(s)
	ev, _ := json.Marshal(map[string]any{"format": formats.Evidence.Name(), "runs": 3, "conform": 3, "code": s})
	none, _ := json.Marshal(map[string]any{"format": formats.Evidence.Name(), "runs": 3, "conform": 3})
	for name, b := range map[string][]byte{"bare.json": bare, "evidence.json": ev, "none.json": none} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bare.json", "evidence.json"} {
		got, err := ReadSummary(filepath.Join(dir, name))
		if err != nil || got.Runs != 3 || got.Hit[1] != 2 {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	if _, err := ReadSummary(filepath.Join(dir, "none.json")); err == nil || !strings.Contains(err.Error(), "qweave -cover") {
		t.Errorf("evidence without code: %v, want the fix", err)
	}
}

func TestReadSummaryRefusesOtherEvidenceVersions(t *testing.T) {
	for _, version := range []string{"gotogether.evidence/2", "gotogether.evidence/4", "gotogether.evidence/not-a-version"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "evidence.json")
			raw, err := json.Marshal(map[string]any{"format": version, "code": Summary{Runs: 1, Hit: map[int]int{1: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadSummary(path); err == nil || got != nil {
				t.Fatalf("accepted evidence %s as current code summary: %+v %v", version, got, err)
			}
		})
	}
}
