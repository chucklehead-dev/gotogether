package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/internal/history"
	"github.com/chucklehead-dev/gotogether/qenv/internal/keyed"
)

// panicOf runs f and returns what it panicked with, or "".
func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// runSim runs s to its end (a minute of virtual time at most) and closes it.
func runSim(t *testing.T, s *qenv.Sim) {
	t.Helper()
	defer s.Close()
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
}

// alternate switches tasks at every scheduling point where it can: the
// task picked is never the one picked last while another is runnable.
// Every other choice is nominal.
type alternate struct{ last int }

func (a *alternate) Choose(string, int) int { return 0 }

func (a *alternate) ChooseTask(ids []int) int {
	for i, id := range ids {
		if id != a.last {
			a.last = id
			return i
		}
	}
	a.last = ids[0]
	return 0
}

// interleaved runs, in a task "a" of a simulation under alternate, the
// operation setup returns (setup builds the simulation's doubles, and
// what it returns prepares in a's context and returns the operation),
// next to a task "b" that only yields, and reports whether b ran while
// the operation was in progress: whether it is a scheduling point.
func interleaved(t *testing.T, setup func(s *qenv.Sim) func(ctx context.Context) func()) bool {
	t.Helper()
	s := qenv.New(&alternate{})
	prepare := setup(s)
	var log []string
	s.Spawn("a", func(ctx context.Context) {
		op := prepare(ctx)
		log = append(log, "a0")
		op()
		log = append(log, "a1")
	})
	s.Spawn("b", func(ctx context.Context) {
		for i := 0; i < 20; i++ {
			log = append(log, "b")
			qenv.Yield(ctx)
		}
	})
	runSim(t, s)
	at := func(e string) int {
		for i, x := range log {
			if x == e {
				return i
			}
		}
		t.Fatalf("%s never logged: %v", e, log)
		return 0
	}
	return at("a1")-at("a0") > 1
}

// Every statement of a transaction is a seam (Store's doc): Begin, Get,
// GetForUpdate, Put, Delete, Scan and Commit each let the scheduler run
// another task, which is what lets a seed or Hegel interleave concurrent
// transactions statement by statement. A statement that did not yield
// would run atomically with the next, hiding the anomalies between them.
func TestStoreStatementsAreSchedulingPoints(t *testing.T) {
	ops := map[string]func(qenv.Txn){
		"Get":          func(x qenv.Txn) { x.Get("k") },
		"GetForUpdate": func(x qenv.Txn) { _, _, _ = x.GetForUpdate("k") },
		"Put":          func(x qenv.Txn) { x.Put("k", 2) },
		"Delete":       func(x qenv.Txn) { x.Delete("k") },
		"Scan":         func(x qenv.Txn) { x.Scan("") },
		"Commit":       func(x qenv.Txn) { _ = x.Commit() },
	}
	for name, op := range ops {
		if !interleaved(t, func(s *qenv.Sim) func(context.Context) func() {
			st := qenv.NewStore(s, "db", qenv.Serializable)
			st.Load("k", 1)
			return func(ctx context.Context) func() {
				tx := st.Begin(ctx)
				return func() { op(tx) }
			}
		}) {
			t.Errorf("%s ran without letting another task run", name)
		}
	}
	if !interleaved(t, func(s *qenv.Sim) func(context.Context) func() {
		st := qenv.NewStore(s, "db", qenv.Serializable)
		return func(ctx context.Context) func() { return func() { st.Begin(ctx) } }
	}) {
		t.Error("Begin ran without letting another task run")
	}
}

// Woven sync calls are scheduling points too (keyed.go's doc: taking a
// lock is a point Hegel or a seed chooses around): Lock, TryLock, RLock,
// TryRLock and WaitGroup.Wait each yield even when they need not wait.
func TestKeyedOpsAreSchedulingPoints(t *testing.T) {
	ops := map[string]func(ctx context.Context, key any){
		"Lock":     func(ctx context.Context, k any) { keyed.Lock(ctx, k); keyed.Unlock(ctx, k) },
		"TryLock":  func(ctx context.Context, k any) { keyed.TryLock(ctx, k) },
		"RLock":    func(ctx context.Context, k any) { keyed.RLock(ctx, k); keyed.RUnlock(ctx, k) },
		"TryRLock": func(ctx context.Context, k any) { keyed.TryRLock(ctx, k) },
		"Wait":     func(ctx context.Context, k any) { keyed.Wait(ctx, k) },
	}
	for name, op := range ops {
		key := new(int)
		if !interleaved(t, func(*qenv.Sim) func(context.Context) func() {
			return func(ctx context.Context) func() { return func() { op(ctx, key) } }
		}) {
			t.Errorf("%s ran without letting another task run", name)
		}
	}
}

