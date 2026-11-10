package qsql_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Boolean predicates use SQL's false-before-true order, independently of
// insertion order. Equality controls also distinguish the selected identities
// from a predicate that merely returns the right number of rows.
func TestBooleanWhereOrdering(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			db := qsql.Open(s, qenv.NewStore(s, "flags", qenv.Serializable))
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE flags (enabled BOOLEAN NOT NULL, id TEXT NOT NULL, PRIMARY KEY (enabled, id))`); err != nil {
				t.Fatal(err)
			}
			rows := []struct {
				id      string
				enabled bool
			}{{"a", false}, {"b", true}, {"c", false}, {"d", true}}
			if reverse {
				slices.Reverse(rows)
			}
			for _, row := range rows {
				if _, err := db.Exec(`INSERT INTO flags (enabled, id) VALUES (?, ?)`, row.enabled, row.id); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				op    string
				bound bool
				want  []string
			}{
				{">", false, []string{"b", "d"}},
				{"<", true, []string{"a", "c"}},
				{"=", false, []string{"a", "c"}},
				{"=", true, []string{"b", "d"}},
				{">", true, nil},
				{"<", false, nil},
			} {
				t.Run(fmt.Sprintf("%s%v", tc.op, tc.bound), func(t *testing.T) {
					result, err := db.Query(`SELECT id FROM flags WHERE enabled `+tc.op+` ?`, tc.bound)
					if err != nil {
						t.Fatal(err)
					}
					defer result.Close()
					var got []string
					for result.Next() {
						var id string
						if err := result.Scan(&id); err != nil {
							t.Fatal(err)
						}
						got = append(got, id)
					}
					if err := result.Err(); err != nil {
						t.Fatal(err)
					}
					slices.Sort(got)
					if !slices.Equal(got, tc.want) {
						t.Errorf("WHERE enabled %s %v selected IDs %v, want %v", tc.op, tc.bound, got, tc.want)
					}
				})
			}
		})
	}
}
