package grist_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type coldProcessorSink struct{ steps atomic.Int64 }

func (s *coldProcessorSink) Record(context.Context, *qtrace.Step) { s.steps.Add(1) }

type coldProcessorAdded struct{ By int }
type coldCheckpoint struct {
	owner string
	pos   store.Position
}

type coldProcessorStore struct {
	*sqlstore.Store
	rows              atomic.Int64
	end               store.Position
	committed         chan coldCheckpoint
	protectedSnapshot store.Position
	replacedSnapshot  atomic.Bool
}

// Check every persisted write, so a transient downgrade cannot be hidden by
// a later head query restoring the same final snapshot.
func (s *coldProcessorStore) PutSnapshot(ctx context.Context, tenant string, snap store.Snapshot) error {
	if snap.Pos < s.protectedSnapshot {
		s.replacedSnapshot.Store(true)
	}
	return s.Store.PutSnapshot(ctx, tenant, snap)
}

func (s *coldProcessorStore) Read(ctx context.Context, tenant string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	events, head, err := s.Store.Read(ctx, tenant, q, at)
	s.rows.Add(int64(len(events)))
	return events, head, err
}

// A final checkpoint notification follows its transaction's commit. Keep that
// poller parked until cancellation rather than racing later work with checks.
func (s *coldProcessorStore) Append(ctx context.Context, tenant string, batch store.Batch) (store.Position, error) {
	head, err := s.Store.Append(ctx, tenant, batch)
	if err == nil && batch.Checkpoint != nil && batch.Checkpoint.To == s.end {
		select {
		case s.committed <- coldCheckpoint{batch.Checkpoint.Processor, batch.Checkpoint.To}:
		case <-ctx.Done():
			return head, nil
		}
		<-ctx.Done()
	}
	return head, err
}

type coldProcessorHandler func(context.Context, *grist.Reads, *grist.ReadModel[int], *grist.QueryBlock[struct{}, int], string, grist.Meta) grist.Result

func coldProcessorApp(owners int, handler coldProcessorHandler) (*grist.App, *grist.EventType[coldProcessorAdded], *grist.QueryBlock[struct{}, int]) {
	a := grist.New("cold-processor", grist.AuditSnapshots(0))
	added := grist.NewEvent[coldProcessorAdded](a, "added")
	count := grist.NewReadModel(a, "count", 1, func() int { return 0 }, grist.On(added, func(n int, e coldProcessorAdded, _ grist.Meta) int { return n + e.By }))
	query := grist.NewQuery(a, "count-query", func(r *grist.Reads, _ struct{}) (int, error) { return count.Get(r), nil })
	if handler != nil {
		for owner := range owners {
			name := fmt.Sprintf("lagging-%d", owner)
			grist.NewProcessor(a, name, added, func(ctx context.Context, r *grist.Reads, _ coldProcessorAdded, m grist.Meta) grist.Result {
				return handler(ctx, r, count, query, name, m)
			})
		}
	}
	return a, added, query
}

func coldSQLite(tb testing.TB, path string) (*sql.DB, *sqlstore.Store) {
	tb.Helper()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(path, 5*time.Second))
	if err != nil {
		tb.Fatal(err)
	}
	st, err := sqlstore.Open(tb.Context(), db, sqlstore.SQLite, sqlstore.Options{Name: "cold-processor"})
	if err != nil {
		db.Close()
		tb.Fatal(err)
	}
	return db, st
}