// A woven Mutex excludes: a second Lock waits, in virtual time, until the
// holder unlocks; TryLock fails while it is held, by a writer or a
// reader, and succeeds when it is free. Unlocking an unlocked mutex
// panics, as sync's does.
func TestKeyedMutex(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	key := new(int)
	var log []string
	s.Spawn("a", func(ctx context.Context) {
		if !keyed.TryLock(ctx, key) {
			t.Error("TryLock of a free mutex failed")
			return
		}
		log = append(log, "a in")
		qenv.Sleep(ctx, 10*time.Millisecond)
		log = append(log, "a out")
		keyed.Unlock(ctx, key)
		qenv.Sleep(ctx, 10*time.Millisecond)
		keyed.RLock(ctx, key)
		qenv.Sleep(ctx, 10*time.Millisecond)
		keyed.RUnlock(ctx, key)
	})
	s.Spawn("b", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		if keyed.TryLock(ctx, key) {
			t.Error("TryLock of a held mutex succeeded")
		}
		keyed.Lock(ctx, key)
		log = append(log, "b in")
		keyed.Unlock(ctx, key)
		qenv.Sleep(ctx, 15*time.Millisecond) // a holds it shared
		if keyed.TryLock(ctx, key) {
			t.Error("TryLock of a mutex held by a reader succeeded")
		}
		if msg := panicOf(func() { keyed.Unlock(ctx, new(int)) }); !strings.Contains(msg, "unlock of unlocked mutex") {
			t.Errorf("Unlock of an unlocked mutex: %q, want a panic", msg)
		}
	})
	runSim(t, s)
	if want := []string{"a in", "a out", "b in"}; !reflect.DeepEqual(log, want) {
		t.Errorf("%v, want %v", log, want)
	}
}

// A woven RWMutex shares among readers and excludes writers: RLock waits
// while a writer holds the lock, TryRLock fails then and succeeds when it
// is free, and RUnlock of an unlocked RWMutex panics.
func TestKeyedRWMutex(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	key := new(int)
	var log []string
	s.Spawn("writer", func(ctx context.Context) {
		keyed.Lock(ctx, key)
		log = append(log, "w in")
		qenv.Sleep(ctx, 10*time.Millisecond)
		log = append(log, "w out")
		keyed.Unlock(ctx, key)
	})
	s.Spawn("reader", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		if keyed.TryRLock(ctx, key) {
			t.Error("TryRLock while a writer holds the lock succeeded")
		}
		keyed.RLock(ctx, key)
		log = append(log, "r in")
		keyed.RUnlock(ctx, key)
		if !keyed.TryRLock(ctx, key) {
			t.Error("TryRLock of a free lock failed")
		}
		if !keyed.TryRLock(ctx, key) {
			t.Error("a second reader's TryRLock failed")
		}
		keyed.RUnlock(ctx, key)
		keyed.RUnlock(ctx, key)
		if msg := panicOf(func() { keyed.RUnlock(ctx, key) }); !strings.Contains(msg, "RUnlock of unlocked RWMutex") {
			t.Errorf("RUnlock of an unlocked RWMutex: %q, want a panic", msg)
		}
	})
	runSim(t, s)
	if want := []string{"w in", "w out", "r in"}; !reflect.DeepEqual(log, want) {
		t.Errorf("%v, want %v", log, want)
	}
}

