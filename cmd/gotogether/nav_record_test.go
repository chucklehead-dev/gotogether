package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
)

func TestNavCapturedRecordFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Record inspection must work even when this action has no model binding.
	step := qtrace.Step{Actor: "checkout/t1", Recorder: "checkout", Seq: 8, Action: "cancel", Outcome: "ok", Args: map[string]any{"intent": "cancel-1", "units": int64(9007199254740993)}}
	if err := qtrace.WriteJSONL(file, []qtrace.Step{step}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	ref := `{"actor":"checkout/t1","recorder":"checkout","seq":8}`
	var out bytes.Buffer
	code, err := nav(&out, []string{"-run", path, "-record", ref, "-json"})
	if err != nil || code != 0 {
		t.Fatalf("nav record: %d %v: %s", code, err, out.String())
	}
	var got diagnostic.RecordDetail
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Reference.Seq != 8 || got.Reference.Actor != step.Actor || got.Action != "cancel" || !bytes.Contains(got.Arguments, []byte("9007199254740993")) {
		t.Fatalf("lost exact captured data: %s", out.String())
	}
	out.Reset()
	code, err = nav(&out, []string{"-run", path, "-record", ref})
	if err != nil || code != 0 || !strings.Contains(out.String(), "captured record · no model judgment or cursor move") || !strings.Contains(out.String(), "9007199254740993") {
		t.Fatalf("record text: %d %v: %s", code, err, out.String())
	}
	out.Reset()
	code, err = nav(&out, []string{"-run", path, "-record", `{"actor":"checkout/t1","recorder":"native","seq":8}`, "-json"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "missing from this capture") {
		t.Fatalf("missing identity silently substituted: %d %v", code, err)
	}
}

func TestNavRecordRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	valid := `{"actor":"a","recorder":"r","seq":1}`
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown", []string{"-record", `{"actor":"a","recorder":"r","seq":1,"other":true}`}, "unknown field"},
		{"trailing", []string{"-record", valid + ` {}`}, "one JSON object"},
		{"empty", []string{"-record", ""}, "nav -record"},
		{"zero", []string{"-record", `{"actor":"a","recorder":"r","seq":0}`}, "nonzero"},
		{"no capture", []string{"-record", valid}, "add -run"},
		{"follow", []string{"-record", valid, "-run", "capture", "-follow", "tick"}, "drop -follow"},
		{"take", []string{"-record", valid, "-run", "capture", "-take", "tick"}, "drop -follow"},
		{"fold", []string{"-record", valid, "-run", "capture", "-fold"}, "drop -follow"},
		{"save", []string{"-record", valid, "-run", "capture", "-save"}, "drop -follow"},
		{"file cursor", []string{"-record", valid, "-run", "capture", "-at", "2"}, "drop -at"},
		{"different actor", []string{"-record", valid, "-run", "capture", "-actor", "b"}, "differs from record actor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			code, err := nav(&out, tc.args)
			if code != 2 || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %d %v, want %q", code, err, tc.want)
			}
		})
	}
}

func TestNavRecordRunTransport(t *testing.T) {
	dir := t.TempDir()
	ref := diagnostic.RecordRef{Actor: "checkout/t1", Recorder: "checkout", Seq: 8}
	record := diagnostic.RecordDetail{Reference: ref, Action: "cancel", Arguments: json.RawMessage(`{"intent":"cancel-1"}`), Observations: json.RawMessage(`{}`)}
	response, err := json.Marshal(navproto.Node{Protocol: navproto.Protocol, ID: "@3", Record: &record})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "response.json")
	if err := os.WriteFile(fixture, response, 0o600); err != nil {
		t.Fatal(err)
	}
	goScript := "#!/bin/sh\nprintf '%s' \"$GOTOGETHER_NAV_RECORD\" > \"$NAV_RECORD_CAPTURE\"\nprintf '%s' \"$GOTOGETHER_NAV\" > \"$NAV_NODE_CAPTURE\"\ncp \"$NAV_RESPONSE_FIXTURE\" \"$GOTOGETHER_NAV_OUT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(goScript), 0o700); err != nil {
		t.Fatal(err)
	}
	recordCapture, nodeCapture := filepath.Join(dir, "requested.json"), filepath.Join(dir, "node.txt")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOTOGETHER_MUTANT", "")
	t.Setenv("NAV_RECORD_CAPTURE", recordCapture)
	t.Setenv("NAV_NODE_CAPTURE", nodeCapture)
	t.Setenv("NAV_RESPONSE_FIXTURE", fixture)
	var out bytes.Buffer
	code, err := nav(&out, []string{"-run", "TestEvidence#1:seed=1", "-pkg", dir, "-at", "@3", "-record", `{"actor":"checkout/t1","recorder":"checkout","seq":8}`, "-json"})
	if err != nil || code != 0 {
		t.Fatalf("nav run record: %d %v: %s", code, err, out.String())
	}
	var got diagnostic.RecordDetail
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Reference != ref {
		t.Fatalf("record output: %v: %s", err, out.String())
	}
	b, err := os.ReadFile(recordCapture)
	if err != nil {
		t.Fatal(err)
	}
	var request diagnostic.RecordRef
	if err := json.Unmarshal(b, &request); err != nil || request != ref {
		t.Fatalf("lost record request: %s: %v", b, err)
	}
	b, err = os.ReadFile(nodeCapture)
	if err != nil || string(b) != "@3" {
		t.Fatalf("changed selected schedule: %s: %v", b, err)
	}
}
