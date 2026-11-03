package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A PostgreSQL role's connection quota includes idle physical sessions. It is
// deliberately tested against PostgreSQL, not inferred from Store.MaxTransactions.
// The configured fixture needs permission to create a disposable login role and
// schema and inspect another role's session state (superuser or suitable
// pg_read_all_stats access), as CI's PostgreSQL administrator does.
func TestPostgresRoleConnectionCapacity(t *testing.T) {
	ctx := t.Context()
	adminConfig, err := pgx.ParseConfig(postgresDSN(t))
	if err != nil {
		// Parser errors can quote the original DSN, including credentials.
		t.Fatal("GRIST_POSTGRES must be a valid PostgreSQL DSN accepted by pgx")
	}
	admin := stdlib.OpenDB(*adminConfig)
	t.Cleanup(func() { _ = admin.Close() })
	id := qenv.NewID(ctx)
	role, schema := "capacity_role_"+id, "capacity_"+id
	roleSQL := pgx.Identifier{role}.Sanitize()
	schemaSQL := pgx.Identifier{schema}.Sanitize()
	// NewID is hexadecimal, so this generated password contains no SQL quoting.
	password := qenv.NewID(ctx)
	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+roleSQL+" LOGIN NOSUPERUSER CONNECTION LIMIT 1 PASSWORD '"+password+"'"); err != nil {
		t.Fatalf("create disposable limited login role (GRIST_POSTGRES must permit role creation): %v", err)
	}
	var writerDB, holderDB *sql.DB
	t.Cleanup(func() {
		if holderDB != nil {
			_ = holderDB.Close()
		}
		if writerDB != nil {
			_ = writerDB.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := waitPostgresRoleCapacity(cleanup, admin, role, 0); err != nil {
			t.Error("wait for capacity fixture connections to close:", err)
		}
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA IF EXISTS "+schemaSQL+" CASCADE"); err != nil {
			t.Error("drop capacity fixture schema:", err)
		}
		if _, err := admin.ExecContext(cleanup, "DROP ROLE "+roleSQL); err != nil {
			t.Error("drop capacity fixture role:", err)
		}
	})
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schemaSQL+" AUTHORIZATION "+roleSQL); err != nil {
		t.Fatal("create capacity fixture schema:", err)
	}
	roleConfig := adminConfig.Copy()
	roleConfig.User, roleConfig.Password = role, password
	roleConfig.RuntimeParams["search_path"] = schema
	// Authenticate as the role: SET ROLE on an administrator session would not
	// exercise the login-time connection quota.
	writerDB = stdlib.OpenDB(*roleConfig)
	writerDB.SetMaxOpenConns(1)
	writerDB.SetMaxIdleConns(1)
	writer, err := sqlstore.Open(ctx, writerDB, sqlstore.Postgres, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	// Keep one session through initialization, then close it once. Repeated
	// zero-idle setup dials could race PostgreSQL's asynchronous disconnects.
	writerDB.SetMaxIdleConns(0)
	if stats := writerDB.Stats(); stats.OpenConnections != 0 {
		t.Fatalf("writer retained a physical connection before saturation: %+v", stats)
	}
	// Closing the zero-idle writer's last connection is asynchronous at the
	// server. Wait for that physical session to disappear before opening another.
	if err := waitPostgresRoleCapacity(ctx, admin, role, 0); err != nil {
		t.Fatal(err)
	}
	before := postgresCapacityRows(t, admin, schema)
	if before != (postgresCapacitySnapshot{}) {
		t.Fatalf("new tenant is not empty: %+v", before)
	}

	holderDB = stdlib.OpenDB(*roleConfig)
	holderDB.SetMaxOpenConns(1)
	holderDB.SetMaxIdleConns(1)
	var pid int
	var sessionUser string
	if err := holderDB.QueryRowContext(ctx, "SELECT pg_backend_pid(), session_user").Scan(&pid, &sessionUser); err != nil {
		t.Fatal("open the quota holder:", err)
	}
	if sessionUser != role {
		t.Fatalf("holder authenticated as %q, need the restricted role", sessionUser)
	}
	if stats := holderDB.Stats(); stats.OpenConnections != 1 || stats.Idle != 1 || stats.InUse != 0 {
		t.Fatalf("holder is not an idle physical pooled session: %+v", stats)
	}
	waitPostgresCapacityIdle(t, admin, pid)
	batch := store.Batch{
		Events: []store.Event{{Type: "capacity", Version: 1, Body: []byte(`{"value":1}`)}},
		Marker: &store.Marker{ID: "capacity-attempt"},
		Step:   []byte(`{"operation":"capacity"}`),
	}
	position, err := writer.Append(ctx, "t", batch)
	var native *pgconn.PgError
	var known *store.NotCommittedError
	if position != 0 || !errors.As(err, &native) || native.Code != "53300" ||
		!errors.As(err, &known) || !store.Transient(err) || store.MayHaveCommitted(err) {
		t.Fatalf("physical admission refusal: position=%d error=%v; need native 53300, transient and known not committed", position, err)
	}
	if after := postgresCapacityRows(t, admin, schema); after != before {
		t.Fatalf("refused append changed persistent state: before=%+v after=%+v", before, after)
	}
	// Returning a connection to the idle pool does not release the quota.
	// Close the whole holder pool and observe server-side release before retry.
	if err := holderDB.Close(); err != nil {
		t.Fatal(err)
	}
	holderDB = nil
	if err := waitPostgresRoleCapacity(ctx, admin, role, 0); err != nil {
		t.Fatal(err)
	}
	position, err = writer.Append(ctx, "t", batch)
	if err != nil || position != 1 {
		t.Fatalf("same writer pool did not recover after physical release: position=%d error=%v", position, err)
	}
	after := postgresCapacityRows(t, admin, schema)
	if after.Head != 1 || after.StepHead != 1 || after.Events != 1 || after.Steps != 1 || after.Markers != 1 {
		t.Fatalf("recovery did not commit exactly one batch: %+v", after)
	}
	if err := waitPostgresRoleCapacity(ctx, admin, role, 0); err != nil {
		t.Fatal(err)
	}
}

