package qsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// eventStore creates grist's sqlstore tables (docs/grist.md §2.10) in w:
// keys of two and three columns.
func eventStore(t *testing.T, w *world) {
	t.Helper()
	w.exec(t, `CREATE TABLE grist_heads (tenant TEXT PRIMARY KEY, head BIGINT NOT NULL)`)
	w.exec(t, `CREATE TABLE grist_events (tenant TEXT NOT NULL, pos BIGINT NOT NULL, type TEXT NOT NULL, body BLOB, PRIMARY KEY (tenant, pos))`)
	w.exec(t, `CREATE TABLE grist_index (tenant TEXT NOT NULL, k TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, k, pos))`)
	w.exec(t, `CREATE TABLE grist_checkpoints (tenant TEXT NOT NULL, processor TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, processor))`)
	w.exec(t, `CREATE TABLE grist_markers (tenant TEXT NOT NULL, id TEXT NOT NULL, outcome TEXT, PRIMARY KEY (tenant, id))`)
}

// appendEvents is sqlstore's append: the head lock and the new head in
// one statement (UPDATE ... RETURNING), the condition (a range of the
// index after the position read), the events and their index rows.
func appendEvents(ctx context.Context, db *sql.DB, tenant string, after int64, types ...string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var head int64
	if err := tx.QueryRowContext(ctx, `UPDATE grist_heads SET head = head + $1 WHERE tenant = $2 RETURNING head`, len(types), tenant).Scan(&head); err != nil {
		return 0, err
	}
	var hit int64
	switch err := tx.QueryRowContext(ctx, `SELECT pos FROM grist_index WHERE tenant = $1 AND k = $2 AND pos > $3 ORDER BY pos LIMIT 1`, tenant, types[0], after).Scan(&hit); err {
	case nil:
		return 0, fmt.Errorf("condition failed: %s at %d", types[0], hit)
	case sql.ErrNoRows:
	default:
		return 0, err
	}
	for i, ty := range types {
		pos := head - int64(len(types)) + int64(i) + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_events (tenant, pos, type) VALUES ($1, $2, $3)`, tenant, pos, ty); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_index (tenant, k, pos) VALUES ($1, $2, $3)`, tenant, ty, pos); err != nil {
			return 0, err
		}
	}
	return head, tx.Commit()
}

// sqlstore's statements run on keys of several columns: the head lock
// returns the new head, the condition is a range of the index, a read is
// a range of a tenant's events (BETWEEN, DESC, LIMIT), and other tenants'
// rows, under the same tables, are in none of them.
func TestEventStoreStatements(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	eventStore(t, w)
	w.exec(t, `INSERT INTO grist_heads (tenant, head) VALUES ('a', 0), ('b', 0) ON CONFLICT (tenant) DO NOTHING`)
	w.s.Spawn("app", func(ctx context.Context) {
		for i, step := range []struct {
			tenant string
			after  int64
			types  []string
			head   int64
			fails  bool
		}{
			{"a", 0, []string{"created", "renamed"}, 2, false},
			{"b", 0, []string{"created"}, 1, false},
			{"a", 2, []string{"created"}, 3, false},
			{"a", 2, []string{"created"}, 0, true}, // created at 3, after 2
			{"a", 3, []string{"created", "created"}, 5, false},
		} {
			head, err := appendEvents(ctx, w.db, step.tenant, step.after, step.types...)
			if (err != nil) != step.fails || !step.fails && head != step.head {
				t.Errorf("append %d: head %d, error %v; want head %d, failing %v", i, head, err, step.head, step.fails)
			}
		}
		rows, err := w.db.QueryContext(ctx, `SELECT pos, type FROM grist_events WHERE tenant = ? AND pos BETWEEN ? AND ? ORDER BY tenant DESC, pos DESC LIMIT ?`, "a", 2, 5, 3)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var pos int64
			var ty string
			if err := rows.Scan(&pos, &ty); err != nil {
				t.Fatal(err)
			}
			got = append(got, fmt.Sprint(pos, " ", ty))
		}
		rows.Close()
		if want := []string{"5 created", "4 created", "3 created"}; !slices.Equal(got, want) {
			t.Errorf("a's events 2..5, newest first, 3 of them: %q, want %q", got, want)
		}
	})
	w.run(t)
	if got := qsql.Table(w.st, "grist_events"); len(got) != 6 || got[0]["tenant"] != "a" || got[5]["tenant"] != "b" {
		t.Errorf("the events, in key order: %v", got)
	}
}

