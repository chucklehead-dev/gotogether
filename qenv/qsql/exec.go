package qsql

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// How tables live in the Store, one key per row and per unique value:
//
//	_qsql/schema/<t>     the table's schema (*table)
//	<t>/<pk>             a row (map[string]any, every column; nil is NULL)
//	<t>!<col>/<value>    a UNIQUE column's value: the key of its row
//
// <pk> and <value> are encoded so that the keys sort as the values do
// (encodeKey): Scan returns keys sorted, so a scan is in primary-key order.

const schemaPrefix = "_qsql/schema/"

// table is a table's schema. It never changes once created.
type table struct {
	name string
	cols []colDef
	pk   int // the PRIMARY KEY column's index
}

func (t *table) col(name string) (int, bool) {
	for i, c := range t.cols {
		if c.name == name {
			return i, true
		}
	}
	return 0, false
}

func (t *table) colNames() []string {
	out := make([]string, len(t.cols))
	for i, c := range t.cols {
		out[i] = c.name
	}
	return out
}

func (t *table) rowKey(pk any) string { return t.name + "/" + encodeKey(pk) }
func (t *table) indexKey(col string, v any) string {
	return t.name + "!" + col + "/" + encodeKey(v)
}

// encodeKey encodes a non-NULL value of a column so that the encodings
// sort (as strings) as the values do: an integer or a time's microseconds
// as 16 hex digits with the sign bit flipped, text as itself, bytes in
// hex, a boolean as 0 or 1.
func encodeKey(v any) string {
	switch v := v.(type) {
	case int64:
		return fmt.Sprintf("%016x", uint64(v)^(1<<63))
	case time.Time:
		return fmt.Sprintf("%016x", uint64(v.UnixMicro())^(1<<63))
	case string:
		return v
	case []byte:
		return hex.EncodeToString(v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	}
	panic(fmt.Sprintf("qsql: no key encoding for %T", v))
}

// coerce checks v against column c's type: a parameter or literal as the
// row stores it. Bytes are copied (the caller may reuse its slice) and
// times kept to microseconds, as PostgreSQL keeps them: a value that does
// not survive the round trip in production must not survive it here.
func coerce(tb *table, c colDef, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	ok := false
	switch c.typ {
	case "integer":
		_, ok = v.(int64)
	case "text":
		_, ok = v.(string)
	case "boolean":
		_, ok = v.(bool)
	case "blob":
		if b, isB := v.([]byte); isB {
			return bytes.Clone(b), nil
		}
	case "timestamp":
		if t, isT := v.(time.Time); isT {
			return t.Truncate(time.Microsecond), nil
		}
	}
	if !ok {
		return nil, &Error{Code: "42804", Message: fmt.Sprintf("column %q of %q is %s; got %T %v (pass %s)", c.name, tb.name, strings.ToUpper(c.typ), v, v, goType[c.typ])}
	}
	return v, nil
}

// goType names the Go value each column type takes. Read-only.
var goType = map[string]string{
	"integer": "an int64 (or any integer)", "text": "a string", "boolean": "a bool", "blob": "a []byte", "timestamp": "a time.Time",
}

func equal(a, b any) bool {
	switch a := a.(type) {
	case nil:
		return false // NULL equals nothing, NULL included
	case []byte:
		b, ok := b.([]byte)
		return ok && bytes.Equal(a, b)
	case time.Time:
		b, ok := b.(time.Time)
		return ok && a.Equal(b)
	}
	return a == b
}

func copyRow(r map[string]any) map[string]any {
	out := make(map[string]any, len(r))
	for k, v := range r {
		if b, ok := v.([]byte); ok {
			v = bytes.Clone(b)
		}
		out[k] = v
	}
	return out
}

// ---- execution --------------------------------------------------------------

// result is what a statement returns: rows for a query, a count for the rest.
type result struct {
	cols     []string
	rows     [][]any
	affected int64
}

// exec runs st in t's Store transaction with args (positional, 1-based in
// the statement).
func (t *tx) exec(st statement, args []any) (*result, error) {
	switch s := st.(type) {
	case *createStmt:
		return t.create(s)
	case *insertStmt:
		return t.insert(s, args)
	case *selectStmt:
		return t.query(s, args)
	case *updateStmt:
		return t.update(s, args)
	case *deleteStmt:
		return t.delete(s, args)
	}
	panic(fmt.Sprintf("qsql: statement %T", st))
}

func (e expr) value(args []any) any {
	if e.param > 0 {
		return args[e.param-1]
	}
	return e.lit
}

// table is the schema of name: one this transaction created, one already
// known, or the Store's.
func (t *tx) table(name string) (*table, error) {
	if tb, ok := t.created[name]; ok {
		return tb, nil
	}
	if tb := t.c.knownTable(name); tb != nil {
		return tb, nil
	}
	if v, ok := t.txn.Get(schemaPrefix + name); ok {
		tb := v.(*table)
		t.c.learnTable(tb) // committed (not this transaction's, which created holds): a schema never changes
		return tb, nil
	}
	return nil, &Error{Code: "42P01", Message: fmt.Sprintf("relation %q does not exist in store %s (create it with CREATE TABLE first)", name, t.c.st.Name)}
}

func (t *tx) writable(what string) error {
	if t.readOnly {
		return &Error{Code: "25006", Message: fmt.Sprintf("cannot execute %s in a read-only transaction (BeginTx with ReadOnly)", what)}
	}
	return nil
}

func (t *tx) create(s *createStmt) (*result, error) {
	if err := t.writable("CREATE TABLE"); err != nil {
		return nil, err
	}
	if s.table == "_qsql" {
		return nil, &Error{Code: "42939", Message: `table name "_qsql" is reserved: qsql keeps its schemas under it`}
	}
	tb := &table{name: s.table, cols: s.cols, pk: -1}
	seen := map[string]bool{}
	for i, c := range s.cols {
		if seen[c.name] {
			return nil, &Error{Code: "42701", Message: fmt.Sprintf("column %q specified more than once in CREATE TABLE %s", c.name, s.table)}
		}
		seen[c.name] = true
		if c.primary {
			if tb.pk >= 0 {
				return nil, &Error{Code: "42P16", Message: fmt.Sprintf("multiple primary keys for table %q (qsql keys rows by one column)", s.table)}
			}
			tb.pk = i
			tb.cols[i].notNull = true
		}
	}
	if tb.pk < 0 {
		return nil, &Error{Code: "42P16", Message: fmt.Sprintf("table %q has no PRIMARY KEY: qsql keys rows by one column's (mark one PRIMARY KEY)", s.table)}
	}
	if _, exists, err := t.txn.GetForUpdate(schemaPrefix + s.table); err != nil {
		return nil, err
	} else if exists {
		if s.ifNotExists {
			return &result{}, nil
		}
		return nil, &Error{Code: "42P07", Message: fmt.Sprintf("relation %q already exists (CREATE TABLE IF NOT EXISTS leaves it)", s.table)}
	}
	t.txn.Put(schemaPrefix+s.table, tb)
	t.created[s.table] = tb
	return &result{}, nil
}

// row is a matched row: its key and its value.
type row struct {
	key string
	val map[string]any
}

// conds resolves a WHERE clause: each column's index and value, coerced.
// none reports a condition no row meets (col = NULL).
func (t *tx) conds(tb *table, where []cond, args []any) (cs []resolved, none bool, err error) {
	for _, w := range where {
		i, ok := tb.col(w.col)
		if !ok {
			return nil, false, undefinedColumn(tb, w.col)
		}
		v, err := coerce(tb, tb.cols[i], w.val.value(args))
		if err != nil {
			return nil, false, err
		}
		if v == nil {
			none = true
		}
		cs = append(cs, resolved{col: tb.cols[i], val: v})
	}
	return cs, none, nil
}

type resolved struct {
	col colDef
	val any
}

func matches(r map[string]any, cs []resolved) bool {
	for _, c := range cs {
		if !equal(r[c.col.name], c.val) {
			return false
		}
	}
	return true
}

func undefinedColumn(tb *table, col string) *Error {
	return &Error{Code: "42703", Message: fmt.Sprintf("column %q of relation %q does not exist (its columns: %s)", col, tb.name, strings.Join(tb.colNames(), ", "))}
}

// find reads the rows meeting cs: by key when a condition names the
// primary key or a UNIQUE column, else by scanning the table. It reads
// without locking; lock re-reads each under its lock.
func (t *tx) find(tb *table, cs []resolved) []row {
	pick := func(r row, ok bool) []row {
		if ok && matches(r.val, cs) {
			return []row{r}
		}
		return nil
	}
	if !t.c.scanOnly {
		for _, c := range cs {
			if c.col.primary {
				k := tb.rowKey(c.val)
				t.read[k] = true
				v, ok := t.txn.Get(k)
				return pick(row{k, asRow(v)}, ok)
			}
		}
		for _, c := range cs {
			if !c.col.unique {
				continue
			}
			// the index, then the row: two reads, two seams. At read
			// committed a commit between them may have moved the value
			// to another row (PostgreSQL reads both in the statement's
			// one snapshot); then the row no longer holds it, and the
			// index is read again. Under a snapshot both reads are of it.
			ik := tb.indexKey(c.col.name, c.val)
			for range 100 {
				t.read[ik] = true
				pk, ok := t.txn.Get(ik)
				if !ok {
					return nil
				}
				k := pk.(string)
				t.read[k] = true
				v, ok := t.txn.Get(k)
				if ok && equal(asRow(v)[c.col.name], c.val) || t.c.st.Iso != qenv.ReadCommitted {
					return pick(row{k, asRow(v)}, ok)
				}
			}
			break // the value moves faster than it is read: scan, one read
		}
	}
	var out []row
	t.scanned = append(t.scanned, tb.name+"/")
	for _, kv := range t.txn.Scan(tb.name + "/") {
		if r := asRow(kv.Val); matches(r, cs) {
			out = append(out, row{kv.Key, r})
		}
	}
	return out
}

func asRow(v any) map[string]any {
	r, _ := v.(map[string]any)
	return r
}

// claim locks key k for a write that must find it free (an INSERT's key,
// an UPDATE's new unique value) and reports whether it is taken, with
// PostgreSQL's codes. Under a snapshot the Store's GetForUpdate aborts
// (ErrConflict) on a key committed after the snapshot, but PostgreSQL's
// unique check reads the latest committed rows, whatever the level: a
// key another transaction inserted and committed is a duplicate (23505),
// unless the transaction had read it and found it absent at serializable
// (its predicate lock: 40001), or this is ON CONFLICT DO NOTHING's
// arbiter, whose conflicting row is not in the snapshot (40001, as
// PostgreSQL's ExecCheckTupleVisible). A key deleted after the snapshot
// is 40001 here where PostgreSQL would insert: the Store has aborted the
// transaction by then. Either way the Store aborted it: nothing commits
// that its level forbids.
func (t *tx) claim(k string, arbiter bool) (taken bool, err error) {
	_, exists, err := t.txn.GetForUpdate(k)
	if err == nil || errors.Is(err, qenv.ErrDeadlock) || !errors.Is(err, qenv.ErrConflict) {
		return exists, err
	}
	if _, live := t.c.st.Read(k); !live || arbiter || t.c.st.Iso == qenv.Serializable && t.wasRead(k) {
		return false, err
	}
	return true, nil
}

// wasRead reports whether the transaction read key k (a lookup, or a scan
// of its table).
func (t *tx) wasRead(k string) bool {
	if t.read[k] {
		return true
	}
	for _, p := range t.scanned { // a scan read every row, and so every unique value, of its table
		if tb := strings.TrimSuffix(p, "/"); strings.HasPrefix(k, tb+"/") || strings.HasPrefix(k, tb+"!") {
			return true
		}
	}
	return false
}

// lock locks each of rows (GetForUpdate) and keeps those that still meet
// cs as they are now, at most limit of them (-1: all): a row changed while
// the statement waited for its lock is judged as it is, as PostgreSQL's
// read committed re-checks the WHERE clause on the row's latest version.
// Under snapshot isolation and serializable, a row changed since the
// snapshot aborts the transaction (40001) instead.
func (t *tx) lock(rows []row, cs []resolved, limit int64) ([]row, error) {
	var out []row
	for _, r := range rows {
		if limit >= 0 && int64(len(out)) >= limit {
			break
		}
		v, ok, err := t.txn.GetForUpdate(r.key)
		if err != nil {
			return nil, err
		}
		if cur := asRow(v); ok && matches(cur, cs) {
			out = append(out, row{r.key, cur})
		}
	}
	return out, nil
}

func (t *tx) limit(s *selectStmt, args []any) (int64, error) {
	if s.limit == nil {
		return -1, nil
	}
	n, ok := s.limit.value(args).(int64)
	if !ok || n < 0 {
		return 0, &Error{Code: "2201W", Message: fmt.Sprintf("LIMIT must be a non-negative integer; got %T %v", s.limit.value(args), s.limit.value(args))}
	}
	return n, nil
}

func (t *tx) query(s *selectStmt, args []any) (*result, error) {
	tb, err := t.table(s.table)
	if err != nil {
		return nil, err
	}
	if s.forUpdate {
		if err := t.writable("SELECT FOR UPDATE"); err != nil {
			return nil, err
		}
	}
	cols := s.cols
	if cols == nil {
		cols = tb.colNames()
	}
	for _, c := range cols {
		if _, ok := tb.col(c); !ok {
			return nil, undefinedColumn(tb, c)
		}
	}
	if s.orderBy != "" && s.orderBy != tb.cols[tb.pk].name {
		return nil, &Error{Code: "42601", Message: fmt.Sprintf("ORDER BY %s: rows come in primary-key order only (ORDER BY %s [DESC]); %s", s.orderBy, tb.cols[tb.pk].name, subset)}
	}
	limit, err := t.limit(s, args)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil || none {
		return &result{cols: cols}, err
	}
	rows := t.find(tb, cs)
	if s.desc {
		slices.Reverse(rows)
	}
	if s.forUpdate {
		if rows, err = t.lock(rows, cs, limit); err != nil {
			return nil, err
		}
	} else if limit >= 0 && int64(len(rows)) > limit {
		rows = rows[:limit]
	}
	res := &result{cols: cols, affected: int64(len(rows))}
	for _, r := range rows {
		out := make([]any, len(cols))
		for i, c := range cols {
			out[i] = r.val[c]
			if b, ok := out[i].([]byte); ok {
				out[i] = bytes.Clone(b)
			}
		}
		res.rows = append(res.rows, out)
	}
	return res, nil
}

func notNull(tb *table, r map[string]any) error {
	for _, c := range tb.cols {
		if c.notNull && r[c.name] == nil {
			return &Error{Code: "23502", Message: fmt.Sprintf("null value in column %q of relation %q violates not-null constraint", c.name, tb.name)}
		}
	}
	return nil
}

func uniqueViolation(tb *table, c colDef, v any) *Error {
	constraint := tb.name + "_" + c.name + "_key"
	if c.primary {
		constraint = tb.name + "_pkey"
	}
	return &Error{Code: "23505", Message: fmt.Sprintf("duplicate key value violates unique constraint %q: Key (%s)=(%v) already exists", constraint, c.name, v)}
}

func (t *tx) insert(s *insertStmt, args []any) (*result, error) {
	if err := t.writable("INSERT"); err != nil {
		return nil, err
	}
	tb, err := t.table(s.table)
	if err != nil {
		return nil, err
	}
	cols := s.cols
	if cols == nil {
		cols = tb.colNames()
	}
	idx := make([]int, len(cols))
	for i, c := range cols {
		j, ok := tb.col(c)
		if !ok {
			return nil, undefinedColumn(tb, c)
		}
		idx[i] = j
	}
	// the constraints a row may conflict on, ON CONFLICT's target first:
	// PostgreSQL checks the arbiter, and a conflict there does nothing
	// even where another constraint would conflict too
	var keys []int
	if s.target != "" {
		j, ok := tb.col(s.target)
		if !ok {
			return nil, undefinedColumn(tb, s.target)
		}
		if !tb.cols[j].primary && !tb.cols[j].unique {
			return nil, &Error{Code: "42P10", Message: fmt.Sprintf("there is no unique or exclusion constraint matching the ON CONFLICT specification (%s is neither PRIMARY KEY nor UNIQUE)", s.target)}
		}
		keys = append(keys, j)
	}
	for j, c := range tb.cols {
		if (c.primary || c.unique) && (len(keys) == 0 || keys[0] != j) {
			keys = append(keys, j)
		}
	}
	res := &result{}
rows:
	for _, vals := range s.rows {
		if len(vals) != len(cols) {
			return nil, &Error{Code: "42601", Message: fmt.Sprintf("INSERT into %s has %d values for %d columns", tb.name, len(vals), len(cols))}
		}
		r := make(map[string]any, len(tb.cols))
		for _, c := range tb.cols {
			r[c.name] = nil
		}
		for i, e := range vals {
			v, err := coerce(tb, tb.cols[idx[i]], e.value(args))
			if err != nil {
				return nil, err
			}
			r[cols[i]] = v
		}
		if err := notNull(tb, r); err != nil {
			return nil, err
		}
		pk := tb.cols[tb.pk]
		for _, j := range keys {
			c := tb.cols[j]
			if r[c.name] == nil {
				continue // NULLs never conflict
			}
			k := tb.rowKey(r[c.name])
			if !c.primary {
				k = tb.indexKey(c.name, r[c.name])
			}
			arbiter := s.onConflict && (s.target == "" || s.target == c.name)
			exists, err := t.claim(k, arbiter)
			if err != nil {
				return nil, err
			}
			if exists {
				if arbiter {
					continue rows
				}
				return nil, uniqueViolation(tb, c, r[c.name])
			}
		}
		key := tb.rowKey(r[pk.name])
		t.txn.Put(key, r)
		for _, c := range tb.cols {
			if c.unique && !c.primary && r[c.name] != nil {
				t.txn.Put(tb.indexKey(c.name, r[c.name]), key)
			}
		}
		t.changed(Change{Table: tb.name, Op: "insert", New: r})
		res.affected++
	}
	return res, nil
}

func (t *tx) update(s *updateStmt, args []any) (*result, error) {
	if err := t.writable("UPDATE"); err != nil {
		return nil, err
	}
	tb, err := t.table(s.table)
	if err != nil {
		return nil, err
	}
	type set struct {
		col colDef
		op  byte
		val any
	}
	var sets []set
	for _, sc := range s.sets {
		j, ok := tb.col(sc.col)
		if !ok {
			return nil, undefinedColumn(tb, sc.col)
		}
		c := tb.cols[j]
		if c.primary {
			return nil, &Error{Code: "0A000", Message: fmt.Sprintf("UPDATE of %s's primary key %s is not supported (DELETE the row and INSERT it anew)", tb.name, c.name)}
		}
		if sc.op != '=' && c.typ != "integer" {
			return nil, &Error{Code: "42883", Message: fmt.Sprintf("operator %c on column %q of type %s: arithmetic is on INTEGER columns only", sc.op, c.name, strings.ToUpper(c.typ))}
		}
		v, err := coerce(tb, c, sc.val.value(args))
		if err != nil {
			return nil, err
		}
		sets = append(sets, set{c, sc.op, v})
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil || none {
		return &result{}, err
	}
	rows, err := t.lock(t.find(tb, cs), cs, -1)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		nr := copyRow(r.val)
		for _, st := range sets {
			if st.op == '=' {
				nr[st.col.name] = st.val
				continue
			}
			cur, _ := nr[st.col.name].(int64) // from the locked row: no update is lost
			if nr[st.col.name] == nil || st.val == nil {
				nr[st.col.name] = nil
				continue
			}
			d := st.val.(int64)
			if st.op == '-' {
				if d == math.MinInt64 {
					return nil, outOfRange(st.col)
				}
				d = -d
			}
			if (d > 0 && cur > math.MaxInt64-d) || (d < 0 && cur < math.MinInt64-d) {
				return nil, outOfRange(st.col)
			}
			nr[st.col.name] = cur + d
		}
		if err := notNull(tb, nr); err != nil {
			return nil, err
		}
		for _, c := range tb.cols {
			if !c.unique || c.primary || equal(nr[c.name], r.val[c.name]) || nr[c.name] == nil && r.val[c.name] == nil {
				continue
			}
			if nr[c.name] != nil {
				k := tb.indexKey(c.name, nr[c.name])
				if exists, err := t.claim(k, false); err != nil {
					return nil, err
				} else if exists {
					return nil, uniqueViolation(tb, c, nr[c.name])
				}
				t.txn.Put(k, r.key)
			}
			if r.val[c.name] != nil {
				t.txn.Delete(tb.indexKey(c.name, r.val[c.name]))
			}
		}
		t.txn.Put(r.key, nr)
		t.changed(Change{Table: tb.name, Op: "update", Old: r.val, New: nr})
	}
	return &result{affected: int64(len(rows))}, nil
}

func outOfRange(c colDef) *Error {
	return &Error{Code: "22003", Message: fmt.Sprintf("bigint out of range in column %q", c.name)}
}

func (t *tx) delete(s *deleteStmt, args []any) (*result, error) {
	if err := t.writable("DELETE"); err != nil {
		return nil, err
	}
	tb, err := t.table(s.table)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil || none {
		return &result{}, err
	}
	rows, err := t.lock(t.find(tb, cs), cs, -1)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		t.txn.Delete(r.key)
		for _, c := range tb.cols {
			if c.unique && !c.primary && r.val[c.name] != nil {
				t.txn.Delete(tb.indexKey(c.name, r.val[c.name]))
			}
		}
		t.changed(Change{Table: tb.name, Op: "delete", Old: r.val})
	}
	return &result{affected: int64(len(rows))}, nil
}

// changed notes a row's change for OnCommit, copied: the caller's maps are
// the Store's values, which must not change.
func (t *tx) changed(ch Change) {
	if t.c.opt.OnCommit == nil {
		return
	}
	if ch.Old != nil {
		ch.Old = copyRow(ch.Old)
	}
	if ch.New != nil {
		ch.New = copyRow(ch.New)
	}
	t.changes = append(t.changes, ch)
}

// sqlError maps the Store's errors to SQLSTATEs; others pass as they are.
func sqlError(store string, err error) error {
	var e *Error
	switch {
	case err == nil || errors.As(err, &e):
		return err
	case errors.Is(err, qenv.ErrDeadlock):
		return &Error{Code: "40P01", Message: "deadlock detected (lock waits formed a cycle): retry the transaction, and lock rows in one order", Err: err}
	case errors.Is(err, qenv.ErrConflict):
		return &Error{Code: "40001", Message: "could not serialize access due to concurrent update: retry the transaction", Err: err}
	case errors.Is(err, qenv.ErrUnknownOutcome):
		return &Error{Code: "08006", Message: fmt.Sprintf("connection to store %s lost during COMMIT: the transaction may or may not have committed", store), Err: err}
	}
	return err
}
