package qsql_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

func TestUnquotedIdentifierLookup(t *testing.T) {
	for _, name := range []string{"y", "z", "az", "z9", "Z"} {
		t.Run(name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			db := qsql.Open(s, qenv.NewStore(s, "identifiers", qenv.Serializable))
			defer db.Close()
			// Quoted setup keeps the unquoted lookup independent: legal names
			// must resolve, including z initially, within a name and before a digit.
			quoted := `"` + strings.ToLower(name) + `"`
			if _, err := db.Exec(`CREATE TABLE ` + quoted + ` (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO `+quoted+` (id, value) VALUES (?, ?)`, "one", "stored"); err != nil {
				t.Fatal(err)
			}
			var got string
			err := db.QueryRow(fmt.Sprintf(`SELECT value FROM %s WHERE id = ?`, name), "one").Scan(&got)
			if err != nil || got != "stored" {
				t.Errorf("unquoted table %s: value=%q, error=%v; want stored, nil", name, got, err)
			}
		})
	}
}
