package qhistory_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhistory"
)

func app(k string, v int) qhistory.Op { return qhistory.Op{Kind: qhistory.Append, Key: k, Value: v} }
func rd(k string, l ...int) qhistory.Op {
	return qhistory.Op{Kind: qhistory.Read, Key: k, List: append([]int{}, l...)}
}
func txn(id int, ops ...qhistory.Op) qhistory.Txn {
	return qhistory.Txn{ID: id, Ops: ops, Outcome: qhistory.Committed}
}

// Each anomaly, in the smallest history that shows it, is found and
// named, forbidden or allowed as the level says; a serial history shows
// none.
func TestAnomalies(t *testing.T) {
	aborted := txn(2, app("x", 2))
	aborted.Outcome = qhistory.Aborted
	for _, c := range []struct {
		name  string
		kind  string // "" for none
		txns  []qhistory.Txn
		level qhistory.Level // the weakest level that forbids it
	}{
		{"serial", "", []qhistory.Txn{txn(1, app("x", 1)), txn(2, rd("x", 1), app("x", 2)), txn(3, rd("x", 1, 2))}, qhistory.ReadCommitted},
		// x: 1 then 2, y: 2 then 1, as the readers saw: T1 and T2 each wrote first
		{"write cycle", "G0", []qhistory.Txn{txn(1, app("x", 1), app("y", 2)), txn(2, app("x", 2), app("y", 1)),
			txn(3, rd("x", 1, 2), rd("y", 1, 2))}, qhistory.ReadCommitted},
		{"aborted read", "G1a", []qhistory.Txn{txn(1, app("x", 1)), aborted, txn(3, rd("x", 1, 2))}, qhistory.ReadCommitted},
		{"intermediate read", "G1b", []qhistory.Txn{txn(1, app("x", 1), app("x", 2)), txn(2, rd("x", 1))}, qhistory.ReadCommitted},
		// T2 saw T1's x, T1 saw T2's y
		{"circular information flow", "G1c", []qhistory.Txn{txn(1, app("x", 1), rd("y", 1)), txn(2, app("y", 1), rd("x", 1))}, qhistory.ReadCommitted},
		// T2 read x before T1's append, then y after it
		{"read skew", "G-single", []qhistory.Txn{txn(1, app("x", 1), app("y", 1)), txn(2, rd("x"), rd("y", 1)), txn(3, rd("x", 1))}, qhistory.SnapshotIsolation},
		// each read the other's key before the other appended to it
		{"write skew", "G2", []qhistory.Txn{txn(1, rd("x"), rd("y"), app("x", 1)), txn(2, rd("x"), rd("y"), app("y", 1)),
			txn(3, rd("x", 1), rd("y", 1))}, qhistory.Serializable},
		// 1 -rw(x)-> 2 -wr(y)-> 3 -rw(z)-> 4 -wr(w)-> 1: two rw, never adjacent
		{"nonadjacent anti-dependencies", "G-nonadjacent", []qhistory.Txn{txn(1, rd("x"), rd("w", 1)), txn(2, app("x", 1), app("y", 1)),
			txn(3, rd("y", 1), rd("z")), txn(4, app("z", 1), app("w", 1)), txn(5, rd("x", 1), rd("z", 1))}, qhistory.SnapshotIsolation},
		{"duplicate element", "duplicate", []qhistory.Txn{txn(1, app("x", 1)), txn(2, rd("x", 1, 1))}, qhistory.ReadCommitted},
		{"incompatible order", "incompatible-order", []qhistory.Txn{txn(1, app("x", 1)), txn(2, app("x", 2)), txn(3, rd("x", 1, 2)), txn(4, rd("x", 2, 1))}, qhistory.ReadCommitted},
		{"garbage", "garbage", []qhistory.Txn{txn(1, rd("x", 9))}, qhistory.ReadCommitted},
		{"own write unseen", "internal", []qhistory.Txn{txn(1, app("x", 1), rd("x"))}, qhistory.ReadCommitted},
		// its own appends read back in the wrong order (the count matched)
		{"own writes reordered", "internal", []qhistory.Txn{txn(1, app("x", 1), app("x", 2), rd("x", 2, 1))}, qhistory.ReadCommitted},
		// garbage in a read that is not the longest, once seen only as a
		// G1b "of transaction 0"
		{"garbage in a short read", "garbage", []qhistory.Txn{txn(1, app("x", 1)), txn(2, app("x", 2)), txn(3, rd("x", 1, 2)), txn(4, rd("x", 99))}, qhistory.ReadCommitted},
	} {
		for _, level := range []qhistory.Level{qhistory.ReadCommitted, qhistory.SnapshotIsolation, qhistory.Serializable} {
			rep, err := qhistory.Check(level, c.txns)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if c.kind == "" {
				if len(rep.Anomalies) > 0 {
					t.Errorf("%s: %s", c.name, rep)
				}
				continue
			}
			if !rep.Has(c.kind) {
				t.Errorf("%s at %s: want %s, got %s", c.name, level, c.kind, rep)
			}
			if c.kind == "garbage" && rep.Has("G1b") {
				t.Errorf("%s at %s: a G1b of a value no one appended: %s", c.name, level, rep)
			}
			if want := level >= c.level; rep.OK() == want {
				t.Errorf("%s at %s: OK %v, want forbidden %v: %s", c.name, level, rep.OK(), want, rep)
			}
		}
	}
}

