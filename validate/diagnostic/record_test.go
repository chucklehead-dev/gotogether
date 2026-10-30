package diagnostic

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

func TestParseRecordRef(t *testing.T) {
	ref, err := ParseRecordRef(`{"actor":"a","recorder":"r","seq":18446744073709551615}`)
	if err != nil || ref.Seq != ^uint64(0) {
		t.Fatalf("exact sequence: %+v %v", ref, err)
	}
	for _, bad := range []string{`null`, `{}`, `{"actor":"a","recorder":"r","seq":0}`, `{"actor":"a","recorder":"r","seq":1,"typo":2}`, `{"actor":"a","recorder":"r","seq":1} {}`, `{"actor":"a","recorder":"r","seq":1.5}`} {
		if _, err := ParseRecordRef(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestLookupRecordIdentityAndIsolation(t *testing.T) {
	steps := []qtrace.Step{
		{Actor: "tenant-a", Recorder: "node", Seq: 7, Action: "cancel", Args: map[string]any{"id": "same", "amount": int64(9007199254740993)}, Source: "capture:9"},
		{Actor: "tenant-b", Recorder: "node", Seq: 7, Action: "place", Args: map[string]any{"id": "same"}},
	}
	ref := RecordRef{Actor: "tenant-a", Recorder: "node", Seq: 7, Source: "arbitrary display text"}
	d, err := LookupRecord(steps, ref)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "cancel" || d.Reference.Source != "capture:9" || !strings.Contains(string(d.Arguments), "9007199254740993") {
		t.Fatalf("wrong captured record: %+v", d)
	}
	d.Arguments[0] = '!'
	again, err := LookupRecord(steps, ref)
	if err != nil || !strings.HasPrefix(string(again.Arguments), "{") {
		t.Fatal("inspection mutated capture")
	}
	copy := steps[0]
	copy.Source = "copy:10"
	if _, err := LookupRecord(append(steps, copy), ref); err != nil {
		t.Fatalf("duplicate export: %v", err)
	}
	copy.Outcome = "changed"
	if _, err := LookupRecord(append(steps, copy), ref); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("conflicting identity: %v", err)
	}
	ref.Seq = 8
	if _, err := LookupRecord(steps, ref); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing capture: %v", err)
	}
	ref.Seq = 0
	if _, err := LookupRecord(steps, ref); err == nil {
		t.Fatal("accepted absent identity")
	}
}