// Build the eligible snapshot by actually reading the prefix before appending
// the tail. The treatment replaces it with a genuine HEAD snapshot; the
// control leaves it in place. No checksums or retention semantics are invented.
func coldProcessorFixture(tb testing.TB, n, owners int, eligible bool) ([]byte, store.Snapshot) {
	tb.Helper()
	ctx := qobs.WithRecorder(tb.Context(), qobs.NewRecorder(&coldProcessorSink{}))
	path := filepath.Join(tb.TempDir(), "fixture.db")
	db, st := coldSQLite(tb, path)
	defer db.Close()
	a, added, query := coldProcessorApp(owners, nil)
	if err := st.CreateTenant(ctx, "t"); err != nil {
		tb.Fatal(err)
	}
	first := n * 9 / 10
	appendTo := func(from, to int) {
		for from < to {
			end := min(from+1000, to)
			events := make([]store.Event, end-from)
			for i := range events {
				events[i] = added.Of(coldProcessorAdded{1})
			}
			if _, err := st.Append(ctx, "t", store.Batch{Events: events}); err != nil {
				tb.Fatal(err)
			}
			from = end
		}
	}
	ask := func(want int) {
		got, _, err := query.Run(grist.SnapshotEvery(ctx, 1), st, grist.Envelope{ID: "fixture", Tenant: "t"}, struct{}{})
		if err != nil || got != want {
			tb.Fatalf("fixture count %d (%v), want %d", got, err, want)
		}
	}
	appendTo(0, first-1)
	ask(first - 1)
	appendTo(first-1, n)
	if !eligible {
		ask(n)
	}
	for owner := range owners {
		if _, err := st.Append(ctx, "t", store.Batch{Checkpoint: &store.CheckpointMove{Processor: fmt.Sprintf("lagging-%d", owner), To: store.Position(first - 1)}}); err != nil {
			tb.Fatal(err)
		}
	}
	snap, ok, err := st.GetSnapshot(ctx, "t", "count", "", a.Catalog().ReadModels[0].Fingerprint)
	wantSnapshot := store.Position(n)
	if eligible {
		wantSnapshot = store.Position(first - 1)
	}
	if err != nil || !ok || snap.Pos != wantSnapshot {
		tb.Fatalf("fixture snapshot: %+v, found %v, error %v; want position %d", snap, ok, err, wantSnapshot)
	}
	if err := db.Close(); err != nil {
		tb.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return data, snap
}

type coldProcessorMeasure struct {
	first, warm, total                 time.Duration
	firstFolds, warmFolds, folds, rows int64
	firstGet, firstRows                int64
}

// Each sample has a fresh App memo and reopened SQLite connection. It shares
// its process and likely-warm OS page cache with setup and previous samples.
// Copying, opening, validation and closing are outside the timing callbacks.
// Cleanup cancels work and closes the database (waiting for active SQL), but
// App.Start exposes no join: return does not certify that every canceled
// background goroutine has exited. Every final processor is parked at its
// committed checkpoint and cannot start further callback work.
func coldProcessorSample(tb testing.TB, data []byte, snapshot store.Snapshot, n, owners int, start, stop func()) coldProcessorMeasure {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "sample.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		tb.Fatal(err)
	}
	db, base := coldSQLite(tb, path)
	sink := &coldProcessorSink{}
	ctx, cancel := context.WithCancel(qobs.WithRecorder(tb.Context(), qobs.NewRecorder(sink)))
	defer func() { cancel(); db.Close() }()
	first := store.Position(n * 9 / 10)
	st := &coldProcessorStore{Store: base, end: store.Position(n), protectedSnapshot: snapshot.Pos, committed: make(chan coldCheckpoint, owners)}
	firstRead, nextRead, warmRead := make(chan string, owners), make(chan string, owners), make(chan string, owners)
	releaseReads, releaseNext, releaseWarm := make(chan struct{}), make(chan struct{}), make(chan struct{})
	problems := make(chan error, 1)
	report := func(err error) {
		select {
		case problems <- err:
		default:
		}
	}
	var firstGet atomic.Int64
	var headMu sync.Mutex // alternate shared head queries, avoiding duplicate cold head work
	a, _, _ := coldProcessorApp(owners, func(ctx context.Context, r *grist.Reads, count *grist.ReadModel[int], query *grist.QueryBlock[struct{}, int], owner string, m grist.Meta) grist.Result {
		if m.Pos == first+1 {
			select {
			case nextRead <- owner:
			case <-ctx.Done():
				return grist.Skip()
			}
			select {
			case <-releaseNext:
			case <-ctx.Done():
				return grist.Skip()
			}
		}
		var readStart time.Time
		if m.Pos == first {
			readStart = time.Now()
		}
		gotCount := count.Get(r)
		if m.Pos == first {
			firstGet.Add(time.Since(readStart).Nanoseconds())
		}
		if got := gotCount; got != int(m.Pos) {
			report(fmt.Errorf("%s count at %d: %d", owner, m.Pos, got))
		}
		if m.Pos == first {
			select {
			case firstRead <- owner:
			case <-ctx.Done():
				return grist.Skip()
			}
			select {
			case <-releaseReads:
			case <-ctx.Done():
				return grist.Skip()
			}
		}
		headMu.Lock()
		got, _, err := query.Run(ctx, st, grist.Envelope{ID: fmt.Sprintf("head-%s-%d", owner, m.Pos), Tenant: "t"}, struct{}{})
		headMu.Unlock()
		if err != nil || got != n {
			report(fmt.Errorf("%s head: %d (%v), want %d", owner, got, err, n))
		}
		if m.Pos == first+100 {
			select {
			case warmRead <- owner:
			case <-ctx.Done():
				return grist.Skip()
			}
			select {
			case <-releaseWarm:
			case <-ctx.Done():
				return grist.Skip()
			}
		}
		return grist.Emit()
	})
	waitReads := func(reads <-chan string) {
		for range owners {
			select {
			case <-reads:
			case err := <-problems:
				tb.Fatal(err)
			case <-ctx.Done():
				tb.Fatal(ctx.Err())
			}
		}
	}
	commits := func(pos store.Position) {
		seen := map[string]bool{}
		for range owners {
			select {
			case c := <-st.committed:
				if c.pos != pos || seen[c.owner] {
					tb.Fatalf("checkpoint %+v, want unique owner at %d", c, pos)
				}
				seen[c.owner] = true
			case err := <-problems:
				tb.Fatal(err)
			case <-ctx.Done():
				tb.Fatal(ctx.Err())
			}
		}
	}
	start()
	begin := time.Now()
	if err := a.Start(ctx, st); err != nil {
		tb.Fatal(err)
	}
	waitReads(firstRead)
	firstDone := time.Now()
	firstFolds := a.ReadStats().Folded
	firstRows := st.rows.Load()
	close(releaseReads)
	waitReads(nextRead)
	beforeWarm := a.ReadStats().Folded
	warmBegin := time.Now()
	close(releaseNext)
	waitReads(warmRead)
	warmDone := time.Now()
	warmFolds := a.ReadStats().Folded - beforeWarm
	close(releaseWarm)
	commits(store.Position(n))
	end := time.Now()
	stop()
	cancel()
	select {
	case err := <-problems:
		tb.Fatal(err)
	default:
	}
	if err := db.Close(); err != nil {
		tb.Fatal(err)
	}
	verifyDB, verifyStore := coldSQLite(tb, path)
	defer verifyDB.Close()
	for owner := range owners {
		got, err := verifyStore.Checkpoint(tb.Context(), "t", fmt.Sprintf("lagging-%d", owner))
		if err != nil || got != store.Position(n) {
			tb.Fatalf("durable checkpoint %d (%v), want %d", got, err, n)
		}
	}
	eligible := snapshot.Pos < first
	wantSnapshot := store.Position(n)
	// A control with less than the normal 1000-event cadence need not replace
	// its eligible prefix snapshot; the ordinary head memo still warms once.
	if eligible && store.Position(n)-snapshot.Pos < grist.DefaultSnapshotEvery {
		wantSnapshot = snapshot.Pos
	}
	if st.replacedSnapshot.Load() {
		tb.Fatal("historical read attempted to replace a newer persisted snapshot")
	}
	got, ok, err := verifyStore.GetSnapshot(tb.Context(), "t", "count", "", a.Catalog().ReadModels[0].Fingerprint)
	if err != nil || !ok || got.Pos != wantSnapshot {
		tb.Fatalf("final snapshot: %+v %v %v, want position %d", got, ok, err, wantSnapshot)
	}
	if !eligible && (got.Sum != snapshot.Sum || !bytes.Equal(got.State, snapshot.State)) {
		tb.Fatal("historical reads replaced persisted HEAD snapshot")
	}
	wantFirst, wantTotal := int64(first)*int64(owners), int64(n)*int64(owners)
	if eligible {
		wantFirst = int64(owners)
		wantTotal = int64(n-int(snapshot.Pos)) * int64(owners+1)
	}
	if sink.steps.Load() == 0 {
		tb.Fatal("owned recorder captured no native recovery steps")
	}
	stats := a.ReadStats()
	if firstFolds != wantFirst || warmFolds != int64(100*owners) || stats.Folded != wantTotal {
		tb.Fatalf("folds first/warm/total %d/%d/%d, want %d/%d/%d", firstFolds, warmFolds, stats.Folded, wantFirst, 100*owners, wantTotal)
	}
	return coldProcessorMeasure{first: firstDone.Sub(begin), warm: warmDone.Sub(warmBegin), total: end.Sub(begin), firstFolds: firstFolds, warmFolds: warmFolds, folds: stats.Folded, rows: st.rows.Load(), firstGet: firstGet.Load(), firstRows: firstRows}
}

