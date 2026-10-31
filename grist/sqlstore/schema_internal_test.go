package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Open initializes fresh storage with immutable event identities and refuses
// old schemas rather than inventing identities for events that already ran.
func TestMigrations(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) *sql.DB {
		db, err := sql.Open("sqlite", SQLiteDSN(filepath.Join(t.TempDir(), "grist.db"), 5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	t.Run("fresh", func(t *testing.T) {
		s, err := Open(ctx, open(t), SQLite, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if v, err := s.Version(ctx); v != SchemaVersion || err != nil {
			t.Errorf("version %d (%v), want %d", v, err, SchemaVersion)
		}
	})
	for _, versioned := range []bool{false, true} {
		t.Run(fmt.Sprintf("old schema versioned=%v", versioned), func(t *testing.T) {
			db := open(t)
			for _, q := range []string{
				`CREATE TABLE grist_heads (tenant TEXT NOT NULL, head BIGINT NOT NULL, steps BIGINT NOT NULL, PRIMARY KEY (tenant))`,
				`CREATE TABLE grist_events (tenant TEXT NOT NULL, pos BIGINT NOT NULL, type TEXT NOT NULL, version BIGINT NOT NULL, at_ms BIGINT NOT NULL, cause TEXT NOT NULL, tags TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, pos))`,
				`INSERT INTO grist_heads (tenant, head, steps) VALUES ('t1', 1, 0)`,
				`INSERT INTO grist_events (tenant, pos, type, version, at_ms, cause, tags, body) VALUES ('t1', 1, 'added', 1, 0, 'one', '[]', '{}')`,
			} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if versioned {
				if _, err := db.Exec(schemaTable); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO grist_schema (id, version) VALUES (1, 1)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Open(ctx, db, SQLite, Options{}); !errors.Is(err, errOlderSchema) || !strings.Contains(err.Error(), "fresh schema") {
				t.Fatalf("old schema: %v, want explicit refusal and fresh-schema instruction", err)
			}
			var body string
			if err := db.QueryRow(`SELECT body FROM grist_events WHERE tenant = 't1' AND pos = 1`).Scan(&body); err != nil || body != "{}" {
				t.Fatalf("old event changed: body %q, err %v", body, err)
			}
			var columns int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('grist_events') WHERE name = 'event_id'`).Scan(&columns); err != nil || columns != 0 {
				t.Fatalf("old event received a backfilled identity: columns=%d, err=%v", columns, err)
			}
		})
	}
	t.Run("newer", func(t *testing.T) {
		db := open(t)
		if _, err := Open(ctx, db, SQLite, Options{}); err != nil {
			t.Fatal(err)
		}
		db.Exec(`UPDATE grist_schema SET version = 99`)
		_, err := Open(ctx, db, SQLite, Options{})
		if !errors.Is(err, errNewerSchema) || !strings.Contains(err.Error(), "version 99") {
			t.Errorf("a database at version 99: %v, want errNewerSchema naming it", err)
		}
	})
}

// Two nodes opening one database at once migrate it once: the schema
// row's lock (UPDATE ... RETURNING) makes the second wait for the first's
// migration and find nothing to do. With a migration that is not
// idempotent (a CREATE TABLE without IF NOT EXISTS, as an ALTER TABLE
// is not, added for the test), eight Opens interleaved statement by
// statement on qsql all succeed; without the lock (the version read with
// a plain SELECT) a second runs it after the first committed and fails,
// the table there (42P07, which no retry fixes).
func TestMigrationsLocked(t *testing.T) {
	once := migration{version: 3, why: "a test's table", stmts: []string{
		`CREATE TABLE grist_once (id BIGINT NOT NULL, PRIMARY KEY (id))`,
	}}
	run := func(seed uint64) (errs []error, version int64) {
		sim := qenv.New(qenv.NewSeeded(seed))
		defer sim.Close()
		db := qsql.Open(sim, qenv.NewStore(sim, "grist", qenv.ReadCommitted))
		for i := range 8 {
			sim.Spawn(fmt.Sprintf("node%d", i), func(ctx context.Context) {
				if err := migrateTo(ctx, db, append(migrations, once)); err != nil {
					errs = append(errs, err)
				}
			})
		}
		if err := sim.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		sim.Spawn("check", func(ctx context.Context) {
			db.QueryRowContext(ctx, `SELECT version FROM grist_schema WHERE id = $1`, 1).Scan(&version)
		})
		sim.Run(time.Minute)
		return errs, version
	}
	for seed := uint64(1); seed <= 20; seed++ {
		if errs, v := run(seed); len(errs) > 0 || v != 3 {
			t.Fatalf("seed %d: %v, version %d; want every Open to succeed at version 3", seed, errs, v)
		}
	}
}