// A history that is no list-append workload's is an error, with the fix.
func TestNotAWorkload(t *testing.T) {
	_, err := qhistory.Check(qhistory.Serializable, []qhistory.Txn{txn(1, app("x", 1)), txn(2, app("x", 1))})
	if err == nil || !strings.Contains(err.Error(), "must be unique per key") {
		t.Errorf("Check = %v", err)
	}
	if _, err := qhistory.Check(qhistory.Serializable, []qhistory.Txn{txn(0, app("x", 1))}); err == nil || !strings.Contains(err.Error(), "nonzero") {
		t.Errorf("id 0: Check = %v", err)
	}
}

// A transaction whose outcome is unknown committed if a read saw its
// value, and then its own reads count: T2 read x before T1's append and y
// after it, a read skew if T2 committed, which T3's read of T2's z shows.
// Unseen, it is left out, and so is its read.
func TestUnknownOutcome(t *testing.T) {
	u := txn(2, rd("x"), rd("y", 1), app("z", 1))
	u.Outcome = qhistory.Unknown
	t1 := txn(1, app("x", 1), app("y", 1))
	seen := []qhistory.Txn{t1, u, txn(3, rd("x", 1), rd("z", 1))}
	if rep, _ := qhistory.Check(qhistory.Serializable, seen); !rep.Has("G-single") {
		t.Errorf("seen: %s", rep)
	}
	unseen := []qhistory.Txn{t1, u, txn(3, rd("x", 1), rd("z"))}
	if rep, _ := qhistory.Check(qhistory.Serializable, unseen); !rep.OK() {
		t.Errorf("unseen: %s", rep)
	}
}

// The workload against qenv's store at each isolation level, eight
// clients at once: each level's history passes its own level, and a
// weaker level's shows what a stronger one forbids, read skew under read
// committed and write skew under snapshot isolation, in some run.
func TestStoreHistories(t *testing.T) {
	type found struct{ ok, single, skew bool }
	run := func(iso qenv.Isolation, seed uint64) []qhistory.Txn {
		txns := qhistory.Workload{Keys: 3, Txns: 64, Seed: seed}.Generate()
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults
		s := qenv.New(c)
		defer s.Close()
		db := qenv.NewStore(s, "db", iso)
		for client := range 8 {
			s.Spawn("client", func(ctx context.Context) {
				for i := client; i < len(txns); i += 8 {
					runTxn(ctx, db, &txns[i])
				}
			})
		}
		if err := s.Run(0); err != nil {
			t.Fatal(err)
		}
		return txns
	}
	levels := []qhistory.Level{qhistory.ReadCommitted, qhistory.SnapshotIsolation, qhistory.Serializable}
	got := map[qenv.Isolation]*found{}
	for i, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		f := &found{ok: true}
		got[iso] = f
		for seed := uint64(1); seed <= 20; seed++ {
			txns := run(iso, seed)
			own, err := qhistory.Check(levels[i], txns)
			if err != nil {
				t.Fatal(err)
			}
			if !own.OK() {
				f.ok = false
				t.Errorf("%v, seed %d: the store's history breaks its own level: %s", iso, seed, own)
			}
			strict, _ := qhistory.Check(qhistory.Serializable, txns)
			f.single = f.single || strict.Has("G-single")
			f.skew = f.skew || strict.Has("G2")
		}
	}
	if !got[qenv.ReadCommitted].single {
		t.Error("read committed never showed read skew (G-single) in 20 runs")
	}
	if !got[qenv.SnapshotIsolation].skew {
		t.Error("snapshot isolation never showed write skew (G2) in 20 runs")
	}
	if got[qenv.Serializable].single || got[qenv.Serializable].skew {
		t.Error("serializable showed a cycle")
	}
}

