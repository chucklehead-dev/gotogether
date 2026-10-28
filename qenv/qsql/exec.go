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
// A key of several columns is their components in key order, each its
// value's encoding with NUL escaped and a NUL 0x01 after it (component),
// so the keys sort as the tuples do and the rows whose leading columns
// are fixed are one prefix: a range on the next column is a range of
// keys within it (qenv.KeyRange), which the Store and its history checker
// see as a predicate read of the range alone.

const schemaPrefix = "_qsql/schema/"

// table is a table's schema. It never changes once created.
type table struct {
	name string
	cols []colDef
	key  []int // the PRIMARY KEY's columns' indexes, in key order
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

// keyPos is column i's position in the key, or -1.
func (t *table) keyPos(i int) int { return slices.Index(t.key, i) }

func (t *table) keyNames() []string {
	out := make([]string, len(t.key))
	for j, i := range t.key {
		out[j] = t.cols[i].name
	}
	return out
}

// rowKey is the Store key of the row whose key columns hold vals (by name).
func (t *table) rowKey(vals map[string]any) string {
	if len(t.key) == 1 { // one column: its encoding alone, as before keys of several
		return t.name + "/" + encodeKey(vals[t.cols[t.key[0]].name])
	}
	var b strings.Builder
	b.WriteString(t.name + "/")
	for _, i := range t.key {
		b.WriteString(component(vals[t.cols[i].name]))
	}
	return b.String()
}

func (t *table) indexKey(col string, v any) string {
	return t.name + "!" + col + "/" + encodeKey(v)
}

// component is one column of a key of several: its encoding, every NUL
// written NUL 0xff, then NUL 0x01. No component is a prefix of another,
// and they sort as the values do, so a key's components sort as tuples.
func component(v any) string { return escapeNUL(encodeKey(v)) + "\x00\x01" }

func escapeNUL(s string) string { return strings.ReplaceAll(s, "\x00", "\x00\xff") }

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

// compare orders two non-NULL values of one column's type: text by its
// bytes (SQLite's BINARY collation, PostgreSQL's "C"), false before true.
func compare(a, b any) int {
	switch a := a.(type) {
	case int64:
		return cmpOrdered(a, b.(int64))
	case string:
		return strings.Compare(a, b.(string))
	case []byte:
		return bytes.Compare(a, b.([]byte))
	case bool:
		x, y := 0, 0
		if a {
			x = 1
		}
		if b.(bool) {
			y = 1
		}
		return x - y
	case time.Time:
		return a.Compare(b.(time.Time))
	}
	panic(fmt.Sprintf("qsql: no order for %T", a))
}

func cmpOrdered(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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

// result is what a statement returns: rows for a query (or a RETURNING
// clause), a count for every statement.
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
	switch {
	case strings.HasPrefix(s.table, "_qsql"):
		return nil, &Error{Code: "42939", Message: fmt.Sprintf("table name %q is reserved: qsql keeps its schemas under _qsql/; name the table without the _qsql prefix", s.table)}
	case strings.ContainsAny(s.table, "/!"):
		// a table's rows are keys <t>/..., its unique values <t>!<col>/...:
		// a "/" or "!" in its name would put its keys among another table's
		return nil, &Error{Code: "42602", Message: fmt.Sprintf("table name %q has a %q: qsql keys a table's rows <table>/<key> and its unique values <table>!<column>/<value> in one Store, where it would collide with another table's; name the table without / or !", s.table, string(s.table[strings.IndexAny(s.table, "/!")]))}
	}
	tb := &table{name: s.table, cols: slices.Clone(s.cols)}
	seen := map[string]bool{}
	for i, c := range tb.cols {
		if seen[c.name] {
			return nil, &Error{Code: "42701", Message: fmt.Sprintf("column %q specified more than once in CREATE TABLE %s", c.name, s.table)}
		}
		seen[c.name] = true
		if c.primary {
			if tb.key != nil || s.key != nil {
				return nil, &Error{Code: "42P16", Message: fmt.Sprintf("multiple primary keys for table %q are not allowed (one PRIMARY KEY: a column's, or PRIMARY KEY (a, b) for a key of several)", s.table)}
			}
			tb.key = []int{i}
			tb.cols[i].notNull = true
		}
	}
	for _, name := range s.key {
		i, ok := tb.col(name)
		if !ok {
			return nil, &Error{Code: "42703", Message: fmt.Sprintf("column %q named in key does not exist (the columns of %s: %s)", name, s.table, strings.Join(tb.colNames(), ", "))}
		}
		if tb.keyPos(i) >= 0 {
			return nil, &Error{Code: "42701", Message: fmt.Sprintf("column %q appears twice in the primary key of %s", name, s.table)}
		}
		tb.key = append(tb.key, i)
		tb.cols[i].notNull = true
		if len(s.key) == 1 {
			tb.cols[i].primary = true // PRIMARY KEY (a) is a's PRIMARY KEY
		}
	}
	if tb.key == nil {
		return nil, &Error{Code: "42P16", Message: fmt.Sprintf("table %q has no PRIMARY KEY: qsql keys rows by it (mark one PRIMARY KEY: a column's, or PRIMARY KEY (a, b) for a key of several)", s.table)}
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

type resolved struct {
	col colDef
	idx int // the column's index
	op  string
	val any
}

// conds resolves a WHERE clause: each column's index and value, coerced,
// and checks its ranges against the key (a range is on one key column,
// every key column before it fixed by equality). none reports a
// condition no row meets (a comparison with NULL).
func (t *tx) conds(tb *table, where []cond, args []any) (cs []resolved, none bool, err error) {
	for _, w := range where {
		i, ok := tb.col(w.col)
		if !ok {
			return nil, false, undefinedColumn(tb, w.col)
		}
		cs = append(cs, resolved{col: tb.cols[i], idx: i, op: w.op})
	}
	fixed := fixedKey(tb, cs)
	for _, c := range cs {
		if c.op == "=" {
			continue
		}
		if p := tb.keyPos(c.idx); p < 0 || p > fixed {
			return nil, false, rangeErr(tb, c)
		}
	}
	for n, w := range where {
		if cs[n].val, err = coerce(tb, cs[n].col, w.val.value(args)); err != nil {
			return nil, false, err
		}
		if cs[n].val == nil {
			none = true
		}
	}
	return cs, none, nil
}

// fixedKey is how many of the key's leading columns cs fixes by equality.
func fixedKey(tb *table, cs []resolved) int {
	n := 0
	for _, i := range tb.key {
		if !slices.ContainsFunc(cs, func(c resolved) bool { return c.idx == i && c.op == "=" }) {
			break
		}
		n++
	}
	return n
}

func rangeErr(tb *table, c resolved) *Error {
	keys := tb.keyNames()
	why := fmt.Sprintf("%s is not in the PRIMARY KEY (%s) of %s", c.col.name, strings.Join(keys, ", "), tb.name)
	if tb.keyPos(c.idx) >= 0 {
		why = fmt.Sprintf("the key columns before %s in PRIMARY KEY (%s) are not all fixed by equality", c.col.name, strings.Join(keys, ", "))
	}
	example := keys[len(keys)-1] + " > ?"
	if len(keys) > 1 {
		example = strings.Join(keys[:len(keys)-1], " = ? AND ") + " = ? AND " + example
	}
	return &Error{Code: "42601", Message: fmt.Sprintf("WHERE %s %s ...: %s (a range is on a key column after those fixed by equality: WHERE %s); %s", c.col.name, c.op, why, example, subset)}
}

func matches(r map[string]any, cs []resolved) bool {
	for _, c := range cs {
		v := r[c.col.name]
		if v == nil || c.val == nil {
			return false // a comparison with NULL holds for no row
		}
		switch c.op {
		case "=":
			if !equal(v, c.val) {
				return false
			}
		case "<":
			if compare(v, c.val) >= 0 {
				return false
			}
		case "<=":
			if compare(v, c.val) > 0 {
				return false
			}
		case ">":
			if compare(v, c.val) <= 0 {
				return false
			}
		case ">=":
			if compare(v, c.val) < 0 {
				return false
			}
		}
	}
	return true
}

func undefinedColumn(tb *table, col string) *Error {
	return &Error{Code: "42703", Message: fmt.Sprintf("column %q of relation %q does not exist (its columns: %s)", col, tb.name, strings.Join(tb.colNames(), ", "))}
}

// keyRange is the range of row keys that cs's conditions on the key
// leave, the first fixed key columns fixed: the prefix of their values,
// and within it the bounds on the next key column's ranges.
func keyRange(tb *table, cs []resolved, fixed int) qenv.KeyRange {
	r := qenv.KeyRange{Prefix: tb.name + "/"}
	one := len(tb.key) == 1
	for _, i := range tb.key[:fixed] {
		for _, c := range cs {
			if c.idx == i && c.op == "=" {
				r.Prefix += component(c.val)
				break
			}
		}
	}
	if fixed == len(tb.key) {
		return r
	}
	next := tb.key[fixed]
	for _, c := range cs {
		if c.idx != next || c.op == "=" {
			continue
		}
		// a key of one column has no component's terminator: the key
		// after k is k+NUL. In a key of several, the keys whose next
		// column is v are those between v's encoding+NUL 0x01 and +NUL 0x02.
		var at, after string
		if one {
			at = r.Prefix + encodeKey(c.val)
			after = at + "\x00"
		} else {
			base := r.Prefix + escapeNUL(encodeKey(c.val))
			at, after = base+"\x00\x01", base+"\x00\x02"
		}
		var from, to string
		switch c.op {
		case ">=":
			from = at
		case ">":
			from = after
		case "<":
			to = at
		case "<=":
			to = after
		}
		if from > r.From {
			r.From = from
		}
		if to != "" && (r.To == "" || to < r.To) {
			r.To = to
		}
	}
	return r
}

// find reads the rows meeting cs, in key order: by key when every key
// column is fixed by equality, through the index when a UNIQUE column
// is, by a range of the key when its leading columns are fixed or its
// first is ranged, else by scanning the table. It reads without locking;
// lock re-reads each under its lock.
func (t *tx) find(tb *table, cs []resolved) []row {
	pick := func(r row, ok bool) []row {
		if ok && matches(r.val, cs) {
			return []row{r}
		}
		return nil
	}
	fixed := fixedKey(tb, cs)
	scan := qenv.KeyRange{Prefix: tb.name + "/"}
	if !t.c.scanOnly {
		if fixed == len(tb.key) {
			vals := map[string]any{}
			for _, c := range cs {
				if c.op == "=" && tb.keyPos(c.idx) >= 0 {
					if _, ok := vals[c.col.name]; !ok {
						vals[c.col.name] = c.val
					}
				}
			}
			k := tb.rowKey(vals)
			t.read[k] = true
			v, ok := t.txn.Get(k)
			return pick(row{k, asRow(v)}, ok)
		}
		for _, c := range cs {
			if !c.col.unique || c.op != "=" {
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
		scan = keyRange(tb, cs, fixed)
	}
	var out []row
	t.scanned = append(t.scanned, scan)
	if t.c.scanHook != nil {
		t.c.scanHook(scan)
	}
	for _, kv := range t.txn.ScanRange(scan) {
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
// an UPDATE's new unique value) and reports whether it is taken, with the
// value there, with PostgreSQL's codes. Under a snapshot the Store's
// GetForUpdate aborts (ErrConflict) on a key committed after the
// snapshot, but PostgreSQL's unique check reads the latest committed
// rows, whatever the level: a key another transaction inserted and
// committed is a duplicate (23505), unless the transaction had read it
// and found it absent at serializable (its predicate lock: 40001), or
// this is ON CONFLICT's arbiter, whose conflicting row is not in the
// snapshot (40001, as PostgreSQL's ExecCheckTupleVisible). A key deleted
// after the snapshot is 40001 here where PostgreSQL would insert: the
// Store has aborted the transaction by then. Either way the Store aborted
// it: nothing commits that its level forbids.
func (t *tx) claim(k string, arbiter bool) (val any, taken bool, err error) {
	val, exists, err := t.txn.GetForUpdate(k)
	if err == nil || errors.Is(err, qenv.ErrDeadlock) || !errors.Is(err, qenv.ErrConflict) {
		return val, exists, err
	}
	if _, live := t.c.st.Read(k); !live || arbiter || t.c.st.Iso == qenv.Serializable && t.wasRead(k) {
		return nil, false, err
	}
	return nil, true, nil
}

// wasRead reports whether the transaction read key k (a lookup, or a scan
// of a range holding it).
func (t *tx) wasRead(k string) bool {
	if t.read[k] {
		return true
	}
	for _, r := range t.scanned {
		if r.Contains(k) {
			return true
		}
		// a scan of the whole table read every row, and so every unique value
		if tb := strings.TrimSuffix(r.Prefix, "/"); r.From == "" && r.To == "" && !strings.Contains(tb, "/") && strings.HasPrefix(k, tb+"!") {
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
	v := s.limit.value(args)
	if v == nil {
		return -1, nil // LIMIT NULL, or a nil argument: no limit, as in Postgres
	}
	n, ok := v.(int64)
	if !ok || n < 0 {
		return 0, &Error{Code: "2201W", Message: fmt.Sprintf("LIMIT must be a non-negative integer; got %T %v", s.limit.value(args), s.limit.value(args))}
	}
	return n, nil
}

// order checks ORDER BY against the key and reports whether it is
// descending. Rows come in key order, so ORDER BY names key columns in
// key order, one direction for all, every key column it skips before
// its last fixed by equality: then key order is the order asked for
// (ties in the columns after it in key order too, reversed with DESC).
// A column fixed by equality (a key's or any other) holds one value in
// every row, so it orders nothing: it may be named anywhere, in either
// direction.
func order(tb *table, terms []orderTerm, cs []resolved) (desc bool, err error) {
	refuse := func(why string) (bool, error) {
		var cols []string
		for _, o := range terms {
			cols = append(cols, o.col)
		}
		return false, &Error{Code: "42601", Message: fmt.Sprintf("ORDER BY %s: rows come in primary-key order only (ORDER BY %s [DESC]); %s; %s", strings.Join(cols, ", "), strings.Join(tb.keyNames(), ", "), why, subset)}
	}
	fixed := map[int]bool{}
	for _, c := range cs {
		if c.op == "=" {
			fixed[c.idx] = true
		}
	}
	at := -1
	var first *orderTerm // the first term that orders: its direction is every one's
	for _, o := range terms {
		i, ok := tb.col(o.col)
		if !ok {
			return false, undefinedColumn(tb, o.col)
		}
		if fixed[i] {
			continue
		}
		p := tb.keyPos(i)
		if p < 0 {
			return refuse(fmt.Sprintf("%s is not a key column", o.col))
		}
		if p <= at {
			return refuse("the key's columns go in key order")
		}
		for _, j := range tb.key[at+1 : p] {
			if !fixed[j] {
				return refuse(fmt.Sprintf("ORDER BY skips the key column %s, which the WHERE clause does not fix by equality", tb.cols[j].name))
			}
		}
		if first == nil {
			first = &o
		}
		if o.desc != first.desc {
			return refuse(fmt.Sprintf("%s's direction differs from %s's (one direction for all the columns not fixed by equality)", o.col, first.col))
		}
		at = p
	}
	return first != nil && first.desc, nil
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
	if s.join != nil {
		return t.joinQuery(s, tb, args)
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
	limit, err := t.limit(s, args)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil {
		return nil, err
	}
	desc, err := order(tb, s.orderBy, cs)
	if err != nil || none {
		return &result{cols: cols}, err
	}
	rows := t.find(tb, cs)
	if desc {
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
		res.rows = append(res.rows, project(r.val, cols))
	}
	return res, nil
}

// joinQuery runs a SELECT with a JOIN: the statement's table's rows as a
// query of it alone finds them, in its order, and for each the joined
// table's row whose key ON names, by key (a read, a seam, per row, as
// find's unique lookup; at read committed a commit may fall between
// them, where PostgreSQL reads the statement in one snapshot: a join of
// rows that never change once committed, as grist's events, reads the
// same). A row with no joined row, or one ON's other columns do not
// match, is left out (an inner join); LIMIT counts the joined rows.
func (t *tx) joinQuery(s *selectStmt, tb *table, args []any) (*result, error) {
	j := s.join
	ub, err := t.table(j.table)
	if err != nil {
		return nil, err
	}
	of := func(table string) *table {
		if table == tb.name {
			return tb
		}
		return ub
	}
	var cols []string
	for _, c := range j.cols {
		if _, ok := of(c.table).col(c.col); !ok {
			return nil, undefinedColumn(of(c.table), c.col)
		}
		cols = append(cols, c.col)
	}
	// on: each of u's columns ON names, with t's column it equals
	type eq struct {
		t, u colDef
	}
	var on []eq
	for _, pair := range j.on {
		ti, ok := tb.col(pair[0].col)
		if !ok {
			return nil, undefinedColumn(tb, pair[0].col)
		}
		ui, ok := ub.col(pair[1].col)
		if !ok {
			return nil, undefinedColumn(ub, pair[1].col)
		}
		on = append(on, eq{tb.cols[ti], ub.cols[ui]})
	}
	for _, i := range ub.key {
		if !slices.ContainsFunc(on, func(e eq) bool { return e.u.name == ub.cols[i].name }) {
			return nil, &Error{Code: "42601", Message: fmt.Sprintf("JOIN %s ON ...: ON does not fix %s.%s, a column of its PRIMARY KEY (%s): a join looks the joined table's rows up by its whole key (ON %s); %s",
				ub.name, ub.name, ub.cols[i].name, strings.Join(ub.keyNames(), ", "), joinExample(tb, ub), subset)}
		}
	}
	limit, err := t.limit(s, args)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil {
		return nil, err
	}
	desc, err := order(tb, s.orderBy, cs)
	if err != nil || none {
		return &result{cols: cols}, err
	}
	rows := t.find(tb, cs)
	if desc {
		slices.Reverse(rows)
	}
	res := &result{cols: cols}
	for _, r := range rows {
		if limit >= 0 && int64(len(res.rows)) >= limit {
			break
		}
		key := map[string]any{}
		null := false
		for _, e := range on {
			v := r.val[e.t.name]
			if v == nil {
				null = true // NULL equals nothing
				break
			}
			if v, err = coerce(ub, e.u, v); err != nil {
				return nil, err
			}
			key[e.u.name] = v
		}
		if null {
			continue
		}
		k := ub.rowKey(key)
		t.read[k] = true
		v, ok := t.txn.Get(k)
		if !ok {
			continue
		}
		u := asRow(v)
		if !slices.ContainsFunc(on, func(e eq) bool { return !equal(u[e.u.name], key[e.u.name]) }) {
			out := make([]any, len(j.cols))
			for i, c := range j.cols {
				row := r.val
				if c.table != tb.name {
					row = u
				}
				out[i] = project(row, []string{c.col})[0]
			}
			res.rows = append(res.rows, out)
		}
	}
	res.affected = int64(len(res.rows))
	return res, nil
}

// joinExample is an ON clause that fixes u's key by t's same-named
// columns, for an error's fix.
func joinExample(tb, ub *table) string {
	var on []string
	for _, k := range ub.keyNames() {
		on = append(on, fmt.Sprintf("%s.%s = %s.%s", ub.name, k, tb.name, k))
	}
	return strings.Join(on, " AND ")
}

// project is a row's values of cols, bytes copied.
func project(r map[string]any, cols []string) []any {
	out := make([]any, len(cols))
	for i, c := range cols {
		out[i] = r[c]
		if b, ok := out[i].([]byte); ok {
			out[i] = bytes.Clone(b)
		}
	}
	return out
}

// returned checks a RETURNING clause's columns and returns them (nil: no
// clause).
func returned(tb *table, r returning) ([]string, error) {
	if !r.on {
		return nil, nil
	}
	if r.cols == nil {
		return tb.colNames(), nil
	}
	for _, c := range r.cols {
		if _, ok := tb.col(c); !ok {
			return nil, undefinedColumn(tb, c)
		}
	}
	return r.cols, nil
}

func notNull(tb *table, r map[string]any) error {
	for _, c := range tb.cols {
		if c.notNull && r[c.name] == nil {
			return &Error{Code: "23502", Message: fmt.Sprintf("null value in column %q of relation %q violates not-null constraint", c.name, tb.name)}
		}
	}
	return nil
}

// constraint is a uniqueness a row must keep: the primary key (cols, the
// key's) or a UNIQUE column.
type constraint struct {
	cols    []int
	primary bool
}

// storeKey is the Store key that holds r's value of the constraint, and
// false if a column of it is NULL (NULLs never conflict).
func (c constraint) storeKey(tb *table, r map[string]any) (string, bool) {
	for _, i := range c.cols {
		if r[tb.cols[i].name] == nil {
			return "", false
		}
	}
	if c.primary {
		return tb.rowKey(r), true
	}
	n := tb.cols[c.cols[0]].name
	return tb.indexKey(n, r[n]), true
}

func uniqueViolation(tb *table, c constraint, r map[string]any) *Error {
	var names []string
	var vals []string
	for _, i := range c.cols {
		names = append(names, tb.cols[i].name)
		vals = append(vals, fmt.Sprint(r[tb.cols[i].name]))
	}
	constraint := tb.name + "_" + names[0] + "_key"
	if c.primary {
		constraint = tb.name + "_pkey"
	}
	return &Error{Code: "23505", Message: fmt.Sprintf("duplicate key value violates unique constraint %q: Key (%s)=(%s) already exists", constraint, strings.Join(names, ", "), strings.Join(vals, ", "))}
}

// target resolves ON CONFLICT (cols): the primary key's columns, in any
// order, or one UNIQUE column.
func target(tb *table, cols []string) (constraint, error) {
	var idx []int
	for _, c := range cols {
		i, ok := tb.col(c)
		if !ok {
			return constraint{}, undefinedColumn(tb, c)
		}
		idx = append(idx, i)
	}
	if len(idx) == len(tb.key) && !slices.ContainsFunc(idx, func(i int) bool { return tb.keyPos(i) < 0 }) && len(uniq(idx)) == len(idx) {
		return constraint{cols: tb.key, primary: true}, nil
	}
	if len(idx) == 1 && tb.cols[idx[0]].unique {
		return constraint{cols: idx}, nil
	}
	return constraint{}, &Error{Code: "42P10", Message: fmt.Sprintf("there is no unique or exclusion constraint matching the ON CONFLICT specification (%s): name the PRIMARY KEY's columns (%s) or one UNIQUE column", strings.Join(cols, ", "), strings.Join(tb.keyNames(), ", "))}
}

func uniq(idx []int) []int {
	s := slices.Clone(idx)
	slices.Sort(s)
	return slices.Compact(s)
}

// resolvedSet is an assignment of SET, its value coerced (or, for
// excluded.col, the proposed row's column).
type resolvedSet struct {
	col colDef
	op  byte
	val any
	ref string
}

// resolveSets checks SET's assignments against tb: no key column, the
// arithmetic on integers, excluded.col (in an upsert) a column of tb's of
// the same type.
func resolveSets(tb *table, sets []setClause, args []any) ([]resolvedSet, error) {
	var out []resolvedSet
	for _, sc := range sets {
		j, ok := tb.col(sc.col)
		if !ok {
			return nil, undefinedColumn(tb, sc.col)
		}
		c := tb.cols[j]
		if tb.keyPos(j) >= 0 {
			return nil, &Error{Code: "0A000", Message: fmt.Sprintf("UPDATE of %s's primary key %s is not supported (DELETE the row and INSERT it anew)", tb.name, c.name)}
		}
		if sc.op != '=' && c.typ != "integer" {
			return nil, &Error{Code: "42883", Message: fmt.Sprintf("operator %c on column %q of type %s: arithmetic is on INTEGER columns only", sc.op, c.name, strings.ToUpper(c.typ))}
		}
		if sc.val.ref != "" {
			k, ok := tb.col(sc.val.ref)
			if !ok {
				return nil, undefinedColumn(tb, sc.val.ref)
			}
			if tb.cols[k].typ != c.typ {
				return nil, &Error{Code: "42804", Message: fmt.Sprintf("column %q of %q is %s; excluded.%s is %s (set a column from one of its type)", c.name, tb.name, strings.ToUpper(c.typ), sc.val.ref, strings.ToUpper(tb.cols[k].typ))}
			}
			out = append(out, resolvedSet{col: c, op: sc.op, ref: sc.val.ref})
			continue
		}
		v, err := coerce(tb, c, sc.val.value(args))
		if err != nil {
			return nil, err
		}
		out = append(out, resolvedSet{col: c, op: sc.op, val: v})
	}
	return out, nil
}

// assign is old with sets applied (excluded: the proposed row, for
// excluded.col), checked against NOT NULL.
func assign(tb *table, old map[string]any, sets []resolvedSet, excluded map[string]any) (map[string]any, error) {
	nr := copyRow(old)
	for _, st := range sets {
		val := st.val
		if st.ref != "" {
			val = excluded[st.ref]
		}
		if st.op == '=' {
			nr[st.col.name] = val
			continue
		}
		cur, _ := nr[st.col.name].(int64) // from the locked row: no update is lost
		if nr[st.col.name] == nil || val == nil {
			nr[st.col.name] = nil
			continue
		}
		d := val.(int64)
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
	return nr, nil
}

// rewrite writes r's new value nr: its UNIQUE columns' index entries
// moved (a value another row holds is 23505), the row, its change.
func (t *tx) rewrite(tb *table, r row, nr map[string]any) error {
	for j, c := range tb.cols {
		if !c.unique || tb.keyPos(j) >= 0 || equal(nr[c.name], r.val[c.name]) || nr[c.name] == nil && r.val[c.name] == nil {
			continue
		}
		if nr[c.name] != nil {
			k := tb.indexKey(c.name, nr[c.name])
			if _, exists, err := t.claim(k, false); err != nil {
				return err
			} else if exists {
				return uniqueViolation(tb, constraint{cols: []int{j}}, nr)
			}
			t.txn.Put(k, r.key)
		}
		if r.val[c.name] != nil {
			t.txn.Delete(tb.indexKey(c.name, r.val[c.name]))
		}
	}
	t.txn.Put(r.key, nr)
	t.changed(Change{Table: tb.name, Op: "update", Old: r.val, New: nr})
	return nil
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
	ret, err := returned(tb, s.ret)
	if err != nil {
		return nil, err
	}
	// the constraints a row may conflict on, ON CONFLICT's target first:
	// PostgreSQL checks the arbiter, and a conflict there does nothing (or
	// updates) even where another constraint would conflict too
	var keys []constraint
	hasTarget := s.target != nil
	if hasTarget {
		c, err := target(tb, s.target)
		if err != nil {
			return nil, err
		}
		keys = append(keys, c)
	}
	if !hasTarget || !keys[0].primary {
		keys = append(keys, constraint{cols: tb.key, primary: true})
	}
	for j, c := range tb.cols {
		if c.unique && tb.keyPos(j) < 0 && !(hasTarget && !keys[0].primary && keys[0].cols[0] == j) {
			keys = append(keys, constraint{cols: []int{j}})
		}
	}
	var sets []resolvedSet
	if s.doUpdate != nil {
		if sets, err = resolveSets(tb, s.doUpdate, args); err != nil {
			return nil, err
		}
	}
	var updWhere []resolved
	if s.updWhere != nil {
		for _, w := range s.updWhere { // a condition on the existing row, any column
			i, ok := tb.col(w.col)
			if !ok {
				return nil, undefinedColumn(tb, w.col)
			}
			v, err := coerce(tb, tb.cols[i], w.val.value(args))
			if err != nil {
				return nil, err
			}
			updWhere = append(updWhere, resolved{col: tb.cols[i], idx: i, op: w.op, val: v})
		}
	}
	res := &result{cols: ret}
	touched := map[string]bool{} // the rows this statement inserted or updated
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
		for n, c := range keys {
			k, ok := c.storeKey(tb, r)
			if !ok {
				continue
			}
			arbiter := s.onConflict && (!hasTarget || n == 0)
			cur, exists, err := t.claim(k, arbiter)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
			if !arbiter {
				return nil, uniqueViolation(tb, c, r)
			}
			if sets == nil {
				continue rows // DO NOTHING
			}
			// DO UPDATE: the existing row, locked (the arbiter's key is
			// the row's, or a UNIQUE value's whose row is then locked)
			old := row{key: k, val: asRow(cur)}
			if !c.primary {
				old.key = cur.(string)
				v, ok, err := t.txn.GetForUpdate(old.key)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("qsql: the index of %s names the row %s, which is gone", tb.name, old.key)
				}
				old.val = asRow(v)
			}
			if touched[old.key] {
				return nil, &Error{Code: "21000", Message: fmt.Sprintf("ON CONFLICT DO UPDATE command cannot affect row a second time (two rows of the INSERT into %s have one key: give each key once)", tb.name)}
			}
			if !matches(old.val, updWhere) {
				continue rows // DO UPDATE ... WHERE false: the row stays, locked
			}
			nr, err := assign(tb, old.val, sets, r)
			if err != nil {
				return nil, err
			}
			if err := t.rewrite(tb, old, nr); err != nil {
				return nil, err
			}
			touched[old.key] = true
			res.affected++
			if ret != nil {
				res.rows = append(res.rows, project(nr, ret))
			}
			continue rows
		}
		key := tb.rowKey(r)
		t.txn.Put(key, r)
		for j, c := range tb.cols {
			if c.unique && tb.keyPos(j) < 0 && r[c.name] != nil {
				t.txn.Put(tb.indexKey(c.name, r[c.name]), key)
			}
		}
		touched[key] = true
		t.changed(Change{Table: tb.name, Op: "insert", New: r})
		res.affected++
		if ret != nil {
			res.rows = append(res.rows, project(r, ret))
		}
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
	sets, err := resolveSets(tb, s.sets, args)
	if err != nil {
		return nil, err
	}
	ret, err := returned(tb, s.ret)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil || none {
		return &result{cols: ret}, err
	}
	rows, err := t.lock(t.find(tb, cs), cs, -1)
	if err != nil {
		return nil, err
	}
	res := &result{cols: ret, affected: int64(len(rows))}
	for _, r := range rows {
		nr, err := assign(tb, r.val, sets, nil)
		if err != nil {
			return nil, err
		}
		if err := t.rewrite(tb, r, nr); err != nil {
			return nil, err
		}
		if ret != nil {
			res.rows = append(res.rows, project(nr, ret))
		}
	}
	return res, nil
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
	ret, err := returned(tb, s.ret)
	if err != nil {
		return nil, err
	}
	cs, none, err := t.conds(tb, s.where, args)
	if err != nil || none {
		return &result{cols: ret}, err
	}
	rows, err := t.lock(t.find(tb, cs), cs, -1)
	if err != nil {
		return nil, err
	}
	res := &result{cols: ret, affected: int64(len(rows))}
	for _, r := range rows {
		t.txn.Delete(r.key)
		for j, c := range tb.cols {
			if c.unique && tb.keyPos(j) < 0 && r.val[c.name] != nil {
				t.txn.Delete(tb.indexKey(c.name, r.val[c.name]))
			}
		}
		t.changed(Change{Table: tb.name, Op: "delete", Old: r.val})
		if ret != nil {
			res.rows = append(res.rows, project(r.val, ret))
		}
	}
	return res, nil
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
