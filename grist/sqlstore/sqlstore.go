// Package sqlstore is a store.Store over database/sql: grist's real tier
// (PostgreSQL, SQLite) and, on qsql over a qenv.Store, its simulated one,
// both running the same statements (docs/grist.md §2.10). It imports no
// driver: the application opens the database with its own (pgx's stdlib,
// modernc.org/sqlite) and names its Dialect.
//
// Tables (Open initializes the current SchemaVersion under grist_schema's
// lock; schemas without immutable event identities are refused):
//
//	grist_schema(id, version)                    the schema's version, its row the migrations' lock
//	grist_heads(tenant, head, steps)             the tenant's lock, its head, its step rows' count
//	grist_events(tenant, pos, event_id, type, version, at_ms, cause, tags, body)
//	grist_index(tenant, k, pos)                  k: i/<type>, or g/<type>/<key>=<value>
//	grist_markers(tenant, id, hash, outcome, pos, events)
//	grist_checkpoints(tenant, processor, pos)
//	grist_steps(tenant, seq, head, at_ns, step)  the step log (store.StepLog), at_ns its commit's time
//	grist_snapshots(tenant, name, k, fingerprint, pos, state, checksum)  read models' snapshots (store.SnapshotStore)
//
// An append's first statement takes the tenant's lock and reads the new
// head in one round trip, `UPDATE grist_heads SET head = head + $1, steps
// = steps + $2 WHERE tenant = $3 RETURNING head, steps` (no advisory
// lock: one invisible to the simulation and to qhistory); then the marker,
// the checkpoint and the condition (a range of the index after the
// decision's position, `SELECT pos FROM grist_index WHERE tenant = $1 AND
// k = $2 AND pos > $3 ORDER BY pos LIMIT 1`), all read under the lock;
// then the rows, the batch's step row among them, and COMMIT. Positions
// are assigned under the lock, so they are dense and in commit order
// (never a sequence, drawn before commit). Under read committed the
// loser of a race for the lock waits for it and then sees every commit
// before it; under snapshot isolation or serializable it gets 40001, a
// transient error grist retries from the key's marker (the head-lock
// conflict).
//
// A read is the head, then one statement per type of its selector:
// `grist_index JOIN grist_events` on (tenant, pos), a range of the
// index's key and each event's row looked up by its key (not a statement
// per event: a round trip each on PostgreSQL), with its LIMIT when the
// index holds every condition of the selector.
//
// Errors: a failed condition, a marker that exists and a moved
// checkpoint are the contract's (*store.ConditionFailed,
// *store.CheckpointMoved); a statement no retry fixes (a missing table,
// a syntax error, SQLSTATE classes 42, 0A, 22, 3D, 3F, 28, SQLite's
// SQLITE_ERROR) is a *store.PermanentError; everything else (40001,
// 40P01, lock_timeout's 55P03, a lost connection, SQLITE_BUSY after the
// busy_timeout) is transient: a *store.NotCommittedError before COMMIT
// (nothing committed), unmarked at or after it (the outcome unknown).
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// PostgresNotifyChannel is the database-scoped channel for commit wake hints.
// Payloads are empty; a notification always means to reread authoritative data.
const PostgresNotifyChannel = "grist_changed"

// Options configure a Store.
type Options struct {
	// Name is the store's name: the process of every step recorded at it
	// (default "grist"). gristsim names it after the App.
	Name string
	// Isolation is the level each transaction states (default: the
	// dialect's; PostgreSQL's and qsql's read committed). A stronger one
	// makes a lost race for the head lock a 40001, transient.
	Isolation sql.IsolationLevel
	// LockTimeout and IdleTimeout are PostgreSQL's lock_timeout and
	// idle_in_transaction_session_timeout, set in each transaction
	// (defaults 5 s, grain's, and 60 s).
	LockTimeout, IdleTimeout time.Duration
	// Poll is how often Wait, WaitSteps and WaitTenants read the
	// database when no append of this Store wakes them: another process's
	// appends (default 250 ms). On qsql the default is never: every
	// writer commits through the database's qenv.Store, whose own signal
	// (qenv.Store.Changed) wakes every Store's waiters, and a timer would
	// keep a settled simulation busy.
	Poll time.Duration
	// PostgresNotify queues a database-scoped wake hint in each tenant creation
	// and append transaction. Pair it with a listener (sqlstore/pgnotify.Run)
	// on every reader; Poll remains the backstop for missed notifications.
	// It is valid only with the Postgres dialect.
	PostgresNotify bool
}

// Store is a store.Store and store.StepLog over a database/sql database.
type Store struct {
	readOnly bool // OpenReadOnly refuses every mutation before sending SQL
	db       *sql.DB
	d        Dialect
	o        Options
	wakeMu   sync.Mutex
	wakeCh   chan struct{} // closed and replaced on every local or external commit hint
	commits  atomic.Uint64 // the COMMITs tried: a waiter that read before one does not sleep through it
	// sim is the qenv.Store a qsql database runs on (qsql.StoreOf), nil on
	// a real one: its signal and version stand for wakeCh and commits,
	// so a waiter wakes at any client's commit, another Store's over the
	// same database included
	sim *qenv.Store
}

