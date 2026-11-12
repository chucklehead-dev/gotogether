package qsql_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Schema visibility follows the same durable Store history as the rows, even
// for pools that previously created or read a table whose DDL is later lost.
func TestSchemaFollowsRestoredStoreHistory(t *testing.T) {
	for _, event := range []string{"restore", "failover loses DDL"} {
		t.Run(event, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			st := qenv.NewStore(s, "schema", qenv.Serializable)
			creator, reader := qsql.Open(s, st), qsql.Open(s, st)
			defer creator.Close()
			defer reader.Close()
			st.Backup()
			if _, err := creator.Exec(`CREATE TABLE items (id TEXT PRIMARY KEY, old_text TEXT)`); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := reader.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("initial reader: count=%d error=%v", count, err)
			}
			if event == "restore" {
				if !st.Restore() {
					t.Fatal("restore had no backup")
				}
			} else if lost := st.FailOver(1, 0); lost != 1 {
				t.Fatalf("failover lost %d commits; want the table's DDL commit", lost)
			}
			fresh := qsql.Open(s, st)
			defer fresh.Close()
			pools := []struct {
				name string
				db   *sql.DB
			}{{"creator", creator}, {"reader", reader}, {"fresh", fresh}}
			for _, pool := range pools {
				err := pool.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count)
				if code(err) != "42P01" {
					t.Errorf("%s sees lost table: error=%v; want undefined relation 42P01", pool.name, err)
				}
			}
			if _, err := fresh.Exec(`CREATE TABLE items (id TEXT PRIMARY KEY, new_value BIGINT)`); err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.Exec(`INSERT INTO items VALUES ('one', 7)`); err != nil {
				t.Fatal(err)
			}
			for _, pool := range pools {
				var value int64
				if err := pool.db.QueryRow(`SELECT new_value FROM items WHERE id='one'`).Scan(&value); err != nil || value != 7 {
					t.Errorf("%s after recreation: value=%d error=%v; want 7", pool.name, value, err)
				}
				var obsolete any
				err := pool.db.QueryRow(`SELECT old_text FROM items WHERE id='one'`).Scan(&obsolete)
				if code(err) != "42703" {
					t.Errorf("%s accepts obsolete column: error=%v; want undefined column 42703", pool.name, err)
				}
			}
		})
	}
}

func TestTransactionalSchemaAndCommitDelivery(t *testing.T) {
	for _, outcome := range []string{"rollback", "commit", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			var chooser qenv.Chooser = qenv.Nominal{}
			if outcome == "unknown" {
				chooser = pick{"fault.store.paydb.commit": {1}}
			}
			var batches [][]qsql.Change
			w := newWorld(t, chooser, qenv.Serializable, qsql.Options{OnCommit: func(_ context.Context, changes []qsql.Change) {
				batches = append(batches, changes)
			}})
			batches = nil
			w.st.CommitFaults = outcome == "unknown"
			reader := qsql.Open(w.s, w.st)
			w.s.Spawn("schema writer", func(ctx context.Context) {
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, `CREATE TABLE draft (id TEXT PRIMARY KEY, value BIGINT)`); err != nil {
					t.Error(err)
					return
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO draft VALUES ('one', 7)`); err != nil {
					t.Error(err)
					return
				}
				var own int64
				if err := tx.QueryRowContext(ctx, `SELECT value FROM draft WHERE id='one'`).Scan(&own); err != nil || own != 7 {
					t.Errorf("transaction's own schema/data: %d, %v", own, err)
				}
				if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM draft`).Scan(&own); code(err) != "42P01" {
					t.Errorf("uncommitted schema was visible: %v", err)
				}
				if outcome == "rollback" {
					err = tx.Rollback()
				} else {
					err = tx.Commit()
				}
				if outcome == "unknown" {
					if !errors.Is(err, qenv.ErrUnknownOutcome) || code(err) != "08006" {
						t.Errorf("applied DDL commit lost outcome: %v", err)
					}
				} else if err != nil {
					t.Error(err)
				}
			})
			w.run(t)
			var got int64
			err := reader.QueryRow(`SELECT value FROM draft WHERE id='one'`).Scan(&got)
			if outcome == "rollback" {
				if code(err) != "42P01" || len(batches) != 0 {
					t.Errorf("rolled-back DDL: query=%v changes=%v", err, batches)
				}
			} else if err != nil || got != 7 || len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].Table != "draft" || batches[0][0].Op != "insert" {
				t.Errorf("applied DDL: value=%d query=%v changes=%v; want 7 and one committed insert", got, err, batches)
			}
		})
	}
}

func TestSchemaVisibilityIndependentOfPoolWarmth(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		t.Run(iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, iso)
			cold := qsql.Open(w.s, w.st)
			w.s.Spawn("reader", func(ctx context.Context) {
				warmTx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer warmTx.Rollback()
				coldTx, err := cold.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer coldTx.Rollback()
				if _, err := w.db.ExecContext(ctx, `CREATE TABLE later (id TEXT PRIMARY KEY)`); err != nil {
					t.Error(err)
					return
				}
				if _, err := w.db.ExecContext(ctx, `INSERT INTO later VALUES ('one')`); err != nil {
					t.Error(err)
					return
				}
				var warmCount, coldCount int64
				warmErr := warmTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM later`).Scan(&warmCount)
				coldErr := coldTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM later`).Scan(&coldCount)
				if code(warmErr) != code(coldErr) || warmCount != coldCount {
					t.Errorf("same snapshot depends on pool warmth: creator=%d/%v cold=%d/%v", warmCount, warmErr, coldCount, coldErr)
				}
				if iso == qenv.ReadCommitted && (warmErr != nil || warmCount != 1) {
					t.Errorf("read-committed schema/data: count=%d error=%v; want 1", warmCount, warmErr)
				}
			})
			w.run(t)
		})
	}
}