// Two appends to one tenant at once: the second's head lock waits for the
// first's commit and returns the head after it, at read committed; under
// serializable it gets 40001 (the row changed after its snapshot).
func TestHeadLock(t *testing.T) {
	for _, tc := range []struct {
		iso   qenv.Isolation
		heads [2]int64
		errs  [2]string
	}{
		{qenv.ReadCommitted, [2]int64{1, 2}, [2]string{"", ""}},
		{qenv.Serializable, [2]int64{1, 0}, [2]string{"", "40001"}},
	} {
		t.Run(tc.iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			eventStore(t, w)
			w.exec(t, `INSERT INTO grist_heads VALUES ('a', 0)`)
			var heads [2]int64
			var errs [2]string
			for i := range 2 {
				w.s.Spawn(fmt.Sprint("node", i), func(ctx context.Context) {
					tx, err := w.db.BeginTx(ctx, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer tx.Rollback()
					if i == 1 {
						qenv.Sleep(ctx, time.Millisecond) // its snapshot before the first's commit, its lock after the first's
					}
					err = tx.QueryRowContext(ctx, `UPDATE grist_heads SET head = head + ? WHERE tenant = ? RETURNING head`, 1, "a").Scan(&heads[i])
					if err == nil {
						if i == 0 {
							qenv.Sleep(ctx, time.Second)
						}
						err = tx.Commit()
					}
					errs[i] = code(err)
				})
			}
			w.run(t)
			if heads != tc.heads || errs != tc.errs {
				t.Fatalf("heads %v, errors %q; want %v, %q", heads, errs, tc.heads, tc.errs)
			}
		})
	}
}

// A phantom in a range: a scanner reads a tenant's events after 5 (none)
// and appends to its log; meanwhile another transaction commits event 7
// of that tenant. Serializable aborts the scanner (40001: its range read
// is a predicate read); an event outside the range (3, or another
// tenant's 7) does not abort it. Snapshot isolation commits both. The
// Store's history checker judges every commit (newWorld sets Check).
func TestRangePhantom(t *testing.T) {
	for _, tc := range []struct {
		iso      qenv.Isolation
		tenant   string
		pos      int64
		scanErr  string
		comments string
	}{
		{qenv.Serializable, "a", 7, "40001", "in the range"},
		{qenv.Serializable, "a", 6, "40001", "in the range, at its start"},
		{qenv.Serializable, "a", 3, "", "below the range"},
		{qenv.Serializable, "a", 10, "", "above the range"},
		{qenv.Serializable, "b", 7, "", "another tenant's"},
		{qenv.SnapshotIsolation, "a", 7, "", "snapshot isolation allows it"},
	} {
		t.Run(fmt.Sprint(tc.iso, "/", tc.tenant, tc.pos), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			eventStore(t, w)
			w.exec(t, `CREATE TABLE log (n INTEGER PRIMARY KEY, seen BIGINT NOT NULL)`)
			w.exec(t, `INSERT INTO grist_events (tenant, pos, type) VALUES ('a', 1, 'x'), ('a', 5, 'x'), ('b', 1, 'x')`)
			var scanErr string
			w.s.Spawn("scanner", func(ctx context.Context) {
				scanErr = code(func() error {
					tx, err := w.db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					rows, err := tx.QueryContext(ctx, `SELECT pos FROM grist_events WHERE tenant = ? AND pos > ? AND pos < ?`, "a", 5, 10)
					if err != nil {
						return err
					}
					n := 0
					for rows.Next() {
						n++
					}
					rows.Close()
					qenv.Sleep(ctx, time.Second)
					if _, err := tx.ExecContext(ctx, `INSERT INTO log VALUES (1, ?)`, n); err != nil {
						return err
					}
					return tx.Commit()
				}())
			})
			w.s.Spawn("appender", func(ctx context.Context) {
				qenv.Sleep(ctx, time.Millisecond)
				if _, err := w.db.ExecContext(ctx, `INSERT INTO grist_events (tenant, pos, type) VALUES (?, ?, 'y')`, tc.tenant, tc.pos); err != nil {
					t.Error(err)
				}
			})
			w.run(t)
			if scanErr != tc.scanErr {
				t.Fatalf("%s: the scanner's commit: %q, want %q", tc.comments, scanErr, tc.scanErr)
			}
		})
	}
}