// schema is the current tables, in the SQL every dialect runs. The initial
// versioned creation refuses an unversioned pre-release schema. Read-only.
var schema = []string{
	`CREATE TABLE grist_heads (tenant TEXT NOT NULL, head BIGINT NOT NULL, steps BIGINT NOT NULL, PRIMARY KEY (tenant))`,
	`CREATE TABLE grist_events (tenant TEXT NOT NULL, pos BIGINT NOT NULL, event_id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, version BIGINT NOT NULL, at_ms BIGINT NOT NULL, cause TEXT NOT NULL, tags TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, pos))`,
	`CREATE TABLE grist_index (tenant TEXT NOT NULL, k TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, k, pos))`,
	`CREATE TABLE grist_markers (tenant TEXT NOT NULL, id TEXT NOT NULL, hash TEXT NOT NULL, outcome TEXT NOT NULL, pos BIGINT NOT NULL, events BIGINT NOT NULL, PRIMARY KEY (tenant, id))`,
	`CREATE TABLE grist_checkpoints (tenant TEXT NOT NULL, processor TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, processor))`,
	`CREATE TABLE grist_steps (tenant TEXT NOT NULL, seq BIGINT NOT NULL, head BIGINT NOT NULL, at_ns BIGINT NOT NULL, step TEXT NOT NULL, PRIMARY KEY (tenant, seq))`,
	`CREATE TABLE grist_snapshots (tenant TEXT NOT NULL, name TEXT NOT NULL, k TEXT NOT NULL, fingerprint TEXT NOT NULL, pos BIGINT NOT NULL, state TEXT NOT NULL, checksum TEXT NOT NULL, PRIMARY KEY (tenant, name, k, fingerprint))`,
}

// Open returns a store over db in dialect d, initializing a fresh schema or
// opening the current one. It refuses old schemas without immutable event
// identities; on SQLite it checks the single-writer mode (SQLiteDSN).
// It checks the options (a negative duration, a name a recorder cannot
// carry) and begins one transaction at the isolation level, so a level
// the database or its driver does not offer is Open's error, naming the
// fix, rather than every append's transient one.
func Open(ctx context.Context, db *sql.DB, d Dialect, o Options) (*Store, error) {
	return open(ctx, db, d, o, false)
}

// OpenReadOnly opens an existing store at the current SchemaVersion without
// initializing or migrating it. It sends only reads, needs no write privileges,
// and rejects every mutating Store operation. The caller owns db and must open
// SQLite with mode=ro to prevent its driver creating a missing database file.
func OpenReadOnly(ctx context.Context, db *sql.DB, d Dialect, o Options) (*Store, error) {
	return open(ctx, db, d, o, true)
}

func open(ctx context.Context, db *sql.DB, d Dialect, o Options, readOnly bool) (*Store, error) {
	if d.name == "" {
		return nil, errors.New("sqlstore: the zero Dialect: use sqlstore.Postgres, sqlstore.SQLite or sqlstore.QSQL")
	}
	if readOnly && o.PostgresNotify {
		return nil, errors.New("sqlstore: OpenReadOnly cannot enable PostgresNotify: disable write notifications")
	}
	if o.PostgresNotify && d.name != Postgres.name {
		return nil, errors.New("sqlstore: Options.PostgresNotify requires sqlstore.Postgres: disable it for SQLite or QSQL")
	}
	if o.Name == "" {
		o.Name = "grist"
	}
	if !utf8.ValidString(o.Name) || strings.ContainsRune(o.Name, 0) {
		return nil, fmt.Errorf("sqlstore: Options.Name %q is not valid UTF-8 without NUL: it names the process of every step recorded at the store; give a plain name (the App's)", o.Name)
	}
	for _, d := range []struct {
		name string
		d    time.Duration
	}{{"LockTimeout", o.LockTimeout}, {"IdleTimeout", o.IdleTimeout}, {"Poll", o.Poll}} {
		if d.d < 0 {
			return nil, fmt.Errorf("sqlstore: Options.%s is %v: give a positive duration, or 0 for the default", d.name, d.d)
		}
	}
	if o.Isolation == sql.LevelDefault {
		o.Isolation = d.iso
	}
	if o.LockTimeout == 0 {
		o.LockTimeout = 5 * time.Second
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = time.Minute
	}
	if d.pragmas && !readOnly {
		if err := checkSQLite(ctx, db); err != nil {
			return nil, err
		}
	}
	if readOnly {
		var version int64
		if err := db.QueryRowContext(ctx, "SELECT version FROM grist_schema WHERE id = $1", 1).Scan(&version); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = store.Permanent(err)
			}
			return nil, fmt.Errorf("sqlstore: OpenReadOnly requires an existing current schema: %w", classify(err))
		}
		if version != SchemaVersion {
			return nil, store.Permanent(fmt.Errorf("sqlstore: OpenReadOnly found schema version %d, need %d: inspect it with a matching binary; this reader never initializes or migrates data", version, SchemaVersion))
		}
	} else if err := migrate(ctx, db); err != nil {
		return nil, err
	}
	s := &Store{db: db, d: d, o: o, sim: qsql.StoreOf(db), readOnly: readOnly}
	// one transaction at the level, as every append begins one: a level
	// the driver refuses (pgx's "unsupported isolation", no SQLSTATE) would
	// otherwise read as transient, and every append would retry it forever
	if d.pragmas && o.Isolation != sql.LevelDefault && o.Isolation != sql.LevelSerializable {
		// the driver takes any level and runs SQLite's one: a stated level
		// would be a promise nothing keeps
		return nil, fmt.Errorf("sqlstore: Options.Isolation %s on SQLite, whose one writer makes every transaction serializable: leave it unset (or sql.LevelSerializable)", o.Isolation)
	}
	tx, err := s.begin(ctx, readOnly)
	var nc *store.NotCommittedError
	if errors.As(err, &nc) {
		err = nc.Err
	}
	if err != nil {
		return nil, fmt.Errorf("sqlstore: a transaction at isolation level %s (Options.Isolation) on %s: %w; name a level the database offers (PostgreSQL: read committed, repeatable read, serializable; SQLite: the default; qsql: its Store's or a weaker one), or leave it unset for the dialect's", o.Isolation, d, err)
	}
	tx.Rollback()
	return s, nil
}

