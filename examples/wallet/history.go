package wallet

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhistory"
)

// History is the bank's store's transactions as a list-append workload
// sees them: each account a list of entry tags, a read the list it
// returned, a write the tag it appended, each attempt (Retry's included)
// with its outcome. qhistory.Check judges it against an isolation level,
// as it would a real database's (Elle's method).
type History struct {
	Txns []qhistory.Txn
}

// recording is a DB whose transactions History records.
type recording struct {
	db qenv.DB
	h  *History
}

func (r *recording) Begin(ctx context.Context) qenv.Txn {
	i := len(r.h.Txns)
	r.h.Txns = append(r.h.Txns, qhistory.Txn{ID: i + 1})
	// its index first: Begin yields, and another transaction begins meanwhile
	return &recorded{Txn: r.db.Begin(ctx), h: r.h, i: i}
}

// recorded is one transaction, its operations on accounts recorded as it
// makes them. A transaction a crash cuts off stays Unknown.
type recorded struct {
	qenv.Txn
	h *History
	i int
}

func (t *recorded) txn() *qhistory.Txn { return &t.h.Txns[t.i] }

func (t *recorded) readOp(k string, v any) {
	if a, ok := isAccount(k); ok {
		t.txn().Ops = append(t.txn().Ops, qhistory.Op{Kind: qhistory.Read, Key: a, List: tags(entries(v))})
	}
}

func (t *recorded) Get(k string) (any, bool) {
	v, ok := t.Txn.Get(k)
	t.readOp(k, v)
	return v, ok
}

func (t *recorded) GetForUpdate(k string) (any, bool, error) {
	v, ok, err := t.Txn.GetForUpdate(k)
	if err == nil {
		t.readOp(k, v)
	}
	return v, ok, err
}

// Put records an account's write as the append of its last entry: the
// wallet only ever appends.
func (t *recorded) Put(k string, v any) {
	t.Txn.Put(k, v)
	if a, ok := isAccount(k); ok {
		if es := entries(v); len(es) > 0 {
			t.txn().Ops = append(t.txn().Ops, qhistory.Op{Kind: qhistory.Append, Key: a, Value: es[len(es)-1].Tag})
		}
	}
}

func (t *recorded) Commit() error {
	err := t.Txn.Commit()
	if err == nil {
		t.txn().Outcome = qhistory.Committed
	} else {
		t.txn().Outcome = qhistory.Aborted
	}
	return err
}

// Rollback after a commit does nothing (Retry rolls back what it began,
// committed or not), and records nothing either.
func (t *recorded) Rollback() {
	t.Txn.Rollback()
	if t.txn().Outcome == qhistory.Unknown {
		t.txn().Outcome = qhistory.Aborted
	}
}

// OnAccounts is the history without the transactions that touched no
// account (a request's record, the outbox).
func (h *History) OnAccounts() []qhistory.Txn {
	var out []qhistory.Txn
	for _, t := range h.Txns {
		if len(t.Ops) > 0 {
			out = append(out, t)
		}
	}
	return out
}
