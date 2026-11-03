package qhistory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhistory"
)

// lists reads keys "l/<name>" as lists of ints.
func lists(key string, val any) (string, []int, bool) {
	name, ok := strings.CutPrefix(key, "l/")
	if !ok {
		return "", nil, false
	}
	l, _ := val.([]int)
	return name, l, true
}

// fakeDB's transactions commit with err.
type fakeDB struct{ err error }

func (d fakeDB) Begin(context.Context) (qenv.Txn, error) {
	return &fakeTxn{err: d.err, vals: map[string]any{"l/x": []int{1}}}, nil
}

type fakeTxn struct {
	qenv.Txn
	err  error
	vals map[string]any
}

func (t *fakeTxn) Get(k string) (any, bool)                 { v, ok := t.vals[k]; return v, ok }
func (t *fakeTxn) GetForUpdate(k string) (any, bool, error) { v, ok := t.vals[k]; return v, ok, nil }
func (t *fakeTxn) Put(k string, v any)                      { t.vals[k] = v }
func (t *fakeTxn) Commit() error                            { return t.err }
func (t *fakeTxn) Rollback()                                {}

// A transaction's reads and appends of lists are recorded, other keys'
// are not; its outcome is its commit's (an unknown one stays Unknown,
// whatever Rollback follows); one rolled back before committing aborted.
func TestRecordOutcomes(t *testing.T) {
	for _, c := range []struct {
		err      error
		rollback bool // instead of committing
		want     qhistory.Outcome
	}{
		{nil, false, qhistory.Committed},
		{qenv.ErrUnknownOutcome, false, qhistory.Unknown},
		{qenv.ErrConflict, false, qhistory.Aborted},
		{nil, true, qhistory.Aborted},
	} {
		var h qhistory.History
		db := qhistory.Record(fakeDB{c.err}, &h, lists)
		tx, err := db.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		v, _ := tx.Get("l/x")
		tx.Put("l/x", append(v.([]int), 2))
		tx.Put("other", 3)
		if _, _, err := tx.GetForUpdate("other"); err != nil {
			t.Fatal(err)
		}
		if !c.rollback {
			if err := tx.Commit(); !errors.Is(err, c.err) {
				t.Fatalf("Commit: %v", err)
			}
		}
		tx.Rollback()
		got := h.Txns()
		if len(got) != 1 || got[0].ID != 1 || got[0].Outcome != c.want || fmt.Sprint(got[0].Ops) != "[read(x) = [1] append(x, 2)]" {
			t.Errorf("commit error %v (rollback %v): %+v, want outcome %v", c.err, c.rollback, got, c.want)
		}
	}
}

