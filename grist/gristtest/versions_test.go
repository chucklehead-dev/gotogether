package gristtest_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

type logged struct {
	Topic string `json:"topic" grist:"tag"`
}

// A log captured from one store, written and read back, restores into a
// fresh store the same history: the events at the same positions, the
// markers and the step rows (timed anew), so a later version's node
// relays and serves the old one's log.
func TestLogRoundTrip(t *testing.T) {
	a := grist.New("notes")
	ev := grist.NewEvent[logged](a, "noted")
	note := grist.NewCommand(a, "note", func(r *grist.Reads, c logged) ([]grist.Event, error) {
		if r.Exists(ev, grist.Tag("topic", c.Topic)) {
			return nil, grist.Rejection("taken")
		}
		return []grist.Event{ev.Of(c)}, nil
	}, grist.Refuses(grist.Rejection("taken")))
	ctx := context.Background()
	src := gristtest.Capture(memstore.New("notes"))
	src.CreateTenant(ctx, "t1")
	for i, topic := range []string{"a", "b", "a"} { // two commits, a refusal (a marker and a step row, no event)
		note.Execute(ctx, src, grist.Envelope{ID: []string{"c1", "c2", "c3"}[i], Tenant: "t1"}, logged{Topic: topic})
	}
	path := filepath.Join(t.TempDir(), "log.jsonl")
	if err := gristtest.WriteLog(src.Log(), path); err != nil {
		t.Fatal(err)
	}
	l := gristtest.ReadLog(t, path)
	if len(l.Batches) != 3 || !reflect.DeepEqual(l.Tenants(), []string{"t1"}) {
		t.Fatalf("log of %d batches, tenants %v: want 3 of t1", len(l.Batches), l.Tenants())
	}
	dst := memstore.New("notes")
	if err := l.Restore(ctx, dst); err != nil {
		t.Fatal(err)
	}
	all := store.Selector{Types: []string{"noted"}}
	want, _, _ := src.Read(ctx, "t1", all, 0)
	got, _, _ := dst.Read(ctx, "t1", all, 0)
	for i := range got {
		got[i].Time, want[i].Time = 0, 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("restored events %v, want %v", got, want)
	}
	if m, ok, _ := dst.Marker(ctx, "t1", "c3"); !ok || m.Outcome != "taken" {
		t.Errorf("restored refusal's marker: %+v %v", m, ok)
	}
	srcRows, _ := src.Steps(ctx, "t1", 0, 0)
	dstRows, _ := dst.Steps(ctx, "t1", 0, 0)
	if len(dstRows) != 3 || string(dstRows[2].Step) != string(srcRows[2].Step) {
		t.Errorf("restored step rows %d, want the 3 with their steps", len(dstRows))
	}
	if err := l.Restore(ctx, dst); err == nil {
		t.Error("a log restored twice into one store: want its first batch's failure (restore into a fresh store)")
	}
}