type postgresCapacitySnapshot struct {
	Head, StepHead, Events, Steps, Markers int64
}

func postgresCapacityRows(t *testing.T, admin *sql.DB, schema string) postgresCapacitySnapshot {
	t.Helper()
	var snapshot postgresCapacitySnapshot
	table := func(name string) string { return pgx.Identifier{schema, name}.Sanitize() }
	query := fmt.Sprintf(`SELECT head, steps,
        (SELECT count(*) FROM %s WHERE tenant=$1),
        (SELECT count(*) FROM %s WHERE tenant=$1),
        (SELECT count(*) FROM %s WHERE tenant=$1)
        FROM %s WHERE tenant=$1`, table("grist_events"), table("grist_steps"), table("grist_markers"), table("grist_heads"))
	if err := admin.QueryRowContext(t.Context(), query, "t").Scan(&snapshot.Head, &snapshot.StepHead, &snapshot.Events, &snapshot.Steps, &snapshot.Markers); err != nil {
		t.Fatal("inspect capacity fixture state:", err)
	}
	return snapshot
}

// These condition waits synchronize with PostgreSQL's asynchronous session
// teardown/activity publication; elapsed time is not a performance assertion.
func waitPostgresRoleCapacity(parent context.Context, admin *sql.DB, role string, want int) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond) //qenv:ok real PostgreSQL fixture synchronization
	defer ticker.Stop()
	for {
		var count int
		if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE usename=$1", role).Scan(&count); err != nil {
			return fmt.Errorf("observe physical role sessions: %w", err)
		}
		if count == want {
			return nil
		}
		select { //qenv:ok real PostgreSQL fixture synchronization, outside simulation
		case <-ctx.Done():
			return fmt.Errorf("role still has %d physical sessions, want %d: %w", count, want, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitPostgresCapacityIdle(t *testing.T, admin *sql.DB, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond) //qenv:ok real PostgreSQL fixture synchronization
	defer ticker.Stop()
	for {
		var idle sql.NullBool
		err := admin.QueryRowContext(ctx, "SELECT state='idle' AND xact_start IS NULL FROM pg_stat_activity WHERE pid=$1", pid).Scan(&idle)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("inspect idle physical holder:", err)
		}
		if err == nil && !idle.Valid {
			t.Fatal("PostgreSQL fixture cannot inspect another role's session state: use a fixture administrator with superuser or pg_read_all_stats visibility")
		}
		if idle.Valid && idle.Bool {
			return
		}
		select { //qenv:ok real PostgreSQL fixture synchronization, outside simulation
		case <-ctx.Done():
			t.Fatalf("holder never became idle without a transaction: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
