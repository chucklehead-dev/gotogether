// Package sqlstore is a store.Store over database/sql: grist's real tier
// (PostgreSQL, SQLite) and, on qsql over a qenv.Store, its simulated one,
// both running the same statements (docs/grist.md §2.10). It imports no
// driver: the application opens the database with its own (pgx's stdlib,
// modernc.org/sqlite) and names its Dialect.
//
// Tables (Open creates them):
//
//	grist_heads(tenant, head, steps)             the tenant's lock, its head, its step rows' count
//	grist_events(tenant, pos, type, version, at_ms, cause, tags, body)
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
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

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
}

// Store is a store.Store and store.StepLog over a database/sql database.
type Store struct {
	db      *sql.DB
	d       Dialect
	o       Options
	changed qenv.Signal   // notified after every append's COMMIT, whatever its answer
	commits atomic.Uint64 // the COMMITs tried: a waiter that read before one does not sleep through it
	// sim is the qenv.Store a qsql database runs on (qsql.StoreOf), nil on
	// a real one: its signal and version stand for changed and commits,
	// so a waiter wakes at any client's commit, another Store's over the
	// same database included
	sim *qenv.Store
}

// schema is the tables, in the SQL every dialect runs. Read-only.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS grist_heads (tenant TEXT NOT NULL, head BIGINT NOT NULL, steps BIGINT NOT NULL, PRIMARY KEY (tenant))`,
	`CREATE TABLE IF NOT EXISTS grist_events (tenant TEXT NOT NULL, pos BIGINT NOT NULL, type TEXT NOT NULL, version BIGINT NOT NULL, at_ms BIGINT NOT NULL, cause TEXT NOT NULL, tags TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, pos))`,
	`CREATE TABLE IF NOT EXISTS grist_index (tenant TEXT NOT NULL, k TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, k, pos))`,
	`CREATE TABLE IF NOT EXISTS grist_markers (tenant TEXT NOT NULL, id TEXT NOT NULL, hash TEXT NOT NULL, outcome TEXT NOT NULL, pos BIGINT NOT NULL, events BIGINT NOT NULL, PRIMARY KEY (tenant, id))`,
	`CREATE TABLE IF NOT EXISTS grist_checkpoints (tenant TEXT NOT NULL, processor TEXT NOT NULL, pos BIGINT NOT NULL, PRIMARY KEY (tenant, processor))`,
	`CREATE TABLE IF NOT EXISTS grist_steps (tenant TEXT NOT NULL, seq BIGINT NOT NULL, head BIGINT NOT NULL, at_ns BIGINT NOT NULL, step TEXT NOT NULL, PRIMARY KEY (tenant, seq))`,
	`CREATE TABLE IF NOT EXISTS grist_snapshots (tenant TEXT NOT NULL, name TEXT NOT NULL, k TEXT NOT NULL, fingerprint TEXT NOT NULL, pos BIGINT NOT NULL, state TEXT NOT NULL, checksum TEXT NOT NULL, PRIMARY KEY (tenant, name, k, fingerprint))`,
}

