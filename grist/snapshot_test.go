package grist_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// counting is a memstore that counts the events its reads return, its
// snapshot gets and its puts.
type counting struct {
	*memstore.Store
	read, gets, puts atomic.Int64
}

func (c *counting) GetSnapshot(ctx context.Context, t, name, key, fp string) (store.Snapshot, bool, error) {
	c.gets.Add(1)
	return c.Store.GetSnapshot(ctx, t, name, key, fp)
}

func (c *counting) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	evs, p, err := c.Store.Read(ctx, t, q, at)
	c.read.Add(int64(len(evs)))
	return evs, p, err
}

func (c *counting) PutSnapshot(ctx context.Context, t string, s store.Snapshot) error {
	c.puts.Add(1)
	return c.Store.PutSnapshot(ctx, t, s)
}

func newCounting(t *testing.T) *counting {
	return &counting{Store: newStoreNamed(t, "items")}
}

// addItems appends n additions of item, each of count 1, a batch each.
func addItems(t *testing.T, s grist.Store, added *grist.EventType[ItemAdded], item string, n int) {
	t.Helper()
	for range n {
		if _, err := s.Append(context.Background(), "t1", storeBatch(added.Of(ItemAdded{Item: item, Count: 1}))); err != nil {
			t.Fatal(err)
		}
	}
}

// A read folds from the read model's snapshot, not from the start: the
// first read of 50 events folds them all and takes a snapshot (it folded
// SnapshotEvery's 10 or more), the next reads read the first 10 (which
// say the log is long enough to have one) and fold only what came after
// the snapshot, and every answer is the fold from the start's. With
// snapshots off every read folds the whole log.
func TestSnapshotsCutReads(t *testing.T) {
	_, added, q := items()
	s := newCounting(t)
	addItems(t, s, added, "x", 50)
	addItems(t, s, added, "y", 7) // another key's: never read for x
	ctx := grist.NoMemo(grist.SnapshotEvery(context.Background(), 10))
	ask := func(ctx context.Context, want int) int64 {
		t.Helper()
		before := s.read.Load()
		n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"})
		if err != nil || n != want {
			t.Fatalf("count of x: %d (%v), want %d", n, err, want)
		}
		return s.read.Load() - before
	}
	if n := ask(ctx, 50); n != 50 || s.puts.Load() != 1 {
		t.Fatalf("the first read folded %d events and took %d snapshots, want 50 and 1", n, s.puts.Load())
	}
	addItems(t, s, added, "x", 4)
	if n := ask(ctx, 54); n != 10+1+4 || s.puts.Load() != 1 {
		t.Errorf("a read after the snapshot read %d events (took %d snapshots), want the first 10, the snapshot's last event and the 4 after it (and no new one: fewer than 10)", n, s.puts.Load())
	}
	addItems(t, s, added, "x", 10)
	ask(ctx, 64) // 14 since the snapshot: a new one at the head
	if n := ask(ctx, 64); n != 10+1 || s.puts.Load() != 2 {
		t.Errorf("a read at the newest snapshot's head read %d events (%d snapshots taken), want the first 10 and the snapshot's last event (2)", n, s.puts.Load())
	}
	if n := ask(grist.SnapshotEvery(context.Background(), 0), 64); n != 64 {
		t.Errorf("with snapshots off a read folded %d events, want all 64", n)
	}
	// a key of fewer events than SnapshotEvery can have no snapshot: its
	// read is the one read it is with snapshots off, no snapshot looked up
	// (so a simulation's short logs keep their schedules)
	gets := s.gets.Load()
	if n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "y"}); err != nil || n != 7 || s.gets.Load() != gets {
		t.Errorf("count of y: %d (%v), %d snapshot lookups; want 7 and none", n, err, s.gets.Load()-gets)
	}
}

