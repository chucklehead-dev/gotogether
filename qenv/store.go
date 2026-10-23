package qenv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/qenv/internal/history"
)

// Isolation is a store's contract for concurrent transactions.
type Isolation int

const (
	// Serializable: committed transactions are equivalent to some serial
	// order; a transaction whose reads were overwritten before it commits
	// is aborted (optimistic validation of reads, scans and writes).
	Serializable Isolation = iota
	// SnapshotIsolation: reads come from the snapshot at Begin; of two
	// concurrent transactions writing the same key, the second to commit
	// aborts. Write skew is allowed: two transactions that read what the
	// other writes both commit. (Oracle's and older PostgreSQL's
	// "serializable", PostgreSQL's REPEATABLE READ.)
	SnapshotIsolation
	// ReadCommitted: every read sees the latest committed value, and the
	// last writer wins. Lost updates are allowed. (PostgreSQL's default.)
	ReadCommitted
)

func (i Isolation) String() string {
	return [...]string{"serializable", "snapshot-isolation", "read-committed"}[i]
}

// ErrConflict is returned when a transaction is aborted to keep its
// isolation level: retry it.
var ErrConflict = errors.New("qenv: transaction conflict (serialization failure); retry")

// ErrDeadlock is a transaction aborted because its lock wait would close
// a cycle of waits (PostgreSQL's deadlock_detected). It is an ErrConflict:
// Retry retries it. Locking rows in one order avoids it.
var ErrDeadlock = fmt.Errorf("%w: deadlock (lock waits form a cycle); lock rows in one order", ErrConflict)

// ErrUnknownOutcome is returned by Commit when the connection is lost
// during commit (fault kind "store.<name>.commit"): the transaction may or may
// not have committed, and the caller cannot tell.
var ErrUnknownOutcome = errors.New("qenv: commit outcome unknown (connection lost)")

// DB is the seam of a transactional store. *Store implements it in a
// simulation; in production an adapter over database/sql (or a client of
// any transactional store) implements it, and the code under test sees no
// difference.
type DB interface {
	Begin(ctx context.Context) Txn
}

// Txn is a transaction of a DB.
type Txn interface {
	Get(key string) (any, bool)
	GetForUpdate(key string) (any, bool, error)
	Put(key string, val any)
	Delete(key string)
	Scan(prefix string) []KV
	Commit() error
	Rollback()
	// OnCommit runs fn when the commit is applied, before Commit returns,
	// even if the caller is then told the outcome is unknown: the place to
	// record a model step, whose linearization point is the commit. (In
	// production: write the step to a table in the same transaction.)
	OnCommit(fn func())
}

// Store is a transactional key-value store with a chosen isolation level:
// a stand-in for a SQL database, whose anomalies (lost updates, write skew)
// are exactly those its level allows. Every operation is a seam, so the
// scheduler interleaves concurrent transactions statement by statement.
//
// GetForUpdate locks a row until the transaction ends, as SELECT ... FOR
// UPDATE does. A crashed process's open transactions abort, releasing their
// locks, as a database does when a connection drops.
//
// Values must be treated as immutable: store copies, not pointers that are
// changed afterwards.
type Store struct {
	Name string
	Iso  Isolation
	// CommitFaults enables the fault kind "store.<name>.commit": a commit that is applied
	// but reported as ErrUnknownOutcome, a connection lost after the
	// database committed. Off by default, unlike qenv.Service's crash
	// window: a service's lost response is what every caller of a remote
	// call must handle, where an unknown commit is the database driver's
	// rarer case, and code with nothing to do about it (no way to read
	// its own write back) would fail every exploration. Turn it on for
	// code that claims to handle it.
	CommitFaults bool

	sim   *Sim
	hist  *history.History // under Sim.Check: the committed history, checked against Iso
	rows  map[string][]version
	seq   int64
	locks map[string]*tx
	open  map[*tx]bool
	txs   int64

	commit       FaultKind // "store.<name>.commit"
	noValidation bool      // tests only: a store that keeps no level, for the history check to catch
}