// A woven WaitGroup's Wait waits until the counter is back to zero, Add
// and Done to zero are fine, and a negative counter panics, as sync's
// does. Outside a simulation task the keyed calls panic, naming the call.
func TestKeyedWaitGroup(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	key := new(int)
	var waited time.Duration
	s.Spawn("main", func(ctx context.Context) {
		start := qenv.Now(ctx)
		keyed.Add(ctx, key, 2)
		keyed.Add(ctx, key, -1)
		qenv.Go(ctx, "done", func(ctx context.Context) {
			qenv.Sleep(ctx, 10*time.Millisecond)
			keyed.Add(ctx, key, -1)
		})
		keyed.Wait(ctx, key)
		waited = qenv.Now(ctx).Sub(start)
		if msg := panicOf(func() { keyed.Add(ctx, key, -1) }); !strings.Contains(msg, "negative WaitGroup counter") {
			t.Errorf("Done below zero: %q, want a panic", msg)
		}
	})
	runSim(t, s)
	if waited != 10*time.Millisecond {
		t.Errorf("Wait returned after %v, want 10ms (when the counter reached zero)", waited)
	}
	if msg := panicOf(func() { keyed.Lock(context.Background(), key) }); !strings.Contains(msg, "Lock outside a simulation task") {
		t.Errorf("Lock outside a task: %q, want a panic", msg)
	}
}

// A second Store of one name in a simulation panics, naming the fix: its
// commit fault kind would be the first's.
func TestDuplicateStoreName(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	qenv.NewStore(s, "db", qenv.Serializable)
	if msg := panicOf(func() { qenv.NewStore(s, "db", qenv.ReadCommitted) }); !strings.Contains(msg, `a second Store named "db"`) {
		t.Errorf("recovered %q, want the duplicate named", msg)
	}
}

// Every statement of a transaction that has ended (committed or rolled
// back) panics: a use after the end would otherwise read or write outside
// any transaction, silently.
func TestStoreTxnUsedAfterEnd(t *testing.T) {
	ops := map[string]func(qenv.Txn){
		"Get":          func(x qenv.Txn) { x.Get("k") },
		"GetForUpdate": func(x qenv.Txn) { _, _, _ = x.GetForUpdate("k") },
		"Put":          func(x qenv.Txn) { x.Put("k", 1) },
		"Delete":       func(x qenv.Txn) { x.Delete("k") },
		"Scan":         func(x qenv.Txn) { x.Scan("") },
		"Commit":       func(x qenv.Txn) { _ = x.Commit() },
	}
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	s.Spawn("t", func(ctx context.Context) {
		for name, op := range ops {
			for _, end := range []string{"commit", "rollback"} {
				tx := st.Begin(ctx)
				if end == "commit" {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					tx.Rollback()
				}
				if msg := panicOf(func() { op(tx) }); !strings.Contains(msg, "transaction used after it ended") {
					t.Errorf("%s after %s: %q, want a panic", name, end, msg)
				}
			}
		}
	})
	runSim(t, s)
}

