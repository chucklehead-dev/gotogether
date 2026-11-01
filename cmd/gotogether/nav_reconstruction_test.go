package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func navReconstructionStep(actor, recorder string, amount int64) qtrace.Step {
	return qtrace.Step{Action: "deposit", Actor: actor, Recorder: recorder, Process: actor, Thread: recorder, Seq: 1, Outcome: "ok", Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": amount}}
}

func navReconstructionCapture(t *testing.T, steps []qtrace.Step) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = qtrace.WriteJSONL(f, steps)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// A conflicting identity must not become an apparently observed JSON state,
// including the initial cursor: the run may determine the model's constants.
func TestNavReconstructionConflictEmitsNoNode(t *testing.T) {
	skip.Quint(t)
	path := navReconstructionCapture(t, []qtrace.Step{navReconstructionStep("a", "r", 5), navReconstructionStep("a", "r", 9)})
	for _, at := range []string{"0", "1", "-1"} {
		var out bytes.Buffer
		code, err := nav(&out, []string{"-binding", "../../validate/testdata/account.yaml", "-run", path, "-actor", "a", "-at", at, "-json"})
		if code != 2 || err == nil || out.Len() != 0 {
			t.Errorf("cursor %s fabricated a node: code %d err %v output bytes %d", at, code, err, out.Len())
			continue
		}
		for _, want := range []string{"actor a", "duplicate", "recorder r", "seq 1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("cursor %s missing %q in diagnostic: %v", at, want, err)
			}
		}
	}
	var out bytes.Buffer
	code, err := nav(&out, []string{"-run", path, "-record", `{"actor":"a","recorder":"r","seq":1}`, "-json"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "ambiguous identity") || out.Len() != 0 {
		t.Fatalf("exact record inspection should also refuse the conflict: %d %v %s", code, err, out.String())
	}
}

func TestNavReconstructionCopiesAndUnrelatedActorsRemainUsable(t *testing.T) {
	skip.Quint(t)
	a := navReconstructionStep("a", "r", 5)
	for _, tc := range []struct {
		name, actor string
		steps       []qtrace.Step
		balance     string
	}{
		{"identical retry", "a", []qtrace.Step{a, a}, "5"},
		{"unrelated actor", "b", []qtrace.Step{a, navReconstructionStep("a", "r", 9), navReconstructionStep("b", "clean", 7)}, "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := navReconstructionCapture(t, tc.steps)
			var out bytes.Buffer
			code, err := nav(&out, []string{"-binding", "../../validate/testdata/account.yaml", "-run", path, "-actor", tc.actor, "-json"})
			if code != 0 || err != nil {
				t.Fatalf("valid selected actor refused: %d %v %s", code, err, out.String())
			}
			var node validate.Node
			if err := json.Unmarshal(out.Bytes(), &node); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, value := range node.State {
				if value.Name == "balance" {
					found = value.Value == tc.balance
				}
			}
			if !found || len(node.Path) != 1 || !strings.Contains(node.Start, "actor "+tc.actor) {
				t.Fatalf("wrong selected actor state: %s", out.String())
			}
		})
	}
}
