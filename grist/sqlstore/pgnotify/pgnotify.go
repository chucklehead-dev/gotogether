// Package pgnotify wakes SQL store readers after PostgreSQL commits. A
// dedicated pgx connection listens independently of the application's SQL pool;
// notifications are hints and the store's polling remains the recovery path.
package pgnotify

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/jackc/pgx/v5"
)

// Serve enables transactional PostgreSQL notifications and a managed listener
// for grist.Main's serve command. SQLite continues to use polling. The listener
// lives through node drain and is stopped and joined before the database closes.
func Serve() grist.ServeOption {
	return grist.WithPostgresNotifications(Run)
}

// Run listens until ctx ends, waking every waiter of s after LISTEN succeeds
// and whenever a commit hint arrives. It reconnects after connection failures,
// with backoff from 100 ms to 5 s. The immediate wake on each subscription
// catches commits missed while disconnected; polling covers the disconnection.
//
// The DSN must name the same database as s, with the same routing assumptions
// (use a session-capable PostgreSQL endpoint, not transaction-pooled LISTEN).
// Run opens its own connection, so it cannot exhaust s's database/sql pool.
// Pair it with sqlstore.Options.PostgresNotify on every writing Store. Run
// returns ctx.Err() on cancellation; malformed DSNs and nil stores fail at once.
// Availability transitions are reported through slog without DSNs or raw errors.
func Run(ctx context.Context, dsn string, s *sqlstore.Store) error {
	if s == nil {
		return errors.New("pgnotify: nil store: pass the PostgreSQL store whose waiters should wake")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		// Parse errors can contain the DSN, including its password.
		return errors.New("pgnotify: invalid PostgreSQL DSN: check its connection parameters")
	}
	if config.RuntimeParams["application_name"] == "" {
		config.RuntimeParams["application_name"] = "grist-notify"
	}
	backoff := 100 * time.Millisecond
	unavailable := false
	for ctx.Err() == nil {
		phase := "connect"
		conn, err := pgx.ConnectConfig(ctx, config.Copy())
		if err == nil {
			phase = "listen"
			_, err = conn.Exec(ctx, `LISTEN `+sqlstore.PostgresNotifyChannel)
			if err == nil {
				phase = "receive"
				if unavailable {
					slog.InfoContext(ctx, "PostgreSQL notification listener recovered", "component", "pgnotify")
					unavailable = false
				}
				backoff = 100 * time.Millisecond
				s.NotifyExternal()
				for ctx.Err() == nil {
					_, err = conn.WaitForNotification(ctx)
					if err != nil {
						break
					}
					s.NotifyExternal()
				}
			}
			// Bound the graceful close even if the peer stops reading. A canceled
			// service context makes this immediate; pgx always closes the socket.
			closeCtx, cancelClose := context.WithTimeout(ctx, time.Second)
			_ = conn.Close(closeCtx)
			cancelClose()
		}
		if ctx.Err() != nil {
			break
		}
		if !unavailable {
			slog.WarnContext(ctx, "PostgreSQL notification listener unavailable", "component", "pgnotify", "phase", phase, "fallback", "polling")
			unavailable = true
		}
		timer := time.NewTimer(backoff) //qenv:ok real PostgreSQL listener reconnect backoff
		select {                        //qenv:ok native listener, outside the deterministic simulation
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
		backoff = min(backoff*2, 5*time.Second)
	}
	return ctx.Err()
}