// A snapshot changes what a read costs, never what it conditions: a
// command deciding from one records the same read set and position as
// one folding from the start, so its append is conditioned on the read
// model's whole selector either way.
func TestSnapshotsKeepTheReadSet(t *testing.T) {
	a, added, _ := items()
	counts := grist.NewKeyedReadModel(a, "totals", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Count }))
	cmd := grist.NewCommand(a, "add-item", func(r *grist.Reads, c AddItem) ([]grist.Event, error) {
		if counts.For(r, c.Item) >= 100 {
			return nil, errTaken
		}
		return []grist.Event{added.Of(ItemAdded{Item: c.Item, Count: c.Count})}, nil
	}, grist.Refuses(errTaken))
	reads := func(every int) string {
		t.Helper()
		s := newCounting(t)
		addItems(t, s, added, "x", 30)
		ctx := grist.SnapshotEvery(context.Background(), every)
		for i, id := range []string{"c1", "c2"} { // the second decides from the first's snapshot
			if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: id, Tenant: "t1"}, AddItem{Item: "x", Count: i + 1}); err != nil {
				t.Fatal(err)
			}
		}
		sink := &qobs.MemorySink{}
		r, _ := grist.NewRelay(s)
		if err := r.Flush(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, st := range sink.Steps() {
			out = append(out, fmt.Sprint(st.Action, " ", st.Args["reads"], " ", st.Args["readPos"], " ", st.Outcome))
		}
		if every > 0 && s.puts.Load() == 0 {
			t.Fatal("no snapshot taken")
		}
		return strings.Join(out, "\n")
	}
	if with, without := reads(5), reads(0); with != without {
		t.Errorf("read sets with snapshots:\n%s\nwithout:\n%s\nwant the same", with, without)
	}
}

// A snapshot grist cannot trust is never read: one whose checksum fails
// (a torn write), one past the read's position (a log restored to an
// earlier point), one stored under another fingerprint. Each read folds
// from the start, and answers as a fold from the start does.
func TestSnapshotsUntrustedIgnored(t *testing.T) {
	a, added, q := items()
	fp := a.Catalog().ReadModels[0].Fingerprint
	ctx := grist.NoMemo(grist.SnapshotEvery(context.Background(), 2))
	for _, c := range []struct {
		name string
		snap store.Snapshot
		sum  bool // give it a sound checksum
	}{
		{"torn", store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: fp, Pos: 3, State: json.RawMessage(`10`)}, false},
		{"past the head", store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: fp, Pos: 99, State: json.RawMessage(`100`)}, true},
		{"undecodable", store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: fp, Pos: 3, State: json.RawMessage(`"ten"`)}, true},
	} {
		s := newCounting(t)
		addItems(t, s, added, "x", 5)
		if c.sum {
			c.snap.Sum = sum(s.Store, c.snap)
		} else {
			c.snap.Sum = sum(s.Store, store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: fp, Pos: 3, State: json.RawMessage(`100`)}) // the state it was written with, before the tear
		}
		s.Store.PutSnapshot(context.Background(), "t1", c.snap)
		if n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 5 {
			t.Errorf("%s snapshot: count %d (%v), want 5, the fold from the start", c.name, n, err)
		}
		if got, want := s.read.Load(), int64(2+3); c.snap.Pos <= 5 && got != want+3 || c.snap.Pos > 5 && got != want {
			t.Errorf("%s snapshot: %d events read, want the first 2, %sthe 3 after them", c.name, got, map[bool]string{true: "the 3 from the snapshot's position (its last event checked), ", false: ""}[c.snap.Pos <= 5])
		}
	}
	// the control: a sound snapshot at 3 is read, the 2 events after it folded onto it
	s := newCounting(t)
	addItems(t, s, added, "x", 5)
	snap := store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: fp, Pos: 3, State: json.RawMessage(`3`)}
	snap.Sum = sum(s.Store, snap)
	s.Store.PutSnapshot(context.Background(), "t1", snap)
	if n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 5 || s.read.Load() != 2+3 {
		t.Errorf("a sound snapshot: count %d (%v) from %d events read, want 5 from 5 (the first 2, the snapshot's last event, the 2 after it)", n, err, s.read.Load())
	}
}

