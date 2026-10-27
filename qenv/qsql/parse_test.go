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
			&insertStmt{n: 2, table: "payments", cols: []string{"key", "amount"}, rows: [][]expr{{{param: 1}, {param: 2}}}, onConflict: true, target: []string{"key"}}},
		{`INSERT INTO t VALUES ($2, 'it''s', -5, TRUE, NULL), ($1, 'x', 0, false, null) ON CONFLICT DO NOTHING`,
			&insertStmt{n: 2, table: "t", rows: [][]expr{{{param: 2}, {lit: "it's"}, {lit: int64(-5)}, {lit: true}, {lit: nil}}, {{param: 1}, {lit: "x"}, {lit: int64(0)}, {lit: false}, {lit: nil}}}, onConflict: true}},
		{`SELECT * FROM accounts`, &selectStmt{table: "accounts"}},
		{`SELECT id, balance FROM accounts WHERE id = $1 AND balance = 3 ORDER BY id DESC LIMIT $2 FOR UPDATE`,
			&selectStmt{n: 2, table: "accounts", cols: []string{"id", "balance"}, where: []cond{{"id", "=", expr{param: 1}}, {"balance", "=", expr{lit: int64(3)}}},
				orderBy: []orderTerm{{"id", true}}, limit: &expr{param: 2}, forUpdate: true}},
		{`select balance from accounts where id = ? order by id asc limit 1 -- a comment`,
			&selectStmt{n: 1, table: "accounts", cols: []string{"balance"}, where: []cond{{"id", "=", expr{param: 1}}}, orderBy: []orderTerm{{"id", false}}, limit: &expr{lit: int64(1)}}},
		{`UPDATE accounts SET balance = balance - ?, note = ? WHERE id = ?`,
			&updateStmt{n: 3, table: "accounts", sets: []setClause{{"balance", '-', expr{param: 1}}, {"note", '=', expr{param: 2}}}, where: []cond{{"id", "=", expr{param: 3}}}}},
		{`UPDATE accounts SET balance = balance + 10`,
			&updateStmt{table: "accounts", sets: []setClause{{"balance", '+', expr{lit: int64(10)}}}}},
		{`DELETE FROM accounts WHERE id = ?`, &deleteStmt{n: 1, table: "accounts", where: []cond{{"id", "=", expr{param: 1}}}}},
		// slice 2's shapes: keys of several columns, ranges, upserts, RETURNING
		{`CREATE TABLE grist_events (tenant TEXT NOT NULL, pos BIGINT NOT NULL, body BLOB, PRIMARY KEY (tenant, pos))`,
			&createStmt{table: "grist_events", cols: []colDef{{name: "tenant", typ: "text", notNull: true}, {name: "pos", typ: "integer", notNull: true}, {name: "body", typ: "blob"}},
				key: []string{"tenant", "pos"}}},
		{`UPDATE grist_heads SET head = head + ? WHERE tenant = ? RETURNING head`,
			&updateStmt{n: 2, table: "grist_heads", sets: []setClause{{"head", '+', expr{param: 1}}}, where: []cond{{"tenant", "=", expr{param: 2}}}, ret: returning{on: true, cols: []string{"head"}}}},
		{`SELECT pos FROM grist_index WHERE tenant = $1 AND k = $2 AND pos > $3 ORDER BY pos LIMIT 1`,
			&selectStmt{n: 3, table: "grist_index", cols: []string{"pos"}, where: []cond{{"tenant", "=", expr{param: 1}}, {"k", "=", expr{param: 2}}, {"pos", ">", expr{param: 3}}},
				orderBy: []orderTerm{{"pos", false}}, limit: &expr{lit: int64(1)}}},
		{`SELECT * FROM e WHERE e.tenant = ? AND pos BETWEEN ? AND ? AND pos<=5 AND pos >= 1 AND pos < 9 ORDER BY tenant DESC, pos DESC`,
			&selectStmt{n: 3, table: "e", where: []cond{{"tenant", "=", expr{param: 1}}, {"pos", ">=", expr{param: 2}}, {"pos", "<=", expr{param: 3}}, {"pos", "<=", expr{lit: int64(5)}}, {"pos", ">=", expr{lit: int64(1)}}, {"pos", "<", expr{lit: int64(9)}}},
				orderBy: []orderTerm{{"tenant", true}, {"pos", true}}}},
		{`INSERT INTO grist_checkpoints (tenant, processor, pos) VALUES (?, ?, ?) ON CONFLICT (tenant, processor) DO UPDATE SET pos = excluded.pos, n = n + 1 WHERE grist_checkpoints.pos = ? RETURNING *`,
			&insertStmt{n: 4, table: "grist_checkpoints", cols: []string{"tenant", "processor", "pos"}, rows: [][]expr{{{param: 1}, {param: 2}, {param: 3}}}, onConflict: true,
				target: []string{"tenant", "processor"}, doUpdate: []setClause{{"pos", '=', expr{ref: "pos"}}, {"n", '+', expr{lit: int64(1)}}},
				updWhere: []cond{{"pos", "=", expr{param: 4}}}, ret: returning{on: true}}},
		{`INSERT INTO c (k, n) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET n = c.n + excluded.n`,
			&insertStmt{n: 2, table: "c", cols: []string{"k", "n"}, rows: [][]expr{{{param: 1}, {param: 2}}}, onConflict: true, target: []string{"k"}, doUpdate: []setClause{{"n", '+', expr{ref: "n"}}}}},
		{`DELETE FROM accounts WHERE id = ? RETURNING id, balance`, &deleteStmt{n: 1, table: "accounts", where: []cond{{"id", "=", expr{param: 1}}}, ret: returning{on: true, cols: []string{"id", "balance"}}}},
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
		{`SELECT a FROM t WHERE a <> 1`, `"<>" after a (equality and ranges only`},
		{`SELECT a FROM t WHERE a != 1`, `"!" after a (equality and ranges only`},
		{`SELECT a FROM t WHERE a LIKE 'x%'`, `"LIKE" after a (equality and ranges only`},
		{`SELECT a FROM t WHERE u.a = 1`, `the qualifier u. (columns of the statement's table t only`},
		{`INSERT INTO t (a) VALUES (1) ON CONFLICT DO UPDATE SET a = 2`, `ON CONFLICT DO UPDATE without a target`},
		{`INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO SOMETHING`, `"SOMETHING" where NOTHING or UPDATE SET goes`},
		{`UPDATE t SET a = excluded.a`, `the qualifier excluded.`},
		{`UPDATE t SET a = 1 RETURNING a + 1`, `an expression after RETURNING a`},
		{`CREATE TABLE t (a INTEGER, PRIMARY KEY (a), PRIMARY KEY (a))`, `a second PRIMARY KEY`},
		{`SELECT count(*) FROM t`, `the function count(...)`},
		{`SELECT a FROM t WHERE a = 1.5`, `the number "1."`},
		{`INSERT INTO t (a) SELECT a FROM u`, `INSERT ... "SELECT"`},
		{`CREATE TABLE t (a INTEGER, b TEXT, UNIQUE (a, b))`, `the table constraint "UNIQUE"`},
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