// runTxn runs t against db: an append locks the key's list and writes it
// longer (the database's atomic append), a read reads it. A failed
// operation or commit aborts the transaction.
func runTxn(ctx context.Context, db qenv.DB, t *qhistory.Txn) { runTxnAs(ctx, db, t, false) }

// runTxnAs is runTxn; lockless appends read the list without its lock and
// write it back longer: not the database's atomic append, and two of them
// at once lose one.
func runTxnAs(ctx context.Context, db qenv.DB, t *qhistory.Txn, lockless bool) {
	tx := db.Begin(ctx)
	for i := range t.Ops {
		o := &t.Ops[i]
		switch o.Kind {
		case qhistory.Append:
			var v any
			var err error
			if lockless {
				v, _ = tx.Get(o.Key)
			} else {
				v, _, err = tx.GetForUpdate(o.Key)
			}
			if err != nil {
				tx.Rollback()
				t.Outcome = qhistory.Aborted
				return
			}
			l, _ := v.([]int)
			tx.Put(o.Key, append(slices.Clone(l), o.Value))
		case qhistory.Read:
			v, _ := tx.Get(o.Key)
			l, _ := v.([]int)
			o.List = slices.Clone(l)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Outcome = qhistory.Aborted
		if !errors.Is(err, qenv.ErrConflict) {
			t.Outcome = qhistory.Unknown
		}
		return
	}
	t.Outcome = qhistory.Committed
}

// The checker against a client that misuses the store, and against
// outcomes it never learns: an append that reads without the lock under
// read committed loses appends, which the reads show (two lists neither a
// prefix of the other); commits whose outcome is unknown (the store's
// commit faults) leave each level's history passing its own level.
func TestStoreMisuseAndUnknownOutcomes(t *testing.T) {
	run := func(iso qenv.Isolation, seed uint64, lockless, faults bool) []qhistory.Txn {
		txns := qhistory.Workload{Keys: 2, Txns: 48, Seed: seed}.Generate()
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults
		if faults {
			c.Rates = qenv.NoFaults.Set("store", 0.3)
		}
		s := qenv.New(c)
		defer s.Close()
		db := qenv.NewStore(s, "db", iso)
		db.CommitFaults = faults
		for client := range 6 {
			s.Spawn("client", func(ctx context.Context) {
				for i := client; i < len(txns); i += 6 {
					runTxnAs(ctx, db, &txns[i], lockless)
				}
			})
		}
		if err := s.Run(0); err != nil {
			t.Fatal(err)
		}
		return txns
	}
	lost, unknown := false, 0
	for seed := uint64(1); seed <= 20; seed++ {
		rep, err := qhistory.Check(qhistory.ReadCommitted, run(qenv.ReadCommitted, seed, true, false))
		if err != nil {
			t.Fatal(err)
		}
		lost = lost || rep.Has("incompatible-order") && !rep.OK()
		for i, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
			txns := run(iso, seed, false, true)
			for _, x := range txns {
				if x.Outcome == qhistory.Unknown {
					unknown++
				}
			}
			level := []qhistory.Level{qhistory.ReadCommitted, qhistory.SnapshotIsolation, qhistory.Serializable}[i]
			if rep, _ := qhistory.Check(level, txns); !rep.OK() {
				t.Errorf("%v, seed %d, unknown outcomes: %s", iso, seed, rep)
			}
		}
	}
	if !lost {
		t.Error("lockless appends under read committed never showed a lost append in 20 runs")
	}
	if unknown == 0 {
		t.Error("no commit's outcome was unknown: the faults did not fire")
	}
}
