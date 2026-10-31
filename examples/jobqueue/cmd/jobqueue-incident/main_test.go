package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func TestRecordedIncidentAndFreshControls(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, dir := range []string{first, second} {
		if err := run([]string{"-out", dir}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range scenarios {
		path := filepath.Join(first, c.name+".jsonl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		again, err := os.ReadFile(filepath.Join(second, c.name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, again) {
			t.Fatalf("%s recording is not deterministic", c.name)
		}
		steps, err := qtrace.ReadJSONL(bytes.NewReader(body))
		actions := []string{"enqueue", "lease", "perform", "ack"}
		if c.complete {
			actions = append(actions, "lease", "perform", "ack")
		}
		if err != nil || len(steps) != len(actions) {
			t.Fatalf("%s: %d steps, error %v", c.name, len(steps), err)
		}
		for i, action := range actions {
			s := steps[i]
			if s.Action != action || s.Actor != "jobqueue" || s.Seq != uint64(i+1) || !s.Time.Equal(qenv.Epoch) || s.Settled {
				t.Fatalf("%s step %d: %+v", c.name, i+1, s)
			}
			wantProcess := "queue-1"
			if i >= 2 {
				wantProcess = "queue-2"
			}
			if s.Process != wantProcess {
				t.Fatalf("%s step %d process %s, want %s", c.name, i+1, s.Process, wantProcess)
			}
		}
		wantOutcome := "stale"
		if c.name == "incident" {
			wantOutcome = "ok"
		}
		if steps[3].Outcome != wantOutcome {
			t.Fatalf("%s ack outcome %s, want %s", c.name, steps[3].Outcome, wantOutcome)
		}
		if c.complete && (steps[6].Outcome != "ok" || steps[6].Error != "") {
			t.Fatalf("repaired fresh ack must succeed: %+v", steps[6])
		}
	}
}

func TestExistingEvidenceIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "incident.jsonl")
	before := []byte("existing incident evidence\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-out", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing incident evidence accepted for overwrite")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing evidence changed: %q (%v)", after, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "faithful.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("partial controls written before rejecting existing evidence: %v", err)
	}
}