// changedFold is items with its counts read model's fold changed but its
// name and version kept: a deploy that changed what the fold makes of an
// event without bumping the version.
func changedFold() (*grist.App, *grist.EventType[ItemAdded], *grist.QueryBlock[ItemQuery, int]) {
	a := grist.New("items")
	added := grist.NewEvent[ItemAdded](a, "item-added")
	counts := grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + 2*e.Count }))
	q := grist.NewQuery(a, "count", func(r *grist.Reads, c ItemQuery) (int, error) { return counts.For(r, c.Item), nil })
	return a, added, q
}

// A snapshot of a fold that changed is another fingerprint's: the probe
// (what the fold makes of generated events) differs, so the new fold
// never reads the old one's snapshot, and CheckSnapshots agrees with a
// fold from the start. The fingerprint changes with the version, the
// keys and an event's shape too.
func TestSnapshotOfAChangedFold(t *testing.T) {
	oldApp, added, oldQ := items()
	newApp, _, newQ := changedFold()
	s := newCounting(t)
	addItems(t, s, added, "x", 20)
	ctx := grist.SnapshotEvery(context.Background(), 5)
	if n, _, err := oldQ.Run(ctx, s, grist.Envelope{ID: "q1", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 20 || s.puts.Load() != 1 {
		t.Fatalf("the old fold: %d (%v), %d snapshots, want 20 and 1", n, err, s.puts.Load())
	}
	if o, n := oldApp.Catalog().ReadModels[0].Fingerprint, newApp.Catalog().ReadModels[0].Fingerprint; o == n {
		t.Fatalf("both folds' fingerprint is %s: want the changed fold's own", o)
	}
	if n, _, err := newQ.Run(ctx, s, grist.Envelope{ID: "q2", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 40 {
		t.Errorf("the changed fold: %d (%v), want 40, its own fold from the start", n, err)
	}
	if err := newApp.CheckSnapshots(context.Background(), s); err != nil {
		t.Error(err)
	}
	fps := map[string]string{}
	for name, a := range map[string]*grist.App{"items": oldApp, "changed": newApp, "v2": versioned(2), "keyless": keyless(), "unit": withUnit()} {
		fp := a.Catalog().ReadModels[0].Fingerprint
		if other, dup := fps[fp]; dup {
			t.Errorf("%s and %s share fingerprint %s", name, other, fp)
		}
		fps[fp] = name
	}
}

// versioned is items' read model at version v.
func versioned(v int) *grist.App {
	a := grist.New("items")
	added := grist.NewEvent[ItemAdded](a, "item-added")
	grist.NewKeyedReadModel(a, "counts", v, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Count }))
	return a
}

// keyless is items' read model, plain.
func keyless() *grist.App {
	a := grist.New("items")
	added := grist.NewEvent[ItemAdded](a, "item-added")
	grist.NewReadModel(a, "counts", 1, func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Count }))
	return a
}

// ItemAddedUnit is ItemAdded with another field: the same fold of
// another shape.
type ItemAddedUnit struct {
	Item  string `json:"item" grist:"tag"`
	Count int    `json:"count"`
	Note  string `json:"note"`
	Unit  string `json:"unit"`
	Lot   string `json:"lot"`
}

func withUnit() *grist.App {
	a := grist.New("items")
	added := grist.NewEvent[ItemAddedUnit](a, "item-added")
	grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAddedUnit, m grist.Meta) int { return s + e.Count }))
	return a
}

// CheckSnapshots finds a stored snapshot that disagrees with a fold from
// the start (here one written by hand, of a sound checksum and the read
// model's fingerprint: what a fold changed without a new fingerprint would
// leave), naming the read model, the tenant, the key and both states.
func TestCheckSnapshotsFindsAStaleOne(t *testing.T) {
	a, added, _ := items()
	s := newCounting(t)
	addItems(t, s, added, "x", 6)
	if err := a.CheckSnapshots(context.Background(), s); err != nil {
		t.Fatalf("a store with no snapshot: %v", err)
	}
	snap := store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: a.Catalog().ReadModels[0].Fingerprint, Pos: 4, State: json.RawMessage(`8`)}
	snap.Sum = sum(s.Store, snap)
	s.Store.PutSnapshot(context.Background(), "t1", snap)
	err := a.CheckSnapshots(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "read model counts, tenant t1, key item=x: its stored snapshot plus the events after it gives 10 at the head (6); a fold from the start gives 6") {
		t.Errorf("CheckSnapshots over a stale snapshot: %v, want it named", err)
	}
}