// Delete removes a key at commit, as a write: the transaction reads its
// own delete, others see the key until the commit, and afterwards Read
// and Keys do not show it.
func TestStoreDelete(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.Load("k", 1)
	st.Load("j", 1)
	s.Spawn("t", func(ctx context.Context) {
		tx := st.Begin(ctx)
		tx.Delete("k")
		if _, ok := tx.Get("k"); ok {
			t.Error("the transaction reads the key it deleted")
		}
		if _, ok := st.Read("k"); !ok {
			t.Error("an uncommitted delete is visible")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	runSim(t, s)
	if v, ok := st.Read("k"); ok {
		t.Errorf("k = %v after its delete committed", v)
	}
	if got := st.Keys(""); !reflect.DeepEqual(got, []string{"j"}) {
		t.Errorf("Keys = %v, want [j]", got)
	}
}

// Scan returns exactly the keys with the prefix, sorted, as Get would read
// each: the transaction's own puts and deletes over the committed values,
// committed deletes absent, keys outside the prefix absent. Twenty keys,
// so a scan in map order is never sorted by chance.
func TestStoreScan(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	for i := 19; i >= 0; i-- {
		st.Load(fmt.Sprintf("a/%02d", i), i)
	}
	st.Load("b/x", "b")
	st.Load("ab", "ab") // shares a first letter, not the prefix
	var got []qenv.KV
	s.Spawn("t", func(ctx context.Context) {
		if err := qenv.Retry(ctx, st, 1, func(tx qenv.Txn) error { tx.Delete("a/05"); return nil }); err != nil {
			t.Fatal(err)
		}
		tx := st.Begin(ctx)
		defer tx.Rollback()
		tx.Put("a/01", "new")
		tx.Delete("a/02")
		tx.Put("a/20", 20)
		tx.Put("b/y", "b")
		got = tx.Scan("a/")
	})
	runSim(t, s)
	var want []qenv.KV
	for i := 0; i <= 20; i++ {
		switch i {
		case 1:
			want = append(want, qenv.KV{Key: "a/01", Val: "new"})
		case 2, 5:
		default:
			want = append(want, qenv.KV{Key: fmt.Sprintf("a/%02d", i), Val: i})
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scan(a/) =\n%v\nwant\n%v", got, want)
	}
}

// Keys lists the live committed keys with the prefix, sorted: not keys
// outside it, not deleted ones.
func TestStoreKeys(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	for i := 19; i >= 0; i-- {
		st.Load(fmt.Sprintf("a/%02d", i), i)
		st.Load(fmt.Sprintf("b/%02d", i), i)
	}
	s.Spawn("t", func(ctx context.Context) {
		if err := qenv.Retry(ctx, st, 1, func(tx qenv.Txn) error { tx.Delete("a/07"); return nil }); err != nil {
			t.Fatal(err)
		}
	})
	runSim(t, s)
	var want []string
	for i := 0; i < 20; i++ {
		if i != 7 {
			want = append(want, fmt.Sprintf("a/%02d", i))
		}
	}
	if got := st.Keys("a/"); !reflect.DeepEqual(got, want) {
		t.Errorf("Keys(a/) = %v, want %v", got, want)
	}
}

// Retry makes exactly the attempts it is given, and says so; it rolls
// back an attempt whose function failed, so the locks it took do not
// outlive it (another transaction's GetForUpdate of the row then goes
// through).
func TestRetryAttemptsAndRollback(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.Load("l", 0)
	calls := 0
	locked := false
	boom := errors.New("boom")
	s.Spawn("t", func(ctx context.Context) {
		err := qenv.Retry(ctx, st, 3, func(qenv.Txn) error { calls++; return qenv.ErrConflict })
		if !errors.Is(err, qenv.ErrConflict) || !strings.Contains(err.Error(), "after 3 attempts") {
			t.Errorf("Retry: %v, want ErrConflict after 3 attempts", err)
		}
		err = qenv.Retry(ctx, st, 3, func(tx qenv.Txn) error {
			_, _, err := tx.GetForUpdate("l")
			if err != nil {
				return err
			}
			return boom
		})
		if err != boom {
			t.Errorf("Retry: %v, want the function's error", err)
		}
		tx := st.Begin(ctx)
		if _, _, err := tx.GetForUpdate("l"); err != nil {
			t.Error(err)
		}
		tx.Rollback()
		locked = true
	})
	runSim(t, s)
	if calls != 3 {
		t.Errorf("%d attempts, want 3", calls)
	}
	if !locked {
		t.Error("the row stayed locked by a failed attempt Retry did not roll back")
	}
}

// A transaction aborted with ErrConflict has ended, without the caller's
// Rollback: the row it locked with GetForUpdate is free for the next
// transaction. Each way to conflict: GetForUpdate of a row changed since
// the snapshot, and at commit a write-write conflict, a read overwritten
// (Serializable) and a phantom in a scan (Serializable).
func TestConflictEndsTheTransaction(t *testing.T) {
	cases := []struct {
		name string
		iso  qenv.Isolation
		// before runs in the transaction before another commits a write
		// of "k" and an insert of "p/1"; after, once they have committed,
		// returns the transaction's error
		before func(tx qenv.Txn)
		after  func(tx qenv.Txn) error
	}{
		{"GetForUpdate of a changed row", qenv.SnapshotIsolation, func(qenv.Txn) {},
			func(tx qenv.Txn) error { _, _, err := tx.GetForUpdate("k"); return err }},
		{"write-write", qenv.SnapshotIsolation, func(tx qenv.Txn) { tx.Put("k", 9) }, nil},
		{"read overwritten", qenv.Serializable, func(tx qenv.Txn) { tx.Get("k"); tx.Put("z", 9) }, nil},
		{"phantom", qenv.Serializable, func(tx qenv.Txn) { tx.Scan("p/"); tx.Put("z", 9) }, nil},
	}
	for _, c := range cases {
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", c.iso)
		st.Load("k", 0)
		st.Load("l", 0)
		var err1 error
		free := false
		s.Spawn("t1", func(ctx context.Context) {
			tx := st.Begin(ctx)
			if c.after == nil { // the lock is taken first; the commit conflicts
				if _, _, err := tx.GetForUpdate("l"); err != nil {
					t.Fatal(err)
				}
			}
			c.before(tx)
			qenv.Sleep(ctx, 10*time.Millisecond)
			if c.after != nil {
				// GetForUpdate locks the row before it finds it changed
				err1 = c.after(tx)
			} else {
				err1 = tx.Commit()
			}
			lock := map[bool]string{true: "k", false: "l"}[c.after != nil]
			tx2 := st.Begin(ctx)
			if _, _, err := tx2.GetForUpdate(lock); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			tx2.Rollback()
			free = true
		})
		s.Spawn("t2", func(ctx context.Context) {
			err := qenv.Retry(ctx, st, 1, func(tx qenv.Txn) error { tx.Put("k", 1); tx.Put("p/1", 1); return nil })
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		})
		runSim(t, s)
		if !errors.Is(err1, qenv.ErrConflict) || errors.Is(err1, qenv.ErrDeadlock) {
			t.Errorf("%s: %v, want ErrConflict", c.name, err1)
		}
		if !free {
			t.Errorf("%s: the aborted transaction kept its lock", c.name)
		}
	}
}

// A Serializable scan aborts its transaction only for a change under its
// prefix: commits elsewhere in the store, and unchanged rows under it,
// leave it free to commit.
func TestSerializableScanIgnoresOtherKeys(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.Load("p/1", 1)
	st.Load("q/1", 1)
	var err error
	s.Spawn("t1", func(ctx context.Context) {
		tx := st.Begin(ctx)
		tx.Scan("p/")
		tx.Put("z", 1)
		qenv.Sleep(ctx, 10*time.Millisecond)
		err = tx.Commit()
	})
	s.Spawn("t2", func(ctx context.Context) {
		_ = qenv.Retry(ctx, st, 1, func(tx qenv.Txn) error { tx.Put("q/2", 1); return nil })
	})
	runSim(t, s)
	if err != nil {
		t.Errorf("commit after a scan of an unchanged prefix: %v", err)
	}
}

// brokenStore runs setup on a store at iso that keeps no level, with the
// history check on, and returns what the commit that broke the level
// panicked with ("" if none did).
func brokenStore(t *testing.T, iso qenv.Isolation, validate bool, setup func(s *qenv.Sim, st *qenv.Store)) (msg string, st *qenv.Store) {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Check = true
	st = qenv.NewStore(s, "db", iso)
	if !validate {
		qenv.BreakValidation(st)
	}
	setup(s, st)
	msg = panicOf(func() {
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
	})
	return msg, st
}

// Write skew through a locking read: t1 reads x FOR UPDATE and writes y;
// t2, whose snapshot predates t1's commit, reads y and then writes x.
// Snapshot isolation commits both (write skew, which it allows);
// Serializable aborts t2; and a store that keeps no level, committing
// both under Serializable, is caught by the history check as G2, which
// needs GetForUpdate's read in the history (the rw edge t1 -> t2 on x).
func TestWriteSkewThroughGetForUpdate(t *testing.T) {
	var errs [2]error
	setup := func(s *qenv.Sim, st *qenv.Store) {
		errs = [2]error{}
		st.Load("x", 0)
		st.Load("y", 0)
		s.Spawn("t2", func(ctx context.Context) {
			tx := st.Begin(ctx)
			tx.Get("y")
			qenv.Sleep(ctx, 10*time.Millisecond)
			tx.Put("x", 2)
			errs[1] = tx.Commit()
		})
		s.Spawn("t1", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			tx := st.Begin(ctx)
			if _, _, err := tx.GetForUpdate("x"); err != nil {
				errs[0] = err
				return
			}
			tx.Put("y", 1)
			errs[0] = tx.Commit()
		})
	}
	if msg, _ := brokenStore(t, qenv.SnapshotIsolation, true, setup); msg != "" || errs != [2]error{} {
		t.Errorf("snapshot isolation: %q, %v; want both committed", msg, errs)
	}
	if msg, _ := brokenStore(t, qenv.Serializable, true, setup); msg != "" || errs[0] != nil || !errors.Is(errs[1], qenv.ErrConflict) {
		t.Errorf("serializable: %q, %v; want t2 aborted", msg, errs)
	}
	if msg, _ := brokenStore(t, qenv.Serializable, false, setup); !strings.Contains(msg, "committed a history its level forbids: G2") {
		t.Errorf("broken serializable store: %q, want G2 caught", msg)
	}
}

// Write skew through phantoms: t1 scans p/ (empty) and inserts q/1; t2
// scans q/ after t1's commit, from its older snapshot (so it does not see
// q/1), and inserts p/1. Snapshot isolation commits both; Serializable
// aborts t2; a store that keeps no level is caught as G2, which needs
// both scans in the history, each as of its snapshot.
func TestPhantomWriteSkew(t *testing.T) {
	var errs [2]error
	var seen []qenv.KV
	setup := func(s *qenv.Sim, st *qenv.Store) {
		errs = [2]error{}
		st.Load("x", 0)
		s.Spawn("t2", func(ctx context.Context) {
			tx := st.Begin(ctx)
			qenv.Sleep(ctx, 10*time.Millisecond)
			seen = tx.Scan("q/")
			tx.Put("p/1", 1)
			errs[1] = tx.Commit()
		})
		s.Spawn("t1", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			tx := st.Begin(ctx)
			tx.Scan("p/")
			tx.Put("q/1", 1)
			errs[0] = tx.Commit()
		})
	}
	if msg, _ := brokenStore(t, qenv.SnapshotIsolation, true, setup); msg != "" || errs != [2]error{} || len(seen) != 0 {
		t.Errorf("snapshot isolation: %q, %v, t2 saw %v; want both committed, t2 seeing nothing", msg, errs, seen)
	}
	if msg, _ := brokenStore(t, qenv.Serializable, true, setup); msg != "" || errs[0] != nil || !errors.Is(errs[1], qenv.ErrConflict) {
		t.Errorf("serializable: %q, %v; want t2 aborted", msg, errs)
	}
	if msg, _ := brokenStore(t, qenv.Serializable, false, setup); !strings.Contains(msg, "committed a history its level forbids: G2") {
		t.Errorf("broken serializable store: %q, want G2 caught", msg)
	}
}

// commitAll adds txs to a history at level in order and returns the
// first violation's text, or "".
func commitAll(level int, load map[string]history.Version, txs ...history.Txn) string {
	h := history.New(level)
	for _, k := range []string{"a", "b", "w", "x", "y"} {
		if v, ok := load[k]; ok {
			h.Load(k, v)
		}
	}
	for _, tx := range txs {
		if err := h.Commit(tx); err != nil {
			return err.Error()
		}
	}
	return ""
}

// Read skew: t2 reads a before and b after t1 wrote both. Its cycle
// (t2 -rw-> t1 -wr-> t2) is G-single, which snapshot isolation forbids
// and read committed allows; it needs the wr edge from the version's
// writer.
func TestHistoryReadSkew(t *testing.T) {
	load := map[string]history.Version{"a": 1, "b": 2}
	t1 := history.Txn{ID: 1, Commit: 3, Writes: []string{"a", "b"}}
	t2 := history.Txn{ID: 2, Reads: []history.Read{{Key: "a", Version: 1}, {Key: "b", Version: 3}}}
	if got := commitAll(history.ReadCommitted, load, t1, t2); got != "" {
		t.Errorf("read committed: %q, want allowed", got)
	}
	want := "G-single (a lost update or read skew): T2 -rw(a)-> T1, T1 -wr(b)-> T2"
	for _, level := range []int{history.SnapshotIsolation, history.Serializable} {
		if got := commitAll(level, load, t1, t2); got != want {
			t.Errorf("level %d: %q, want %q", level, got, want)
		}
	}
}

// A violation names its edges by key in key order, whatever order the
// transaction listed its reads and writes in: the report is the same for
// the same history. A lost update of two keys reports a's edges; of two
// dirty reads, the first key's.
func TestHistoryReportIsInKeyOrder(t *testing.T) {
	load := map[string]history.Version{"a": 1, "b": 2}
	reads := []history.Read{{Key: "b", Version: 2}, {Key: "a", Version: 1}}
	t1 := history.Txn{ID: 1, Commit: 3, Reads: reads, Writes: []string{"b", "a"}}
	t2 := history.Txn{ID: 2, Commit: 4, Reads: reads, Writes: []string{"b", "a"}}
	want := "G-single (a lost update or read skew): T2 -rw(a)-> T1, T1 -ww(a)-> T2"
	if got := commitAll(history.SnapshotIsolation, load, t1, t2); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	dirty := history.Txn{ID: 1, Reads: []history.Read{{Key: "z", Version: 9}, {Key: "a", Version: 8}}}
	if got := commitAll(history.ReadCommitted, nil, dirty); !strings.HasPrefix(got, "G1a (a dirty read): T1 read version 8 of a,") {
		t.Errorf("%q, want the dirty read of a", got)
	}
}

// Phantoms: t1 scans p/ and inserts q/1, t2 scans q/ as of before t1's
// insert and inserts p/1. Each inserted key is a phantom for the other's
// scan (an rw edge from the scanner to the inserter), a G2 cycle that
// only serializability forbids. Keys outside a scan's prefix are not
// its reads.
func TestHistoryPhantoms(t *testing.T) {
	load := map[string]history.Version{"x": 1}
	t1 := history.Txn{ID: 1, Commit: 2, Scans: []history.Scan{{Prefix: "p/", Limit: 1}}, Writes: []string{"q/1"}}
	t2 := history.Txn{ID: 2, Commit: 3, Scans: []history.Scan{{Prefix: "q/", Limit: 1}}, Writes: []string{"p/1"}}
	if got := commitAll(history.SnapshotIsolation, load, t1, t2); got != "" {
		t.Errorf("snapshot isolation: %q, want allowed", got)
	}
	if got := commitAll(history.Serializable, load, t1, t2); !strings.HasPrefix(got, "G2: T2 -rw(q/1)-> T1, T1 -rw(p/1)-> T2") {
		t.Errorf("serializable: %q, want G2 through both phantoms", got)
	}
}

// A scan as of Limit saw every version at or before Limit: a key inserted
// at exactly Limit is no phantom for it. (Commits come in any order of
// version here: the history accepts them so.) Here the scanner s and the
// inserter i are otherwise linked only by i -rw-> s, so a phantom edge
// would close a G2 cycle.
func TestHistoryScanLimitIsInclusive(t *testing.T) {
	load := map[string]history.Version{"w": 1}
	s := history.Txn{ID: 1, Commit: 4, Scans: []history.Scan{{Prefix: "p/", Limit: 3}}, Writes: []string{"w"}}
	i := history.Txn{ID: 2, Commit: 3, Reads: []history.Read{{Key: "w", Version: 1}}, Writes: []string{"p/x"}}
	if got := commitAll(history.Serializable, load, s, i); got != "" {
		t.Errorf("%q, want no phantom at the scan's own limit", got)
	}
}

// A key's versions are kept in version order whatever order their commits
// arrive in: the version after the one a transaction read is the next by
// number. Here r read x's version 1, after which b installed 3 (and a 5,
// committed first), and r read b's y: r -rw-> b -wr-> r, G-single.
func TestHistoryVersionsInOrder(t *testing.T) {
	load := map[string]history.Version{"x": 1}
	a := history.Txn{ID: 1, Commit: 5, Writes: []string{"x"}}
	b := history.Txn{ID: 2, Commit: 3, Writes: []string{"x", "y"}}
	r := history.Txn{ID: 3, Reads: []history.Read{{Key: "x", Version: 1}, {Key: "y", Version: 3}}}
	if got := commitAll(history.SnapshotIsolation, load, a, b, r); !strings.HasPrefix(got, "G-single (a lost update or read skew): T3 -rw(x)-> T2") {
		t.Errorf("%q, want r's read of x anti-depending on b", got)
	}
}
