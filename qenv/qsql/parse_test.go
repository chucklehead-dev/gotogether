package qsql

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Every statement of the subset parses to what it says.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want statement
	}{
		{`CREATE TABLE IF NOT EXISTS accounts (id TEXT PRIMARY KEY, balance BIGINT NOT NULL, email text UNIQUE NULL, at TIMESTAMPTZ, ok BOOL, raw BYTEA)`,
			&createStmt{table: "accounts", ifNotExists: true, cols: []colDef{
				{name: "id", typ: "text", primary: true}, {name: "balance", typ: "integer", notNull: true},
				{name: "email", typ: "text", unique: true}, {name: "at", typ: "timestamp"}, {name: "ok", typ: "boolean"}, {name: "raw", typ: "blob"}}}},
		{`create table "Quoted" ("Id" integer primary key);`,
			&createStmt{table: "Quoted", cols: []colDef{{name: "Id", typ: "integer", primary: true}}}},
		{`INSERT INTO payments (key, amount) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`,
			&insertStmt{n: 2, table: "payments", cols: []string{"key", "amount"}, rows: [][]expr{{{param: 1}, {param: 2}}}, onConflict: true, target: "key"}},
		{`INSERT INTO t VALUES ($2, 'it''s', -5, TRUE, NULL), ($1, 'x', 0, false, null) ON CONFLICT DO NOTHING`,
			&insertStmt{n: 2, table: "t", rows: [][]expr{{{param: 2}, {lit: "it's"}, {lit: int64(-5)}, {lit: true}, {lit: nil}}, {{param: 1}, {lit: "x"}, {lit: int64(0)}, {lit: false}, {lit: nil}}}, onConflict: true}},
		{`SELECT * FROM accounts`, &selectStmt{table: "accounts"}},
		{`SELECT id, balance FROM accounts WHERE id = $1 AND balance = 3 ORDER BY id DESC LIMIT $2 FOR UPDATE`,
			&selectStmt{n: 2, table: "accounts", cols: []string{"id", "balance"}, where: []cond{{"id", expr{param: 1}}, {"balance", expr{lit: int64(3)}}},
				orderBy: "id", desc: true, limit: &expr{param: 2}, forUpdate: true}},
		{`select balance from accounts where id = ? order by id asc limit 1 -- a comment`,
			&selectStmt{n: 1, table: "accounts", cols: []string{"balance"}, where: []cond{{"id", expr{param: 1}}}, orderBy: "id", limit: &expr{lit: int64(1)}}},
		{`UPDATE accounts SET balance = balance - ?, note = ? WHERE id = ?`,
			&updateStmt{n: 3, table: "accounts", sets: []setClause{{"balance", '-', expr{param: 1}}, {"note", '=', expr{param: 2}}}, where: []cond{{"id", expr{param: 3}}}}},
		{`UPDATE accounts SET balance = balance + 10`,
			&updateStmt{table: "accounts", sets: []setClause{{"balance", '+', expr{lit: int64(10)}}}}},
		{`DELETE FROM accounts WHERE id = ?`, &deleteStmt{n: 1, table: "accounts", where: []cond{{"id", expr{param: 1}}}}},
		{`DELETE FROM accounts`, &deleteStmt{table: "accounts"}},
	} {
		got, err := parse(tc.q)
		if err != nil {
			t.Errorf("%s: %v", tc.q, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %#v\nwant %#v", tc.q, got, tc.want)
		}
	}
}

// Everything outside the subset fails with 42601, naming what was found
// and the subset (or a fix of its own).
func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ q, says string }{
		{`SELECT a FROM t JOIN u ON a = b`, `"JOIN"`},
		{`SELECT a FROM t WHERE a = 1 OR b = 2`, `"OR"`},
		{`SELECT a FROM t WHERE a > 1`, `">" after a (equality only`},
		{`SELECT count(*) FROM t`, `the function count(...)`},
		{`SELECT a FROM t WHERE a = 1.5`, `the number "1."`},
		{`INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = 2`, `DO UPDATE (an upsert`},
		{`INSERT INTO t (a) VALUES (1) RETURNING a`, `"RETURNING"`},
		{`INSERT INTO t (a) SELECT a FROM u`, `INSERT ... "SELECT"`},
		{`INSERT INTO t (a, b) VALUES (1, 2) ON CONFLICT (a, b) DO NOTHING`, `ON CONFLICT on 2 columns`},
		{`CREATE TABLE t (a INTEGER, b TEXT, PRIMARY KEY (a, b))`, `the table constraint "PRIMARY"`},
		{`CREATE TABLE t (a REAL PRIMARY KEY)`, `"REAL" where a column type`},
		{`CREATE TABLE t (a INTEGER PRIMARY KEY DEFAULT 0)`, `"DEFAULT"`},
		{`UPDATE t SET a = b + 1`, `a = "b" ...`},
		{`UPDATE t SET a = a * 2`, `"*" where + or -`},
		{`UPDATE t SET a = 1 FROM u`, `UPDATE ... FROM`},
		{`BEGIN`, `db.BeginTx`},
		{`SELECT a FROM t; SELECT b FROM u`, `one statement per call`},
		{`SELECT a FROM t WHERE a = ? AND b = $2`, `mixes ? and $n`},
		{`SELECT a FROM t WHERE a = 'open`, `an unterminated string`},
		{`SELECT a FROM t WHERE a = $0`, `the placeholder "$0"`},
		{`SELECT a FROM t WHERE a = 99999999999999999999`, `out of BIGINT's range`},
		{`TRUNCATE t`, `"TRUNCATE" where CREATE, INSERT, SELECT, UPDATE or DELETE goes`},
		{`SELECT FROM t`, `"FROM" where a column name or *`},
	} {
		_, err := parse(tc.q)
		var e *Error
		if !errors.As(err, &e) || e.Code != "42601" {
			t.Errorf("%s: got %v, want a 42601", tc.q, err)
			continue
		}
		if !strings.Contains(e.Message, tc.says) {
			t.Errorf("%s: the error does not say %q:\n%s", tc.q, tc.says, e.Message)
		}
		if !strings.Contains(e.Message, "qsql runs one table") && !strings.Contains(e.Message, "mixes") {
			t.Errorf("%s: the error does not name the subset:\n%s", tc.q, e.Message)
		}
	}
}
