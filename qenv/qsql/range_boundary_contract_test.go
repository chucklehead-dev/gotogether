package qsql_test

import (
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Repeating or reordering bounds keeps the intersection, including a finite
// upper bound followed by a condition that contributes only a lower bound.
func TestRepeatedRangeBoundsInEitherOrder(t *testing.T) {
	for _, composite := range []bool{false, true} {
		name := "single key"
		if composite {
			name = "composite key"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
			if composite {
				w.exec(t, `CREATE TABLE bounds (tenant TEXT, id BIGINT, PRIMARY KEY (tenant,id))`)
				for id := range 7 {
					w.exec(t, `INSERT INTO bounds VALUES ('a',?), ('b',?)`, id, id)
				}
			} else {
				w.exec(t, `CREATE TABLE bounds (id BIGINT PRIMARY KEY)`)
				for id := range 7 {
					w.exec(t, `INSERT INTO bounds VALUES (?)`, id)
				}
			}
			for _, tc := range []struct {
				predicate string
				want      []int64
			}{
				{`id >= 2 AND id >= 2`, []int64{2, 3, 4, 5, 6}},
				{`id <= 4 AND id <= 4`, []int64{0, 1, 2, 3, 4}},
				{`id >= 2 AND id > 2`, []int64{3, 4, 5, 6}},
				{`id > 2 AND id >= 2`, []int64{3, 4, 5, 6}},
				{`id <= 4 AND id < 4`, []int64{0, 1, 2, 3}},
				{`id < 4 AND id <= 4`, []int64{0, 1, 2, 3}},
				{`id >= 2 AND id < 4`, []int64{2, 3}},
				{`id < 4 AND id >= 2`, []int64{2, 3}},
				{`id <= 2 AND id >= 2`, []int64{2}},
				{`id >= 2 AND id <= 2`, []int64{2}},
				{`id > 2 AND id < 2`, nil},
			} {
				predicate := tc.predicate
				if composite {
					predicate = `tenant='a' AND ` + predicate
				}
				rows, err := w.db.Query(`SELECT id FROM bounds WHERE ` + predicate + ` ORDER BY id`)
				if err != nil {
					t.Fatalf("%s: %v", predicate, err)
				}
				var got []int64
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if !slices.Equal(got, tc.want) {
					t.Errorf("%s: ids %v, want %v", predicate, got, tc.want)
				}
			}
		})
	}
}