// Open returns a store over db in dialect d, creating its tables if they
// do not exist; on SQLite it checks the single-writer mode (SQLiteDSN).
// It checks the options (a negative duration, a name a recorder cannot
// carry) and begins one transaction at the isolation level, so a level
// the database or its driver does not offer is Open's error, naming the
// fix, rather than every append's transient one.
func Open(ctx context.Context, db *sql.DB, d Dialect, o Options) (*Store, error) {
	if d.name == "" {
		return nil, errors.New("sqlstore: the zero Dialect: use sqlstore.Postgres, sqlstore.SQLite or sqlstore.QSQL")
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
	if d.pragmas {
		if err := checkSQLite(ctx, db); err != nil {
			return nil, err
		}
	}
	for _, q := range schema {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return nil, fmt.Errorf("sqlstore: creating grist's tables (%s): %w", q, err)
		}
	}
	s := &Store{db: db, d: d, o: o, sim: qsql.StoreOf(db)}
	// one transaction at the level, as every append begins one: a level
	// the driver refuses (pgx's "unsupported isolation", no SQLSTATE) would
	// otherwise read as transient, and every append would retry it forever
	if d.pragmas && o.Isolation != sql.LevelDefault && o.Isolation != sql.LevelSerializable {
		// the driver takes any level and runs SQLite's one: a stated level
		// would be a promise nothing keeps
		return nil, fmt.Errorf("sqlstore: Options.Isolation %s on SQLite, whose one writer makes every transaction serializable: leave it unset (or sql.LevelSerializable)", o.Isolation)
	}
	tx, err := s.begin(ctx, false)
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

// begin starts a transaction at the store's level, with the dialect's
// settings.
func (s *Store) begin(ctx context.Context, readOnly bool) (*sql.Tx, error) {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO grist_heads (tenant, head, steps) VALUES ($1, 0, 0) ON CONFLICT (tenant) DO NOTHING`, t)
	s.commits.Add(1) // a tenant's watchers (WaitTenants) wake
	s.changed.Notify()
	return classify(err)
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

// indexed are the positions in (after, through] the index has under the
// key of each of sel's types, distinct, in position order (newest first
// if reverse); with at most one tag every one matches, so a limit is each
// type's query's (the first n of the union are among the first n of each
// type: a read model's first SnapshotEvery events, read from an index of
// every one of its events, without the rest).
func indexed(ctx context.Context, q querier, t string, sel store.Selector, after, through store.Position, reverse bool, limit int) ([]store.Position, error) {
	seen := map[store.Position]bool{}
	var out []store.Position
	order := "ASC"
	if reverse {
		order = "DESC"
	}
	for _, typ := range sel.Types {
		stmt := `SELECT pos FROM grist_index WHERE tenant = $1 AND k = $2 AND pos > $3 AND pos <= $4 ORDER BY pos ` + order
		args := []any{t, indexKey(typ, sel.Tags), int64(after), int64(through)}
		if limit > 0 && len(sel.Tags) <= 1 {
			stmt += ` LIMIT $5`
			args = append(args, limit)
		}
		rows, err := q.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, classify(err)
		}
		for rows.Next() {
			var p int64
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return nil, classify(err)
			}
			if !seen[store.Position(p)] {
				seen[store.Position(p)] = true
				out = append(out, store.Position(p))
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, classify(err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] != reverse })
	return out, nil
}

// event is t's event at p.
func event(ctx context.Context, q querier, t string, p store.Position) (store.Stored, error) {
	var typ, cause, tags, body string
	var version, at int64
	err := q.QueryRowContext(ctx, `SELECT type, version, at_ms, cause, tags, body FROM grist_events WHERE tenant = $1 AND pos = $2`, t, int64(p)).
		Scan(&typ, &version, &at, &cause, &tags, &body)
	if err != nil {
		return store.Stored{}, classify(err)
	}
	var pairs [][2]string
	if err := json.Unmarshal([]byte(tags), &pairs); err != nil {
		return store.Stored{}, store.Permanent(fmt.Errorf("sqlstore: event %d of %s: tags %q: %v", p, t, tags, err))
	}
	e := store.Stored{Event: store.Event{Type: typ, Version: int(version), Body: json.RawMessage(body)}, Tenant: t, Pos: p, Time: at, Cause: cause}
	for _, kv := range pairs {
		e.Tags = append(e.Tags, store.TagPair{Key: kv[0], Value: kv[1]})
	}
	return e, nil
}

// Read implements store.Store: in one read-only transaction, the head,
// the index's positions and each event (rows below the head never
// change, so read committed's statement snapshots agree).
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
	ps, err := indexed(ctx, tx, t, q, q.After, at, q.Reverse, q.Limit)
	if err != nil {
		return nil, 0, err
	}
	for _, p := range ps {
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
		e, err := event(ctx, tx, t, p)
		if err != nil {
			return nil, 0, err
		}
		if q.Matches(e.Event) {
			out = append(out, e)
		}
	}
	return out, at, nil
}

// Append implements store.Store.
func (s *Store) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO grist_events (tenant, pos, type, version, at_ms, cause, tags, body) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			t, p, e.Type, int64(e.Version), b.Time, b.Cause, string(tags), body); err != nil {
			return 0, classify(err)
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
	err = tx.Commit()
	// every COMMIT wakes the waiters, its answer lost or not: one whose
	// outcome is unknown may have committed, and a waiter that slept
	// through it would sleep through its step
	s.commits.Add(1)
	s.changed.Notify()
	if err != nil {
		return 0, errCommitted{classify(err)}
	}
	return store.Position(newHead), nil
}

// check is a condition's selector checked under the lock: the first
// event after after, through h, matching q, as a *store.ConditionFailed.
// With at most one tag the index's first position is the answer; with
// more, the events' other tags are read.
func (s *Store) check(ctx context.Context, tx *sql.Tx, t string, q store.Selector, after, h store.Position) error {
	limit := 0
	if len(q.Tags) <= 1 {
		limit = 1
	}
	var first store.Position
	for _, typ := range q.Types {
		one := store.Selector{Types: []string{typ}, Tags: q.Tags}
		ps, err := indexed(ctx, tx, t, one, after, h, false, limit)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if first != 0 && p >= first {
				break
			}
			if len(q.Tags) > 1 {
				e, err := event(ctx, tx, t, p)
				if err != nil {
					return err
				}
				if !q.Matches(e.Event) {
					continue
				}
			}
			first = p
			break
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
		return h > p, err
	})
}

// WaitSteps implements store.StepLog.
func (s *Store) WaitSteps(ctx context.Context, t string, after int64) error {
	return s.wait(ctx, func() (bool, error) {
		n, err := s.StepCount(ctx, t)
		return n > after, err
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
	signal := &s.changed
	if s.sim != nil {
		signal = s.sim.Changed()
	}
	for {
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
		signal.Wait(ctx, poll)
	}
}

// PutSnapshot implements store.SnapshotStore: one row per (tenant, read
// model, key, fingerprint), the last put's.
func (s *Store) PutSnapshot(ctx context.Context, t string, snap store.Snapshot) error {
	if _, err := head(ctx, s.db, t); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO grist_snapshots (tenant, name, k, fingerprint, pos, state, checksum) VALUES ($1, $2, $3, $4, $5, $6, $7) `+
		`ON CONFLICT (tenant, name, k, fingerprint) DO UPDATE SET pos = excluded.pos, state = excluded.state, checksum = excluded.checksum`,
		t, snap.Name, snap.Key, snap.Fingerprint, int64(snap.Pos), string(snap.State), snap.Sum)
	return classify(err)
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
