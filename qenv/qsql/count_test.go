package qsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// COUNT(*) counts rows, including rows with NULL values, and returns one
// int64 value even when its predicate matches no rows.
func TestCountRows(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	w.exec(t, `CREATE TABLE items (tenant TEXT NOT NULL, pos BIGINT NOT NULL, kind TEXT, PRIMARY KEY (tenant, pos))`)
	checkCountRows(t, w.db, `SELECT COUNT(*) FROM items`, nil, "count", 0)
	w.exec(t, `INSERT INTO items VALUES ('a', 1, NULL), ('a', 2, 'ready'), ('a', 3, 'ready'), ('b', 1, 'ready')`)
	for _, tc := range []struct {
		query string
		args  []any
		col   string
		want  int64
	}{
		{`SELECT COUNT(*) FROM items`, nil, "count", 4},
		{`SELECT count(*) AS total FROM items WHERE tenant = ?`, []any{"a"}, "total", 3},
		{`SELECT COUNT(*) AS "ReadyCount" FROM items WHERE kind = $1`, []any{"ready"}, "ReadyCount", 3},
		{`SELECT COUNT(*) FROM items WHERE tenant = ? AND pos BETWEEN ? AND ?`, []any{"a", int64(2), int64(3)}, "count", 2},
		{`SELECT COUNT(*) FROM items WHERE tenant = 'a' AND pos > 2 AND pos < 2`, nil, "count", 0},
		{`SELECT COUNT(*) FROM items WHERE tenant = 'missing'`, nil, "count", 0},
		{`SELECT COUNT(*) FROM items WHERE tenant = 'a' AND pos = 1`, nil, "count", 1},
		{`SELECT COUNT(*) FROM items WHERE kind = NULL`, nil, "count", 0},
	} {
		checkCountRows(t, w.db, tc.query, tc.args, tc.col, tc.want)
	}
	stmt, err := w.db.Prepare(`SELECT COUNT(*) AS n FROM items WHERE tenant = $1 AND pos >= $2`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		tenant string
		from   int64
		want   int64
	}{{"a", 2, 2}, {"a", 4, 0}, {"b", 1, 1}} {
		var got int64
		if err := stmt.QueryRow(tc.tenant, tc.from).Scan(&got); err != nil || got != tc.want {
			t.Errorf("prepared count %s/%d: %d, %v; want %d", tc.tenant, tc.from, got, err, tc.want)
		}
	}
	// Recognizing the aggregate must not take away ordinary columns named
	// count, including quoted names that preserve their spelling.
	w.exec(t, `CREATE TABLE named_counts (id TEXT PRIMARY KEY, count BIGINT NOT NULL, "COUNT" BIGINT NOT NULL)`)
	w.exec(t, `INSERT INTO named_counts VALUES ('a', 7, 9)`)
	var lower, upper int64
	if err := w.db.QueryRow(`SELECT count, "COUNT" FROM named_counts WHERE id = 'a'`).Scan(&lower, &upper); err != nil || lower != 7 || upper != 9 {
		t.Errorf("ordinary count columns: %d/%d, %v; want 7/9", lower, upper, err)
	}
}

type countQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func checkCountRows(t *testing.T, db countQuerier, query string, args []any, col string, want int64) {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !slices.Equal(cols, []string{col}) {
		t.Fatalf("%s: columns %q, %v; want [%s]", query, cols, err, col)
	}
	if !rows.Next() {
		t.Fatalf("%s: no aggregate row: %v", query, rows.Err())
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	got, ok := value.(int64)
	if !ok || got != want {
		t.Errorf("%s: count %#v (%T); want int64(%d)", query, value, value, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Errorf("%s: aggregate has extra rows or an error: %v", query, rows.Err())
	}
}

func TestCountOwnWritesAndRollback(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		t.Run(iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, iso)
			tx, err := w.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`INSERT INTO accounts VALUES ('charlie', 0)`); err != nil {
				t.Fatal(err)
			}
			checkCountRows(t, tx, `SELECT COUNT(*) FROM accounts`, nil, "count", 3)
			checkCountRows(t, tx, `SELECT COUNT(*) FROM accounts WHERE balance = 0`, nil, "count", 1)
			checkCountRows(t, w.db, `SELECT COUNT(*) FROM accounts`, nil, "count", 2)
			if _, err := tx.Exec(`UPDATE accounts SET balance = 0 WHERE id = 'alice'`); err != nil {
				t.Fatal(err)
			}
			checkCountRows(t, tx, `SELECT COUNT(*) FROM accounts WHERE balance = 0`, nil, "count", 2)
			if _, err := tx.Exec(`DELETE FROM accounts WHERE id = 'bob'`); err != nil {
				t.Fatal(err)
			}
			checkCountRows(t, tx, `SELECT COUNT(*) FROM accounts`, nil, "count", 2)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			checkCountRows(t, w.db, `SELECT COUNT(*) FROM accounts`, nil, "count", 2)
			checkCountRows(t, w.db, `SELECT COUNT(*) FROM accounts WHERE balance = 0`, nil, "count", 0)
		})
	}
}

