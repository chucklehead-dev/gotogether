package qsql_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Change capture follows the transaction's applied effects, including when the
// connection loses the commit answer. Delivery is synchronous with Commit.
func TestOnCommitCapturesOrderedRowChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unknown bool
	}{{"acknowledged", false}, {"unknown outcome", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var chooser qenv.Chooser = qenv.Nominal{}
			if tc.unknown {
				chooser = pick{"fault.store.paydb.commit": {1}}
			}
			var batches [][]qsql.Change
			var applied []map[string]any
			var w *world
			w = newWorld(t, chooser, qenv.Serializable, qsql.Options{OnCommit: func(_ context.Context, changes []qsql.Change) {
				batches = append(batches, changes)
				if w != nil {
					applied = qsql.Table(w.st, "accounts")
				}
			}})
			batches = nil // setup transactions are outside the contract scenario
			w.st.CommitFaults = tc.unknown
			var commitErr error
			w.s.Spawn("client", func(ctx context.Context) {
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer tx.Rollback()
				for _, statement := range []string{
					`INSERT INTO accounts VALUES ('carol',7)`,
					`UPDATE accounts SET balance=balance+3 WHERE id='alice'`,
					`DELETE FROM accounts WHERE id='bob'`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						t.Error(err)
						return
					}
				}
				if len(batches) != 0 {
					t.Errorf("uncommitted changes were delivered: %+v", batches)
				}
				commitErr = tx.Commit()
				if len(batches) != 1 {
					t.Errorf("Commit returned before one change batch was delivered: %+v", batches)
				}
			})
			w.run(t)
			if tc.unknown {
				if code(commitErr) != "08006" || !errors.Is(commitErr, qenv.ErrUnknownOutcome) {
					t.Fatalf("commit=%v, want applied unknown outcome", commitErr)
				}
			} else if commitErr != nil {
				t.Fatal(commitErr)
			}
			want := [][]qsql.Change{{
				{Table: "accounts", Op: "insert", New: map[string]any{"id": "carol", "balance": int64(7)}},
				{Table: "accounts", Op: "update", Old: map[string]any{"id": "alice", "balance": int64(100)}, New: map[string]any{"id": "alice", "balance": int64(103)}},
				{Table: "accounts", Op: "delete", Old: map[string]any{"id": "bob", "balance": int64(100)}},
			}}
			if !reflect.DeepEqual(batches, want) {
				t.Fatalf("OnCommit batches=%#v, want %#v", batches, want)
			}
			wantRows := []map[string]any{{"id": "alice", "balance": int64(103)}, {"id": "carol", "balance": int64(7)}}
			if !reflect.DeepEqual(applied, wantRows) {
				t.Fatalf("callback observed rows=%v, want applied rows=%v", applied, wantRows)
			}
			if got := qsql.Table(w.st, "accounts"); !reflect.DeepEqual(got, wantRows) {
				t.Fatalf("committed rows=%v, want %v", got, wantRows)
			}
		})
	}
}

// Rolled-back changes and transactions that matched no rows produce no
// committed-change receipt, even after another transaction commits later.
func TestOnCommitExcludesRollbackAndUnchangedRows(t *testing.T) {
	var batches [][]qsql.Change
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable, qsql.Options{OnCommit: func(_ context.Context, changes []qsql.Change) { batches = append(batches, changes) }})
	batches = nil
	w.s.Spawn("client", func(ctx context.Context) {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		for _, statement := range []string{
			`INSERT INTO accounts VALUES ('carol',7)`,
			`UPDATE accounts SET balance=999 WHERE id='alice'`,
			`DELETE FROM accounts WHERE id='bob'`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Error(err)
			return
		}
		if len(batches) != 0 {
			t.Errorf("rollback delivered changes: %+v", batches)
		}
		empty, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer empty.Rollback()
		result, err := empty.ExecContext(ctx, `UPDATE accounts SET balance=999 WHERE id='missing'`)
		if err != nil {
			t.Error(err)
			return
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Errorf("no-match update affected=%d err=%v", n, err)
		}
		if err := empty.Commit(); err != nil {
			t.Error(err)
			return
		}
		if len(batches) != 0 {
			t.Errorf("no-match transaction delivered changes: %+v", batches)
		}
		// A later committed change must not pick up a rolled-back batch.
		if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance=101 WHERE id='alice'`); err != nil {
			t.Error(err)
		}
	})
	w.run(t)
	want := [][]qsql.Change{{{Table: "accounts", Op: "update", Old: map[string]any{"id": "alice", "balance": int64(100)}, New: map[string]any{"id": "alice", "balance": int64(101)}}}}
	if !reflect.DeepEqual(batches, want) {
		t.Fatalf("OnCommit included noncommitted changes: got %#v want %#v", batches, want)
	}
	wantRows := []map[string]any{{"id": "alice", "balance": int64(101)}, {"id": "bob", "balance": int64(100)}}
	if got := qsql.Table(w.st, "accounts"); !reflect.DeepEqual(got, wantRows) {
		t.Fatalf("rollback changed stored rows: got %v want %v", got, wantRows)
	}
}

// A retained before/after receipt continues to describe that commit after later
// writes. The callback is an observer: this does not invent a right to mutate
// its maps or the Store's values.
func TestOnCommitRetainsHistoricalRowSnapshots(t *testing.T) {
	var batches [][]qsql.Change
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable, qsql.Options{OnCommit: func(_ context.Context, changes []qsql.Change) { batches = append(batches, changes) }})
	w.exec(t, `CREATE TABLE captures (id TEXT PRIMARY KEY, body BLOB, n BIGINT)`)
	w.exec(t, `INSERT INTO captures VALUES ('key',?,1)`, []byte{1, 2})
	batches = nil
	w.s.Spawn("client", func(ctx context.Context) {
		for i, body := range [][]byte{{3, 4}, {5, 6}} {
			if _, err := w.db.ExecContext(ctx, `UPDATE captures SET body=?,n=? WHERE id='key'`, body, i+2); err != nil {
				t.Error(err)
				return
			}
		}
	})
	w.run(t)
	want := [][]qsql.Change{
		{{Table: "captures", Op: "update", Old: map[string]any{"id": "key", "body": []byte{1, 2}, "n": int64(1)}, New: map[string]any{"id": "key", "body": []byte{3, 4}, "n": int64(2)}}},
		{{Table: "captures", Op: "update", Old: map[string]any{"id": "key", "body": []byte{3, 4}, "n": int64(2)}, New: map[string]any{"id": "key", "body": []byte{5, 6}, "n": int64(3)}}},
	}
	if !reflect.DeepEqual(batches, want) {
		t.Fatalf("retained row snapshots changed: got %#v want %#v", batches, want)
	}
	if got := qsql.Table(w.st, "captures"); !reflect.DeepEqual(got, []map[string]any{want[1][0].New}) {
		t.Fatalf("current rows=%v, want final receipt=%v", got, want[1][0].New)
	}
}
