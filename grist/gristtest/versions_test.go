package gristtest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
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
	if len(got) != len(want) {
		t.Fatalf("restored %d events, want %d", len(got), len(want))
	}
	for i := range got {
		if want[i].ID == "" || got[i].ID != want[i].ID || len(l.Batches[i].Batch.RetainedIDs) != 1 || l.Batches[i].Batch.RetainedIDs[0] != want[i].ID {
			t.Fatalf("capture/restore lost original event identity: source=%+v restored=%+v batch=%+v", want[i], got[i], l.Batches[i])
		}
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

type captureHooks struct {
	*memstore.Store
	appendBatch func(context.Context, string, store.Batch) (store.Position, error)
	readEvents  func(context.Context, string, store.Selector, store.Position) ([]store.Stored, store.Position, error)
}

func (s *captureHooks) Append(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
	if s.appendBatch != nil {
		return s.appendBatch(ctx, tenant, b)
	}
	return s.Store.Append(ctx, tenant, b)
}

func (s *captureHooks) Read(ctx context.Context, tenant string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	if s.readEvents != nil {
		return s.readEvents(ctx, tenant, q, at)
	}
	return s.Store.Read(ctx, tenant, q, at)
}

func capturedBatch() store.Batch {
	return store.Batch{Events: []store.Event{{Type: "noted", Version: 1, Tags: []store.TagPair{{Key: "topic", Value: "same"}}, Body: []byte(`{"topic":"same"}`)}}, Time: 1700000000000, Cause: "same-command"}
}

// Reading identities after Append is unsound: a restore can replace a commit
// with identical payload/envelope before the call returns. Capture submits its
// original identities in the transaction, and never reads the replacement.
func TestCaptureKeepsCommittedIdentityWithoutPostCommitRead(t *testing.T) {
	ctx := t.Context()
	base := memstore.New("capture")
	if err := base.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	backup := base.Backup()
	var committedID string
	reads := 0
	wrapped := &captureHooks{Store: base}
	wrapped.appendBatch = func(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
		p, err := base.Append(ctx, tenant, b)
		if err != nil {
			return p, err
		}
		if len(b.RetainedIDs) != 1 || b.RetainedIDs[0] == "" {
			t.Fatal("capture did not submit an identity with the append")
		}
		committedID = b.RetainedIDs[0]
		base.Restore(backup)
		b.RetainedIDs = nil // Another execution fills the same position.
		if _, err := base.Append(ctx, tenant, b); err != nil {
			t.Fatal(err)
		}
		return p, nil
	}
	wrapped.readEvents = func(context.Context, string, store.Selector, store.Position) ([]store.Stored, store.Position, error) {
		reads++
		return nil, 0, errors.New("post-commit reads are unavailable")
	}
	capture := gristtest.Capture(wrapped)
	input := capturedBatch()
	if p, err := capture.Append(ctx, "t1", input); err != nil || p != 1 {
		t.Fatalf("capture changed known committed result: head=%d err=%v", p, err)
	}
	if reads != 0 || input.RetainedIDs != nil {
		t.Fatalf("capture read after commit or mutated input: reads=%d input=%+v", reads, input)
	}
	log := capture.Log()
	if log.CaptureError != "" || len(log.Batches) != 1 || log.Batches[0].Batch.RetainedIDs[0] != committedID {
		t.Fatalf("capture did not retain the identity it submitted: %+v", log)
	}
	current, _, err := base.Read(ctx, "t1", store.Selector{Types: []string{"noted"}}, 0)
	if err != nil || len(current) != 1 || current[0].ID == committedID {
		t.Fatalf("required identical replacement did not get a new identity: %+v (%v)", current, err)
	}
	restored := memstore.New("restored")
	if err := log.Restore(ctx, restored); err != nil {
		t.Fatal(err)
	}
	events, _, err := restored.Read(ctx, "t1", store.Selector{Types: []string{"noted"}}, 0)
	if err != nil || len(events) != 1 || events[0].ID != committedID {
		t.Fatalf("restored captured original instead of its replacement: %+v (%v)", events, err)
	}
	// Neither caller-owned batches nor returned snapshots can alter capture.
	input.Events[0].Body[0] = 'x'
	log.Batches[0].Batch.Events[0].Tags[0].Value = "changed"
	log.Batches[0].Batch.RetainedIDs[0] = "changed"
	if again := capture.Log(); again.Batches[0].Batch.RetainedIDs[0] != committedID || again.Batches[0].Batch.Events[0].Tags[0].Value != "same" || again.Batches[0].Batch.Events[0].Body[0] != '{' {
		t.Fatalf("capture aliased mutable caller data: %+v", again)
	}
}

func assertUnusableLog(t *testing.T, log gristtest.Log) {
	t.Helper()
	if log.CaptureError == "" {
		t.Fatal("incomplete history was not marked unusable")
	}
	path := filepath.Join(t.TempDir(), "existing.jsonl")
	if err := os.WriteFile(path, []byte("existing evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := gristtest.WriteLog(log, path); err == nil {
		t.Fatal("wrote an incomplete capture")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "existing evidence" {
		t.Fatalf("refused write changed existing evidence: %q (%v)", got, err)
	}
	dst := memstore.New("must-stay-empty")
	if err := log.Restore(t.Context(), dst); err == nil {
		t.Fatal("restored an incomplete capture")
	}
	if tenants, err := dst.Tenants(t.Context()); err != nil || len(tenants) != 0 {
		t.Fatalf("refused restore changed store: %+v (%v)", tenants, err)
	}
}

func TestCaptureRefusesUnknownCommitHistory(t *testing.T) {
	ctx := t.Context()
	base := memstore.New("capture")
	if err := base.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	unknown := errors.New("connection lost after commit")
	capture := gristtest.Capture(&captureHooks{Store: base, appendBatch: func(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
		if _, err := base.Append(ctx, tenant, b); err != nil {
			t.Fatal(err)
		}
		return 0, unknown
	}})
	if p, err := capture.Append(ctx, "t1", capturedBatch()); p != 0 || !errors.Is(err, unknown) {
		t.Fatalf("capture reclassified the underlying unknown result: %d %v", p, err)
	}
	events, head, err := base.Read(ctx, "t1", store.Selector{Types: []string{"noted"}}, 0)
	if err != nil || head != 1 || len(events) != 1 {
		t.Fatalf("required unknown commit never landed: %+v head=%d (%v)", events, head, err)
	}
	assertUnusableLog(t, capture.Log())
}

func TestCaptureRefusesOverlappingAppends(t *testing.T) {
	ctx := t.Context()
	base := memstore.New("capture")
	if err := base.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseAppend := sync.OnceFunc(func() { close(release) })
	defer releaseAppend()
	var attempts atomic.Int32
	capture := gristtest.Capture(&captureHooks{Store: base, appendBatch: func(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
		if attempts.Add(1) == 1 {
			close(entered)
			<-release
		}
		return base.Append(ctx, tenant, b)
	}})
	done := make(chan error, 1)
	go func() { _, err := capture.Append(ctx, "t1", capturedBatch()); done <- err }()
	<-entered
	_, secondErr := capture.Append(ctx, "t1", capturedBatch())
	releaseAppend()
	if err := <-done; err != nil || secondErr != nil {
		t.Fatalf("capture altered successful application results: first=%v second=%v", err, secondErr)
	}
	assertUnusableLog(t, capture.Log())
}

func TestCaptureSnapshotRefusesInFlightAppend(t *testing.T) {
	ctx := t.Context()
	base := memstore.New("capture")
	if err := base.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseAppend := sync.OnceFunc(func() { close(release) })
	defer releaseAppend()
	capture := gristtest.Capture(&captureHooks{Store: base, appendBatch: func(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
		close(entered)
		<-release
		return base.Append(ctx, tenant, b)
	}})
	done := make(chan error, 1)
	go func() { _, err := capture.Append(ctx, "t1", capturedBatch()); done <- err }()
	<-entered
	assertUnusableLog(t, capture.Log()) // Only this outstanding snapshot.
	releaseAppend()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	completed := capture.Log()
	if completed.CaptureError != "" || len(completed.Batches) != 1 {
		t.Fatalf("sampling poisoned an eventually complete history: %+v", completed)
	}
	if err := completed.Restore(ctx, memstore.New("complete")); err != nil {
		t.Fatalf("completed capture could not restore: %v", err)
	}
}

func TestCaptureRetriesOnlyItsOwnIdentityCollisions(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "allocated", true: "retained"}[explicit], func(t *testing.T) {
			ctx := t.Context()
			base := memstore.New("capture")
			if err := base.CreateTenant(ctx, "t1"); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			var firstID string
			capture := gristtest.Capture(&captureHooks{Store: base, appendBatch: func(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
				attempts++
				if attempts == 1 {
					firstID = b.RetainedIDs[0]
					return 0, store.Permanent(&store.IdentityExists{ID: firstID})
				}
				return base.Append(ctx, tenant, b)
			}})
			batch := capturedBatch()
			if explicit {
				batch.RetainedIDs = []string{"00000000000000000000000000000001"}
			}
			_, err := capture.Append(ctx, "t1", batch)
			log := capture.Log()
			if log.CaptureError != "" {
				t.Fatalf("known noncommit collision poisoned capture: %+v", log)
			}
			if explicit {
				var exists *store.IdentityExists
				if attempts != 1 || !errors.As(err, &exists) || len(log.Batches) != 0 || batch.RetainedIDs[0] != firstID {
					t.Fatalf("explicit original identity was retried or rewritten: attempts=%d err=%v log=%+v", attempts, err, log)
				}
			} else if err != nil || attempts != 2 || len(log.Batches) != 1 || log.Batches[0].Batch.RetainedIDs[0] == firstID {
				t.Fatalf("allocation collision was not safely retried: attempts=%d err=%v log=%+v", attempts, err, log)
			}
		})
	}
}
