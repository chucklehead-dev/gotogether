package qsql_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

func checkTransactionCapacity(t *testing.T, err error) {
	t.Helper()
	var capacity *qenv.TransactionCapacityError
	var state sqlState
	if !errors.Is(err, qenv.ErrTransactionCapacity) || !errors.As(err, &capacity) ||
		capacity.Store != "paydb" || capacity.Limit != 1 || capacity.Active != 1 ||
		errors.Is(err, qenv.ErrUnknownOutcome) || errors.Is(err, driver.ErrBadConn) || errors.As(err, &state) {
		t.Errorf("admission refusal = %v, want direct paydb transaction-capacity error (limit/active 1), not SQLSTATE, BadConn or unknown outcome", err)
	}
}

// A logical transaction owns capacity regardless of which physical connection
// database/sql lends it. Refusals run no statements, while ending the holder
// makes the same operation possible. Warm and cold pool variants must preserve
// the complete scheduler choice log, including an actual replay.
func TestTransactionCapacityAdmissionAndPoolReplay(t *testing.T) {
	run := func(c qenv.Chooser, idle int) []qenv.Choice {
		commits := 0
		log := &qenv.ChoiceLog{C: c}
		w := newWorld(t, log, qenv.ReadCommitted, qsql.Options{OnCommit: func(context.Context, []qsql.Change) { commits++ }})
		defer w.s.Close()
		w.db.SetMaxIdleConns(idle)
		w.st.MaxTransactions = 1
		commits = 0
		before := w.st.Version()
		held, refused, released := false, false, false
		w.s.Spawn("holder", func(ctx context.Context) {
			tx, err := w.db.BeginTx(ctx, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=90 WHERE id='alice'`); err != nil {
				t.Error(err)
				return
			}
			held = true
			qenv.Wait(ctx, func() bool { return refused })
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
			released = true
		})
		w.s.Spawn("contender", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return held })
			tx, err := w.db.BeginTx(ctx, nil)
			checkTransactionCapacity(t, err)
			if tx != nil {
				t.Error("refused BeginTx returned a transaction")
				_ = tx.Rollback()
			}
			_, err = w.db.ExecContext(ctx, `UPDATE accounts SET balance=1000 WHERE id='alice'`)
			checkTransactionCapacity(t, err)
			var n int64
			err = w.db.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id='alice'`).Scan(&n)
			checkTransactionCapacity(t, err)
			if w.st.Version() != before || w.balance(t, "alice") != 100 || commits != 0 {
				t.Errorf("refused work changed state: version %d (was %d), balance %d, commits %d", w.st.Version(), before, w.balance(t, "alice"), commits)
			}
			refused = true
			qenv.Wait(ctx, func() bool { return released })
			if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance=balance+1 WHERE id='alice'`); err != nil {
				t.Errorf("capacity unavailable after holder commit: %v", err)
			}
		})
		w.run(t)
		if w.balance(t, "alice") != 91 || commits != 2 {
			t.Errorf("recovered balance %d, commits %d; want 91 and 2", w.balance(t, "alice"), commits)
		}
		return log.Choices
	}
	for seed := range uint64(5) {
		first := run(qenv.NewSeeded(seed), 0)
		warm := run(qenv.NewSeeded(seed), 2)
		if !reflect.DeepEqual(first, warm) {
			t.Fatalf("seed %d: pool reuse changed choices\ncold %v\nwarm %v", seed, first, warm)
		}
		replay := qenv.Replay(first)
		again := run(replay, 2)
		if replay.Err() != nil || replay.Left() != 0 || !reflect.DeepEqual(first, again) {
			t.Fatalf("seed %d: warm-pool replay: %v, %d choices left", seed, replay.Err(), replay.Left())
		}
	}
}

// database/sql's cancellation goroutine must leave Store cleanup to simulation
// tasks. The canceled owner makes no subsequent driver call: admission must
// recover without depending on whether its physical connection was reused.
func TestTransactionCapacityCancellationReleasesAdmission(t *testing.T) {
	run := func(c qenv.Chooser, idle int) []qenv.Choice {
		commits := 0
		log := &qenv.ChoiceLog{C: c}
		w := newWorld(t, log, qenv.ReadCommitted, qsql.Options{OnCommit: func(context.Context, []qsql.Change) { commits++ }})
		w.db.SetMaxIdleConns(idle)
		w.st.MaxTransactions = 1
		commits = 0
		canceled, recovered := false, false
		w.s.Spawn("canceller", func(ctx context.Context) {
			txctx, cancel := context.WithCancel(ctx)
			defer cancel()
			tx, err := w.db.BeginTx(txctx, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tx.ExecContext(txctx, `UPDATE accounts SET balance=0 WHERE id='alice'`); err != nil {
				t.Error(err)
				return
			}
			cancel()
			canceled = true
			qenv.Wait(ctx, func() bool { return recovered })
		})
		w.s.Spawn("next", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return canceled })
			for range 100 {
				_, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance=balance+1 WHERE id='alice'`)
				if errors.Is(err, qenv.ErrTransactionCapacity) {
					continue // Begin yields, giving the reaper a deterministic turn.
				}
				if err != nil {
					t.Error(err)
				}
				recovered = true
				return
			}
			t.Error("canceled transaction retained admission after 100 attempts")
			recovered = true
		})
		if err := w.s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		if !recovered || w.balance(t, "alice") != 101 || commits != 1 {
			t.Errorf("recovered %v, balance %d, commits %d; want true,101,1", recovered, w.balance(t, "alice"), commits)
		}
		w.s.Close()
		return log.Choices
	}
	for seed := range uint64(10) {
		first := run(qenv.NewSeeded(seed), 0)
		warm := run(qenv.NewSeeded(seed), 2)
		if !reflect.DeepEqual(first, warm) {
			t.Fatalf("seed %d: pool reuse changed cancellation choices\ncold %v\nwarm %v", seed, first, warm)
		}
		replay := qenv.Replay(first)
		again := run(replay, 2)
		if replay.Err() != nil || replay.Left() != 0 || !reflect.DeepEqual(first, again) {
			t.Fatalf("seed %d: canceled transaction replay: %v, %d choices left", seed, replay.Err(), replay.Left())
		}
	}
}

// A failed SQL statement aborts its logical transaction immediately, even while
// database/sql still lends the physical connection to the caller's *sql.Tx.
func TestTransactionCapacityFailedStatementReleasesAdmission(t *testing.T) {
	commits := 0
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted, qsql.Options{OnCommit: func(context.Context, []qsql.Change) { commits++ }})
	w.st.MaxTransactions = 1
	commits = 0
	w.s.Spawn("client", func(ctx context.Context) {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=0 WHERE id='alice'`); err != nil {
			t.Error(err)
			return
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts VALUES ('alice',0)`); code(err) != "23505" {
			t.Errorf("duplicate error = %v, want 23505", err)
		}
		if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance=balance+1 WHERE id='alice'`); err != nil {
			t.Errorf("failed statement retained logical admission: %v", err)
		}
		if err := tx.Commit(); code(err) != "25P02" {
			t.Errorf("aborted transaction commit = %v, want 25P02", err)
		}
	})
	w.run(t)
	if w.balance(t, "alice") != 101 || commits != 1 {
		t.Errorf("balance %d, commits %d; want 101 and 1", w.balance(t, "alice"), commits)
	}
}