// Name implements store.Store.
func (s *Store) Name() string { return s.o.Name }

// mutable refuses writes through a read-only store before sending SQL.
func (s *Store) mutable() error {
	if s.readOnly {
		return store.Permanent(errors.New("sqlstore: this store was opened read-only: use a writer explicitly to mutate it"))
	}
	return nil
}

// begin starts a transaction at the store's level, with the dialect's settings.
func (s *Store) begin(ctx context.Context, readOnly bool) (*sql.Tx, error) {
	if !readOnly {
		if err := s.mutable(); err != nil {
			return nil, err
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: s.o.Isolation, ReadOnly: readOnly})
	if err != nil {
		return nil, store.NotCommitted(classify(err))
	}
	if s.d.timeouts {
		// set_config(..., true) is SET LOCAL as a function: both settings
		// in one round trip, ended with the transaction
		_, err := tx.ExecContext(ctx, `SELECT set_config('lock_timeout', $1, true), set_config('idle_in_transaction_session_timeout', $2, true)`,
			fmt.Sprint(s.o.LockTimeout.Milliseconds()), fmt.Sprint(s.o.IdleTimeout.Milliseconds()))
		if err != nil {
			tx.Rollback()
			return nil, store.NotCommitted(classify(err))
		}
	}
	return tx, nil
}

func esc(s string) string { return url.QueryEscape(s) }

// indexKey is the index key a selector of typ and tags reads: the first
// tag's (the others are checked on the events), else the type's.
func indexKey(typ string, tags []store.TagPair) string {
	if len(tags) > 0 {
		return "g/" + esc(typ) + "/" + esc(tags[0].Key) + "=" + esc(tags[0].Value)
	}
	return "i/" + esc(typ)
}

// CreateTenant implements store.Store.
func (s *Store) CreateTenant(ctx context.Context, t string) error {
	if err := s.mutable(); err != nil {
		return err
	}
	const insert = `INSERT INTO grist_heads (tenant, head, steps) VALUES ($1, 0, 0) ON CONFLICT (tenant) DO NOTHING`
	if !s.o.PostgresNotify {
		_, err := s.db.ExecContext(ctx, insert, t)
		s.NotifyExternal()
		return classify(err)
	}
	tx, err := s.begin(ctx, false)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, insert, t); err != nil {
		return classify(err)
	}
	if err := s.notifyTransaction(ctx, tx); err != nil {
		return err
	}
	err = tx.Commit()
	s.NotifyExternal() // a lost COMMIT answer may still have created the tenant
	return classify(err)
}

// notifyTransaction queues the hint for delivery only if this transaction commits.
func (s *Store) notifyTransaction(ctx context.Context, tx *sql.Tx) error {
	if !s.o.PostgresNotify {
		return nil
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_notify($1, '')`, PostgresNotifyChannel)
	return classify(err)
}

// NotifyExternal wakes all native waiters to reread the database. It is a hint,
// not evidence that a particular write committed. Listener adapters call it
// after subscription or reconnection as well as on each notification. On QSQL,
// the simulated database's own commit signal remains authoritative.
func (s *Store) NotifyExternal() {
	s.wakeMu.Lock()
	s.commits.Add(1)
	if s.wakeCh != nil {
		close(s.wakeCh)
		s.wakeCh = nil
	}
	s.wakeMu.Unlock()
}

// wakeTicket subscribes before the predicate read: a hint while the read runs,
// or between the generation check and the wait, closes this exact channel.
func (s *Store) wakeTicket() <-chan struct{} {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wakeCh == nil {
		s.wakeCh = make(chan struct{})
	}
	return s.wakeCh
}

// Tenants implements store.Store.
func (s *Store) Tenants(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant FROM grist_heads ORDER BY tenant`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, classify(err)
		}
		out = append(out, t)
	}
	sort.Strings(out) // the database's collation may not be bytes'
	return out, classify(rows.Err())
}

