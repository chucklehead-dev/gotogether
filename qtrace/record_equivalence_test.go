package qtrace

import (
	"testing"
	"time"
)

func TestSameRecordCapturedEquivalence(t *testing.T) {
	instant := time.Date(2026, 10, 30, 12, 0, 0, 123, time.UTC)
	a := Step{Actor: "a", Recorder: "r", Seq: 1, Action: "observe", Source: "first.jsonl:2", Time: instant, Invoked: instant.Add(-time.Second)}
	b := a
	b.Source = "second.jsonl:9"
	b.Args, b.Obs, b.Order = map[string]any{}, map[string]any{}, map[string]int64{}
	b.Time = b.Time.In(time.FixedZone("other", 3600))
	b.Invoked = b.Invoked.In(time.FixedZone("other", -3600))
	if !SameRecord(&a, &b) || !SameRecord(&b, &a) {
		t.Fatal("duplicate exports differ only in representation and display provenance")
	}
	traces, issues := Reconstruct([]Step{a, b})
	if len(traces) != 1 || len(traces[0].Steps) != 1 || len(traces[0].Issues) != 0 || len(issues) != 1 || issues[0].Severity != "info" {
		t.Fatalf("reconstruction disagrees with record equivalence: traces %+v issues %+v", traces, issues)
	}
	// Loss and watermark bounds are instants too, regardless of location.
	a.LostBefore, a.CompleteThrough = instant, instant
	b.LostBefore = instant.In(time.FixedZone("west", -3600))
	b.CompleteThrough = instant.In(time.FixedZone("east", 3600))
	if !SameRecord(&a, &b) {
		t.Fatal("same loss/watermark bounds compare differently by location")
	}
	for _, alter := range []func(*Step){
		func(s *Step) { s.Outcome = "different" },
		func(s *Step) { s.Ref = "different written artifact" },
		func(s *Step) { s.Time = s.Time.Add(time.Nanosecond) },
		func(s *Step) { s.Args = map[string]any{"value": 1} },
	} {
		c := b
		alter(&c)
		if SameRecord(&a, &c) {
			t.Fatalf("different captured contents considered a duplicate: %+v", c)
		}
	}
	if !SameRecord(nil, nil) || SameRecord(&a, nil) || SameRecord(nil, &a) {
		t.Fatal("nil record equivalence")
	}
}
