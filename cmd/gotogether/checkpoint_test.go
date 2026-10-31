package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The agent-facing report must identify the actual checkpoint record, rather
// than silently passing it or blaming a later call after resetting model state.
func TestValidateCheckpointJSON(t *testing.T) {
	skip.Quint(t)
	for _, tc := range []struct {
		name    string
		balance any
		code    int
	}{
		{"matching", int64(5), 0}, {"different state", int64(6), 1}, {"missing state", nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			checkpoint := map[string]any{"n": int64(1)}
			if tc.balance != nil {
				checkpoint["b"] = tc.balance
			}
			steps := []qtrace.Step{
				{Action: "deposit", Recorder: "r", Process: "p", Thread: "t", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
				{Action: "restore", Recorder: "r", Process: "p", Thread: "t", Seq: 2, Args: checkpoint},
				{Action: "deposit", Recorder: "r", Process: "p", Thread: "t", Seq: 3, Args: map[string]any{"amount": int64(1)}, Obs: map[string]any{"balance": int64(6)}},
			}
			log := filepath.Join(dir, "run.jsonl")
			f, err := os.Create(log)
			if err != nil {
				t.Fatal(err)
			}
			if err = qtrace.WriteJSONL(f, steps); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			code, out, _ := validated([]string{"-json", "-binding", "../../validate/testdata/snapshot.yaml", "-dir", filepath.Join(dir, "out"), log})
			var d validate.Document
			if err = json.Unmarshal(out, &d); err != nil {
				t.Fatalf("status %d: %v\n%s", code, err, out)
			}
			if code != tc.code || d.Exit != tc.code {
				t.Fatalf("status %d want %d\n%s", code, tc.code, out)
			}
			switch tc.code {
			case 0:
				if d.Verdict != "pass" {
					t.Fatalf("%s", out)
				}
			case 1:
				if d.Verdict != "fail" || len(d.Actors) != 1 || len(d.Actors[0].Failures) == 0 {
					t.Fatalf("%s", out)
				}
				failure := d.Actors[0].Failures[0]
				if failure.Kind != "state" || failure.Record == nil || failure.Record.Action != "restore" || failure.Record.Seq != 2 {
					t.Fatalf("wrong failure attribution: %s", out)
				}
			case 2:
				if d.Verdict != "error" || d.Error == "" {
					t.Fatalf("missing state credited as a verdict: %s", out)
				}
			}
		})
	}
}