// querier is what a read needs: a transaction or the database.
type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// head is t's head as q reads it.
func head(ctx context.Context, q querier, t string) (store.Position, error) {
	var h int64
	switch err := q.QueryRowContext(ctx, `SELECT head FROM grist_heads WHERE tenant = $1`, t).Scan(&h); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, store.ErrUnknownTenant
	case err != nil:
		return 0, classify(err)
	}
	return store.Position(h), nil
}

// readStmt reads one index key's events in (after, through], joined to
// their rows in grist_events, in position order (ORDER BY's direction
// and a LIMIT appended): one statement for every event under the key, a
// range of grist_index's key and a lookup of grist_events' per row, on
// PostgreSQL, SQLite and qsql alike. Read-only.
const readStmt = `SELECT grist_events.pos, grist_events.event_id, grist_events.type, grist_events.version, grist_events.at_ms, grist_events.cause, grist_events.tags, grist_events.body ` +
	`FROM grist_index JOIN grist_events ON grist_events.tenant = grist_index.tenant AND grist_events.pos = grist_index.pos ` +
	`WHERE grist_index.tenant = $1 AND grist_index.k = $2 AND grist_index.pos > $3 AND grist_index.pos <= $4 ORDER BY grist_index.pos `

// events are t's events in (after, through] matching sel, in position
// order (newest first if reverse), at most limit of them (0: all): one
// statement per distinct type of sel, the index key of the type and
// sel's first tag joined to the events, whose other tags are checked
// here. A type's events are another's of none, so the union is their
// merge. With at most one tag every row matches, and the limit is each
// statement's (the first n of the union are among the first n of each
// type: a read model's first SnapshotEvery events, read from an index
// of every one of its events, without the rest); with more, a type's
// rows are read until limit of them match.
func events(ctx context.Context, q querier, t string, sel store.Selector, after, through store.Position, reverse bool, limit int) ([]store.Stored, error) {
	order := "ASC"
	if reverse {
		order = "DESC"
	}
	var out []store.Stored
	seen := map[string]bool{}
	for _, typ := range sel.Types {
		if seen[typ] {
			continue
		}
		seen[typ] = true
		stmt := readStmt + order
		args := []any{t, indexKey(typ, sel.Tags), int64(after), int64(through)}
		if limit > 0 && len(sel.Tags) <= 1 {
			stmt += ` LIMIT $5`
			args = append(args, limit)
		}
		rows, err := q.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, classify(err)
		}
		n := 0
		for (limit == 0 || n < limit) && rows.Next() {
			e, err := scanEvent(rows, t)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if sel.Matches(e.Event) {
				out = append(out, e)
				n++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, classify(err)
		}
	}
	if len(seen) > 1 {
		sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos != reverse })
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// scanEvent is t's event in a row of readStmt.
func scanEvent(rows *sql.Rows, t string) (store.Stored, error) {
	var id, typ, cause, tags string
	var body []byte // a copy of its own (Scan's), so the event's body is the caller's
	var p, version, at int64
	if err := rows.Scan(&p, &id, &typ, &version, &at, &cause, &tags, &body); err != nil {
		return store.Stored{}, classify(err)
	}
	e := store.Stored{Event: store.Event{Type: typ, Version: int(version), Body: json.RawMessage(body)}, ID: id, Tenant: t, Pos: store.Position(p), Time: at, Cause: cause}
	pairs, ok := decodeTags(tags)
	if !ok {
		var kvs [][2]string
		if err := json.Unmarshal([]byte(tags), &kvs); err != nil {
			return store.Stored{}, store.Permanent(fmt.Errorf("sqlstore: event %d of %s: tags %q: %v", p, t, tags, err))
		}
		for _, kv := range kvs {
			pairs = append(pairs, store.TagPair{Key: kv[0], Value: kv[1]})
		}
	}
	e.Tags = pairs
	return e, nil
}

