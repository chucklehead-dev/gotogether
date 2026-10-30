package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestTerminalDiagnosticEntry(t *testing.T) {
	requireQuint(t)
	checker, err := validate.NewChecker(filepath.Join(root, "validate/testdata/diagnostic/binding.yaml"), validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink, qobs.WithID("node"), qobs.WithActor("tenant-a"), qobs.WithProcess("kernel"))
	rec.Record(t.Context(), "request", nil, "id", "c1")
	rec.Settled(t.Context())
	nav, title, why, err := Failing(checker, validate.NavStart{Steps: sink.Steps(), Actor: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := checker.Nav(validate.NavStart{Steps: sink.Steps(), Actor: "tenant-a", At: 0})
	if err != nil {
		t.Fatal(err)
	}
	d := drive(t, Config{Title: "recorded history", Model: initial, Failing: func() (ModelNav, string, string, error) { return nav, title, why, nil }})
	d.keys("g")
	screen := d.m.View().Content
	if strings.Contains(screen, "which the model rejects") || !strings.Contains(screen, "completion failure · fixed checked history") {
		t.Fatalf("completion failure described as a rejected call:\n%s", screen)
	}
	for _, want := range []string{"terminal diagnostics", "shared-order", "c1", "at recorded settle", "selected model state; no verdict"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	node, err := nav.Node()
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Diagnostics) != 2 || node.Diagnostics[0].Cause != "" || node.Diagnostics[1].Cause != "defect" {
		t.Fatalf("mixed scopes: %+v", node.Diagnostics)
	}
}

// A run's terminal diagnosis and the cursor's live outstanding work are
// independent payloads. Viewing an earlier cursor must label both boundaries.
func TestRunDiagnosticScopes(t *testing.T) {
	result := diagnostic.Result{Property: "complete", Projection: "outstanding", Scope: "settled_checked_history", Cause: "defect", Status: "available", Total: 1, Evidence: "unresolved", Items: []diagnostic.Item{{Kind: "cancellation", Subject: "order-a", Intent: "cancel-1", Expected: "modeled answer"}}}
	cursor := result
	cursor.Scope = "navigated_state_no_verdict"
	cursor.Cause = ""
	model := New(Config{NoColor: true})
	model.w, model.h = 100, 60
	screen := model.runScreen(&entry{r: &navproto.Node{Run: "sample", ID: "0@1", Receipt: receipt.Summary{Status: receipt.Fail, Reason: "eventually complete failed", Scope: "collected_history", OtherExecutions: receipt.NotChecked}, Diagnostics: []diagnostic.Result{result}, Model: []navproto.ActorState{{Actor: "tenant-a", Diagnostics: []diagnostic.Result{cursor}}}}})
	text := strings.Join(screen.detail, "\n")
	if !strings.Contains(text, "whole-run diagnostics · independent of cursor") || !strings.Contains(text, "diagnostics · cursor state, no verdict") {
		t.Fatalf("scopes not labeled:\n%s", text)
	}
}
