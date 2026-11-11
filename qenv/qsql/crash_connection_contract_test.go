package qsql_test

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// A crashed task's driver call must return a connection-loss error and mark
// its connection invalid before returning it to database/sql. Observe the
// standard Validator only inside Raw: the connection cannot escape its callback.
func TestCrashedDriverConnectionIsInvalidBeforeRelease(t *testing.T) {
	for _, mode := range []string{"inside autocommit", "between transaction statements"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
			held, entered := false, false
			if mode == "inside autocommit" {
				holder := qsql.Open(w.s, w.st)
				w.s.Spawn("holder", func(ctx context.Context) {
					tx, err := holder.BeginTx(ctx, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer tx.Rollback()
					if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=1 WHERE id='alice'`); err != nil {
						t.Error(err)
						return
					}
					held = true
					qenv.Sleep(ctx, time.Hour)
				})
			}
			observed, valid := false, true
			var got error
			var callPanic any
			p := w.s.Process("client", true, func(ctx context.Context) {
				conn, err := w.db.Conn(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.Raw(func(raw any) error {
					exec := raw.(driver.ExecerContext)
					validator := raw.(driver.Validator)
					observe := func() {
						defer func() {
							if value := recover(); value != nil {
								callPanic = value
							}
						}()
						_, got = exec.ExecContext(ctx, `UPDATE accounts SET balance=2 WHERE id='alice'`, nil)
						observed, valid = true, validator.IsValid()
					}
					if mode == "between transaction statements" {
						tx, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
						if err != nil {
							return err
						}
						defer tx.Rollback()
						if _, err := exec.ExecContext(ctx, `UPDATE accounts SET balance=90 WHERE id='bob'`, nil); err != nil {
							return err
						}
						defer observe() // cleanup calls the driver after the crash
						entered = true
						qenv.Sleep(ctx, time.Hour) // the crash is outside the driver
					} else {
						qenv.Wait(ctx, func() bool { return held })
						entered = true
						observe() // waits for the holder's lock inside the driver
					}
					return driver.ErrBadConn // discard this borrowed connection
				})
			})
			w.s.Spawn("crash", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return entered })
				qenv.Sleep(ctx, time.Second)
				p.Crash()
			})
			w.run(t)
			if !p.Down() {
				t.Fatal("client did not crash")
			}
			w.s.Close() // the frozen task unwinds and runs its deferred cleanup
			if callPanic != nil || !observed || code(got) != "08006" || valid {
				t.Errorf("crashed driver call: panic=%v observed=%v error=%v valid=%v; want no panic, returned 08006 and invalid connection", callPanic, observed, got, valid)
			}
			if alice, bob := w.balance(t, "alice"), w.balance(t, "bob"); alice != 100 || bob != 100 {
				t.Errorf("crashed work applied: alice=%d bob=%d; want 100 each", alice, bob)
			}
		})
	}
}