// Upserts: DO UPDATE SET from excluded, from a value, by arithmetic,
// with a WHERE on the existing row (a compare-and-set), RETURNING what
// it wrote; DO NOTHING returns nothing for a conflict; one statement
// cannot update a row twice (21000).
func TestUpsert(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	eventStore(t, w)
	w.exec(t, `CREATE TABLE counts (k TEXT PRIMARY KEY, n BIGINT NOT NULL, email TEXT UNIQUE)`)
	q := func(query string, args ...any) []string {
		t.Helper()
		rows, err := w.db.Query(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(vals...))
		}
		return out
	}
	cp := `INSERT INTO grist_checkpoints (tenant, processor, pos) VALUES (?, ?, ?) ON CONFLICT (processor, tenant) DO UPDATE SET pos = excluded.pos WHERE grist_checkpoints.pos = ? RETURNING pos`
	for i, c := range []struct {
		pos, expect int64
		want        []string
	}{
		{3, 0, []string{"3"}}, // inserted
		{5, 3, []string{"5"}}, // updated: it was 3
		{9, 3, nil},           // not updated: it is 5
		{9, 5, []string{"9"}},
	} {
		if got := q(cp, "a", "welcome", c.pos, c.expect); !slices.Equal(got, c.want) {
			t.Errorf("checkpoint %d: returned %q, want %q", i, got, c.want)
		}
	}
	if got := q(`INSERT INTO counts VALUES ('x', 1, NULL), ('y', 2, 'y@') ON CONFLICT (k) DO UPDATE SET n = counts.n + excluded.n RETURNING k, n`); !slices.Equal(got, []string{"x1", "y2"}) {
		t.Errorf("inserted: %q", got)
	}
	if got := q(`INSERT INTO counts VALUES ('x', 5, NULL), ('z', 1, NULL) ON CONFLICT (k) DO UPDATE SET n = n + excluded.n RETURNING *`); !slices.Equal(got, []string{"x6 <nil>", "z1 <nil>"}) {
		t.Errorf("x upserted, z inserted: %q", got)
	}
	if got := q(`INSERT INTO counts VALUES ('w', 1, 'y@') ON CONFLICT (email) DO UPDATE SET n = 100 RETURNING k, n`); !slices.Equal(got, []string{"y100"}) {
		t.Errorf("an upsert on the UNIQUE column updates y's row: %q", got)
	}
	if got := q(`INSERT INTO counts VALUES ('x', 1, NULL), ('q', 1, NULL) ON CONFLICT DO NOTHING RETURNING k`); !slices.Equal(got, []string{"q"}) {
		t.Errorf("DO NOTHING returns the inserted rows only: %q", got)
	}
	for _, tc := range []struct{ q, code, says string }{
		{`INSERT INTO counts VALUES ('m', 1, NULL), ('m', 2, NULL) ON CONFLICT (k) DO UPDATE SET n = excluded.n`, "21000", "cannot affect row a second time"},
		{`INSERT INTO counts VALUES ('x', 1, 'y@') ON CONFLICT (k) DO UPDATE SET email = excluded.email`, "23505", `Key (email)=(y@) already exists`},
		{`INSERT INTO counts VALUES ('x', 1, NULL) ON CONFLICT (n) DO UPDATE SET n = 1`, "42P10", "name the PRIMARY KEY's columns (k) or one UNIQUE column"},
		{`INSERT INTO grist_checkpoints VALUES ('a', 'p', 1) ON CONFLICT (tenant) DO NOTHING`, "42P10", "name the PRIMARY KEY's columns (tenant, processor)"},
		{`INSERT INTO counts VALUES ('x', 1, NULL) ON CONFLICT (k) DO UPDATE SET k = excluded.k`, "0A000", "primary key k"},
		{`INSERT INTO counts VALUES ('x', 1, NULL) ON CONFLICT (k) DO UPDATE SET n = excluded.email`, "42804", "excluded.email is TEXT"},
		{`INSERT INTO grist_checkpoints VALUES ('a', 'welcome', 1)`, "23505", `"grist_checkpoints_pkey": Key (tenant, processor)=(a, welcome) already exists`},
		{`UPDATE grist_checkpoints SET tenant = 'b'`, "0A000", "primary key tenant"},
		{`UPDATE counts SET n = 1 RETURNING nope`, "42703", `column "nope" of relation "counts"`},
	} {
		if _, err := w.db.Exec(tc.q); code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: got %v, want %s saying %q", tc.q, err, tc.code, tc.says)
		}
	}
	if got := q(`SELECT n FROM counts WHERE k = 'm'`); got != nil {
		t.Errorf("the failed statement wrote %q", got)
	}
	if got := q(`DELETE FROM grist_checkpoints WHERE tenant = 'a' RETURNING processor, pos`); !slices.Equal(got, []string{"welcome9"}) {
		t.Errorf("DELETE RETURNING: %q", got)
	}
}

