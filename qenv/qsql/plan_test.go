package qsql

import (
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A table scan comes out in primary-key order because the keys' encodings
// sort as the values do: checked against sorting the values themselves.
func TestKeyEncodingSortsAsValuesAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	ints := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64}
	for range 200 {
		ints = append(ints, r.Int64()-r.Int64(), int64(r.IntN(100)-50))
	}
	var times []time.Time
	for _, n := range ints {
		times = append(times, time.UnixMicro(n/1000))
	}
	var blobs [][]byte
	for range 200 {
		b := make([]byte, r.IntN(4))
		for i := range b {
			b[i] = byte(r.IntN(3)) * 127
		}
		blobs = append(blobs, b)
	}
	check := func(name string, n int, less func(i, j int) bool, key func(i int) string) {
		for i := range n {
			for j := range n {
				if less(i, j) != (key(i) < key(j)) {
					t.Fatalf("%s: values %d and %d order %v, their keys %q and %q the other way", name, i, j, less(i, j), key(i), key(j))
				}
			}
		}
	}
	check("integer", len(ints), func(i, j int) bool { return ints[i] < ints[j] }, func(i int) string { return encodeKey(ints[i]) })
	check("timestamp", len(times), func(i, j int) bool { return times[i].Before(times[j]) }, func(i int) string { return encodeKey(times[i]) })
	check("blob", len(blobs), func(i, j int) bool { return bytes.Compare(blobs[i], blobs[j]) < 0 }, func(i int) string { return encodeKey(blobs[i]) })
	check("boolean", 2, func(i, j int) bool { return i < j }, func(i int) string { return encodeKey(i == 1) })
}

// The plan (a Get by primary key, a Get through a unique index, else a
// scan) returns what a scan of every row filtered by the whole WHERE
// clause returns, over random tables and queries: the brute force is the
// same Store read through a connector that only scans.
func TestPlanAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for round := range 20 {
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", qenv.Serializable)
		db, _ := open(s, st, Options{Name: "plan"})
		brute, bc := open(s, st, Options{Name: "brute"})
		bc.scanOnly = true
		mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY, u TEXT UNIQUE, a INTEGER, b BOOLEAN)`)
		for range 1 + r.IntN(12) {
			var u any
			if r.IntN(3) > 0 {
				u = fmt.Sprint("u", r.IntN(8))
			}
			_, _ = db.Exec(`INSERT INTO t VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, r.IntN(10), u, r.IntN(3), r.IntN(2) == 1)
		}
		if round%3 == 0 { // some rows changed and deleted, so the index has been maintained
			_, _ = db.Exec(`UPDATE t SET u = ? WHERE a = ?`, fmt.Sprint("u", r.IntN(8)), r.IntN(3))
			_, _ = db.Exec(`DELETE FROM t WHERE b = ?`, r.IntN(2) == 1)
		}
		for range 100 {
			var where []string
			var args []any
			for _, c := range r.Perm(4)[:r.IntN(4)] {
				where = append(where, []string{"id", "u", "a", "b"}[c]+" = ?")
				args = append(args, []any{int64(r.IntN(10)), fmt.Sprint("u", r.IntN(8)), int64(r.IntN(3)), r.IntN(2) == 1}[c])
			}
			q := "SELECT id, u, a, b FROM t"
			if len(where) > 0 {
				q += " WHERE " + strings.Join(where, " AND ")
			}
			q += []string{"", " ORDER BY id DESC", " ORDER BY id"}[r.IntN(3)]
			if r.IntN(2) == 0 {
				q += fmt.Sprintf(" LIMIT %d", r.IntN(4))
			}
			if r.IntN(2) == 0 {
				q += " FOR UPDATE"
			}
			got, want := queryAll(t, db, q, args), queryAll(t, brute, q, args)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d: %s %v:\nplan  %v\nscan  %v", round, q, args, got, want)
			}
		}
		s.Close()
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func queryAll(t *testing.T, db *sql.DB, q string, args []any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, a int64
		var u sql.NullString
		var b bool
		if err := rows.Scan(&id, &u, &a, &b); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(id, u, a, b))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
