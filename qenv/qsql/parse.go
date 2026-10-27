package qsql

import (
	"fmt"
	"strconv"
	"strings"
)

// subset is what the parser accepts, for every error that finds something
// else: the fix is to write the statement in it, or to keep the query out
// of the simulation.
const subset = "qsql runs one table at a time, with equality, and ranges on a key column: " +
	"CREATE TABLE [IF NOT EXISTS] t (col TYPE [PRIMARY KEY] [UNIQUE] [NOT NULL], ... [, PRIMARY KEY (col, ...)]) with TEXT, INTEGER, BIGINT, BOOLEAN, BLOB, BYTEA, TIMESTAMP; " +
	"INSERT INTO t [(cols)] VALUES (...)[, (...)] [ON CONFLICT [(cols)] DO NOTHING | ON CONFLICT (cols) DO UPDATE SET col = ? | col = excluded.col | col = col + ?, ... [WHERE ...]] [RETURNING cols|*]; " +
	"SELECT cols|* FROM t [WHERE col = ? AND ... [AND k > ? AND k <= ? | AND k BETWEEN ? AND ?]] [ORDER BY key cols [ASC|DESC]] [LIMIT n] [FOR UPDATE]; " +
	"UPDATE t SET col = ? | col = col + ? | col = col - ?, ... [WHERE ...] [RETURNING cols|*]; " +
	"DELETE FROM t [WHERE ...] [RETURNING cols|*]; " +
	"a range (<, <=, >, >=, BETWEEN) is on one column of the PRIMARY KEY, every key column before it fixed by equality; " +
	"placeholders ? or $n, transactions with BeginTx"

// ---- lexer ------------------------------------------------------------------

type tokKind int

const (
	tEOF    tokKind = iota
	tIdent          // a name or keyword, unquoted (folded to lower case) or "quoted"
	tNumber         // an integer literal
	tString         // a 'string' literal, unescaped
	tParam          // ? or $n
	tPunct          // ( ) , = * + - ; and anything else of one character
)

type token struct {
	kind   tokKind
	text   string // an identifier folded, a string unescaped, punctuation as written
	raw    string // as written, for errors
	pos    int
	quoted bool // a "quoted" identifier: never a keyword
	n      int  // a $n parameter's number (0 for ?)
}

func (t token) String() string {
	if t.kind == tEOF {
		return "the end of the statement"
	}
	return fmt.Sprintf("%q", t.raw)
}

func lex(q string) ([]token, error) {
	var out []token
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case isLetter(c):
			j := i
			for j < len(q) && (isLetter(q[j]) || isDigit(q[j])) {
				j++
			}
			out = append(out, token{kind: tIdent, text: strings.ToLower(q[i:j]), raw: q[i:j], pos: i})
			i = j
		case c == '"':
			s, j, ok := quotedRun(q, i, '"')
			if !ok {
				return nil, syntaxErr(q, i, "an unterminated quoted identifier")
			}
			out = append(out, token{kind: tIdent, text: s, raw: q[i:j], pos: i, quoted: true})
			i = j
		case c == '\'':
			s, j, ok := quotedRun(q, i, '\'')
			if !ok {
				return nil, syntaxErr(q, i, "an unterminated string")
			}
			out = append(out, token{kind: tString, text: s, raw: q[i:j], pos: i})
			i = j
		case isDigit(c):
			j := i
			for j < len(q) && isDigit(q[j]) {
				j++
			}
			if j < len(q) && (q[j] == '.' || q[j] == 'e' || q[j] == 'E') {
				return nil, syntaxErr(q, i, fmt.Sprintf("the number %q (only integers: there is no REAL type)", q[i:j+1]))
			}
			out = append(out, token{kind: tNumber, text: q[i:j], raw: q[i:j], pos: i})
			i = j
		case c == '?':
			out = append(out, token{kind: tParam, raw: "?", pos: i})
			i++
		case c == '$':
			j := i + 1
			for j < len(q) && isDigit(q[j]) {
				j++
			}
			n, err := strconv.Atoi(q[i+1 : j])
			if err != nil || n < 1 {
				return nil, syntaxErr(q, i, fmt.Sprintf("the placeholder %q (write $1, $2, ...)", q[i:j]))
			}
			out = append(out, token{kind: tParam, raw: q[i:j], pos: i, n: n})
			i = j
		default:
			out = append(out, token{kind: tPunct, text: string(c), raw: string(c), pos: i})
			i++
		}
	}
	return append(out, token{kind: tEOF, pos: len(q)}), nil
}