// decodeTags decodes the tags column as json.Marshal of [][2]string
// writes it when no string needs an escape: [["k","v"],...], no space,
// no backslash, no control character, valid UTF-8. It reports false
// for anything else, which json.Unmarshal then reads (a third of a
// read's time went to it). Its strings share tags's memory.
func decodeTags(tags string) ([]store.TagPair, bool) {
	if len(tags) < 2 || tags[0] != '[' || tags[len(tags)-1] != ']' {
		return nil, false
	}
	i := 1
	str := func() (string, bool) {
		if i >= len(tags) || tags[i] != '"' {
			return "", false
		}
		j := i + 1
		for j < len(tags) && tags[j] != '"' {
			if c := tags[j]; c == '\\' || c < 0x20 {
				return "", false
			}
			j++
		}
		if j >= len(tags) || !utf8.ValidString(tags[i+1:j]) {
			return "", false
		}
		s := tags[i+1 : j]
		i = j + 1
		return s, true
	}
	expect := func(c byte) bool {
		if i < len(tags) && tags[i] == c {
			i++
			return true
		}
		return false
	}
	var out []store.TagPair
	for first := true; i < len(tags)-1; first = false {
		if !first && !expect(',') {
			return nil, false
		}
		if !expect('[') {
			return nil, false
		}
		k, ok := str()
		if !ok || !expect(',') {
			return nil, false
		}
		v, ok := str()
		if !ok || !expect(']') {
			return nil, false
		}
		out = append(out, store.TagPair{Key: k, Value: v})
	}
	return out, i == len(tags)-1
}

// Read implements store.Store: in one read-only transaction, the head,
// then each of the selector's types' events in one statement (rows
// below the head never change, so read committed's statement snapshots
// agree, and a join that reads its rows one by one, as qsql's does,
// reads what one snapshot would).
func (s *Store) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	tx, err := s.begin(ctx, true)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	h, err := head(ctx, tx, t)
	if err != nil {
		return nil, 0, err
	}
	if at == 0 || at > h {
		at = h
	}
	out := []store.Stored{}
	if len(q.Types) == 0 {
		return out, at, nil
	}
	evs, err := events(ctx, tx, t, q, q.After, at, q.Reverse, q.Limit)
	if err != nil {
		return nil, 0, err
	}
	return append(out, evs...), at, nil
}

// ReadEvent implements store.Store. The primary-key lookup does not depend
// on an app catalog or the event-type index.
func (s *Store) ReadEvent(ctx context.Context, t string, position store.Position) (store.Stored, bool, error) {
	if position <= 0 {
		return store.Stored{}, false, store.Permanent(fmt.Errorf("grist: ReadEvent position %d must be positive: give an event position starting at 1", position))
	}
	tx, err := s.begin(ctx, true)
	if err != nil {
		return store.Stored{}, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT pos, event_id, type, version, at_ms, cause, tags, body FROM grist_events WHERE tenant = $1 AND pos = $2", t, int64(position))
	if err != nil {
		return store.Stored{}, false, classify(err)
	}
	if rows.Next() {
		e, err := scanEvent(rows, t)
		rows.Close()
		return e, err == nil, err
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return store.Stored{}, false, classify(err)
	}
	if _, err := head(ctx, tx, t); err != nil {
		return store.Stored{}, false, err
	}
	return store.Stored{}, false, nil
}

// Append implements store.Store.
func (s *Store) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if err := s.mutable(); err != nil {
		return 0, err
	}
	if err := b.CheckRetainedIDs(); err != nil {
		return 0, err
	}
	h, err := s.append(ctx, t, b)
	if err != nil {
		return 0, err
	}
	return h, nil
}

// errCommitted is an error at or after an append's COMMIT, its outcome
// unknown: append returns it as it is, every error before COMMIT marked
// store.NotCommitted.
type errCommitted struct{ err error }

func (e errCommitted) Error() string { return e.err.Error() }

// append is Append, its errors before COMMIT store.NotCommitted (the
// contract's own errors stay as they are) and those of COMMIT unmarked.
func (s *Store) append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	h, err := s.tryAppend(ctx, t, b)
	var ec errCommitted
	switch {
	case err == nil:
		return h, nil
	case errors.As(err, &ec):
		return 0, ec.err
	}
	var cf *store.ConditionFailed
	var cm *store.CheckpointMoved
	if errors.As(err, &cf) || errors.As(err, &cm) || errors.Is(err, store.ErrUnknownTenant) {
		return 0, err
	}
	return 0, store.NotCommitted(err)
}

