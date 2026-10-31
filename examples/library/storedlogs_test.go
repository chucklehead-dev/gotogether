package library

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// The saved v1 prefix intentionally has unmailed receipts. Checking it against
// today's events, folds and model must not require executing that pending work.
func TestStoredVersionOneLog(t *testing.T) {
	qtest.RequireQuint(t)
	file := filepath.Join("testdata", "logs", "v1.jsonl")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	results := gristtest.StoredLogs(t, App, gristtest.StoredLogsConfig{Files: []string{file}, Binding: commits})
	if len(results) != 1 || results[0].Scope != "committed_prefix" || results[0].Events == 0 || results[0].Steps == 0 || results[0].Model == nil || results[0].Model.Verdict != "pass" {
		t.Fatalf("stored v1 prefix was not checked: %+v", results)
	}
	for _, check := range results[0].Checks {
		if check.Name == "liveness" {
			if check.Status != receipt.NotChecked {
				t.Fatalf("retained pending effects acquired a liveness verdict: %+v", check)
			}
			continue
		}
		if check.Status != receipt.Pass {
			t.Errorf("stored v1 check did not pass: %+v", check)
		}
	}
	for _, actor := range results[0].Model.Actors {
		for _, check := range actor.Invariants {
			if check.Kind == "eventually" && check.Verdict == "pass" {
				t.Fatalf("retained history acquired a settled-eventual pass: %+v", check)
			}
		}
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("checking modified the retained fixture: %v", err)
	}
}
