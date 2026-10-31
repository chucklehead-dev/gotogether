package pgnotify_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore/pgnotify"
	"github.com/chucklehead-dev/gotogether/grist/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func withParam(dsn, key, value string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, _ := url.Parse(dsn)
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " " + key + "='" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`) + "'"
}

// This exercises independent SQL writers, real transactional NOTIFY, a
// disconnected listener, and a fresh LISTEN after reconnect. A long Poll makes
// notification delivery observable independently of the safety backstop.
func TestPostgresNotifications(t *testing.T) {
	dsn := os.Getenv("GRIST_POSTGRES")
	if dsn == "" {
		t.Skip("set GRIST_POSTGRES to run PostgreSQL notification integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("grist_notify_%d", time.Now().UnixNano())
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	scoped := withParam(dsn, "search_path", schema)
	sdb, err := sql.Open("pgx", scoped)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	readerApp := schema + "_reader"
	rdb, err := sql.Open("pgx", withParam(scoped, "application_name", readerApp))
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	reader, err := sqlstore.Open(ctx, rdb, sqlstore.Postgres, sqlstore.Options{Name: "reader", Poll: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sqlstore.Open(ctx, sdb, sqlstore.Postgres, sqlstore.Options{Name: "different-writer", PostgresNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.CreateTenant(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	app := schema + "_listener"
	listenCtx, stop := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- pgnotify.Run(listenCtx, withParam(scoped, "application_name", app), reader) }()
	defer func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("listener failed to stop")
		}
	}()
	waitListener := func(previous int) int {
		t.Helper()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			var pid int
			err := db.QueryRowContext(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND state='idle' AND query LIKE 'LISTEN%' AND pid<>$2`, app, previous).Scan(&pid)
			if err == nil {
				return pid
			}
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("listener readiness:", ctx.Err())
			case <-tick.C:
			}
		}
	}
	pid := waitListener(0)
	// Observe an authoritative read on the server before allowing the write.
	// Each fresh query_start barrier excludes earlier reads on pooled sessions.
	startWait := func(fn func() error, pattern string, results chan<- error) {
		t.Helper()
		var since time.Time
		if err := db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&since); err != nil {
			t.Fatal(err)
		}
		go func() { results <- fn() }()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var observed bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='idle' AND query_start >= $2 AND query LIKE $3)`, readerApp, since, pattern).Scan(&observed); err != nil {
				t.Fatal(err)
			}
			if observed {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("waiter did not read:", ctx.Err())
			case <-tick.C:
			}
		}
	}

	// Raw notifications, including one rolled back, cannot invent head progress.
	waitCtx, stopWait := context.WithCancel(ctx)
	result := make(chan error, 1)
	startWait(func() error { return reader.Wait(waitCtx, "first", 0) }, "SELECT head%", result)
	tx, err := sdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE grist_heads SET head=99, steps=99 WHERE tenant='first'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_notify($1,'')`, sqlstore.PostgresNotifyChannel); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = sdb.ExecContext(ctx, `SELECT pg_notify($1,'')`, sqlstore.PostgresNotifyChannel); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-result:
		t.Fatalf("rolled-back write or spurious notification completed Wait: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	// The actual committed append, rather than any hint, satisfies Wait.
	if _, err = writer.Append(ctx, "first", store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	stopWait()
	// One transaction wakes all applicable independent wait APIs.
	results := make(chan error, 3)
	startWait(func() error { return reader.Wait(ctx, "first", 1) }, "SELECT head%", results)
	startWait(func() error { return reader.WaitSteps(ctx, "first", 0) }, "SELECT steps%", results)
	startWait(func() error { return reader.WaitTenants(ctx, 1) }, "SELECT tenant%", results)
	if _, err = writer.Append(ctx, "first", store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`)}}, Step: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err = writer.CreateTenant(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
	waitListener(pid)
	result = make(chan error, 1)
	startWait(func() error { return reader.WaitSteps(ctx, "first", 1) }, "SELECT steps%", result)
	if _, err = writer.Append(ctx, "first", store.Batch{Step: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	// Simulate a subscription gap without timing a write against reconnect backoff.
	result = make(chan error, 1)
	startWait(func() error { return reader.WaitSteps(ctx, "first", 2) }, "SELECT steps%", result)
	stop()
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop: %v", err)
	}
	quietWriter, err := sqlstore.Open(ctx, sdb, sqlstore.Postgres, sqlstore.Options{Name: "silent-writer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = quietWriter.Append(ctx, "first", store.Batch{Step: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	restartCtx, stopRestart := context.WithCancel(ctx)
	defer stopRestart()
	listenCtx, stop = restartCtx, stopRestart
	go func() { stopped <- pgnotify.Run(listenCtx, withParam(scoped, "application_name", app), reader) }()
	waitListener(pid)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}
