package grist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// addCounted appends n additions of item, each of count by.
func addCounted(t *testing.T, s grist.Store, added *grist.EventType[ItemAdded], item string, n, by int) {
	t.Helper()
	for range n {
		if _, err := s.Append(context.Background(), "t1", storeBatch(added.Of(ItemAdded{Item: item, Count: by}))); err != nil {
			t.Fatal(err)
		}
	}
}

// countOf is q's count of x on s under ctx.
func countOf(t *testing.T, ctx context.Context, q *grist.QueryBlock[ItemQuery, int], s grist.Store) int {
	t.Helper()
	n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A read of a state the memo holds reads its last event and the events
// after it: no snapshot looked up, nothing before it read. Only a read
// that folded Snapshots' every events memoizes (a short log reads as it
// always did), and the memo is the store's: another store of the same
// tenant and read model has its own.
func TestMemoCutsReads(t *testing.T) {
	a, added, q := items()
	s := newCounting(t)
	addItems(t, s, added, "x", 50)
	ctx := grist.SnapshotAudit(grist.SnapshotEvery(context.Background(), 10), 0)
	read := func(st *counting, want int) (events, gets int64) {
		t.Helper()
		r, g := st.read.Load(), st.gets.Load()
		if n := countOf(t, ctx, q, st); n != want {
			t.Fatalf("count of x: %d, want %d", n, want)
		}
		return st.read.Load() - r, st.gets.Load() - g
	}
	if n, _ := read(s, 50); n != 50 {
		t.Fatalf("the first read read %d events, want 50", n)
	}
	if n, g := read(s, 50); n != 1 || g != 0 {
		t.Errorf("a read from the memo read %d events and looked up %d snapshots, want its last event alone and none", n, g)
	}
	addItems(t, s, added, "x", 4)
	if n, g := read(s, 54); n != 1+4 || g != 0 {
		t.Errorf("a read from the memo after 4 more read %d events (%d lookups), want its last event and the 4", n, g)
	}
	if rs := a.ReadStats(); rs.MemoHits != 2 {
		t.Errorf("%d memo hits, want 2", rs.MemoHits)
	}
	other := newCounting(t) // another store: another memo
	addItems(t, other, added, "x", 50)
	if n, _ := read(other, 50); n != 50 {
		t.Errorf("another store's first read read %d events, want all 50: the memo is per store", n)
	}
	// a short log: no memo, as without one
	short := newCounting(t)
	addItems(t, short, added, "x", 9)
	read(short, 9)
	if n, g := read(short, 9); n != 9 || g != 0 {
		t.Errorf("a short log's second read read %d events (%d lookups), want its 9 and none, as without a memo", n, g)
	}
	// off: snapshots only
	if n, _ := func() (int64, int64) { ctx = grist.NoMemo(ctx); return read(s, 54) }(); n != 10+1+4 {
		t.Errorf("with the memo off a read read %d events, want the first 10, the snapshot's last event and the 4 after it", n)
	}
}

// rewound is a store whose log went back: 10 additions of x (count 1),
// a backup, 10 more read (memoized and snapshotted at 20), the store
// restored to the backup and 10 others appended (count 2): positions 11
// to 20 again, other events. It returns the store and the snapshot the
// read took before the restore.
func rewound(t *testing.T, ctx context.Context, a *grist.App, q *grist.QueryBlock[ItemQuery, int], added *grist.EventType[ItemAdded]) (*memstore.Store, store.Snapshot) {
	t.Helper()
	s := newStoreNamed(t, "items")
	addCounted(t, s, added, "x", 10, 1)
	b := s.Backup()
	addCounted(t, s, added, "x", 10, 1)
	if n := countOf(t, ctx, q, s); n != 20 {
		t.Fatalf("before the restore: %d, want 20", n)
	}
	fp := a.Catalog().ReadModels[0].Fingerprint
	snap, ok, _ := s.GetSnapshot(context.Background(), "t1", "counts", "item=x", fp)
	if !ok || snap.Pos != 20 {
		t.Fatalf("no snapshot at 20 before the restore: %+v", snap)
	}
	s.Restore(b)
	addCounted(t, s, added, "x", 10, 2)
	return s, snap
}

// A rewound log invalidates the memo: the state it held at position 20
// is the old history's, and position 20 holds another event now, so the
// read refolds (counted stale) and answers the new history's 30. With
// the memo trusting the position alone (the mutant memoTrustsPosition) it
// answers the old 20.
func TestRewindInvalidatesTheMemo(t *testing.T) {
	a, added, q := items()
	ctx := grist.SnapshotAudit(grist.SnapshotEvery(context.Background(), 5), 0)
	s, _ := rewound(t, ctx, a, q, added)
	if n := countOf(t, ctx, q, s); n != 10+20 {
		t.Errorf("after the restore and the refill: %d, want 30 (the old history's memo would say 20)", n)
	}
	if rs := a.ReadStats(); rs.MemoStale != 1 {
		t.Errorf("%d memoized states found stale, want 1", rs.MemoStale)
	}
	if err := a.CheckSnapshots(context.Background(), s); err != nil {
		t.Error(err)
	}
}

// A rewound log invalidates a snapshot: a put of a fold begun before the
// restore that lands after it (here the old history's snapshot at 20,
// put again) is checked against the event the log holds at 20 now, which
// is another, so the read refolds and answers 30. With the checksum
// leaving the last event out (the mutant snapshotTrustsPosition) the
// snapshot is read, and the answer is the old history's 20.
func TestRewindInvalidatesSnapshots(t *testing.T) {
	a, added, q := items()
	ctx := grist.NoMemo(grist.SnapshotAudit(grist.SnapshotEvery(context.Background(), 5), 0))
	s, stale := rewound(t, ctx, a, q, added)
	if err := s.PutSnapshot(context.Background(), "t1", stale); err != nil { // the in-flight put
		t.Fatal(err)
	}
	if n := countOf(t, ctx, q, s); n != 10+20 {
		t.Errorf("after the restore, the refill and the late put: %d, want 30 (the stale snapshot would say 20)", n)
	}
	if rs := a.ReadStats(); rs.SnapshotMisses == 0 {
		t.Errorf("no snapshot lookup missed: the stale one was read")
	}
}

// After a put, the key's snapshots of other fingerprints more than ten
// times Snapshots' every behind it are pruned (a retired release's); one
// near the head is a live release's in a rollout, and kept.
func TestSnapshotsPruned(t *testing.T) {
	oldApp, added, oldQ := items()
	newApp, _, newQ := changedFold()
	ctx := grist.SnapshotAudit(grist.SnapshotEvery(context.Background(), 5), 0)
	s := newStoreNamed(t, "items")
	oldFP, newFP := oldApp.Catalog().ReadModels[0].Fingerprint, newApp.Catalog().ReadModels[0].Fingerprint
	has := func(fp string) bool {
		_, ok, _ := s.GetSnapshot(context.Background(), "t1", "counts", "item=x", fp)
		return ok
	}
	addCounted(t, s, added, "x", 20, 1)
	countOf(t, ctx, oldQ, s) // the old release's snapshot at 20
	addCounted(t, s, added, "x", 60, 1)
	countOf(t, ctx, newQ, s) // the new release's at 80: the old one is 60 behind, past 50
	if has(oldFP) || !has(newFP) {
		t.Errorf("after the new release's put at 80: old fingerprint's kept %v, new's %v; want the old pruned", has(oldFP), has(newFP))
	}
	if rs := newApp.ReadStats(); rs.SnapshotsPruned != 1 {
		t.Errorf("%d pruned, want 1", rs.SnapshotsPruned)
	}
	addCounted(t, s, added, "x", 5, 1)
	countOf(t, grist.NoMemo(ctx), oldQ, s) // an old node still running reads at 85
	if !has(oldFP) || !has(newFP) {
		t.Errorf("two releases live: old fingerprint's kept %v, new's %v; want both", has(oldFP), has(newFP))
	}
}

// failingPuts is a store whose snapshot puts fail (a read-only role).
type failingPuts struct{ *memstore.Store }

func (failingPuts) PutSnapshot(context.Context, string, store.Snapshot) error {
	return errors.New("read-only")
}

// A failed put is counted (Main's /metrics), and the read answers.
func TestSnapshotPutFailuresCounted(t *testing.T) {
	a, added, q := items()
	s := &failingPuts{newStoreNamed(t, "items")}
	addCounted(t, s, added, "x", 12, 1)
	if n := countOf(t, grist.NoMemo(grist.SnapshotEvery(context.Background(), 5)), q, s); n != 12 {
		t.Errorf("count %d, want 12", n)
	}
	if rs := a.ReadStats(); rs.SnapshotPutFailures != 1 || rs.Folded != 12 {
		t.Errorf("ReadStats %+v: want 1 put failure and 12 events folded", rs)
	}
}

// The App's options set its reads: Snapshots(0) turns snapshots and the
// memo off, every read folding the log.
func TestSnapshotsOption(t *testing.T) {
	a := grist.New("items", grist.Snapshots(0))
	added := grist.NewEvent[ItemAdded](a, "item-added")
	counts := grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Count }))
	q := grist.NewQuery(a, "count", func(r *grist.Reads, c ItemQuery) (int, error) { return counts.For(r, c.Item), nil })
	s := newCounting(t)
	addItems(t, s, added, "x", 1200)
	for range 2 {
		countOf(t, context.Background(), q, s)
	}
	if s.puts.Load() != 0 || s.read.Load() != 2400 {
		t.Errorf("Snapshots(0): %d puts, %d events read; want none and every event twice", s.puts.Load(), s.read.Load())
	}
}
