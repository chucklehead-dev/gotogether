package counters

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// readCounted is a store that counts the events its reads return.
type readCounted struct {
	*sqlstore.Store
	n atomic.Int64
}

func (c *readCounted) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	evs, p, err := c.Store.Read(ctx, t, q, at)
	c.n.Add(int64(len(evs)))
	return evs, p, err
}

// TestReadCost: on a SQLite file holding counter c's n increments (10^4
// under -short, 10^5 too in full mode), show-counter's read of c's total
// folds every increment without snapshots or the memo, only those since
// the last snapshot (at most Snapshots' every, 1000) on a node started
// afresh, and only its memoized state's last event on a node that read
// it before. The events read and the times are logged (docs/grist-guide.md
// cites them).
func TestReadCost(t *testing.T) {
	t.Parallel()
	sizes := []int{10_000}
	if !testing.Short() {
		sizes = append(sizes, 100_000)
	}
	for _, n := range sizes {
		path := filepath.Join(t.TempDir(), "counters.db")
		open := func() *readCounted {
			db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(path, 5*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			st, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{Name: App.Name})
			if err != nil {
				t.Fatal(err)
			}
			return &readCounted{Store: st}
		}
		ctx := context.Background()
		st := open()
		st.CreateTenant(ctx, "t1")
		if _, err := st.Append(ctx, "t1", store.Batch{Events: []grist.Event{Created.Of(CounterCreated{Counter: "c", Name: "c", Owner: "u"})}, Time: 1, Cause: "history"}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i += 500 {
			var batch []grist.Event
			for range min(500, n-i) {
				batch = append(batch, Incremented.Of(CounterIncremented{Counter: "c", By: 1}))
			}
			if _, err := st.Append(ctx, "t1", store.Batch{Events: batch, Time: 1, Cause: "history"}); err != nil {
				t.Fatal(err)
			}
		}
		// show reads through st (c, or c as a plain Store: no snapshots,
		// and no memo, which a store value has no identity for)
		show := func(c *readCounted, st grist.Store) (time.Duration, int64) {
			t.Helper()
			before := c.n.Load()
			start := time.Now() //qenv:ok the real tier's read, timed
			res, _, err := App.Query(ctx, st, grist.Envelope{ID: "q", Tenant: "t1", Caller: "probe"}, "show-counter", json.RawMessage(`{"counter":"c"}`))
			var v CounterView
			if err != nil || json.Unmarshal(res, &v) != nil || v.Total != n {
				t.Fatalf("show-counter: %s (%v), want a total of %d", res, err, n)
			}
			return time.Since(start), c.n.Load() - before //qenv:ok
		}
		c := open()
		off, offN := show(c, struct{ grist.Store }{c})
		c = open()
		first, _ := show(c, c) // folds the log, takes a snapshot, memoizes
		fresh := open()
		cold, coldN := show(fresh, fresh) // a node started afresh: the snapshot
		memo, memoN := show(c, c)         // the node that read it: the memo
		t.Logf("%d increments: %d events read in %v without snapshots or the memo; %d in %v on a fresh node (the snapshot); %d in %v from the memo (%v the read that took them)",
			n, offN, off.Round(time.Millisecond), coldN, cold.Round(time.Millisecond), memoN, memo.Round(time.Microsecond), first.Round(time.Millisecond))
		if offN < int64(n) || coldN > 2*grist.DefaultSnapshotEvery+2 || memoN > 3 {
			t.Errorf("%d increments: %d read without (want all), %d from the snapshot (want at most %d), %d from the memo (want c's last increment and its creation)",
				n, offN, coldN, 2*grist.DefaultSnapshotEvery+2, memoN)
		}
	}
}