// BenchmarkColdProcessorRecovery measures actual named processors on SQLite.
// ns/op covers recovery through HEAD including checkpoint commits. first-ready-ns/op
// ends at all owners' first pinned reads, before head queries; first-get-ns/owner
// isolates Get elapsed time from startup/relay work. next100-ns/op
// covers the next 100 trigger callbacks per owner with interleaved head queries and
// ordinary processor batching. Their final checkpoint may commit afterward;
// full recovery ends only when every final checkpoint is durable.
// Fixture construction, copying, opening and checks are not timed. The process
// and OS page cache are not cold; the App memo and connection are fresh.
func BenchmarkColdProcessorRecovery(b *testing.B) {
	for _, n := range []int{10_000, 100_000} {
		for _, owners := range []int{1, 4} {
			for _, eligible := range []bool{false, true} {
				mode := "head-snapshot"
				if eligible {
					mode = "eligible-prefix"
				}
				b.Run(fmt.Sprintf("events=%d/owners=%d/%s", n, owners, mode), func(b *testing.B) {
					data, snapshot := coldProcessorFixture(b, n, owners, eligible)
					var sum coldProcessorMeasure
					b.ReportAllocs()
					for b.Loop() {
						b.StopTimer()
						m := coldProcessorSample(b, data, snapshot, n, owners, b.StartTimer, b.StopTimer)
						sum.first += m.first
						sum.warm += m.warm
						sum.total += m.total
						sum.firstFolds += m.firstFolds
						sum.warmFolds += m.warmFolds
						sum.folds += m.folds
						sum.rows += m.rows
						sum.firstGet += m.firstGet
						sum.firstRows += m.firstRows
						b.StartTimer() // B.Loop requires a running timer at its next call.
					}
					denom := float64(b.N)
					b.ReportMetric(float64(sum.total.Nanoseconds())/denom, "recovery-ns/op")
					b.ReportMetric(float64(sum.first.Nanoseconds())/denom, "first-ready-ns/op")
					b.ReportMetric(float64(sum.firstGet)/denom/float64(owners), "first-get-ns/owner")
					b.ReportMetric(float64(sum.firstRows)/denom, "first-store-rows/op")
					b.ReportMetric(float64(sum.warm.Nanoseconds())/denom, "next100-ns/op")
					b.ReportMetric(float64(sum.firstFolds)/denom, "first-folds/op")
					b.ReportMetric(float64(sum.warmFolds)/denom, "next100-folds/op")
					b.ReportMetric(float64(sum.folds)/denom, "folds/op")
					b.ReportMetric(float64(sum.rows)/denom, "store-rows/op")
				})
			}
		}
	}
}

// Keep the exact benchmark harness executable in ordinary CI at a small size.
func TestColdProcessorRecovery(t *testing.T) {
	for _, owners := range []int{1, 4} {
		for _, eligible := range []bool{false, true} {
			t.Run(fmt.Sprintf("owners=%d/eligible=%t", owners, eligible), func(t *testing.T) {
				data, snapshot := coldProcessorFixture(t, 2000, owners, eligible)
				coldProcessorSample(t, data, snapshot, 2000, owners, func() {}, func() {})
			})
		}
	}
}
