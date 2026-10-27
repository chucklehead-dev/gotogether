package qhistory

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A History is the transactions a recording DB (Record) began, in the
// order they began, each with the reads and appends it made on the
// workload's lists and its outcome as its client saw it.
type History struct {
	mu   sync.Mutex
	txns []Txn
}

// Txns is the history: every transaction begun, those that touched no
// list included (with no operations), for Check.
func (h *History) Txns() []Txn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.txns)
}

// WithOps is the history without the transactions that read or appended
// no list (an application's other rows: a request's record, an outbox).
func (h *History) WithOps() []Txn {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Txn
	for _, t := range h.txns {
		if len(t.Ops) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// Lists says which of a DB's keys hold a workload's lists, and how to
// read one: for a store key and its value (nil if absent), the list's
// name in the history and its elements (the appended values, in order).
// ok is false for a key that holds no list, whose operations go
// unrecorded.
type Lists func(key string, val any) (name string, list []int, ok bool)

// Record wraps db so that its transactions are recorded in h as a
// list-append workload (Elle's) sees them, for Check to judge against an
// isolation level as it would a real database's:
//   - a Get or a successful GetForUpdate of a list's key is a Read of the
//     list it returned;
//   - a Put of a list's key is an Append of its last element: the
//     workload appends one element at a time, so a value put is the list
//     read with one element more (a Put of an empty list records nothing);
//   - Commit's outcome: nil is Committed, qenv.ErrUnknownOutcome stays
//     Unknown (applied or not, the client cannot tell; Check decides by
//     whether a read saw its values), any other error Aborted;
//   - a Rollback before any Commit is Aborted, and one after it (qenv.Retry
//     rolls back what it began, committed or not) records nothing, so an
//     unknown commit stays Unknown;
//   - a transaction a crash cut off, never ended, stays Unknown.
//
// Each attempt of a qenv.Retry is a transaction of its own. Scans and
// Deletes are not recorded: a list-append workload makes none on its
// lists. Every call reaches db's transaction as it was made, so the
// store's level, faults and checks see what they would without Record.
func Record(db qenv.DB, h *History, lists Lists) qenv.DB {
	return &recordingDB{db: db, h: h, lists: lists}
}

type recordingDB struct {
	db    qenv.DB
	h     *History
	lists Lists
}

func (r *recordingDB) Begin(ctx context.Context) qenv.Txn {
	// the transaction's index first: Begin yields, and another
	// transaction may begin meanwhile
	r.h.mu.Lock()
	i := len(r.h.txns)
	r.h.txns = append(r.h.txns, Txn{ID: i + 1})
	r.h.mu.Unlock()
	return &recordedTxn{Txn: r.db.Begin(ctx), r: r, i: i}
}

// recordedTxn is one transaction, its operations on lists recorded as it
// makes them.
type recordedTxn struct {
	qenv.Txn
	r     *recordingDB
	i     int
	ended bool // committed, or tried to: a Rollback after it changes nothing
}

// update changes the transaction's record under the history's lock.
func (t *recordedTxn) update(f func(*Txn)) {
	t.r.h.mu.Lock()
	defer t.r.h.mu.Unlock()
	f(&t.r.h.txns[t.i])
}

func (t *recordedTxn) read(k string, v any) {
	if name, list, ok := t.r.lists(k, v); ok {
		t.update(func(x *Txn) { x.Ops = append(x.Ops, Op{Kind: Read, Key: name, List: list}) })
	}
}

func (t *recordedTxn) Get(k string) (any, bool) {
	v, ok := t.Txn.Get(k)
	t.read(k, v)
	return v, ok
}

func (t *recordedTxn) GetForUpdate(k string) (any, bool, error) {
	v, ok, err := t.Txn.GetForUpdate(k)
	if err == nil {
		t.read(k, v)
	}
	return v, ok, err
}

func (t *recordedTxn) Put(k string, v any) {
	t.Txn.Put(k, v)
	if name, list, ok := t.r.lists(k, v); ok && len(list) > 0 {
		t.update(func(x *Txn) { x.Ops = append(x.Ops, Op{Kind: Append, Key: name, Value: list[len(list)-1]}) })
	}
}

func (t *recordedTxn) Commit() error {
	err := t.Txn.Commit()
	t.ended = true
	switch {
	case err == nil:
		t.update(func(x *Txn) { x.Outcome = Committed })
	case errors.Is(err, qenv.ErrUnknownOutcome):
		// Unknown: a read that saw one of its values says it committed
	default:
		t.update(func(x *Txn) { x.Outcome = Aborted })
	}
	return err
}

func (t *recordedTxn) Rollback() {
	t.Txn.Rollback()
	if !t.ended {
		t.ended = true
		t.update(func(x *Txn) { x.Outcome = Aborted })
	}
}