// tryAppend runs an append's transaction: every error before COMMIT is
// returned as the statement gave it, COMMIT's as an errCommitted.
func (s *Store) tryAppend(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	tx, err := s.begin(ctx, false)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, steps := len(b.Events), 0
	if b.Step != nil {
		steps = 1
	}
	var newHead, seq int64
	// the tenant's lock and the new head, in one round trip
	switch err := tx.QueryRowContext(ctx, `UPDATE grist_heads SET head = head + $1, steps = steps + $2 WHERE tenant = $3 RETURNING head, steps`, n, steps, t).Scan(&newHead, &seq); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, store.ErrUnknownTenant
	case err != nil:
		return 0, classify(err)
	}
	h := store.Position(newHead) - store.Position(n) // the head before the batch
	if c := b.Checkpoint; c != nil && c.To > h {
		return 0, &store.ConditionFailed{Event: c.To, Past: true, Head: h}
	}
	if m := b.Marker; m != nil {
		var hash string
		switch err := tx.QueryRowContext(ctx, `SELECT hash FROM grist_markers WHERE tenant = $1 AND id = $2`, t, m.ID).Scan(&hash); {
		case err == nil:
			return 0, &store.ConditionFailed{Marker: m.ID}
		case !errors.Is(err, sql.ErrNoRows):
			return 0, classify(err)
		}
	}
	if c := b.Checkpoint; c != nil {
		var cur int64
		if err := tx.QueryRowContext(ctx, `SELECT pos FROM grist_checkpoints WHERE tenant = $1 AND processor = $2`, t, c.Processor).Scan(&cur); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, classify(err)
		}
		if store.Position(cur) != c.From {
			return 0, &store.CheckpointMoved{Processor: c.Processor, Expected: c.From, Found: store.Position(cur)}
		}
	}
	if c := b.Condition; c != nil {
		if c.After > h {
			return 0, &store.ConditionFailed{Event: c.After, Past: true, Head: h}
		}
		for _, q := range c.Selectors {
			if err := s.check(ctx, tx, t, q, c.After, h); err != nil {
				return 0, err
			}
		}
	}
	for i, e := range b.Events {
		p := int64(h) + int64(i) + 1
		pairs := make([][2]string, len(e.Tags))
		for j, tag := range e.Tags {
			pairs[j] = [2]string{tag.Key, tag.Value}
		}
		tags, _ := json.Marshal(pairs)
		body := string(e.Body)
		if body == "" {
			body = "null"
		}
		for {
			id := ""
			if b.RetainedIDs != nil {
				id = b.RetainedIDs[i]
			} else {
				id = qenv.NewID(ctx)
			}
			var inserted string
			err := tx.QueryRowContext(ctx, `INSERT INTO grist_events (tenant, pos, event_id, type, version, at_ms, cause, tags, body) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (event_id) DO NOTHING RETURNING event_id`,
				t, p, id, e.Type, int64(e.Version), b.Time, b.Cause, string(tags), body).Scan(&inserted)
			if errors.Is(err, sql.ErrNoRows) {
				if b.RetainedIDs != nil {
					return 0, store.Permanent(&store.IdentityExists{ID: id})
				}
				// Imported simulation history may already own an allocator value.
				// The unique index arbitrates without a separate lookup round trip.
				continue
			}
			if err != nil {
				return 0, classify(err)
			}
			break
		}
		if err := s.index(ctx, tx, t, e, p); err != nil {
			return 0, err
		}
	}
	if m := b.Marker; m != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_markers (tenant, id, hash, outcome, pos, events) VALUES ($1, $2, $3, $4, $5, $6)`,
			t, m.ID, m.Hash, m.Outcome, newHead, int64(n)); err != nil {
			return 0, classify(err)
		}
	}
	if c := b.Checkpoint; c != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_checkpoints (tenant, processor, pos) VALUES ($1, $2, $3) ON CONFLICT (tenant, processor) DO UPDATE SET pos = excluded.pos`,
			t, c.Processor, int64(c.To)); err != nil {
			return 0, classify(err)
		}
	}
	if b.Step != nil {
		// numbered and timed under the lock: the step log's order is the
		// commit order, and its times are in that order too
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_steps (tenant, seq, head, at_ns, step) VALUES ($1, $2, $3, $4, $5)`,
			t, seq, newHead, store.StepTime(ctx), string(b.Step)); err != nil {
			return 0, classify(err)
		}
	}
	if err := s.notifyTransaction(ctx, tx); err != nil {
		return 0, err
	}
	err = tx.Commit()
	// every COMMIT wakes the waiters, its answer lost or not: one whose
	// outcome is unknown may have committed, and a waiter that slept
	// through it would sleep through its step
	s.NotifyExternal()
	if err != nil {
		return 0, errCommitted{classify(err)}
	}
	return store.Position(newHead), nil
}

// check is a condition's selector checked under the lock: the first
// event after after, through h, matching q, as a *store.ConditionFailed.
// With at most one tag the index's first position under each type's key
// is the answer, read without the event; with more, events reads each
// type's events until one matches.
func (s *Store) check(ctx context.Context, tx *sql.Tx, t string, q store.Selector, after, h store.Position) error {
	var first store.Position
	if len(q.Tags) > 1 {
		evs, err := events(ctx, tx, t, q, after, h, false, 1)
		if err != nil {
			return err
		}
		if len(evs) > 0 {
			first = evs[0].Pos
		}
	} else {
		for _, typ := range q.Types {
			var p int64
			err := tx.QueryRowContext(ctx, `SELECT pos FROM grist_index WHERE tenant = $1 AND k = $2 AND pos > $3 AND pos <= $4 ORDER BY pos ASC LIMIT 1`,
				t, indexKey(typ, q.Tags), int64(after), int64(h)).Scan(&p)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				continue
			case err != nil:
				return classify(err)
			}
			if first == 0 || store.Position(p) < first {
				first = store.Position(p)
			}
		}
	}
	if first != 0 {
		return &store.ConditionFailed{Selector: q, Event: first}
	}
	return nil
}

// index writes event e's index rows at p: its type's and each distinct
// tag's, in one statement.
func (s *Store) index(ctx context.Context, tx *sql.Tx, t string, e store.Event, p int64) error {
	keys := []string{indexKey(e.Type, nil)}
	seen := map[string]bool{keys[0]: true}
	for _, tag := range e.Tags {
		if k := indexKey(e.Type, []store.TagPair{tag}); !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO grist_index (tenant, k, pos) VALUES `)
	args := make([]any, 0, 3*len(keys))
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "($%d, $%d, $%d)", 3*i+1, 3*i+2, 3*i+3)
		args = append(args, t, k, p)
	}
	_, err := tx.ExecContext(ctx, b.String(), args...)
	return classify(err)
}