// Under snapshot isolation an upsert whose conflicting row was committed
// after its snapshot fails with 40001 (PostgreSQL's "could not serialize
// access"), as DO NOTHING's does; at read committed it updates the row as
// it is now.
func TestUpsertConcurrent(t *testing.T) {
	for _, tc := range []struct {
		iso  qenv.Isolation
		err  string
		want int64
	}{
		{qenv.ReadCommitted, "", 3},
		{qenv.SnapshotIsolation, "40001", 1},
	} {
		t.Run(tc.iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			w.exec(t, `CREATE TABLE counts (k TEXT PRIMARY KEY, n BIGINT NOT NULL)`)
			var err string
			w.s.Spawn("late", func(ctx context.Context) {
				err = code(func() error {
					tx, err := w.db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					if _, err := tx.ExecContext(ctx, `SELECT n FROM counts WHERE k = 'other'`); err != nil { // the snapshot
						return err
					}
					qenv.Sleep(ctx, time.Second)
					if _, err := tx.ExecContext(ctx, `INSERT INTO counts VALUES ('x', 2) ON CONFLICT (k) DO UPDATE SET n = counts.n + excluded.n`); err != nil {
						return err
					}
					return tx.Commit()
				}())
			})
			w.s.Spawn("early", func(ctx context.Context) {
				if _, err := w.db.ExecContext(ctx, `INSERT INTO counts VALUES ('x', 1)`); err != nil {
					t.Error(err)
				}
			})
			w.run(t)
			if got := qsql.Table(w.st, "counts"); err != tc.err || len(got) != 1 || got[0]["n"] != tc.want {
				t.Fatalf("the late upsert: %q, the table %v; want %q, n = %d", err, got, tc.err, tc.want)
			}
		})
	}
}

// Ranges and ORDER BY outside the subset fail naming it: a range on a
// column that is not in the key, or on a key column whose earlier columns
// are not fixed; ORDER BY a non-key column, out of key order, skipping an
// unfixed key column, or in two directions.
func TestRangeRefusals(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	eventStore(t, w)
	for _, tc := range []struct{ q, says string }{
		{`SELECT * FROM grist_events WHERE type > 'a'`, `type is not in the PRIMARY KEY (tenant, pos) of grist_events (a range is on a key column after those fixed by equality: WHERE tenant = ? AND pos > ?)`},
		{`SELECT * FROM grist_events WHERE pos > 1`, `the key columns before pos in PRIMARY KEY (tenant, pos) are not all fixed by equality`},
		{`SELECT * FROM grist_index WHERE tenant = 'a' AND pos > 1`, `the key columns before pos in PRIMARY KEY (tenant, k, pos)`},
		{`DELETE FROM grist_events WHERE pos <= 3`, `not all fixed by equality`},
		{`UPDATE grist_events SET type = 'x' WHERE body > 'a'`, `body is not in the PRIMARY KEY`},
		{`SELECT * FROM grist_events ORDER BY type`, `ORDER BY type: rows come in primary-key order only (ORDER BY tenant, pos [DESC]); type is not a key column`},
		{`SELECT * FROM grist_events WHERE tenant = 'a' ORDER BY pos, tenant`, `the key's columns go in key order`},
		{`SELECT * FROM grist_events ORDER BY pos`, `skips the key column tenant, which the WHERE clause does not fix by equality`},
		{`SELECT * FROM grist_events WHERE tenant > 'a' ORDER BY pos`, `skips the key column tenant`},
		{`SELECT * FROM grist_events ORDER BY tenant, pos DESC`, `pos's direction differs from tenant's`},
	} {
		_, err := w.db.Exec(tc.q)
		if code(err) != "42601" || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "qsql runs one table at a time") {
			t.Errorf("%s: got %v, want 42601 saying %q and the subset", tc.q, err, tc.says)
		}
	}
	for _, q := range []string{ // and their neighbours that are in it
		`SELECT * FROM grist_events WHERE tenant > 'a'`,
		`SELECT * FROM grist_events WHERE tenant = 'a' ORDER BY pos DESC`,
		`SELECT * FROM grist_events WHERE tenant = 'a' AND type = 'x' AND pos >= 2 ORDER BY tenant, pos`,
		`SELECT * FROM grist_index WHERE tenant = 'a' AND k > 'x' ORDER BY k DESC, pos DESC`,
		`SELECT * FROM grist_events WHERE tenant = 'a' AND tenant < 'b' AND pos < 3`,
	} {
		if _, err := w.db.Exec(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}
