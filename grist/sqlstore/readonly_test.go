package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

func TestOpenReadOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "reader.db")
	writerDB, err := sql.Open("sqlite", sqlstore.SQLiteDSN(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer writerDB.Close()
	writer, err := sqlstore.Open(ctx, writerDB, sqlstore.SQLite, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(ctx, "t", store.Batch{Events: []store.Event{{Type: "unknown-to-app", Version: 1, Body: []byte(`{"n":1}`)}}}); err != nil {
		t.Fatal(err)
	}
	// A driver-level read-only connection also proves opening does not run
	// the writer's schema DDL, row insertion, or migration-lock UPDATE.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reader, err := sqlstore.OpenReadOnly(ctx, db, sqlstore.SQLite, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reader.ReadEvent(ctx, "t", 1)
	if err != nil || !ok || got.Type != "unknown-to-app" || got.ID == "" {
		t.Fatalf("read-only exact read: %+v found=%t error=%v", got, ok, err)
	}
	if _, head, err := reader.Read(ctx, "t", store.Selector{Types: []string{"unknown-to-app"}}, 0); err != nil || head != 1 {
		t.Fatalf("read-only read: head=%d error=%v", head, err)
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"CreateTenant", func() error { return reader.CreateTenant(ctx, "new") }},
		{"Append", func() error { _, err := reader.Append(ctx, "t", store.Batch{}); return err }},
		{"PutSnapshot", func() error { return reader.PutSnapshot(ctx, "t", store.Snapshot{}) }},
		{"PruneSnapshots", func() error { _, err := reader.PruneSnapshots(ctx, "t", "name", "key", "keep", 2); return err }},
		{"Prune", func() error { _, err := reader.Prune(ctx, sqlstore.Retention{}); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.run()
			if err == nil || store.Transient(err) || !strings.Contains(err.Error(), "opened read-only") {
				t.Fatalf("mutator error: %v; want local permanent read-only refusal", err)
			}
		})
	}
	if _, err := sqlstore.OpenReadOnly(ctx, db, sqlstore.SQLite, sqlstore.Options{PostgresNotify: true}); err == nil || !strings.Contains(err.Error(), "PostgresNotify") {
		t.Fatalf("read-only write notification option: %v", err)
	}
}

func TestOpenReadOnlyDoesNotInitialize(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := sqlstore.OpenReadOnly(t.Context(), db, sqlstore.SQLite, sqlstore.Options{}); err == nil || !strings.Contains(err.Error(), "existing current schema") {
		t.Fatalf("empty database reader: %v", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("reader initialized tables: count=%d error=%v", count, err)
	}
}

func TestOpenReadOnlyRequiresCurrentSchema(t *testing.T) {
	t.Parallel()
	for _, version := range []int{0, 1, sqlstore.SchemaVersion + 1} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			ctx := context.Background()
			if _, err := db.ExecContext(ctx, "CREATE TABLE grist_schema (id BIGINT PRIMARY KEY, version BIGINT)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO grist_schema (id,version) VALUES (1,?)", version); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlstore.OpenReadOnly(ctx, db, sqlstore.SQLite, sqlstore.Options{}); err == nil || store.Transient(err) || !strings.Contains(err.Error(), "never initializes or migrates") {
				t.Fatalf("schema %d reader: %v", version, err)
			}
			var unchanged int
			if err := db.QueryRowContext(ctx, "SELECT version FROM grist_schema WHERE id = 1").Scan(&unchanged); err != nil || unchanged != version {
				t.Fatalf("schema changed %d -> %d: %v", version, unchanged, err)
			}
		})
	}
}

// The same non-initializing opening path runs over qsql, whose transaction
// operations are scheduled and checked by the simulated database.
func TestOpenReadOnlyOnQSQL(t *testing.T) {
	t.Parallel()
	sim := qenv.New(qenv.NewSeeded(1))
	defer sim.Close()
	sim.Check = true
	db := qsql.Open(sim, qenv.NewStore(sim, "reader", qenv.Serializable))
	defer db.Close()
	var runErr error
	sim.Spawn("reader", func(ctx context.Context) {
		writer, err := sqlstore.Open(ctx, db, sqlstore.QSQL, sqlstore.Options{})
		if err != nil {
			runErr = err
			return
		}
		if err = writer.CreateTenant(ctx, "t"); err != nil {
			runErr = err
			return
		}
		if _, err = writer.Append(ctx, "t", store.Batch{Events: []store.Event{{Type: "raw", Version: 1, Body: []byte(`{}`)}}}); err != nil {
			runErr = err
			return
		}
		reader, err := sqlstore.OpenReadOnly(ctx, db, sqlstore.QSQL, sqlstore.Options{})
		if err != nil {
			runErr = err
			return
		}
		_, ok, err := reader.ReadEvent(ctx, "t", 1)
		if err != nil {
			runErr = err
			return
		}
		if !ok {
			runErr = errors.New("raw event not found")
			return
		}
		if err := reader.CreateTenant(ctx, "cannot-write"); err == nil {
			runErr = errors.New("reader created tenant")
		}
	})
	if err := sim.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
}

// A real PostgreSQL role with SELECT alone can open and inspect the store.
// Its independently refused UPDATE ensures the opener does not rely on the
// administrative credentials used to create this disposable fixture.
func TestOpenReadOnlyOnPostgres(t *testing.T) {
	dsn := postgresDSN(t)
	ctx := t.Context()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	id := qenv.NewID(ctx)
	schema, role := "reader_"+id, "reader_role_"+id
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "42501" {
			t.Skip("SELECT-only role control needs a PostgreSQL fixture allowed to create disposable roles")
		}
		t.Fatal(err)
	}
	defer func() {
		// Schema grants must disappear before the role can be removed.
		admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		admin.ExecContext(context.Background(), "DROP ROLE "+role)
	}()
	db, err := sql.Open("pgx", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer, err := sqlstore.Open(ctx, db, sqlstore.Postgres, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(ctx, "t", store.Batch{Events: []store.Event{{Type: "raw", Version: 1, Body: []byte(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "GRANT SELECT ON ALL TABLES IN SCHEMA "+schema+" TO "+role); err != nil {
		t.Fatal(err)
	}
	readerDB, err := sql.Open("pgx", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer readerDB.Close()
	readerDB.SetMaxOpenConns(1)
	if _, err := readerDB.ExecContext(ctx, "SET ROLE "+role); err != nil {
		t.Fatal(err)
	}
	reader, err := sqlstore.OpenReadOnly(ctx, readerDB, sqlstore.Postgres, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reader.ReadEvent(ctx, "t", 1); err != nil || !ok {
		t.Fatalf("SELECT-only read: found=%t error=%v", ok, err)
	}
	if _, err := readerDB.ExecContext(ctx, "UPDATE grist_schema SET version=version WHERE id=1"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("role has write access: %v", err)
	}
}
