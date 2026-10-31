package pgnotify_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore/pgnotify"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	if err := pgnotify.Run(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "nil store") {
		t.Fatalf("nil store: %v", err)
	}
	const secret = "not-for-error-output"
	err := pgnotify.Run(context.Background(), "postgres://user:"+secret+"@localhost:invalid-port/db", &sqlstore.Store{})
	if err == nil || !strings.Contains(err.Error(), "invalid PostgreSQL DSN") {
		t.Fatalf("malformed DSN: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("DSN exposed in error: %v", err)
	}
}

// Cancellation must interrupt a connected server that never answers the
// startup handshake, without requiring a PostgreSQL installation.
func TestRunCancellationDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	acceptError := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptError <- err
			return
		}
		accepted <- conn
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	port := listener.Addr().(*net.TCPAddr).Port
	go func() {
		result <- pgnotify.Run(ctx, fmt.Sprintf("host=127.0.0.1 port=%d user=test dbname=test sslmode=disable connect_timeout=10", port), &sqlstore.Store{})
	}()
	var peer net.Conn
	select {
	case peer = <-accepted:
	case err := <-acceptError:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("listener was not connected:", ctx.Err())
	}
	defer peer.Close()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not cancel a stalled handshake")
	}
	// Drain the already-written startup packet and require the client socket to
	// close, rather than merely letting Run abandon a connected goroutine.
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, peer); err != nil && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("client socket remained open or failed unexpectedly: %v", err)
	}
}
