package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// TestUpcasts: book-added's upcaster from version 1 is total, pure, keeps
// the book's tag and carries every field of version 1 into version 2
// (gristtest.Upcasts; the dropsCopies mutant's contrast).
func TestUpcasts(t *testing.T) {
	gristtest.Upcasts(t, App)
}

// oldLog is the real tier on library version 1's log
// (testdata/logs/v1.jsonl, recorded by TestRecordLog before book-added's
// version 2: its additions stored with copies, its borrows never
// receipted), served by today's app: the node relays the old steps (each
// addition re-rendered as version 2), mails the old borrows' receipts,
// and today's clients catalogue, borrow and return on it.
func oldLog(t *testing.T) *gristtest.Tier {
	log := gristtest.ReadLog(t, filepath.Join("testdata", "logs", "v1.jsonl"))
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{Log: &log, Local: map[*grist.Dependency]grist.Handlers{Mailer: nil}})
	tier.Start("node")
	ctx := qobs.Thread(tier.Clients())
	for i, c := range []struct {
		name string
		args any
	}{
		{"add-book", AddBook{Book: "w", Copies: 1}}, // version 2's first
		{"borrow", Borrow{Member: "b", Book: "y"}},  // y's one copy (version 1's) is a's: unavailable
		{"return-book", ReturnBook{Member: "a", Book: "y"}},
		{"borrow", Borrow{Member: "b", Book: "y"}}, // now free
		{"borrow", Borrow{Member: "a", Book: "x"}}, // x: two copies at version 1, one more, b holds one
		{"borrow", Borrow{Member: "a", Book: "w"}}, // the version 2 addition's copy
		{"borrow", Borrow{Member: "a", Book: "x"}}, // a third of x's copies
		{"borrow", Borrow{Member: "b", Book: "x"}}, // none left: unavailable
		{"member-loans", MemberLoans{Member: "a"}},
		{"join", Join{Member: "c"}},
	} {
		tier.Do(ctx, grist.Envelope{ID: fmt.Sprintf("new-%d", i+1), Tenant: "t1", Caller: "librarian"}, c.name, c.args)
	}
	tier.Settle(func(ctx context.Context) {
		for i, m := range []string{"a", "b", "c"} {
			tier.Do(ctx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: "t1", Caller: "probe"}, "member-loans", MemberLoans{Member: m})
		}
	})
	return tier
}

// TestOldLogConforms: version 1's log and today's steps after it conform
// to today's model, which knows book-added's version 2 only (count and
// shelf): every old addition is judged as its upcast, every old borrow
// is receipted after the settle, every client's answer has its store
// side; then every read model's snapshots of the log, taken at each of
// its positions, agree with a fold from its start (old events upcast).
func TestOldLogConforms(t *testing.T) {
	qtest.RequireQuint(t)
	tier := oldLog(t)
	tier.Check(commits, calls)
	gristtest.Snapshots(t, App, tier.Store)
}

// TestSnapshots: through node crashes, commit faults, a lossy API and
// mailer, and a snapshot every two events with a third of the puts torn
// or lost, every run conforms and settles: a decision made from a
// snapshot is the model's, and so is one whose snapshot was torn (its
// checksum fails: a fold from the start).
func TestSnapshots(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := contended
	cfg.SnapshotEvery = 2
	system(commits, cfg).WithRates(faulty.Set("store.library.snapshot", 0.3)).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.library.snapshot").Faulted())
}

// counted is a store that counts the events its reads return.
type counted struct {
	*sqlstore.Store
	n atomic.Int64
}

func (c *counted) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	evs, p, err := c.Store.Read(ctx, t, q, at)
	c.n.Add(int64(len(evs)))
	return evs, p, err
}

// TestSnapshotsMakeStartFast: on a SQLite file holding a long history of
// member a's loans (book x borrowed and returned, n events, a copy of x
// added at version 1 every hundred), a node started afresh (the store
// opened again) answers member-loans folding every one of a's events
// without snapshots, and only those since the last snapshot with them:
// at most the cadence's (and the snapshot's last event), whatever n. The times are logged for
// docs/grist.md (10^4 under -short, 10^5 too in full mode).
func TestSnapshotsMakeStartFast(t *testing.T) {
	sizes := []int{10_000}
	if !testing.Short() {
		sizes = append(sizes, 100_000)
	}
	for _, n := range sizes {
		path := filepath.Join(t.TempDir(), "library.db")
		open := func() *counted {
			db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(path, 5*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			st, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{Name: App.Name})
			if err != nil {
				t.Fatal(err)
			}
			return &counted{Store: st}
		}
		ctx := context.Background()
		st := open()
		st.CreateTenant(ctx, "t1")
		// the history, appended in batches of 100 of a's events (no
		// decision: the log is the point), each with an addition of x
		// stored at version 1
		v1, _ := json.Marshal(BookAddedV1{Book: "x", Copies: 1})
		for i := 0; i < n; i += 100 {
			batch := []grist.Event{{Type: "book-added", Version: 1, Tags: []grist.TagPair{{Key: "book", Value: "x"}}, Body: v1}}
			for j := i; j < min(i+100, n); j++ {
				if j%2 == 0 {
					batch = append(batch, Borrowed.Of(BookBorrowed{Member: "a", Book: "x"}))
				} else {
					batch = append(batch, Returned.Of(BookReturned{Member: "a", Book: "x"}))
				}
			}
			if _, err := st.Append(ctx, "t1", store.Batch{Events: batch, Time: 1, Cause: "history"}); err != nil {
				t.Fatal(err)
			}
		}
		// ask reads through st (c, or c hidden as a plain Store: no
		// snapshots, and no memo, which a store value has no identity for)
		ask := func(c *counted, st grist.Store, ctx context.Context) (time.Duration, int64) {
			t.Helper()
			before := c.n.Load()
			start := time.Now() //qenv:ok the real tier's start, timed
			res, _, err := App.Query(ctx, st, grist.Envelope{ID: "q", Tenant: "t1", Caller: "probe"}, "member-loans", json.RawMessage(`{"member":"a"}`))
			var loans struct{ Count int }
			if err != nil || json.Unmarshal(res, &loans) != nil || loans.Count != 0 {
				t.Fatalf("member-loans: %s (%v), want no loans", res, err)
			}
			return time.Since(start), c.n.Load() - before //qenv:ok
		}
		c := open()
		cold, coldN := ask(c, struct{ grist.Store }{c}, ctx)
		c = open()
		first, _ := ask(c, c, ctx) // folds the log and takes a snapshot
		c = open()
		warm, warmN := ask(c, c, ctx)
		t.Logf("%d events: a fresh node's first read folds %d events in %v without snapshots; %d in %v from the snapshot (%v the read that took it)",
			n, coldN, cold.Round(time.Millisecond), warmN, warm.Round(time.Microsecond), first.Round(time.Millisecond))
		if coldN < int64(n) || warmN > grist.DefaultSnapshotEvery+1 {
			t.Errorf("%d events: %d folded without snapshots (want all of a's), %d with (want at most %d: the first %d and the snapshot's last event)",
				n, coldN, warmN, grist.DefaultSnapshotEvery+1, grist.DefaultSnapshotEvery)
		}
	}
}
