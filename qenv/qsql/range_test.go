package qsql

import (
	"cmp"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// texts are key values whose encodings are hard to keep in order: the
// empty string, NUL inside and at the end, one a prefix of another.
var texts = []string{"", "a", "a\x00", "a\x00b", "a\x01", "ab", "b"}

// Rows of a key of several columns come in tuple order, and a range on a
// key column (its leading columns fixed by equality) is a range of the
// Store's keys holding exactly the rows it selects: checked against the
// tuples themselves, compared in Go.
func TestCompositeKeyEncodingAgainstBruteForce(t *testing.T) {
	tb := &table{name: "t", cols: []colDef{{name: "a", typ: "text"}, {name: "b", typ: "integer"}}, key: []int{0, 1}}
	type tup struct {
		a string
		b int64
	}
	var all []tup
	for _, a := range texts {
		for b := int64(-2); b <= 2; b++ {
			all = append(all, tup{a, b})
		}
	}
	key := func(x tup) string { return tb.rowKey(map[string]any{"a": x.a, "b": x.b}) }
	less := func(x, y tup) bool { return x.a < y.a || x.a == y.a && x.b < y.b }
	for _, x := range all {
		for _, y := range all {
			if less(x, y) != (key(x) < key(y)) {
				t.Fatalf("%#v and %#v order %v, their keys %q and %q the other way", x, y, less(x, y), key(x), key(y))
			}
		}
	}
	ops := []string{"<", "<=", ">", ">="}
	holds := func(op string, c int) bool {
		return map[string]bool{"<": c < 0, "<=": c <= 0, ">": c > 0, ">=": c >= 0}[op]
	}
	for _, op := range ops {
		for _, v := range texts { // a range on the first column
			r := keyRange(tb, []resolved{{col: tb.cols[0], idx: 0, op: op, val: v}}, 0)
			for _, x := range all {
				if want := holds(op, strings.Compare(x.a, v)); r.Contains(key(x)) != want {
					t.Fatalf("a %s %q: the range %+v holds %#v: %v, want %v", op, v, r, x, !want, want)
				}
			}
		}
		for _, a := range texts { // a fixed, a range on b
			for v := int64(-3); v <= 3; v++ {
				cs := []resolved{{col: tb.cols[0], idx: 0, op: "=", val: a}, {col: tb.cols[1], idx: 1, op: op, val: v}}
				r := keyRange(tb, cs, 1)
				for _, x := range all {
					if want := x.a == a && holds(op, cmp.Compare(x.b, v)); r.Contains(key(x)) != want {
						t.Fatalf("a = %q AND b %s %d: the range %+v holds %#v: %v, want %v", a, op, v, r, x, !want, want)
					}
				}
			}
		}
	}
}

// A brute-force row of the table t below, and the conditions of a query.
type bfRow struct {
	a    string
	b, c int64
	v    *int64
}

type bfCond struct {
	col string
	op  string
	val any
}

func (r bfRow) get(col string) any {
	switch col {
	case "a":
		return r.a
	case "b":
		return r.b
	case "c":
		return r.c
	}
	if r.v == nil {
		return nil
	}
	return *r.v
}

func (r bfRow) holds(cs []bfCond) bool {
	for _, c := range cs {
		v := r.get(c.col)
		if v == nil {
			return false
		}
		var n int
		switch v := v.(type) {
		case string:
			n = strings.Compare(v, c.val.(string))
		case int64:
			n = cmp.Compare(v, c.val.(int64))
		}
		if !map[string]bool{"=": n == 0, "<": n < 0, "<=": n <= 0, ">": n > 0, ">=": n >= 0}[c.op] {
			return false
		}
	}
	return true
}

func (r bfRow) String() string {
	v := "NULL"
	if r.v != nil {
		v = fmt.Sprint(*r.v)
	}
	return fmt.Sprintf("%q %d %d %s", r.a, r.b, r.c, v)
}

// The plan of a WHERE clause over a key of several columns (a Get when
// the whole key is fixed, a range of the key when its leading columns
// are, a scan otherwise), ORDER BY the key either way, LIMIT and FOR
// UPDATE return what the brute force does: the table's rows filtered by
// every condition, sorted as tuples in Go, reversed, cut. The plan is
// also the scan connector's, and each range it scans holds exactly the
// rows its key conditions select (with the plan turned off, a scan of
// the table holds them all, and the test fails). UPDATE and DELETE with
// ranges and RETURNING change exactly the rows the brute force selects.
func TestRangePlanAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	keyCols := []string{"a", "b", "c"}
	val := func(col string) any {
		if col == "a" {
			return texts[r.IntN(len(texts))]
		}
		return int64(r.IntN(6) - 1)
	}
	ranged := 0
	for round := range 40 {
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", qenv.Serializable)
		db, pc := open(s, st, Options{Name: "plan"})
		brute, bc := open(s, st, Options{Name: "brute"})
		bc.scanOnly = true
		var scans []qenv.KeyRange
		pc.scanHook = func(r qenv.KeyRange) { scans = append(scans, r) }
		mustExec(t, db, `CREATE TABLE t (a TEXT NOT NULL, b INTEGER NOT NULL, c INTEGER NOT NULL, v INTEGER, PRIMARY KEY (a, b, c))`)
		for range r.IntN(30) {
			var v any
			if r.IntN(4) > 0 {
				v = int64(r.IntN(3))
			}
			mustExec(t, db, `INSERT INTO t VALUES (?, ?, ?, ?) ON CONFLICT (a, b, c) DO NOTHING`, val("a"), int64(r.IntN(4)), int64(r.IntN(4)), v)
		}
		for q := range 60 {
			rows := bfTable(st)
			fixed := r.IntN(4)
			var where []string
			var args []any
			var cs, keyCs []bfCond
			add := func(c bfCond, key bool) {
				where = append(where, c.col+" "+c.op+" ?")
				args = append(args, c.val)
				cs = append(cs, c)
				if key {
					keyCs = append(keyCs, c)
				}
			}
			for _, col := range keyCols[:fixed] {
				add(bfCond{col, "=", val(col)}, true)
			}
			if fixed < 3 {
				col := keyCols[fixed]
				for range r.IntN(3) {
					if r.IntN(4) == 0 { // BETWEEN, two conditions
						lo, hi := val(col), val(col)
						where = append(where, col+" BETWEEN ? AND ?")
						args = append(args, lo, hi)
						cs = append(cs, bfCond{col, ">=", lo}, bfCond{col, "<=", hi})
						keyCs = append(keyCs, bfCond{col, ">=", lo}, bfCond{col, "<=", hi})
						continue
					}
					add(bfCond{col, []string{"<", "<=", ">", ">="}[r.IntN(4)], val(col)}, true)
				}
			}
			if fixed > 0 && r.IntN(5) == 0 { // a range on a fixed column: a filter
				add(bfCond{keyCols[0], ">=", val(keyCols[0])}, false)
			}
			vFixed := r.IntN(4) == 0
			if vFixed {
				add(bfCond{"v", "=", int64(r.IntN(3))}, false)
			}
			if r.IntN(3) == 0 { // a DML statement: what it changes, against the brute force
				dmlRound(t, db, st, rows, where, args, cs, r.IntN(2) == 0)
				continue
			}
			query := "SELECT a, b, c, v FROM t"
			if len(where) > 0 {
				query += " WHERE " + strings.Join(where, " AND ")
			}
			desc := false
			if r.IntN(3) > 0 { // ORDER BY key columns from one at or before the first unfixed
				from := r.IntN(min(fixed, 2) + 1)
				to := from + r.IntN(3-from)
				d := r.IntN(2) == 0
				dir := func(desc bool) string {
					return map[bool]string{true: " DESC", false: []string{"", " ASC"}[r.IntN(2)]}[desc]
				}
				var terms []string
				for i, col := range keyCols[from : to+1] {
					if from+i < fixed { // fixed by equality: it orders nothing, in either direction
						terms = append(terms, col+dir(r.IntN(2) == 0))
						continue
					}
					terms = append(terms, col+dir(d))
					desc = d
				}
				if vFixed && r.IntN(2) == 0 { // a column fixed by equality, not the key's, anywhere
					at := r.IntN(len(terms) + 1)
					terms = slices.Insert(terms, at, "v"+dir(r.IntN(2) == 0))
				}
				query += " ORDER BY " + strings.Join(terms, ", ")
			}
			limit := -1
			switch r.IntN(4) {
			case 0, 1:
				limit = r.IntN(4)
				query += fmt.Sprintf(" LIMIT %d", limit)
			case 2:
				query += " LIMIT NULL" // no limit, as in Postgres
			}
			if r.IntN(3) == 0 {
				query += " FOR UPDATE"
			}
			var want []string
			for _, row := range rows {
				if row.holds(cs) {
					want = append(want, row.String())
				}
			}
			if desc {
				slices.Reverse(want)
			}
			if limit >= 0 && len(want) > limit {
				want = want[:limit]
			}
			scans = scans[:0]
			got, scanned := bfQuery(t, db, query, args), bfQuery(t, brute, query, args)
			if !slices.Equal(got, want) || !slices.Equal(scanned, want) {
				t.Fatalf("round %d, query %d: %s %q:\nplan        %q\nscan        %q\nbrute force %q", round, q, query, args, got, scanned, want)
			}
			if fixed == 3 {
				continue // a Get
			}
			if len(scans) != 1 {
				t.Fatalf("%s: the plan scanned %d ranges, want 1", query, len(scans))
			}
			in, want2 := 0, 0
			for _, k := range st.Keys("t/") {
				if scans[0].Contains(k) {
					in++
				}
			}
			for _, row := range rows {
				if row.holds(keyCs) {
					want2++
				}
			}
			if in != want2 {
				t.Fatalf("round %d, %s %q: the plan's scan %+v holds %d rows; its key conditions select %d", round, query, args, scans[0], in, want2)
			}
			if in < len(rows) {
				ranged++
			}
		}
		s.Close()
	}
	if ranged < 100 {
		t.Fatalf("only %d queries scanned less than their table: the test does not exercise ranges", ranged)
	}
}

