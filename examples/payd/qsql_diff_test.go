package harness

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// qsql's keys of several columns, ranges, ORDER BY with LIMIT, upserts
// and RETURNING (what grist's sqlstore issues, docs/grist.md §2.10)
// against SQLite: random statements over small tables run on both, and
// every result (rows, rows affected, whether it failed) agrees. Rows
// SQL leaves unordered (no ORDER BY, RETURNING) are compared as sets;
// ORDER BY names the key's columns after those fixed, so the order is
// total. Texts carry no NUL (SQLite's text functions stop at one); qsql's
// own brute-force tests cover it.
func TestQsqlAgainstSQLite(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	texts := []string{"", "a", "a\x01", "a b", "ab", "b", "B"}
	cols := []string{"a", "b", "c"}
	val := func(col string) any {
		if col == "a" {
			return texts[r.IntN(len(texts))]
		}
		return int64(r.IntN(5) - 1)
	}
	ranged := 0
	for round := range 30 {
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", qenv.Serializable)
		q := qsql.Open(s, st)
		lite, err := sql.Open("sqlite", fmt.Sprintf("file:diff%d?mode=memory", round))
		if err != nil {
			t.Fatal(err)
		}
		lite.SetMaxOpenConns(1) // one connection, one in-memory database
		for _, db := range []*sql.DB{q, lite} {
			for _, ddl := range []string{
				`CREATE TABLE t (a TEXT NOT NULL, b INTEGER NOT NULL, c INTEGER NOT NULL, v INTEGER, PRIMARY KEY (a, b, c))`,
				`CREATE TABLE h (tenant TEXT PRIMARY KEY, head INTEGER NOT NULL)`,
			} {
				if _, err := db.Exec(ddl); err != nil {
					t.Fatalf("%s: %v", ddl, err)
				}
			}
		}
		both := func(ordered bool, stmt string, args ...any) {
			t.Helper()
			got, gerr := diffRun(q, stmt, args)
			want, werr := diffRun(lite, stmt, args)
			if !ordered {
				slices.Sort(got)
				slices.Sort(want)
			}
			if (gerr != nil) != (werr != nil) || !slices.Equal(got, want) {
				t.Fatalf("round %d: %s %q:\nqsql   %q (%v)\nsqlite %q (%v)", round, stmt, args, got, gerr, want, werr)
			}
		}
		for range 120 {
			switch r.IntN(10) {
			case 0, 1, 2: // inserts, plain or DO NOTHING, one row or several, RETURNING
				n := 1 + r.IntN(3)
				var vals []string
				var args []any
				for range n {
					vals = append(vals, "(?, ?, ?, ?)")
					args = append(args, val("a"), int64(r.IntN(3)), int64(r.IntN(3)), nullable(r))
				}
				stmt := "INSERT INTO t (a, b, c, v) VALUES " + strings.Join(vals, ", ")
				stmt += []string{"", " ON CONFLICT DO NOTHING", " ON CONFLICT (a, b, c) DO NOTHING"}[r.IntN(3)]
				if r.IntN(2) == 0 {
					stmt += " RETURNING a, b, c, v"
				}
				both(false, stmt, args...)
			case 3: // an upsert of one row
				set := []string{"v = excluded.v", "v = t.v + excluded.v", "v = ?"}[r.IntN(3)]
				stmt := "INSERT INTO t (a, b, c, v) VALUES (?, ?, ?, ?) ON CONFLICT (a, b, c) DO UPDATE SET " + set
				args := []any{val("a"), int64(r.IntN(3)), int64(r.IntN(3)), nullable(r)}
				if strings.HasSuffix(set, "?") {
					args = append(args, int64(r.IntN(9)))
				}
				if r.IntN(2) == 0 {
					stmt += " WHERE t.v = ?"
					args = append(args, int64(r.IntN(3)))
				}
				if r.IntN(2) == 0 {
					stmt += " RETURNING a, b, c, v"
				}
				both(false, stmt, args...)
				// unqualified, SQLite takes the existing row's v; Postgres
				// refuses it (42702), and so does qsql
				if bare := strings.NewReplacer("t.v + ", "v + ", "WHERE t.v", "WHERE v").Replace(stmt); bare != stmt {
					var e *qsql.Error
					if _, err := q.Exec(bare, args...); !errors.As(err, &e) || e.Code != "42702" {
						t.Fatalf("round %d: %s: got %v, want 42702 (ambiguous, as Postgres says)", round, bare, err)
					}
				}
			case 4: // the head lock, on a tenant that exists or not
				tenant := texts[r.IntN(3)]
				both(false, "INSERT INTO h (tenant, head) VALUES (?, 0) ON CONFLICT (tenant) DO NOTHING", tenant)
				both(false, "UPDATE h SET head = head + ? WHERE tenant = ? RETURNING head", int64(1+r.IntN(3)), texts[r.IntN(4)])
			default: // a range query, an UPDATE or a DELETE over one
				fixed := r.IntN(4)
				var where []string
				var args []any
				for _, col := range cols[:fixed] {
					where = append(where, col+" = ?")
					args = append(args, val(col))
				}
				if fixed < 3 {
					col := cols[fixed]
					for range r.IntN(3) {
						if r.IntN(4) == 0 {
							where = append(where, col+" BETWEEN ? AND ?")
							args = append(args, val(col), val(col))
							continue
						}
						where = append(where, col+" "+[]string{"<", "<=", ">", ">="}[r.IntN(4)]+" ?")
						args = append(args, val(col))
					}
				}
				if r.IntN(4) == 0 {
					where = append(where, "v = ?")
					args = append(args, int64(r.IntN(3)))
				}
				clause := ""
				if len(where) > 0 {
					clause = " WHERE " + strings.Join(where, " AND ")
				}
				if strings.ContainsAny(clause, "<>") || strings.Contains(clause, "BETWEEN") {
					ranged++
				}
				switch r.IntN(6) {
				case 0:
					both(false, "UPDATE t SET v = v + ?"+clause+" RETURNING a, b, c, v", append([]any{int64(10)}, args...)...)
				case 1:
					both(false, "DELETE FROM t"+clause+" RETURNING a, b, c, v", args...)
				default:
					stmt := "SELECT a, b, c, v FROM t" + clause
					ordered := r.IntN(4) > 0
					if ordered { // every key column from one at or before the first unfixed: a total order
						dir := []string{"", " ASC", " DESC"}[r.IntN(3)]
						var terms []string
						for _, col := range cols[r.IntN(min(fixed, 2)+1):] {
							terms = append(terms, col+dir)
						}
						stmt += " ORDER BY " + strings.Join(terms, ", ")
						if r.IntN(2) == 0 {
							stmt += " LIMIT ?"
							args = append(args, int64(r.IntN(4)))
						}
					}
					both(ordered, stmt, args...)
				}
			}
		}
		lite.Close()
		s.Close()
	}
	if ranged < 300 {
		t.Fatalf("only %d statements had a range", ranged)
	}
}

func nullable(r *rand.Rand) any {
	if r.IntN(4) == 0 {
		return nil
	}
	return int64(r.IntN(3))
}

// diffRun runs a statement and returns its rows as strings, then the
// rows it affected (a statement without RETURNING returns none).
func diffRun(db *sql.DB, stmt string, args []any) ([]string, error) {
	if !strings.HasPrefix(stmt, "SELECT") && !strings.Contains(stmt, "RETURNING") {
		res, err := db.Exec(stmt, args...)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		return []string{fmt.Sprint("affected ", n)}, err
	}
	rows, err := db.Query(stmt, args...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, fmt.Sprintf("%q", vals))
	}
	if err := rows.Err(); err != nil {
		return nil, err // the rows SQLite returned before the error are not the statement's
	}
	return out, nil
}
