package sqlstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Identity is stored data, not a position/clock/payload hash or a reader's
// memory. Reopening and pruning preserve it; filling a restored position with
// byte-identical work creates another identity.
func TestSQLiteEventIdentitySurvivesReopenPruneAndRestore(t *testing.T) {
	ctx := context.Background()
	dsn := sqlstore.SQLiteDSN(filepath.Join(t.TempDir(), "identity.db"), time.Second)
	var db *sql.DB
	var s *sqlstore.Store
	open := func() {
		t.Helper()
		var err error
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		s, err = sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{})
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	open()
	defer func() { db.Close() }()
	if err := s.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(marker string) {
		t.Helper()
		_, err := s.Append(ctx, "t", store.Batch{
			Events: []store.Event{{Type: "added", Version: 1, Body: []byte(`{"value":1}`)}},
			Cause:  "same-cause", Time: 1, Marker: &store.Marker{ID: marker}, Step: []byte(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read := func() []store.Stored {
		t.Helper()
		evs, _, err := s.Read(ctx, "t", store.Selector{Types: []string{"added"}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return evs
	}
	appendEvent("kept")
	appendEvent("lost")
	before := read()
	canonical := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if len(before) != 2 || !canonical.MatchString(before[0].ID) || !canonical.MatchString(before[1].ID) || before[0].ID == before[1].ID {
		t.Fatalf("missing/distinct event identities: %+v", before)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	if got := read(); len(got) != 2 || got[0].ID != before[0].ID || got[1].ID != before[1].ID {
		t.Fatalf("reopen changed identities: %+v", got)
	}
	pruned, err := s.Prune(ctx, sqlstore.Retention{StepsBefore: time.Now().Add(time.Hour), MarkersBefore: time.UnixMilli(2)})
	if err != nil || pruned.Steps != 1 || pruned.Markers != 2 {
		t.Fatalf("prune: %+v, %v", pruned, err)
	}
	if got := read(); len(got) != 2 || got[0].ID != before[0].ID || got[1].ID != before[1].ID {
		t.Fatalf("pruning changed identities: %+v", got)
	}
	// A restore loses the tail. Both timestamp and cause are deliberately
	// reused: neither is the identity of the replacement event.
	for _, q := range []string{
		`DELETE FROM grist_events WHERE tenant = 't' AND pos > 1`,
		`DELETE FROM grist_index WHERE tenant = 't' AND pos > 1`,
		`DELETE FROM grist_steps WHERE tenant = 't'`,
		`UPDATE grist_heads SET head = 1, steps = 0 WHERE tenant = 't'`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("lost")
	after := read()
	if len(after) != 2 || after[0].ID != before[0].ID || after[1].Pos != before[1].Pos || !canonical.MatchString(after[1].ID) || after[1].ID == before[1].ID || after[1].ID == after[0].ID {
		t.Fatalf("replacement reused identity or changed retained prefix: before=%+v after=%+v", before, after)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	if got := read(); len(got) != 2 || got[1].ID != after[1].ID {
		t.Fatalf("replacement identity was not durable: %+v", got)
	}
}
