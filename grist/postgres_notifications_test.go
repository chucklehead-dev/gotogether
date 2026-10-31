package grist_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestMainPostgresNotificationsIgnoresSQLite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called atomic.Bool
	_, _, done := startServe(t, ctx, apiApp(), t.TempDir(), nil,
		grist.WithPostgresNotifications(func(context.Context, string, *sqlstore.Store) error {
			called.Store(true)
			return nil
		}))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if called.Load() {
		t.Fatal("PostgreSQL listener started for SQLite")
	}
}

// A request holds Shutdown open, proving the listener survives the node's
// drain. The listener then holds its return open, proving serve joins it before
// closing the store or returning to its caller.
func TestMainPostgresNotificationsLifecycle(t *testing.T) {
	dsn := os.Getenv("GRIST_POSTGRES")
	if dsn == "" {
		t.Skip("GRIST_POSTGRES enables PostgreSQL listener lifecycle coverage")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open PostgreSQL test database")
	}
	defer admin.Close()
	schema := fmt.Sprintf("grist_listener_%d", time.Now().UnixNano()) //qenv:ok real-tier test schema identity
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal("create PostgreSQL test schema")
	}
	defer admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("parse PostgreSQL test connection string")
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	spec := dsn
	if !strings.HasPrefix(spec, "postgres://") && !strings.HasPrefix(spec, "postgresql://") {
		spec = "postgres:" + spec
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseRequestOnce sync.Once
	finishRequest := func() { releaseRequestOnce.Do(func() { close(releaseRequest) }) }
	defer finishRequest()
	releaseListener := make(chan struct{})
	var releaseListenerOnce sync.Once
	finishListener := func() { releaseListenerOnce.Do(func() { close(releaseListener) }) }
	defer finishListener()
	listenerStarted := make(chan context.Context, 1)
	listenerStopped := make(chan error, 1)
	listen := func(lctx context.Context, gotDSN string, st *sqlstore.Store) error {
		if gotDSN != dsn {
			listenerStopped <- fmt.Errorf("listener received a different connection string")
			return nil
		}
		listenerStarted <- lctx
		<-lctx.Done()
		readCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, err := st.Tenants(readCtx)
		listenerStopped <- err
		<-releaseListener
		return lctx.Err()
	}
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- grist.MainArgs(ctx, apiApp(), []string{"serve", "-db", spec, "-addr", "127.0.0.1:0", "-health-addr", ""}, &out, &out,
			grist.WithPostgresNotifications(listen),
			grist.WithDrain(grist.Drain{Delay: -1, Shutdown: 5 * time.Second}),
			grist.Middleware(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/listener-drain-test" {
						close(requestEntered)
						<-releaseRequest
						w.WriteHeader(http.StatusNoContent)
						return
					}
					next.ServeHTTP(w, r)
				})
			}))
	}()
	addr := waitServing(t, &out, done)
	var listenerCtx context.Context
	select {
	case listenerCtx = <-listenerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not start")
	}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(addr + "/listener-drain-test")
		if err == nil {
			resp.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-requestEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach drain barrier")
	}
	cancel()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), `"msg":"draining"`); {
		if time.Now().After(deadline) {
			t.Fatal("serve did not enter drain")
		}
		time.Sleep(time.Millisecond)
	}
	if listenerCtx.Err() != nil {
		t.Fatal("listener canceled while the HTTP request still needs the node")
	}
	finishRequest()
	if err := <-requestDone; err != nil {
		t.Fatal("draining request failed")
	}
	select {
	case err := <-listenerStopped:
		if err != nil {
			t.Fatal("database closed before listener stopped")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("listener was not canceled after drain")
	}
	select {
	case <-done:
		t.Fatal("serve returned before joining listener")
	default:
	}
	finishListener()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("serve failed during clean listener shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not join listener")
	}
}