// Recorded from a Store's transactions, the history shows what the
// store's level allows: at read committed two appends to one list both
// read it empty, and the second's list replaces the first's, read in
// between ([1], then [2]: no order of appends gives both), which Check
// finds; at
// serializable the second's commit aborts (recorded Aborted, its retry
// committed), and the history is serializable. Transactions that touch
// no list are left out by WithOps.
func TestRecordStore(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.Serializable} {
		var h qhistory.History
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", iso)
		db := qhistory.Record(st, &h, lists)
		tag := 0 // unique per attempt, as the workload's values must be
		for i := 1; i <= 2; i++ {
			s.Spawn(fmt.Sprint("client", i), func(ctx context.Context) {
				err := qenv.Retry(ctx, db, 5, func(tx qenv.Txn) error {
					v, _ := tx.Get("l/x") // unlocked: at read committed, a lost update
					l, _ := v.([]int)
					// both read, then the first commits (and is read), then the second
					qenv.Sleep(ctx, time.Duration(i)*time.Millisecond)
					tag++
					tx.Put("l/x", append(append([]int(nil), l...), tag))
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			})
		}
		s.Spawn("reader", func(ctx context.Context) {
			qenv.Sleep(ctx, 1500*time.Microsecond)
			_ = qenv.Retry(ctx, db, 1, func(tx qenv.Txn) error { tx.Get("l/x"); return nil })
		})
		s.Spawn("other", func(ctx context.Context) {
			_ = qenv.Retry(ctx, db, 1, func(tx qenv.Txn) error { tx.Put("done", true); return nil })
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		// a final read, which shows what was lost
		s.Spawn("probe", func(ctx context.Context) {
			_ = qenv.Retry(ctx, db, 1, func(tx qenv.Txn) error { tx.Get("l/x"); return nil })
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if len(h.Txns()) != len(h.WithOps())+1 {
			t.Errorf("%v: %d transactions, %d with list operations: want one without (other's)", iso, len(h.Txns()), len(h.WithOps()))
		}
		aborted := 0
		for _, x := range h.Txns() {
			if x.Outcome == qhistory.Aborted {
				aborted++
			}
		}
		if want := map[qenv.Isolation]int{qenv.ReadCommitted: 0, qenv.Serializable: 1}[iso]; aborted != want {
			t.Errorf("%v: %d transactions aborted, want %d: %+v", iso, aborted, want, h.Txns())
		}
		rep, err := qhistory.Check(qhistory.Serializable, h.WithOps())
		if err != nil {
			t.Fatal(err)
		}
		if lost := !rep.OK(); lost != (iso == qenv.ReadCommitted) {
			t.Errorf("%v: Check at serializable: %v; history %+v", iso, rep, h.Txns())
		}
	}
}

// Begin errors are known refusals, never phantom transactions of unknown outcome.
// Reserving their invocation before Begin yields keeps later attempts' IDs stable.
func TestRecordBeginRefusalKeepsInvocationOrder(t *testing.T) {
	sim := qenv.New(qenv.Nominal{})
	defer sim.Close()
	var h qhistory.History
	refused := errors.New("admission refused")
	secondBegan := false
	calls := 0
	db := qhistory.Record(beginDB{begin: func(ctx context.Context) (qenv.Txn, error) {
		calls++
		if calls == 1 {
			qenv.Wait(ctx, func() bool { return secondBegan })
			return nil, refused
		}
		secondBegan = true
		return &fakeTxn{vals: map[string]any{}}, nil
	}}, &h, lists)
	sim.Spawn("refused", func(ctx context.Context) {
		tx, err := db.Begin(ctx)
		if tx != nil || !errors.Is(err, refused) {
			t.Errorf("refused Begin = %v, %v", tx, err)
		}
	})
	sim.Spawn("admitted", func(ctx context.Context) {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		tx.Rollback()
	})
	if err := sim.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	got := h.Txns()
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 || got[0].Outcome != qhistory.Aborted || got[1].Outcome != qhistory.Aborted || len(got[0].Ops) != 0 || len(h.WithOps()) != 0 {
		t.Fatalf("refused and admitted attempts: %+v", got)
	}
}

type beginDB struct {
	begin func(context.Context) (qenv.Txn, error)
}

func (d beginDB) Begin(ctx context.Context) (qenv.Txn, error) { return d.begin(ctx) }

func TestRecordCapacityRefusalThenRecovery(t *testing.T) {
	sim := qenv.New(qenv.Nominal{})
	defer sim.Close()
	st := qenv.NewStore(sim, "db", qenv.Serializable)
	st.MaxTransactions = 1
	var h qhistory.History
	db := qhistory.Record(st, &h, lists)
	sim.Spawn("client", func(ctx context.Context) {
		holder, err := st.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer holder.Rollback()
		if tx, err := db.Begin(ctx); tx != nil || !errors.Is(err, qenv.ErrTransactionCapacity) {
			t.Errorf("capacity refusal: %v, %v", tx, err)
		}
		holder.Rollback()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		tx.Put("l/x", []int{1})
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	if err := sim.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	got := h.Txns()
	if len(got) != 2 || got[0].Outcome != qhistory.Aborted || len(got[0].Ops) != 0 || got[1].ID != 2 || got[1].Outcome != qhistory.Committed || len(h.WithOps()) != 1 {
		t.Fatalf("capacity refusal/recovery history: %+v", got)
	}
}
