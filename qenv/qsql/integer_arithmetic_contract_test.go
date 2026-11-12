package qsql_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

func integerArithmeticDB(t *testing.T) *sql.DB {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	db := qsql.Open(s, qenv.NewStore(s, "integer-arithmetic", qenv.Serializable))
	t.Cleanup(func() {
		s.Close()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec(`CREATE TABLE numbers (id INTEGER PRIMARY KEY, n BIGINT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestIntegerArithmeticBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, op             string
		initial, delta, want int64
		overflow             bool
	}{
		{"minus_min_from_min", "-", math.MinInt64, math.MinInt64, 0, false},
		{"minus_min_from_negative_one", "-", -1, math.MinInt64, math.MaxInt64, false},
		{"minus_min_from_negative_two", "-", -2, math.MinInt64, math.MaxInt64 - 1, false},
		{"minus_min_from_zero", "-", 0, math.MinInt64, 0, true},
		{"minus_min_from_positive", "-", 1, math.MinInt64, 0, true},
		{"minus_max_from_negative_one", "-", -1, math.MaxInt64, math.MinInt64, false},
		{"subtract_underflow", "-", math.MinInt64, 1, 0, true},
		{"subtract_overflow", "-", math.MaxInt64, -1, 0, true},
		{"add_min_to_zero", "+", 0, math.MinInt64, math.MinInt64, false},
		{"add_max_to_min", "+", math.MinInt64, math.MaxInt64, -1, false},
		{"add_underflow", "+", -1, math.MinInt64, 0, true},
		{"add_overflow", "+", math.MaxInt64, 1, 0, true},
		{"subtract_zero", "-", math.MinInt64, 0, math.MinInt64, false},
		{"add_zero", "+", math.MaxInt64, 0, math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := integerArithmeticDB(t)
			if _, err := db.Exec(`INSERT INTO numbers VALUES (?, ?)`, int64(1), tc.initial); err != nil {
				t.Fatal(err)
			}
			_, err := db.Exec(`UPDATE numbers SET n = n `+tc.op+` ? WHERE id = ?`, tc.delta, int64(1))
			want := tc.want
			if tc.overflow {
				if code(err) != "22003" {
					t.Fatalf("%d %s %d: %v; want SQLSTATE22003", tc.initial, tc.op, tc.delta, err)
				}
				want = tc.initial
			} else if err != nil {
				t.Fatalf("%d %s %d: %v; want %d", tc.initial, tc.op, tc.delta, err, want)
			}
			var got int64
			if err := db.QueryRow(`SELECT n FROM numbers WHERE id = ?`, int64(1)).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("stored value=%d, want %d", got, want)
			}
		})
	}
}

func TestIntegerArithmeticPreservesNull(t *testing.T) {
	for _, op := range []string{"+", "-"} {
		for _, tc := range []struct{ initial, delta any }{{nil, int64(math.MinInt64)}, {int64(1), nil}, {nil, nil}} {
			t.Run(fmt.Sprintf("%s/%v/%v", op, tc.initial, tc.delta), func(t *testing.T) {
				db := integerArithmeticDB(t)
				if _, err := db.Exec(`INSERT INTO numbers VALUES (1, ?)`, tc.initial); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE numbers SET n = n `+op+` ? WHERE id = 1`, tc.delta); err != nil {
					t.Fatal(err)
				}
				var got sql.NullInt64
				if err := db.QueryRow(`SELECT n FROM numbers WHERE id = 1`).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got.Valid {
					t.Errorf("NULL arithmetic produced %d", got.Int64)
				}
			})
		}
	}
}

func TestIntegerArithmeticOverflowAbortsWholeUpdate(t *testing.T) {
	db := integerArithmeticDB(t)
	if _, err := db.Exec(`INSERT INTO numbers VALUES (1, -1), (2, 0)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE numbers SET n = n - ?`, int64(math.MinInt64)); code(err) != "22003" {
		t.Fatalf("mixed update: %v; want22003", err)
	}
	if _, err := tx.Exec(`UPDATE numbers SET n = 7 WHERE id = 1`); code(err) != "25P02" {
		t.Fatalf("after overflow: %v; want25P02", err)
	}
	if err := tx.Commit(); code(err) != "25P02" {
		t.Fatalf("commit after overflow: %v; want25P02", err)
	}
	for id, want := range []int64{-1, 0} {
		var got int64
		if err := db.QueryRow(`SELECT n FROM numbers WHERE id = ?`, int64(id+1)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("row%d=%d, want original%d", id+1, got, want)
		}
	}
}
