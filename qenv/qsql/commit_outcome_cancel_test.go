package qsql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Cancellation observed after application cannot turn an applied commit into a
// rollback report. In particular, a lost commit reply retains its ambiguity.
func TestCancellationAfterAppliedCommitPreservesOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option int
	}{{"success", 0}, {"unknown", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var cancel context.CancelFunc
			hooks := 0
			w := newWorld(t, pick{"fault.store.paydb.commit": {tc.option}}, qenv.ReadCommitted,
				qsql.Options{OnCommit: func(context.Context, []qsql.Change) {
					if cancel != nil {
						hooks++
						cancel()
					}
				}})
			w.st.CommitFaults = true
			var got error
			w.s.Spawn("payer", func(ctx context.Context) {
				var txctx context.Context
				txctx, cancel = context.WithCancel(ctx)
				defer cancel()
				tx, err := w.db.BeginTx(txctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(txctx, `UPDATE accounts SET balance = 90 WHERE id = 'alice'`); err != nil {
					t.Error(err)
					return
				}
				got = tx.Commit()
			})
			w.run(t)
			if tc.option == 0 && got != nil {
				t.Fatalf("applied commit reported aborted: %v", got)
			}
			if tc.option == 1 && (!errors.Is(got, qenv.ErrUnknownOutcome) || code(got) != "08006") {
				t.Fatalf("lost commit outcome replaced by cancellation: %v", got)
			}
			if hooks != 1 || w.balance(t, "alice") != 90 {
				t.Fatalf("hooks=%d balance=%d; want one applied commit", hooks, w.balance(t, "alice"))
			}
		})
	}
}