type version struct {
	seq     int64
	val     any
	deleted bool
}

// NewStore returns an empty store in s.
func NewStore(s *Sim, name string, iso Isolation) *Store {
	st := &Store{Name: name, Iso: iso, sim: s, rows: map[string][]version{}, locks: map[string]*tx{}, open: map[*tx]bool{},
		commit: DeclareFaults("store."+name+".commit", "unknownOutcome")} // a commit applied whose outcome the client never learns
	if s.Check {
		st.hist = history.New([...]int{history.Serializable, history.SnapshotIsolation, history.ReadCommitted}[iso])
	}
	s.OnCrash(func(p *Process) {
		var txs []*tx
		for tx := range st.open {
			if tx.proc == p {
				txs = append(txs, tx)
			}
		}
		sort.Slice(txs, func(i, j int) bool { return txs[i].id < txs[j].id })
		for _, tx := range txs {
			tx.end()
		}
	})
	return st
}

// tx is a transaction. It is used by one task.
type tx struct {
	st     *Store
	ctx    context.Context
	proc   *Process
	id     int64
	snap   int64
	reads  map[string]bool
	scans  []string
	writes map[string]*version
	order  []string // write order, for determinism
	locked []string
	hooks  []func()
	done   bool
	// under Check: what the transaction read, for the history
	seen    []history.Read
	scanned []history.Scan
	// waitKeys are the keys whose locks the transaction waits for (nil:
	// none): the deadlock check follows the one still locked
	waitKeys []string
}

// Begin starts a transaction.
func (st *Store) Begin(ctx context.Context) Txn {
	Yield(ctx)
	st.txs++
	tx := &tx{st: st, ctx: ctx, proc: ProcessOf(ctx), id: st.txs, snap: st.seq, reads: map[string]bool{}, writes: map[string]*version{}}
	st.open[tx] = true
	return tx
}

func (tx *tx) check() {
	if tx.done {
		panic("qenv: transaction used after it ended")
	}
}

// visible is the committed version of key a read sees.
func (tx *tx) visible(key string) (version, bool) {
	vs := tx.st.rows[key]
	limit := tx.snap
	if tx.st.Iso == ReadCommitted {
		limit = tx.st.seq
	}
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].seq <= limit {
			return vs[i], !vs[i].deleted
		}
	}
	return version{}, false
}

// Get reads a key: the transaction's own write if any, else the committed
// value its isolation level shows.
func (tx *tx) Get(key string) (any, bool) {
	tx.check()
	Yield(tx.ctx)
	if w, ok := tx.writes[key]; ok {
		return w.val, !w.deleted
	}
	tx.reads[key] = true
	v, ok := tx.visible(key)
	tx.saw(key, v.seq)
	return v.val, ok
}

// saw records a read of key's version seq for the history (Check).
func (tx *tx) saw(key string, seq int64) {
	if tx.st.hist != nil {
		tx.seen = append(tx.seen, history.Read{Key: key, Version: history.Version(seq)})
	}
}

// GetForUpdate locks key until the transaction ends, waiting for another
// transaction's lock, then reads the latest committed value. Under
// Serializable and SnapshotIsolation, a key changed since the snapshot
// aborts the transaction with ErrConflict (PostgreSQL's behaviour).
func (tx *tx) GetForUpdate(key string) (any, bool, error) {
	tx.check()
	Yield(tx.ctx)
	if err := tx.lockWait([]string{key}); err != nil {
		return nil, false, err
	}
	if tx.done {
		return nil, false, ErrConflict
	}
	if tx.st.locks[key] == nil {
		tx.st.locks[key] = tx
		tx.locked = append(tx.locked, key)
	}
	if w, ok := tx.writes[key]; ok {
		return w.val, !w.deleted, nil
	}
	tx.reads[key] = true
	if tx.st.Iso != ReadCommitted && tx.st.latest(key) > tx.snap {
		tx.Rollback()
		return nil, false, ErrConflict
	}
	vs := tx.st.rows[key]
	tx.saw(key, tx.st.latest(key))
	if len(vs) == 0 || vs[len(vs)-1].deleted {
		return nil, false, nil
	}
	return vs[len(vs)-1].val, true, nil
}

