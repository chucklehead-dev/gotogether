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

// Open migrates a database to SchemaVersion: a fresh one, and one slice
// 2's boots made (its tables, no grist_schema), whose rows stay; one a
// newer grist migrated is refused, naming the fix.
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
	t.Run("slice 2's tables", func(t *testing.T) {
		db := open(t)
		for _, q := range schema {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO grist_heads (tenant, head, steps) VALUES ('t1', 0, 0)`); err != nil {
			t.Fatal(err)
		}
		s, err := Open(ctx, db, SQLite, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if ts, _ := s.Tenants(ctx); len(ts) != 1 {
			t.Errorf("tenants after the migration: %v, want t1 kept", ts)
		}
		if v, _ := s.Version(ctx); v != SchemaVersion {
			t.Errorf("version %d, want %d", v, SchemaVersion)
		}
	})
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
	once := migration{version: 2, why: "a test's table", stmts: []string{
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
		if errs, v := run(seed); len(errs) > 0 || v != 2 {
			t.Fatalf("seed %d: %v, version %d; want every Open to succeed at version 2", seed, errs, v)
		}
	}
}