// Opaque's state has an unexported field, which JSON drops: a snapshot
// of it would lose the field, so it is never taken (the probe finds the
// state does not survive JSON), and CheckSnapshots says why.
type Opaque struct {
	Seen  int
	items []string
}

func TestUnsnapshottableNeverSnapshotted(t *testing.T) {
	a := grist.New("items")
	added := grist.NewEvent[ItemAdded](a, "item-added")
	rm := grist.NewReadModel(a, "opaque", 1, func() Opaque { return Opaque{} },
		grist.On(added, func(s Opaque, e ItemAdded, m grist.Meta) Opaque {
			return Opaque{Seen: s.Seen + 1, items: append(append([]string(nil), s.items...), e.Item)}
		}))
	q := grist.NewQuery(a, "opaque", func(r *grist.Reads, c ItemQuery) (int, error) { return len(rm.Get(r).items), nil })
	s := newCounting(t)
	addItems(t, s, added, "x", 30)
	if n, _, err := q.Run(grist.SnapshotEvery(context.Background(), 2), s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{}); err != nil || n != 30 || s.puts.Load() != 0 {
		t.Errorf("opaque: %d (%v), %d snapshots, want 30 and none", n, err, s.puts.Load())
	}
	if err := a.CheckSnapshots(context.Background(), s); err == nil || !strings.Contains(err.Error(), "does not survive JSON") {
		t.Errorf("CheckSnapshots: %v, want the unexported field's reason", err)
	}
}

// interleaved is a log of two items' additions, interleaved unevenly, and
// an event of no read model's between.
func interleaved(t *testing.T, s grist.Store, added *grist.EventType[ItemAdded]) {
	t.Helper()
	for i, item := range []string{"x", "y", "x", "x", "y", "x", "y", "y", "x"} {
		if _, err := s.Append(context.Background(), "t1", storeBatch(added.Of(ItemAdded{Item: item, Count: i + 1}))); err != nil {
			t.Fatal(err)
		}
		if i == 4 {
			s.Append(context.Background(), "t1", storeBatch(grist.Event{Type: "other", Version: 1, Body: json.RawMessage(`{}`)}))
		}
	}
}

