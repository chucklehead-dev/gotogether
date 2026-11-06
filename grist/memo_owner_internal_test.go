package grist

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

type ownedMemoAdded struct{ By int }

func ownedMemoApp(entries int) (*App, *EventType[ownedMemoAdded], *ReadModel[int], *QueryBlock[struct{}, int]) {
	a := New("owned-memo", Snapshots(10), AuditSnapshots(0), Memo(entries))
	added := NewEvent[ownedMemoAdded](a, "added")
	count := NewReadModel(a, "count", 1, func() int { return 0 }, On(added, func(s int, e ownedMemoAdded, _ Meta) int { return s + e.By }))
	query := NewQuery(a, "count-query", func(r *Reads, _ struct{}) (int, error) { return count.Get(r), nil })
	return a, added, count, query
}

func appendOwnedMemo(t *testing.T, st *memstore.Store, added *EventType[ownedMemoAdded], n, by int) {
	t.Helper()
	for range n {
		if _, err := st.Append(t.Context(), "t", store.Batch{Events: []Event{added.Of(ownedMemoAdded{by})}}); err != nil {
			t.Fatal(err)
		}
	}
}

// A real processor's historical reads and ordinary head queries share a model
// and store. Alternating them must not refold the processor's whole prefix or
// replace an observed later snapshot. Its first historical read is still cold.
func TestProcessorMemoOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, added, count, query := ownedMemoApp(4)
		st := memstore.New("memo-owner")
		if err := st.CreateTenant(t.Context(), "t"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		head, seen, fp := 1000, 0, ""
		NewProcessor(a, "lagging", added, func(ctx context.Context, r *Reads, _ ownedMemoAdded, meta Meta) Result {
			before := a.ReadStats().Folded
			got := count.Get(r)
			folds := a.ReadStats().Folded - before
			wantFolds := int64(1)
			if seen == 0 {
				wantFolds = 950
			}
			if got != int(meta.Pos) || folds != wantFolds {
				t.Errorf("trigger %d: count %d, folded %d; want %d, %d", meta.Pos, got, folds, meta.Pos, wantFolds)
			}
			seen++
			snapshot, ok, err := st.GetSnapshot(ctx, "t", "count", "", fp)
			if err != nil || !ok || snapshot.Pos != Position(head) {
				t.Errorf("trigger %d replaced head snapshot: %+v, found %v, err %v; want %d", meta.Pos, snapshot, ok, err, head)
			}
			// Advance the head after the historical cursor warmed: its next
			// cadence must recheck the snapshot rather than reuse an old observation.
			if meta.Pos == 951 {
				appendOwnedMemo(t, st, added, 500, 1)
				head = 1500
			}
			n, _, err := query.Run(ctx, st, Envelope{ID: fmt.Sprint(meta.Pos), Tenant: "t"}, struct{}{})
			if err != nil || n != head {
				t.Errorf("head after trigger %d: %d, %v; want %d", meta.Pos, n, err, head)
			}
			// End at a fixed cursor instead of depending on whether the
			// newly appended head's next batch has become runnable at Wait.
			if meta.Pos == 1000 {
				cancel()
			}
			return Emit()
		})
		appendOwnedMemo(t, st, added, 1000, 1)
		if _, err := st.Append(ctx, "t", store.Batch{Checkpoint: &store.CheckpointMove{Processor: "lagging", To: 949}}); err != nil {
			t.Fatal(err)
		}
		if n, _, err := query.Run(ctx, st, Envelope{ID: "warm", Tenant: "t"}, struct{}{}); err != nil || n != 1000 {
			t.Fatalf("warm head: %d, %v", n, err)
		}
		fp = a.Catalog().ReadModels[0].Fingerprint
		if err := a.Start(ctx, st); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if seen != 51 {
			t.Fatalf("processed %d initial triggers, want 950..1000", seen)
		}
		cancel()
		synctest.Wait()
	})
}

// Distinct named processor cursors remain independent, while their entries
// and the ordinary head entry all compete for one LRU capacity.
func TestMemoOwnersShareCapacity(t *testing.T) {
	for _, capacity := range []int{2, 3} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			a, added, count, query := ownedMemoApp(capacity)
			st := memstore.New("owner-capacity")
			if err := st.CreateTenant(t.Context(), "t"); err != nil {
				t.Fatal(err)
			}
			appendOwnedMemo(t, st, added, 100, 1)
			if n, _, err := query.Run(t.Context(), st, Envelope{ID: "head", Tenant: "t"}, struct{}{}); err != nil || n != 100 {
				t.Fatalf("head: %d %v", n, err)
			}
			read := func(owner string, pos Position) int64 {
				t.Helper()
				r := readsAt(t.Context(), a, st, "t", pos, "trigger")
				r.memoOwner = owner
				before := a.ReadStats().Folded
				if got := count.Get(r); got != int(pos) || r.err != nil || r.bad != nil {
					t.Fatalf("%s at %d: %d, %v, %v", owner, pos, got, r.err, r.bad)
				}
				return a.ReadStats().Folded - before
			}
			read("slow", 50)
			read("faster", 70)
			if n := read("slow", 51); n != 1 {
				t.Fatalf("slow cursor folded %d, want 1", n)
			}
			if n := read("faster", 71); n != 1 {
				t.Fatalf("faster cursor folded %d, want 1", n)
			}
			table := a.memos.of(st)
			table.mu.Lock()
			entries := table.lru.Len()
			table.mu.Unlock()
			if entries != capacity {
				t.Fatalf("memo keeps %d states, want total capacity %d", entries, capacity)
			}
			before := a.ReadStats().MemoHits
			if n, _, err := query.Run(t.Context(), st, Envelope{ID: "head-again", Tenant: "t"}, struct{}{}); err != nil || n != 100 {
				t.Fatalf("head again: %d %v", n, err)
			}
			hit := a.ReadStats().MemoHits != before
			if hit != (capacity == 3) {
				t.Fatalf("head memo hit %v with capacity %d; owners must share one budget", hit, capacity)
			}
		})
	}
}

func TestMemoOwnerRewindAndPinnedLimit(t *testing.T) {
	a, added, count, query := ownedMemoApp(4)
	st := memstore.New("owner-rewind")
	if err := st.CreateTenant(t.Context(), "t"); err != nil {
		t.Fatal(err)
	}
	appendOwnedMemo(t, st, added, 50, 1)
	backup := st.Backup()
	appendOwnedMemo(t, st, added, 50, 1)
	if n, _, err := query.Run(t.Context(), st, Envelope{ID: "old-head", Tenant: "t"}, struct{}{}); err != nil || n != 100 {
		t.Fatalf("old head: %d %v", n, err)
	}
	read := func(pos Position, want int) {
		t.Helper()
		r := readsAt(t.Context(), a, st, "t", pos, "trigger")
		r.memoOwner = "lagging"
		if n := count.Get(r); n != want || r.err != nil || r.bad != nil {
			t.Fatalf("cursor at %d: %d %v %v; want %d", pos, n, r.err, r.bad, want)
		}
	}
	read(80, 80)
	read(70, 70) // a future cached state cannot answer an earlier pinned read
	read(80, 80)
	st.Restore(backup)
	appendOwnedMemo(t, st, added, 50, 2)
	read(81, 112)
	if n, _, err := query.Run(t.Context(), st, Envelope{ID: "new-head", Tenant: "t"}, struct{}{}); err != nil || n != 150 {
		t.Fatalf("replacement head: %d %v", n, err)
	}
	if n := a.ReadStats().MemoStale; n != 2 {
		t.Fatalf("stale owners %d, want both processor and head", n)
	}
}