// Marker implements store.Store.
func (s *Store) Marker(ctx context.Context, t, id string) (store.Marker, bool, error) {
	tx, err := s.begin(ctx, true)
	if err != nil {
		return store.Marker{}, false, err
	}
	defer tx.Rollback()
	if _, err := head(ctx, tx, t); err != nil {
		return store.Marker{}, false, err
	}
	m := store.Marker{ID: id}
	var pos, n int64
	switch err := tx.QueryRowContext(ctx, `SELECT hash, outcome, pos, events FROM grist_markers WHERE tenant = $1 AND id = $2`, t, id).Scan(&m.Hash, &m.Outcome, &pos, &n); {
	case errors.Is(err, sql.ErrNoRows):
		return store.Marker{}, false, nil
	case err != nil:
		return store.Marker{}, false, classify(err)
	}
	m.Receipt = store.NewReceipt(t, store.Position(pos), int(n))
	return m, true, nil
}

// Checkpoint implements store.Store.
func (s *Store) Checkpoint(ctx context.Context, t, processor string) (store.Position, error) {
	tx, err := s.begin(ctx, true)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := head(ctx, tx, t); err != nil {
		return 0, err
	}
	var p int64
	if err := tx.QueryRowContext(ctx, `SELECT pos FROM grist_checkpoints WHERE tenant = $1 AND processor = $2`, t, processor).Scan(&p); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, classify(err)
	}
	return store.Position(p), nil
}