// A snapshot taken at any position, plus the events after it, folds to
// what a fold from the start does: CheckSnapshots takes one at each
// event's position and the one before it, for every key of a keyed read
// model and for a plain one, and every one agrees.
func TestCheckSnapshotsAgree(t *testing.T) {
	a, added, _ := items()
	grist.NewReadModel(a, "all", 1, func() []string { return nil },
		grist.On(added, func(s []string, e ItemAdded, m grist.Meta) []string {
			return append(append([]string(nil), s...), e.Item)
		}))
	s := newCounting(t)
	interleaved(t, s, added)
	if err := a.CheckSnapshots(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.puts.Load() < 9 {
		t.Errorf("%d snapshots taken, want one at each position that matters", s.puts.Load())
	}
}

// buggyThenFixed is a log of 20 additions of x stored at version 1, a
// snapshot of it taken by an app whose upcaster forgot qty (every count
// 0), and the app with the upcaster fixed: what the fixed app answers.
func buggyThenFixed(t *testing.T) (buggy, fixed *grist.App, answer int) {
	forgets := grist.Upcast(1, func(v ItemAddedV1) ItemAddedV2 { return ItemAddedV2{Item: v.Item} })
	buggy, _, bq := items(forgets, grist.Upcast(2, v2ToV3))
	fixed, _, fq := items(grist.Upcast(1, v1ToV2), grist.Upcast(2, v2ToV3))
	s := newCounting(t)
	ctx := context.Background()
	for range 20 {
		s.Append(ctx, "t1", storeBatch(raw(1, ItemAddedV1{Item: "x", Qty: 1})))
	}
	every := grist.SnapshotEvery(ctx, 5)
	if n, _, err := bq.Run(every, s, grist.Envelope{ID: "q1", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 0 || s.puts.Load() != 1 {
		t.Fatalf("the buggy upcaster's count: %d (%v), %d snapshots; want 0 and 1", n, err, s.puts.Load())
	}
	n, _, err := fq.Run(every, s, grist.Envelope{ID: "q2", Tenant: "t1"}, ItemQuery{Item: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return buggy, fixed, n
}

// A fixed upcaster refolds: the fingerprint holds what each upcaster
// makes of generated values of its own version, so the snapshot folded
// through the buggy one is another fingerprint's, and the fixed app folds
// the old events through the fix.
func TestFixedUpcasterRefolds(t *testing.T) {
	buggy, fixed, n := buggyThenFixed(t)
	if b, f := buggy.Catalog().ReadModels[0].Fingerprint, fixed.Catalog().ReadModels[0].Fingerprint; b == f {
		t.Errorf("the buggy and the fixed upcaster's read model share fingerprint %s", b)
	}
	if n != 20 {
		t.Errorf("the fixed upcaster's count: %d, want 20 (the buggy one's snapshot never read)", n)
	}
}

// A deployed node audits a sample of its reads from snapshots
// (SnapshotAudit): the key refolded from the start and compared. A wrong
// snapshot (here one claiming 8 at position 4, where the fold is 4) is
// answered by the refold, replaced, and counted, for Main's /metrics;
// unaudited, the read answers the snapshot's sum. A right one is counted
// audited only.
func TestSnapshotAudit(t *testing.T) {
	a, added, q := items()
	s := newCounting(t)
	addItems(t, s, added, "x", 12)
	snap := store.Snapshot{Name: "counts", Key: "item=x", Fingerprint: a.Catalog().ReadModels[0].Fingerprint, Pos: 4, State: json.RawMessage(`8`)}
	snap.Sum = sum(s.Store, snap)
	s.Store.PutSnapshot(context.Background(), "t1", snap)
	ctx := grist.NoMemo(grist.SnapshotEvery(context.Background(), 10))
	count := func(ctx context.Context) int {
		t.Helper()
		n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(grist.SnapshotAudit(ctx, 0)); n != 8+8 {
		t.Fatalf("unaudited, the read from the wrong snapshot: %d, want its 8 plus the 8 after it (the test's premise)", n)
	}
	if n := count(grist.SnapshotAudit(ctx, 1)); n != 12 {
		t.Errorf("audited: %d, want the refold's 12", n)
	}
	if rs := a.ReadStats(); rs.Audits != 1 || rs.AuditMismatches != 1 {
		t.Errorf("ReadStats: %d audited, %d wrong; want 1 and 1", rs.Audits, rs.AuditMismatches)
	}
	// the snapshot replaced: the next read from it is right, unaudited
	if n := count(grist.SnapshotAudit(ctx, 0)); n != 12 {
		t.Errorf("after the audit, unaudited: %d, want 12 from the replaced snapshot", n)
	}
	count(grist.SnapshotAudit(ctx, 1))
	if rs := a.ReadStats(); rs.Audits != 2 || rs.AuditMismatches != 1 {
		t.Errorf("ReadStats: %d audited, %d wrong; want 2 and 1: the replaced snapshot is right", rs.Audits, rs.AuditMismatches)
	}
}

// sum is snap's checksum (grist.SnapshotSum) with its anchor: the identity
// of the item-added event st holds at snap.Pos in t1 ("" for none).
func sum(st grist.Store, snap store.Snapshot) string {
	evs, _, err := st.Read(context.Background(), "t1", grist.Selector{Types: []string{"item-added"}, After: snap.Pos - 1, Limit: 1}, 0)
	anchor := ""
	if err == nil && len(evs) > 0 && evs[0].Pos == snap.Pos {
		anchor = grist.EventID(evs[0])
	}
	return grist.SnapshotSum(snap, anchor)
}