// Put writes key at commit.
func (tx *tx) Put(key string, val any) {
	tx.check()
	Yield(tx.ctx)
	if _, ok := tx.writes[key]; !ok {
		tx.order = append(tx.order, key)
	}
	tx.writes[key] = &version{val: val}
}

// Delete removes key at commit.
func (tx *tx) Delete(key string) {
	tx.check()
	Yield(tx.ctx)
	if _, ok := tx.writes[key]; !ok {
		tx.order = append(tx.order, key)
	}
	tx.writes[key] = &version{deleted: true}
}

// KV is a key and its value.
type KV struct {
	Key string
	Val any
}

// Scan returns the keys with prefix, sorted, as Get would read them.
// Under Serializable, a later commit that writes a key with the prefix
// aborts the transaction (no phantoms).
func (tx *tx) Scan(prefix string) []KV {
	tx.check()
	Yield(tx.ctx)
	tx.scans = append(tx.scans, prefix)
	if tx.st.hist != nil {
		limit := tx.snap
		if tx.st.Iso == ReadCommitted {
			limit = tx.st.seq
		}
		tx.scanned = append(tx.scanned, history.Scan{Prefix: prefix, Limit: history.Version(limit)})
	}
	keys := map[string]bool{}
	for k := range tx.st.rows {
		if strings.HasPrefix(k, prefix) {
			keys[k] = true
		}
	}
	for k := range tx.writes {
		if strings.HasPrefix(k, prefix) {
			keys[k] = true
		}
	}
	var out []KV
	for _, k := range sortedSet(keys) {
		if w, ok := tx.writes[k]; ok {
			if !w.deleted {
				out = append(out, KV{k, w.val})
			}
			continue
		}
		if v, ok := tx.visible(k); ok {
			out = append(out, KV{k, v.val})
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (st *Store) latest(key string) int64 {
	if vs := st.rows[key]; len(vs) > 0 {
		return vs[len(vs)-1].seq
	}
	return 0
}

// blocker is the first of keys another transaction has locked, or "".
func (tx *tx) blocker(keys []string) string {
	for _, k := range keys {
		if owner := tx.st.locks[k]; owner != nil && owner != tx {
			return k
		}
	}
	return ""
}

// lockWait waits until no other transaction holds a lock on any of keys.
// The deadlock check follows the chain of waits: the owner of a key tx
// would wait for waits for a key whose owner waits ...; if the chain comes
// back to tx, waiting would never end, and tx aborts with ErrDeadlock. A
// link is the key a waiter's keys still have locked, computed when the
// chain is followed, not when the waiter last looked: on the native
// runtime a waiter looks only when woken, and a key it saw locked may be
// free. A commit's wait is a lock wait as GetForUpdate's is (it was
// invisible to the check). Waiting for every key at once lets lockers
// that take keys one at a time pass a commit, which may wait long; it is
// what makes the commit's writes atomic.
func (tx *tx) lockWait(keys []string) error {
	k := tx.blocker(keys)
	if k == "" {
		return nil
	}
	for o, n := tx.st.locks[k], 0; o != nil && n <= len(tx.st.open); n++ {
		if o == tx {
			tx.Rollback()
			return ErrDeadlock
		}
		next := o.blocker(o.waitKeys)
		if next == "" {
			break
		}
		o = tx.st.locks[next]
	}
	tx.waitKeys = keys
	Wait(tx.ctx, func() bool { return tx.blocker(keys) == "" })
	tx.waitKeys = nil
	return nil
}

// Commit applies the writes, or aborts with ErrConflict as the isolation
// level requires. Writes to keys another transaction has locked wait until
// none is locked, and a wait that would close a cycle of lock waits aborts
// with ErrDeadlock, as GetForUpdate's does. With CommitFaults, a commit may be applied and still
// return ErrUnknownOutcome.
func (tx *tx) Commit() error {
	tx.check()
	Yield(tx.ctx)
	st := tx.st
	// every written key free at once, then validated and applied in that
	// same step: waiting for each in turn let another transaction lock a
	// key already waited for, and the commit wrote under its lock
	if err := tx.lockWait(tx.order); err != nil {
		return err
	}
	if tx.done {
		return ErrConflict
	}
	if st.Iso != ReadCommitted && !st.noValidation {
		for _, k := range tx.order {
			if st.latest(k) > tx.snap {
				tx.end()
				return ErrConflict
			}
		}
	}
	if st.Iso == Serializable && !st.noValidation {
		for k := range tx.reads {
			if st.latest(k) > tx.snap {
				tx.end()
				return ErrConflict
			}
		}
		for _, p := range tx.scans {
			for k, vs := range st.rows {
				if strings.HasPrefix(k, p) && vs[len(vs)-1].seq > tx.snap {
					tx.end()
					return ErrConflict
				}
			}
		}
	}
	var commit int64
	if len(tx.order) > 0 {
		st.seq++
		commit = st.seq
		for _, k := range tx.order {
			w := tx.writes[k]
			st.rows[k] = append(st.rows[k], version{seq: st.seq, val: w.val, deleted: w.deleted})
		}
	}
	if st.hist != nil {
		if err := st.hist.Commit(history.Txn{ID: tx.id, Commit: history.Version(commit), Reads: tx.seen, Scans: tx.scanned, Writes: tx.order}); err != nil {
			panic(fmt.Sprintf("qenv: store %s (%s) committed a history its level forbids: %v (GOTOGETHER_CHECK)", st.Name, st.Iso, err))
		}
	}
	tx.end()
	for _, h := range tx.hooks {
		h()
	}
	if st.CommitFaults && len(tx.order) > 0 && st.sim.FaultOf(st.commit) == "unknownOutcome" {
		return ErrUnknownOutcome
	}
	return nil
}

// OnCommit implements Txn.
func (tx *tx) OnCommit(fn func()) { tx.hooks = append(tx.hooks, fn) }

// Rollback abandons the transaction. It is safe to call after Commit.
func (tx *tx) Rollback() {
	if !tx.done {
		tx.end()
	}
}

func (tx *tx) end() {
	tx.done = true
	for _, k := range tx.locked {
		if tx.st.locks[k] == tx {
			delete(tx.st.locks, k)
		}
	}
	delete(tx.st.open, tx)
}

// Read returns the latest committed value of key, outside any
// transaction: for tests and for checking final states.
func (st *Store) Read(key string) (any, bool) {
	vs := st.rows[key]
	if len(vs) == 0 || vs[len(vs)-1].deleted {
		return nil, false
	}
	return vs[len(vs)-1].val, true
}

// Keys lists the committed live keys with prefix, sorted.
func (st *Store) Keys(prefix string) []string {
	var out []string
	for k := range st.rows {
		if _, ok := st.Read(k); ok && strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Retry runs fn in a new transaction until it commits without
// ErrConflict, at most attempts times. Other errors (including
// ErrUnknownOutcome) are returned as they are.
func Retry(ctx context.Context, db DB, attempts int, fn func(tx Txn) error) error {
	for i := 0; i < attempts; i++ {
		tx := db.Begin(ctx)
		err := fn(tx)
		if err == nil {
			err = tx.Commit()
		}
		tx.Rollback()
		if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return fmt.Errorf("%w (after %d attempts)", ErrConflict, attempts)
}

// Load writes initial data, outside any transaction (as a migration or
// fixture would, before the system starts).
func (st *Store) Load(key string, val any) {
	st.seq++
	st.rows[key] = append(st.rows[key], version{seq: st.seq, val: val})
	if st.hist != nil {
		st.hist.Load(key, history.Version(st.seq))
	}
}