// quotedRun reads a run quoted by quote from q[i], a doubled quote standing
// for one: the text, the index after it, and whether it was closed.
func quotedRun(q string, i int, quote byte) (string, int, bool) {
	var b strings.Builder
	for j := i + 1; j < len(q); j++ {
		if q[j] != quote {
			b.WriteByte(q[j])
			continue
		}
		if j+1 < len(q) && q[j+1] == quote {
			b.WriteByte(quote)
			j++
			continue
		}
		return b.String(), j + 1, true
	}
	return "", len(q), false
}

func isLetter(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// syntaxErr is a 42601 naming what was found where, and the subset.
func syntaxErr(q string, pos int, found string) *Error {
	return &Error{Code: "42601", Message: fmt.Sprintf("found %s at offset %d of %q; %s", found, pos, q, subset)}
}

// ---- statements -------------------------------------------------------------

// statement is a parsed statement; params is how many arguments it takes.
type statement interface{ params() int }

type expr struct {
	param int    // 1-based argument number; 0: lit or ref
	lit   any    // nil (NULL), int64, string, bool
	ref   string // DO UPDATE's excluded.<ref>: a column of the row the INSERT proposed
}

// cond is one conjunct of a WHERE clause: col op val. BETWEEN a AND b is
// two, >= a and <= b.
type cond struct {
	col string
	op  string // "=", "<", "<=", ">", ">="
	val expr
}

type colDef struct {
	name, typ                string
	primary, unique, notNull bool
}

type createStmt struct {
	n           int
	table       string
	ifNotExists bool
	cols        []colDef
	key         []string // PRIMARY KEY (a, b, ...), the table constraint; nil: a column's PRIMARY KEY
}

// returning is a RETURNING clause: on, with its columns (nil: *).
type returning struct {
	on   bool
	cols []string
}

type insertStmt struct {
	n          int
	table      string
	cols       []string // nil: every column, in the table's order
	rows       [][]expr
	onConflict bool
	target     []string    // ON CONFLICT (target); nil: any unique constraint (DO NOTHING only)
	doUpdate   []setClause // ON CONFLICT ... DO UPDATE SET; nil: DO NOTHING
	updWhere   []cond      // DO UPDATE ... WHERE, on the existing row
	ret        returning
}

// orderTerm is one column of ORDER BY.
type orderTerm struct {
	col  string
	desc bool
}

type selectStmt struct {
	n         int
	table     string
	cols      []string // nil: *
	where     []cond
	orderBy   []orderTerm
	limit     *expr
	forUpdate bool
}

type setClause struct {
	col string
	op  byte // '=' (col = val), '+' or '-' (col = col ± val)
	val expr
}

type updateStmt struct {
	n     int
	table string
	sets  []setClause
	where []cond
	ret   returning
}

type deleteStmt struct {
	n     int
	table string
	where []cond
	ret   returning
}

func (s *createStmt) params() int { return s.n }
func (s *insertStmt) params() int { return s.n }
func (s *selectStmt) params() int { return s.n }
func (s *updateStmt) params() int { return s.n }
func (s *deleteStmt) params() int { return s.n }

// ---- parser -----------------------------------------------------------------

type parser struct {
	q      string
	toks   []token
	i      int
	nextQ  int  // the next ? parameter's number
	max    int  // the highest parameter number
	dollar bool // $n seen
	quest  bool // ? seen
}

// parse parses one statement of the subset.
func parse(q string) (statement, error) {
	toks, err := lex(q)
	if err != nil {
		return nil, err
	}
	p := &parser{q: q, toks: toks}
	st, err := p.statement()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tPunct && p.peek().text == ";" {
		p.i++
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, p.unexpected(t, "one statement per call")
	}
	if p.dollar && p.quest {
		return nil, &Error{Code: "42601", Message: fmt.Sprintf("%q mixes ? and $n placeholders: use one style", q)}
	}
	return st, nil
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

// unexpected is the error for a token the grammar has no place for.
func (p *parser) unexpected(t token, want string) *Error {
	return syntaxErr(p.q, t.pos, fmt.Sprintf("%s where %s goes", t, want))
}

// isKw reports whether t is the keyword kw (lower case), never quoted.
func isKw(t token, kw string) bool { return t.kind == tIdent && !t.quoted && t.text == kw }

func (p *parser) kw(kw string) bool {
	if isKw(p.peek(), kw) {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectKw(kws ...string) error {
	for _, kw := range kws {
		if !p.kw(kw) {
			return p.unexpected(p.peek(), strings.ToUpper(kw))
		}
	}
	return nil
}

func (p *parser) punct(c string) bool {
	if t := p.peek(); t.kind == tPunct && t.text == c {
		p.i++
		return true
	}
	return false
}

// adjacent reports whether the next token is the punctuation c written
// right after the previous one (the "=" of "<=").
func (p *parser) adjacent(c string) bool {
	if t := p.peek(); t.kind == tPunct && t.text == c && t.pos == p.toks[p.i-1].pos+1 {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectPunct(c string) error {
	if !p.punct(c) {
		return p.unexpected(p.peek(), fmt.Sprintf("%q", c))
	}
	return nil
}

// reserved are the keywords that end a clause: never a name where a
// name is expected. Read-only.
var reserved = map[string]bool{
	"select": true, "from": true, "where": true, "and": true, "or": true, "not": true, "order": true, "by": true,
	"limit": true, "for": true, "insert": true, "into": true, "values": true, "update": true, "set": true,
	"delete": true, "create": true, "table": true, "on": true, "conflict": true, "do": true, "nothing": true,
	"null": true, "true": true, "false": true, "join": true, "group": true, "having": true, "returning": true,
	"as": true, "in": true, "is": true, "like": true, "offset": true, "union": true, "distinct": true,
	"between": true, "primary": true,
}

func (p *parser) name(what string) (string, error) {
	t := p.peek()
	if t.kind != tIdent || (!t.quoted && reserved[t.text]) {
		return "", p.unexpected(t, what)
	}
	p.i++
	return t.text, nil
}

// column reads a column's name, which may be qualified by the statement's
// table (t.col).
func (p *parser) column(table, what string) (string, error) {
	n, err := p.name(what)
	if err != nil {
		return "", err
	}
	if t := p.peek(); t.kind == tPunct && t.text == "." {
		p.i++
		if n != table {
			return "", syntaxErr(p.q, t.pos, fmt.Sprintf("the qualifier %s. (columns of the statement's table %s only: one table at a time)", n, table))
		}
		return p.name(what)
	}
	return n, nil
}

func (p *parser) statement() (statement, error) {
	t := p.next()
	switch {
	case isKw(t, "create"):
		return p.create()
	case isKw(t, "insert"):
		return p.insert()
	case isKw(t, "select"):
		return p.selectStmt()
	case isKw(t, "update"):
		return p.update()
	case isKw(t, "delete"):
		return p.delete()
	case isKw(t, "begin"), isKw(t, "commit"), isKw(t, "rollback"), isKw(t, "start"), isKw(t, "savepoint"):
		return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("%s (begin a transaction with db.BeginTx and end it with Commit or Rollback)", t))
	}
	return nil, p.unexpected(t, "CREATE, INSERT, SELECT, UPDATE or DELETE")
}

func (p *parser) create() (statement, error) {
	if err := p.expectKw("table"); err != nil {
		return nil, err
	}
	s := &createStmt{}
	if p.kw("if") {
		if err := p.expectKw("not", "exists"); err != nil {
			return nil, err
		}
		s.ifNotExists = true
	}
	var err error
	if s.table, err = p.name("a table name"); err != nil {
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	for {
		if t := p.peek(); isKw(t, "primary") {
			// the table constraint PRIMARY KEY (a, b, ...): a key of several columns
			p.i++
			if err := p.expectKw("key"); err != nil {
				return nil, err
			}
			if s.key != nil {
				return nil, syntaxErr(p.q, t.pos, "a second PRIMARY KEY (a table has one)")
			}
			if err := p.expectPunct("("); err != nil {
				return nil, err
			}
			if s.key, err = p.names(); err != nil {
				return nil, err
			}
		} else if isKw(t, "unique") || isKw(t, "constraint") || isKw(t, "foreign") || isKw(t, "check") {
			return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("the table constraint %s (PRIMARY KEY (cols) is the one table constraint; declare UNIQUE on one column)", t))
		} else {
			c, err := p.colDef()
			if err != nil {
				return nil, err
			}
			s.cols = append(s.cols, c)
		}
		if p.punct(")") {
			return s, nil
		}
		if err := p.expectPunct(","); err != nil {
			return nil, err
		}
	}
}

func (p *parser) colDef() (colDef, error) {
	var c colDef
	var err error
	if c.name, err = p.name("a column name"); err != nil {
		return c, err
	}
	tt := p.peek()
	typ, ok := types[tt.text]
	if tt.kind != tIdent || tt.quoted || !ok {
		return c, p.unexpected(tt, "a column type (TEXT, INTEGER, BIGINT, BOOLEAN, BLOB, BYTEA, TIMESTAMP)")
	}
	p.i++
	c.typ = typ
	for {
		t := p.peek()
		switch {
		case isKw(t, "primary"):
			p.i++
			if err := p.expectKw("key"); err != nil {
				return c, err
			}
			c.primary = true
		case isKw(t, "unique"):
			p.i++
			c.unique = true
		case isKw(t, "not"):
			p.i++
			if err := p.expectKw("null"); err != nil {
				return c, err
			}
			c.notNull = true
		case isKw(t, "null"):
			p.i++
		case t.kind == tPunct && (t.text == "," || t.text == ")"):
			return c, nil
		default:
			return c, p.unexpected(t, "PRIMARY KEY, UNIQUE, NOT NULL, \",\" or \")\"")
		}
	}
}

// types maps a type's names to its canonical one. Read-only.
var types = map[string]string{
	"text": "text", "integer": "integer", "bigint": "integer", "int": "integer",
	"boolean": "boolean", "bool": "boolean", "blob": "blob", "bytea": "blob",
	"timestamp": "timestamp", "timestamptz": "timestamp",
}

func (p *parser) insert() (statement, error) {
	if err := p.expectKw("into"); err != nil {
		return nil, err
	}
	s := &insertStmt{}
	var err error
	if s.table, err = p.name("a table name"); err != nil {
		return nil, err
	}
	if p.punct("(") {
		if s.cols, err = p.names(); err != nil {
			return nil, err
		}
	}
	if t := p.peek(); isKw(t, "select") || isKw(t, "default") {
		return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("INSERT ... %s (give VALUES)", t))
	}
	if err := p.expectKw("values"); err != nil {
		return nil, err
	}
	for {
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		var row []expr
		for {
			e, err := p.expr()
			if err != nil {
				return nil, err
			}
			row = append(row, e)
			if p.punct(")") {
				break
			}
			if err := p.expectPunct(","); err != nil {
				return nil, err
			}
		}
		s.rows = append(s.rows, row)
		if !p.punct(",") {
			break
		}
	}
	if on := p.peek(); p.kw("on") {
		if err := p.expectKw("conflict"); err != nil {
			return nil, err
		}
		if p.punct("(") {
			if s.target, err = p.names(); err != nil {
				return nil, err
			}
		}
		if err := p.expectKw("do"); err != nil {
			return nil, err
		}
		s.onConflict = true
		if p.kw("update") {
			if s.target == nil {
				return nil, syntaxErr(p.q, on.pos, "ON CONFLICT DO UPDATE without a target (name the key's columns or a UNIQUE one: ON CONFLICT (cols) DO UPDATE)")
			}
			if err := p.expectKw("set"); err != nil {
				return nil, err
			}
			if s.doUpdate, err = p.sets(s.table, true); err != nil {
				return nil, err
			}
			if s.updWhere, err = p.where(s.table); err != nil {
				return nil, err
			}
		} else if err := p.expectKw("nothing"); err != nil {
			return nil, p.unexpected(p.peek(), "NOTHING or UPDATE SET")
		}
	}
	if s.ret, err = p.returning(); err != nil {
		return nil, err
	}
	s.n = p.max
	return s, nil
}

// returning reads an optional RETURNING clause: column names or *.
func (p *parser) returning() (returning, error) {
	if !p.kw("returning") {
		return returning{}, nil
	}
	if p.punct("*") {
		return returning{on: true}, nil
	}
	r := returning{on: true}
	for {
		n, err := p.name("a column name or * (RETURNING returns columns)")
		if err != nil {
			return r, err
		}
		if t := p.peek(); t.kind == tPunct && (t.text == "(" || t.text == "+" || t.text == "-") {
			return r, syntaxErr(p.q, t.pos, fmt.Sprintf("an expression after RETURNING %s (RETURNING returns columns)", n))
		}
		r.cols = append(r.cols, n)
		if !p.punct(",") {
			return r, nil
		}
	}
}

// names reads a parenthesized list of names, after its "(".
func (p *parser) names() ([]string, error) {
	var out []string
	for {
		n, err := p.name("a column name")
		if err != nil {
			return nil, err
		}
		out = append(out, n)
		if p.punct(")") {
			return out, nil
		}
		if err := p.expectPunct(","); err != nil {
			return nil, err
		}
	}
}

func (p *parser) selectStmt() (statement, error) {
	s := &selectStmt{}
	if !p.punct("*") {
		for {
			n, err := p.name("a column name or *")
			if err != nil {
				return nil, err
			}
			if t := p.peek(); t.kind == tPunct && t.text == "(" {
				return nil, syntaxErr(p.q, p.toks[p.i-1].pos, fmt.Sprintf("the function %s(...) (select columns)", n))
			}
			s.cols = append(s.cols, n)
			if !p.punct(",") {
				break
			}
		}
	}
	if err := p.expectKw("from"); err != nil {
		return nil, err
	}
	var err error
	if s.table, err = p.name("a table name"); err != nil {
		return nil, err
	}
	if s.where, err = p.where(s.table); err != nil {
		return nil, err
	}
	if p.kw("order") {
		if err := p.expectKw("by"); err != nil {
			return nil, err
		}
		for {
			var o orderTerm
			if o.col, err = p.column(s.table, "the primary key's column"); err != nil {
				return nil, err
			}
			if p.kw("desc") {
				o.desc = true
			} else {
				p.kw("asc")
			}
			s.orderBy = append(s.orderBy, o)
			if !p.punct(",") {
				break
			}
		}
	}
	if p.kw("limit") {
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		s.limit = &e
	}
	if p.kw("for") {
		if err := p.expectKw("update"); err != nil {
			return nil, err
		}
		s.forUpdate = true
	}
	s.n = p.max
	return s, nil
}

// where reads an optional WHERE clause: conjuncts col = value, col < value
// (<=, >, >=) and col BETWEEN value AND value.
func (p *parser) where(table string) ([]cond, error) {
	if !p.kw("where") {
		return nil, nil
	}
	var out []cond
	for {
		col, err := p.column(table, "a column name")
		if err != nil {
			return nil, err
		}
		t := p.peek()
		var op string
		switch {
		case isKw(t, "between"):
			p.i++
			lo, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expectKw("and"); err != nil {
				return nil, err
			}
			hi, err := p.expr()
			if err != nil {
				return nil, err
			}
			out = append(out, cond{col, ">=", lo}, cond{col, "<=", hi})
			if !p.kw("and") {
				return out, nil
			}
			continue
		case t.kind == tPunct && t.text == "=":
			p.i++
			op = "="
		case t.kind == tPunct && (t.text == "<" || t.text == ">"):
			p.i++
			op = t.text
			if p.adjacent("=") {
				op += "="
			} else if t.text == "<" && p.adjacent(">") {
				return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("\"<>\" after %s (equality and ranges only: =, <, <=, >, >=, BETWEEN)", col))
			}
		default:
			return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("%s after %s (equality and ranges only: col = value, col < value, <=, >, >=, BETWEEN)", t, col))
		}
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		out = append(out, cond{col: col, op: op, val: e})
		if !p.kw("and") {
			return out, nil
		}
	}
}

// sets reads SET's assignments: col = value, col = col ± value, and in an
// upsert's DO UPDATE, excluded.col for a value.
func (p *parser) sets(table string, upsert bool) ([]setClause, error) {
	var out []setClause
	for {
		col, err := p.name("a column name")
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct("="); err != nil {
			return nil, err
		}
		set := setClause{col: col, op: '='}
		if t := p.peek(); (t.kind == tIdent && !reserved[t.text] || t.quoted) && !(upsert && isExcluded(p)) {
			// col = itself ± value
			self, err := p.column(table, "a column name")
			if err != nil {
				return nil, err
			}
			if self != col {
				return nil, syntaxErr(p.q, t.pos, fmt.Sprintf("%s = %s ... (a column is set to a value, or to itself plus or minus one)", col, t))
			}
			op := p.peek()
			if op.kind != tPunct || (op.text != "+" && op.text != "-") {
				return nil, p.unexpected(op, "+ or -")
			}
			p.i++
			set.op = op.text[0]
		}
		if upsert && isExcluded(p) {
			p.i += 2
			if set.val.ref, err = p.name("a column of excluded"); err != nil {
				return nil, err
			}
		} else if set.val, err = p.expr(); err != nil {
			return nil, err
		}
		out = append(out, set)
		if !p.punct(",") {
			return out, nil
		}
	}
}

// isExcluded reports whether the next tokens are "excluded.".
func isExcluded(p *parser) bool {
	t, dot := p.peek(), p.toks[min(p.i+1, len(p.toks)-1)]
	return isKw(t, "excluded") && dot.kind == tPunct && dot.text == "."
}

func (p *parser) update() (statement, error) {
	s := &updateStmt{}
	var err error
	if s.table, err = p.name("a table name"); err != nil {
		return nil, err
	}
	if err := p.expectKw("set"); err != nil {
		return nil, err
	}
	if s.sets, err = p.sets(s.table, false); err != nil {
		return nil, err
	}
	if t := p.peek(); isKw(t, "from") {
		return nil, syntaxErr(p.q, t.pos, "UPDATE ... FROM (one table at a time)")
	}
	if s.where, err = p.where(s.table); err != nil {
		return nil, err
	}
	if s.ret, err = p.returning(); err != nil {
		return nil, err
	}
	s.n = p.max
	return s, nil
}

func (p *parser) delete() (statement, error) {
	if err := p.expectKw("from"); err != nil {
		return nil, err
	}
	s := &deleteStmt{}
	var err error
	if s.table, err = p.name("a table name"); err != nil {
		return nil, err
	}
	if s.where, err = p.where(s.table); err != nil {
		return nil, err
	}
	if s.ret, err = p.returning(); err != nil {
		return nil, err
	}
	s.n = p.max
	return s, nil
}

// expr is a value: a placeholder or a literal.
func (p *parser) expr() (expr, error) {
	t := p.next()
	switch {
	case t.kind == tParam:
		n := t.n
		if n == 0 {
			p.quest = true
			p.nextQ++
			n = p.nextQ
		} else {
			p.dollar = true
		}
		p.max = max(p.max, n)
		return expr{param: n}, nil
	case t.kind == tNumber:
		v, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return expr{}, syntaxErr(p.q, t.pos, fmt.Sprintf("the integer %s, out of BIGINT's range", t))
		}
		return expr{lit: v}, nil
	case t.kind == tPunct && t.text == "-":
		n := p.next()
		if n.kind != tNumber {
			return expr{}, p.unexpected(n, "a number after -")
		}
		v, err := strconv.ParseInt("-"+n.text, 10, 64)
		if err != nil {
			return expr{}, syntaxErr(p.q, n.pos, fmt.Sprintf("the integer -%s, out of BIGINT's range", n.text))
		}
		return expr{lit: v}, nil
	case t.kind == tString:
		return expr{lit: t.text}, nil
	case isKw(t, "true"):
		return expr{lit: true}, nil
	case isKw(t, "false"):
		return expr{lit: false}, nil
	case isKw(t, "null"):
		return expr{lit: nil}, nil
	}
	return expr{}, p.unexpected(t, "a value (?, $n, an integer, a 'string', TRUE, FALSE or NULL)")
}
