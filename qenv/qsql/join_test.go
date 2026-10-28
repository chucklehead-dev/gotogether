package qsql

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A join is the statement's table's rows, as its WHERE, ORDER BY and
// LIMIT select them, each with the joined table's row ON names (rows
// without one left out, LIMIT counting joined rows): checked against a
// nested loop over both tables' whole contents. An index of positions
// joined to the events at them, as grist's read is, with some positions'
// events missing and an ON of a non-key column that some rows fail.
func TestJoinAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	type ixRow struct {
		tenant, k string
		pos       int64
	}
	type evRow struct {
		tenant string
		pos    int64
		tag    sql.NullString
		body   string
	}
	for round := range 20 {
		s := qenv.New(qenv.Nominal{})
		db, _ := open(s, qenv.NewStore(s, "db", qenv.Serializable), Options{})
		mustExec(t, db, `CREATE TABLE ix (tenant TEXT NOT NULL, k TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, k, pos))`)
		mustExec(t, db, `CREATE TABLE ev (tenant TEXT NOT NULL, pos BIGINT NOT NULL, tag TEXT, body TEXT NOT NULL, PRIMARY KEY (tenant, pos))`)
		var ix []ixRow
		var ev []evRow
		for range 1 + r.IntN(30) {
			row := ixRow{fmt.Sprint("t", r.IntN(2)), fmt.Sprint("k", r.IntN(3)), int64(r.IntN(12))}
			if _, err := db.Exec(`INSERT INTO ix VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, row.tenant, row.k, row.pos); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(ix, row) {
				ix = append(ix, row)
			}
		}
		for pos := range int64(12) {
			for _, tenant := range []string{"t0", "t1"} {
				if r.IntN(4) == 0 {
					continue // a position with no event
				}
				row := evRow{tenant: tenant, pos: pos, body: fmt.Sprint("e", tenant, pos)}
				if r.IntN(3) > 0 {
					row.tag = sql.NullString{String: fmt.Sprint("k", r.IntN(3)), Valid: true}
				}
				mustExec(t, db, `INSERT INTO ev VALUES (?, ?, ?, ?)`, row.tenant, row.pos, row.tag, row.body)
				ev = append(ev, row)
			}
		}
		for range 100 {
			tenant, k := fmt.Sprint("t", r.IntN(2)), fmt.Sprint("k", r.IntN(3))
			lo, hi := int64(r.IntN(12)), int64(r.IntN(12))
			withTag, desc, limit := r.IntN(3) == 0, r.IntN(2) == 0, -1
			q := `SELECT ix.pos, ev.body, ev.tag FROM ix JOIN ev ON ev.tenant = ix.tenant AND ix.pos = ev.pos`
			if withTag {
				q += ` AND ev.tag = ix.k`
			}
			q += ` WHERE ix.tenant = $1 AND ix.k = $2 AND ix.pos > $3 AND ix.pos <= $4 ORDER BY ix.pos`
			if desc {
				q += ` DESC`
			}
			args := []any{tenant, k, lo, hi}
			if r.IntN(2) == 0 {
				limit = r.IntN(4)
				q += ` LIMIT $5`
				args = append(args, limit)
			}
			var want []string
			for _, i := range ix {
				if i.tenant != tenant || i.k != k || i.pos <= lo || i.pos > hi {
					continue
				}
				for _, e := range ev {
					if e.tenant == i.tenant && e.pos == i.pos && (!withTag || e.tag.Valid && e.tag.String == i.k) {
						want = append(want, fmt.Sprint(i.pos, e.body, e.tag))
					}
				}
			}
			slices.SortFunc(want, func(a, b string) int { // one tenant and k: by pos, a number
				var x, y int
				fmt.Sscan(a, &x)
				fmt.Sscan(b, &y)
				return x - y
			})
			if desc {
				slices.Reverse(want)
			}
			if limit >= 0 && len(want) > limit {
				want = want[:limit]
			}
			rows, err := db.Query(q, args...)
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			var got []string
			for rows.Next() {
				var pos int64
				var body string
				var tag sql.NullString
				if err := rows.Scan(&pos, &body, &tag); err != nil {
					t.Fatal(err)
				}
				got = append(got, fmt.Sprint(pos, body, tag))
			}
			rows.Close()
			if !slices.Equal(got, want) {
				t.Fatalf("round %d: %s %v:\njoin  %v\nloop  %v", round, q, args, got, want)
			}
		}
		s.Close()
	}
}

// A join whose ON leaves part of the joined table's key open would be a
// scan per row: refused, naming the key and an ON that fixes it.
func TestJoinNeedsTheKey(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	db, _ := open(s, qenv.NewStore(s, "db", qenv.Serializable), Options{})
	mustExec(t, db, `CREATE TABLE ix (tenant TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, pos))`)
	mustExec(t, db, `CREATE TABLE ev (tenant TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, pos))`)
	_, err := db.Query(`SELECT ev.pos FROM ix JOIN ev ON ev.pos = ix.pos WHERE ix.tenant = ?`, "t")
	var e *Error
	if !errors.As(err, &e) || e.Code != "42601" {
		t.Fatalf("got %v, want a 42601", err)
	}
	for _, want := range []string{"ON does not fix ev.tenant", "PRIMARY KEY (tenant, pos)", "ON ev.tenant = ix.tenant AND ev.pos = ix.pos"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the error does not say %q:\n%s", want, e.Message)
		}
	}
}