// Heads implements store.Store: one query for every tenant, a change
// feed's poll.
func (s *Store) Heads(ctx context.Context) (map[string]store.Position, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant, head FROM grist_heads`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out := map[string]store.Position{}
	for rows.Next() {
		var t string
		var h int64
		if err := rows.Scan(&t, &h); err != nil {
			return nil, classify(err)
		}
		out[t] = store.Position(h)
	}
	return out, classify(rows.Err())
}

// Steps implements store.StepLog.
func (s *Store) Steps(ctx context.Context, t string, after int64, limit int) ([]store.StepRow, error) {
	tx, err := s.begin(ctx, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := head(ctx, tx, t); err != nil {
		return nil, err
	}
	stmt, args := `SELECT seq, head, at_ns, step FROM grist_steps WHERE tenant = $1 AND seq > $2 ORDER BY seq`, []any{t, after}
	if limit > 0 {
		stmt, args = stmt+` LIMIT $3`, append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out := []store.StepRow{}
	for rows.Next() {
		var r store.StepRow
		var h int64
		var step string
		if err := rows.Scan(&r.Seq, &h, &r.Time, &step); err != nil {
			return nil, classify(err)
		}
		r.Head, r.Step = store.Position(h), json.RawMessage(step)
		out = append(out, r)
	}
	return out, classify(rows.Err())
}

// Wait implements store.Store.
func (s *Store) Wait(ctx context.Context, t string, p store.Position) error {
	return s.wait(ctx, func() (bool, error) {
		h, err := head(ctx, s.db, t)
		return h != p, err
	})
}

// WaitSteps implements store.StepLog.
func (s *Store) WaitSteps(ctx context.Context, t string, after int64) error {
	return s.wait(ctx, func() (bool, error) {
		n, err := s.StepCount(ctx, t)
		return n != after, err
	})
}

// StepCount implements store.StepLog: the head row's count, one read.
func (s *Store) StepCount(ctx context.Context, t string) (int64, error) {
	var n int64
	switch err := s.db.QueryRowContext(ctx, `SELECT steps FROM grist_heads WHERE tenant = $1`, t).Scan(&n); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, store.ErrUnknownTenant
	case err != nil:
		return 0, classify(err)
	}
	return n, nil
}

// WaitTenants implements store.TenantFeed.
func (s *Store) WaitTenants(ctx context.Context, n int) error {
	return s.wait(ctx, func() (bool, error) {
		ts, err := s.Tenants(ctx) // the tenants' names, not COUNT(*): one SQL on every dialect, qsql's included
		return len(ts) > n, err
	})
}

// version is the count of commits a waiter compares before and after its
// read: the database's on qsql (every client's), else this Store's.
func (s *Store) version() uint64 {
	if s.sim != nil {
		return uint64(s.sim.Version())
	}
	return s.commits.Load()
}

// wait waits until done, woken by this Store's appends (on qsql, by any
// commit to the database) and, every Poll, by the clock (another
// process's appends).
func (s *Store) wait(ctx context.Context, done func() (bool, error)) error {
	poll := s.o.Poll
	if poll == 0 && s.sim == nil {
		poll = 250 * time.Millisecond
	}
	for {
		var ticket <-chan struct{}
		if s.sim == nil {
			ticket = s.wakeTicket()
		}
		seen := s.version()
		ok, err := done() // a read: other tasks commit meanwhile
		if err != nil && !store.Transient(err) {
			return err
		}
		if ok {
			return nil // a commit while it read changes nothing: it held already
		}
		if s.version() != seen {
			continue // a commit while it read: read again, its Notify is past
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && poll == 0 {
			qenv.Sleep(ctx, 10*time.Millisecond) // a transient error: read again soon, not at once
			continue
		}
		if s.sim == nil {
			waitNative(ctx, ticket, poll)
			continue
		}
		signal := s.sim.Changed()
		if err == nil {
			signal.WaitIdle(ctx, poll) // the successful read established no new work
		} else {
			signal.Wait(ctx, poll) // an unsuccessful read cannot establish quiescence
		}
	}
}

// waitNative is outside the simulation: QSQL uses its scheduler's signal.
func waitNative(ctx context.Context, ticket <-chan struct{}, poll time.Duration) {
	timer := time.NewTimer(poll) //qenv:ok the real database's polling backstop
	defer timer.Stop()
	select { //qenv:ok native database wait; the simulated tier uses qenv.Signal
	case <-ctx.Done():
	case <-ticket:
	case <-timer.C:
	}
}

// PutSnapshot implements store.SnapshotStore: one row per (tenant, read
// model, key, fingerprint), the last put's.
func (s *Store) PutSnapshot(ctx context.Context, t string, snap store.Snapshot) error {
	if err := s.mutable(); err != nil {
		return err
	}
	if _, err := head(ctx, s.db, t); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO grist_snapshots (tenant, name, k, fingerprint, pos, state, checksum) VALUES ($1, $2, $3, $4, $5, $6, $7) `+
		`ON CONFLICT (tenant, name, k, fingerprint) DO UPDATE SET pos = excluded.pos, state = excluded.state, checksum = excluded.checksum`,
		t, snap.Name, snap.Key, snap.Fingerprint, int64(snap.Pos), string(snap.State), snap.Sum)
	return classify(err)
}

// PruneSnapshots implements store.SnapshotPruner: the key's rows read by
// its primary key's prefix, each one pruned deleted by its whole key (the
// statements every dialect and qsql take).
func (s *Store) PruneSnapshots(ctx context.Context, t, name, key, keep string, before store.Position) (int, error) {
	if err := s.mutable(); err != nil {
		return 0, err
	}
	if _, err := head(ctx, s.db, t); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT fingerprint, pos FROM grist_snapshots WHERE tenant = $1 AND name = $2 AND k = $3`, t, name, key)
	if err != nil {
		return 0, classify(err)
	}
	var gone []string
	for rows.Next() {
		var fp string
		var pos int64
		if err := rows.Scan(&fp, &pos); err != nil {
			rows.Close()
			return 0, classify(err)
		}
		if fp != keep && store.Position(pos) < before {
			gone = append(gone, fp)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, classify(err)
	}
	rows.Close()
	n := 0
	for _, fp := range gone {
		res, err := s.db.ExecContext(ctx, `DELETE FROM grist_snapshots WHERE tenant = $1 AND name = $2 AND k = $3 AND fingerprint = $4`, t, name, key, fp)
		if err != nil {
			return n, classify(err)
		}
		if k, err := res.RowsAffected(); err == nil {
			n += int(k)
		}
	}
	return n, nil
}

// GetSnapshot implements store.SnapshotStore.
func (s *Store) GetSnapshot(ctx context.Context, t, name, key, fingerprint string) (store.Snapshot, bool, error) {
	if _, err := head(ctx, s.db, t); err != nil {
		return store.Snapshot{}, false, err
	}
	var pos int64
	var state, sum string
	switch err := s.db.QueryRowContext(ctx, `SELECT pos, state, checksum FROM grist_snapshots WHERE tenant = $1 AND name = $2 AND k = $3 AND fingerprint = $4`,
		t, name, key, fingerprint).Scan(&pos, &state, &sum); {
	case errors.Is(err, sql.ErrNoRows):
		return store.Snapshot{}, false, nil
	case err != nil:
		return store.Snapshot{}, false, classify(err)
	}
	return store.Snapshot{Name: name, Key: key, Fingerprint: fingerprint, Pos: store.Position(pos), State: json.RawMessage(state), Sum: sum}, true, nil
}
