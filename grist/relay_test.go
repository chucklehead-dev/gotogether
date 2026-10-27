package grist_test

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A commit's step is recorded from its step row, whoever relays it and
// however often: a command whose node recorded nothing (it died between
// its COMMIT and any record) has its steps recorded by a later relay, with
// the fields the head gives (obs.head, order.pos), and a second relay of
// the same rows (a restarted node, another node) makes the same records,
// which Reconstruct keeps once. Recorded through qobs.Record instead (a
// recorder's own numbering), the second relay's copies would be new steps
// and every commit would be in the trace twice.
func TestRelayRecordsCommitsOnce(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	for i, name := range []string{"a", "a", "b"} { // a commit, a refusal (no head moved), a commit
		cmd.Execute(context.Background(), s, grist.Envelope{ID: []string{"c1", "c2", "c3"}[i], Tenant: "t1"}, Create{Counter: "x", Name: name})
	}
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	for range 2 {
		r, err := grist.NewRelay(s)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.Behind(ctx); err != nil || n != 3 {
			t.Fatalf("a new relay of three rows is behind by %d (%v), want 3", n, err)
		}
		if err := r.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if err := r.Flush(ctx); err != nil { // nothing new: records nothing
			t.Fatal(err)
		}
		if n, err := r.Behind(ctx); err != nil || n != 0 {
			t.Fatalf("a flushed relay is behind by %d (%v), want 0", n, err)
		}
	}
	if n := len(sink.Steps()); n != 6 {
		t.Fatalf("%d records from two relays of three rows, want 6", n)
	}
	traces, issues := qtrace.Reconstruct(sink.Steps())
	if len(traces) != 1 || len(traces[0].Steps) != 3 {
		t.Fatalf("traces %v: want t1's three steps, each kept once (issues %v)", traces, issues)
	}
	dups := 0
	for _, is := range issues {
		if is.Kind == "duplicate" && is.Severity == "info" {
			dups++
		}
	}
	if dups != 3 {
		t.Errorf("issues %v: want three copies dropped as duplicates", issues)
	}
	for i, want := range []struct {
		outcome   string
		head, pos int64
	}{{"ok", 1, 2}, {"name-taken", 1, 3}, {"ok", 2, 4}} {
		st := traces[0].Steps[i]
		if st.Outcome != want.outcome || st.Obs["head"] != want.head || st.Order["pos"] != want.pos ||
			st.Recorder != grist.RecorderID("counters", "t1") || st.Seq != uint64(i+1) || st.Actor != "t1" || st.Process != "counters" {
			t.Errorf("step %d: %v; want %s at head %d, order.pos %d, seq %d of t1's relayed recorder", i+1, st, want.outcome, want.head, want.pos, i+1)
		}
	}
}

// A node needs a store that keeps a step log: it records every commit
// from it.
func TestStartNeedsAStepLog(t *testing.T) {
	a, _, _ := app(false)
	if err := a.Start(context.Background(), noLog{newStore(t)}); err == nil {
		t.Error("Start on a store without a step log: no error")
	}
}

// noLog is a store that keeps no step log.
type noLog struct{ grist.Store }