// Force the first count before a concurrent insert and the second after
// its commit. A bounded predicate records its actual key range: a phantom
// inside it aborts Serializable, while neighboring keys do not. The other
// profiles keep their documented visibility instead of inheriting locks.
func TestCountPhantom(t *testing.T) {
	for _, tc := range []struct {
		iso    qenv.Isolation
		tenant string
		pos    int64
		second int64
		commit string
	}{
		{qenv.Serializable, "a", 7, 0, "40001"},
		{qenv.Serializable, "a", 6, 0, "40001"},
		{qenv.Serializable, "a", 5, 0, ""},
		{qenv.Serializable, "a", 10, 0, ""},
		{qenv.Serializable, "b", 7, 0, ""},
		{qenv.SnapshotIsolation, "a", 7, 0, ""},
		{qenv.ReadCommitted, "a", 7, 1, ""},
	} {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.iso, tc.tenant, tc.pos), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			w.exec(t, `CREATE TABLE items (tenant TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, pos))`)
			w.exec(t, `CREATE TABLE observations (id TEXT PRIMARY KEY, n BIGINT NOT NULL)`)
			read, inserted := false, false
			var counts [2]int64
			var scannerErr, insertErr error
			w.s.Spawn("scanner", func(ctx context.Context) {
				defer func() { read = true }()
				scannerErr = func() error {
					tx, err := w.db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					query := `SELECT COUNT(*) FROM items WHERE tenant = 'a' AND pos > 5 AND pos < 10`
					if err := tx.QueryRowContext(ctx, query).Scan(&counts[0]); err != nil {
						return err
					}
					read = true
					qenv.Wait(ctx, func() bool { return inserted })
					if err := tx.QueryRowContext(ctx, query).Scan(&counts[1]); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO observations VALUES ('count', ?)`, counts[1]); err != nil {
						return err
					}
					return tx.Commit()
				}()
			})
			w.s.Spawn("inserter", func(ctx context.Context) {
				defer func() { inserted = true }()
				qenv.Wait(ctx, func() bool { return read })
				_, insertErr = w.db.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, tc.tenant, tc.pos)
			})
			w.run(t)
			if insertErr != nil || counts != [2]int64{0, tc.second} || code(scannerErr) != tc.commit {
				t.Fatalf("counts %v, scanner %v, inserter %v; want [0 %d], scanner %q", counts, scannerErr, insertErr, tc.second, tc.commit)
			}
			want := int64(1)
			if tc.commit != "" {
				want = 0
			}
			checkCountRows(t, w.db, `SELECT COUNT(*) FROM observations`, nil, "count", want)
		})
	}
}

// Deliberately bounded aggregate syntax must reject neighboring SQL rather
// than silently applying row LIMIT/locking or SQLite's bare-column rules.
func TestCountUnsupported(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	for _, query := range []string{
		`SELECT COUNT(id) FROM accounts`,
		`SELECT COUNT(DISTINCT id) FROM accounts`,
		`SELECT COUNT() FROM accounts`,
		`SELECT DISTINCT COUNT(*) FROM accounts`,
		`SELECT COUNT(*), id FROM accounts`,
		`SELECT id, COUNT(*) FROM accounts`,
		`SELECT * COUNT(*) FROM accounts`,
		`SELECT COUNT(*) FROM accounts GROUP BY balance`,
		`SELECT COUNT(*) FROM accounts HAVING COUNT(*) > 1`,
		`SELECT COUNT(*) FROM accounts ORDER BY id`,
		`SELECT COUNT(*) FROM accounts LIMIT 1`,
		`SELECT COUNT(*) FROM accounts FOR UPDATE`,
		`SELECT COUNT(*) FROM accounts JOIN other ON other.id = accounts.id`,
	} {
		rows, err := w.db.Query(query)
		if rows != nil {
			rows.Close()
		}
		if code(err) != "42601" || !strings.Contains(err.Error(), "qsql") {
			t.Errorf("%s: got %v; want explicit qsql subset error 42601", query, err)
		}
	}
}

func TestCountValidation(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	w.exec(t, `CREATE TABLE empty_items (id TEXT PRIMARY KEY, kind TEXT)`)
	for _, tc := range []struct{ query, state string }{
		{`SELECT COUNT(*) FROM missing_table`, "42P01"},
		{`SELECT COUNT(*) FROM empty_items WHERE missing_column = 'a'`, "42703"},
		{`SELECT COUNT(*) FROM empty_items WHERE kind > 'a'`, "42601"},
	} {
		rows, err := w.db.Query(tc.query)
		if rows != nil {
			rows.Close()
		}
		if code(err) != tc.state {
			t.Errorf("%s: got %v; want SQLSTATE %s even on empty input", tc.query, err, tc.state)
		}
	}
}