// dmlRound runs an UPDATE (v = v + 10) or a DELETE with where and
// RETURNING, and checks the rows it returns and the table after it
// against the brute force's selection of rows.
func dmlRound(t *testing.T, db *sql.DB, st *qenv.Store, rows []bfRow, where []string, args []any, cs []bfCond, update bool) {
	t.Helper()
	q := "DELETE FROM t"
	if update {
		q = "UPDATE t SET v = v + 10"
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " RETURNING a, b, c, v"
	var wantRet, wantAfter []string
	for _, row := range rows {
		hit := row.holds(cs)
		if hit && update && row.v != nil {
			n := *row.v + 10
			row.v = &n
		}
		if hit {
			wantRet = append(wantRet, row.String())
		}
		if !hit || update {
			wantAfter = append(wantAfter, row.String())
		}
	}
	got := bfQuery(t, db, q, args)
	var after []string
	for _, row := range bfTable(st) {
		after = append(after, row.String())
	}
	if !slices.Equal(got, wantRet) || !slices.Equal(after, wantAfter) {
		t.Fatalf("%s %q:\nreturned %q, want %q\ntable    %q, want %q", q, args, got, wantRet, after, wantAfter)
	}
}

// bfTable is t's committed rows, sorted as tuples in Go (not by key).
func bfTable(st *qenv.Store) []bfRow {
	var out []bfRow
	for _, m := range Table(st, "t") {
		row := bfRow{a: m["a"].(string), b: m["b"].(int64), c: m["c"].(int64)}
		if v, ok := m["v"].(int64); ok {
			row.v = &v
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y bfRow) int {
		return cmp.Or(strings.Compare(x.a, y.a), cmp.Compare(x.b, y.b), cmp.Compare(x.c, y.c))
	})
	return out
}

func bfQuery(t *testing.T, db *sql.DB, q string, args []any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row bfRow
		var v sql.NullInt64
		if err := rows.Scan(&row.a, &row.b, &row.c, &v); err != nil {
			t.Fatal(err)
		}
		if v.Valid {
			row.v = &v.Int64
		}
		out = append(out, row.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A table keyed by one TEXT column keeps its keys' encoding (the text
// itself), and ranges on it select what the brute force does, the
// awkward texts included.
func TestSingleKeyRangeAgainstBruteForce(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.Serializable)
	db, _ := open(s, st, Options{Name: "plan"})
	mustExec(t, db, `CREATE TABLE w (name TEXT PRIMARY KEY, n INTEGER)`)
	for i, x := range texts {
		mustExec(t, db, `INSERT INTO w VALUES (?, ?)`, x, int64(i))
	}
	if got := st.Keys("w/"); got[1] != "w/a" {
		t.Fatalf("keys %q: a key of one TEXT column is the text", got)
	}
	for _, op := range []string{"<", "<=", ">", ">="} {
		for _, v := range texts {
			for _, desc := range []bool{false, true} {
				var want []string
				for _, x := range texts {
					c := strings.Compare(x, v)
					if map[string]bool{"<": c < 0, "<=": c <= 0, ">": c > 0, ">=": c >= 0}[op] {
						want = append(want, x)
					}
				}
				q := "SELECT name FROM w WHERE name " + op + " ? ORDER BY name"
				if desc {
					slices.Reverse(want)
					q += " DESC"
				}
				rows, err := db.Query(q, v)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for rows.Next() {
					var x string
					if err := rows.Scan(&x); err != nil {
						t.Fatal(err)
					}
					got = append(got, x)
				}
				rows.Close()
				if !slices.Equal(got, want) {
					t.Fatalf("%s %q: %q, want %q", q, v, got, want)
				}
			}
		}
	}
}
