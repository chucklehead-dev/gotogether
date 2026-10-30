package diagnostic

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Inspection must apply the same duplicate policy as trace reconstruction:
// ingestion representation differences cannot turn a valid copy into a conflict.
func TestLookupRecordMatchesReconstructionEquivalence(t *testing.T) {
	instant := time.Date(2026, 10, 30, 12, 0, 0, 123, time.UTC)
	a := qtrace.Step{Actor: "a", Recorder: "r", Seq: 1, Action: "observe", Source: "first.jsonl:2", Time: instant, Invoked: instant.Add(-time.Second)}
	ref := RecordRef{Actor: a.Actor, Recorder: a.Recorder, Seq: a.Seq}
	for _, tc := range []struct {
		name   string
		change func(*qtrace.Step)
	}{
		{"empty arguments", func(s *qtrace.Step) { s.Args = map[string]any{} }},
		{"empty observations", func(s *qtrace.Step) { s.Obs = map[string]any{} }},
		{"empty order", func(s *qtrace.Step) { s.Order = map[string]int64{} }},
		{"same instants", func(s *qtrace.Step) {
			s.Time = s.Time.In(time.FixedZone("east", 3600))
			s.Invoked = s.Invoked.In(time.FixedZone("west", -3600))
		}},
		{"display provenance", func(s *qtrace.Step) { s.Source = "second.jsonl:9" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := a
			tc.change(&b)
			steps := []qtrace.Step{a, b}
			traces, issues := qtrace.Reconstruct(steps)
			if len(traces) != 1 || len(traces[0].Steps) != 1 || len(traces[0].Issues) != 0 || len(issues) != 1 || issues[0].Severity != "info" {
				t.Fatalf("reconstruction rejected duplicate export: traces %+v issues %+v", traces, issues)
			}
			detail, err := LookupRecord(steps, ref)
			if err != nil || detail.Source != a.Source || detail.Reference.Seq != a.Seq {
				t.Fatalf("inspection disagreed with reconstruction: %+v %v", detail, err)
			}
		})
	}
	b := a
	b.Outcome = "conflicting"
	traces, _ := qtrace.Reconstruct([]qtrace.Step{a, b})
	if len(traces) != 1 || len(traces[0].Issues) != 1 || traces[0].Issues[0].Severity != "error" {
		t.Fatal("reconstruction did not flag conflicting capture")
	}
	if _, err := LookupRecord([]qtrace.Step{a, b}, ref); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("inspection accepted conflicting capture: %v", err)
	}
}

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
