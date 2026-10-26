package qsql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// sqlState is the interface retry loops written for PostgreSQL drivers
// look for.
type sqlState interface{ SQLState() string }

func code(err error) string {
	var e sqlState
	if errors.As(err, &e) {
		return e.SQLState()
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// world is a simulation with a store and a database over it, the
// accounts table created and loaded.
type world struct {
	s  *qenv.Sim
	st *qenv.Store
	db *sql.DB
}

func newWorld(t *testing.T, c qenv.Chooser, iso qenv.Isolation, o ...qsql.Options) *world {
	t.Helper()
	s := qenv.New(c)
	s.Check = true // the Store's history checker judges every commit
	st := qenv.NewStore(s, "paydb", iso)
	w := &world{s: s, st: st, db: qsql.Open(s, st, o...)}
	t.Cleanup(s.Close)
	w.exec(t, `CREATE TABLE accounts (id TEXT PRIMARY KEY, balance BIGINT NOT NULL)`)
	w.exec(t, `INSERT INTO accounts (id, balance) VALUES ('alice', 100), ('bob', 100)`)
	return w
}

// exec runs a statement from the test, outside any task (before Run).
func (w *world) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (w *world) balance(t *testing.T, id string) int64 {
	t.Helper()
	for _, r := range qsql.Table(w.st, "accounts") {
		if r["id"] == id {
			return r["balance"].(int64)
		}
	}
	t.Fatalf("no account %s", id)
	return 0
}

func (w *world) run(t *testing.T) {
	t.Helper()
	if err := w.s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
}

// withdraw is a withdrawal of 10 from alice, written three ways; the
// first task reads, then sleeps while the second runs whole, then writes.
type withdraw func(ctx context.Context, tx *sql.Tx, pause func()) error

var withdrawals = map[string]withdraw{
	// read, then write what was read minus 10: a lost update at read
	// committed
	"read-then-write": func(ctx context.Context, tx *sql.Tx, pause func()) error {
		var b int64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?`, "alice").Scan(&b); err != nil {
			return err
		}
		pause()
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = ? WHERE id = ?`, b-10, "alice")
		return err
	},
	// the database subtracts: no read to lose
	"relative": func(ctx context.Context, tx *sql.Tx, pause func()) error {
		var b int64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?`, "alice").Scan(&b); err != nil {
			return err
		}
		pause()
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - ? WHERE id = ?`, 10, "alice")
		return err
	},
	// the read locks the row: the second waits for the first to commit
	"for-update": func(ctx context.Context, tx *sql.Tx, pause func()) error {
		var b int64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ? FOR UPDATE`, "alice").Scan(&b); err != nil {
			return err
		}
		pause()
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = ? WHERE id = ?`, b-10, "alice")
		return err
	},
}

// The lost-update table: two concurrent withdrawals of 10 from 100, at
// each isolation level. A lost update is a final balance that does not
// count every committed withdrawal: only read-then-write at read
// committed has one; the others either serialize or fail one with 40001.
func TestLostUpdates(t *testing.T) {
	type outcome struct {
		balance int64
		errs    [2]string // each withdrawal's SQLSTATE, "" if it committed
	}
	for _, tc := range []struct {
		how  string
		iso  qenv.Isolation
		want outcome
	}{
		{"read-then-write", qenv.ReadCommitted, outcome{90, [2]string{"", ""}}}, // lost
		{"read-then-write", qenv.SnapshotIsolation, outcome{90, [2]string{"40001", ""}}},
		{"read-then-write", qenv.Serializable, outcome{90, [2]string{"40001", ""}}},
		{"relative", qenv.ReadCommitted, outcome{80, [2]string{"", ""}}},
		{"relative", qenv.SnapshotIsolation, outcome{90, [2]string{"40001", ""}}},
		{"relative", qenv.Serializable, outcome{90, [2]string{"40001", ""}}},
		{"for-update", qenv.ReadCommitted, outcome{80, [2]string{"", ""}}},
		{"for-update", qenv.SnapshotIsolation, outcome{90, [2]string{"", "40001"}}},
		{"for-update", qenv.Serializable, outcome{90, [2]string{"", "40001"}}},
	} {
		t.Run(tc.how+"/"+tc.iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			var got outcome
			for i := range 2 {
				w.s.Spawn(fmt.Sprint("withdraw", i), func(ctx context.Context) {
					got.errs[i] = code(func() error {
						tx, err := w.db.BeginTx(ctx, nil)
						if err != nil {
							return err
						}
						defer tx.Rollback()
						pause := func() {
							if i == 0 {
								qenv.Sleep(ctx, time.Second) // the other runs whole meanwhile
							}
						}
						if err := withdrawals[tc.how](ctx, tx, pause); err != nil {
							return err
						}
						return tx.Commit()
					}())
				})
			}
			w.run(t)
			got.balance = w.balance(t, "alice")
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			committed := int64(0)
			for _, e := range got.errs {
				if e == "" {
					committed++
				}
			}
			if lost := got.balance != 100-10*committed; lost != (tc.how == "read-then-write" && tc.iso == qenv.ReadCommitted) {
				t.Fatalf("lost update %v: balance %d after %d withdrawals", lost, got.balance, committed)
			}
		})
	}
}

// Write skew: two doctors on call each go off call after seeing the
// other still on. Snapshot isolation lets both commit (no one on call);
// serializable aborts one.
func TestWriteSkew(t *testing.T) {
	for _, tc := range []struct {
		iso    qenv.Isolation
		oncall int
		errs   [2]string
	}{
		{qenv.ReadCommitted, 0, [2]string{"", ""}},
		{qenv.SnapshotIsolation, 0, [2]string{"", ""}},
		{qenv.Serializable, 1, [2]string{"40001", ""}},
	} {
		t.Run(tc.iso.String(), func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, tc.iso)
			w.exec(t, `CREATE TABLE doctors (id INTEGER PRIMARY KEY, shift TEXT NOT NULL, oncall BOOLEAN NOT NULL)`)
			w.exec(t, `INSERT INTO doctors VALUES (1, 'night', TRUE), (2, 'night', TRUE)`)
			var errs [2]string
			for i := range 2 {
				w.s.Spawn(fmt.Sprint("doctor", i+1), func(ctx context.Context) {
					errs[i] = code(func() error {
						tx, err := w.db.BeginTx(ctx, nil)
						if err != nil {
							return err
						}
						defer tx.Rollback()
						rows, err := tx.QueryContext(ctx, `SELECT id FROM doctors WHERE shift = ? AND oncall = ?`, "night", true)
						if err != nil {
							return err
						}
						n := 0
						for rows.Next() {
							n++
						}
						rows.Close()
						if i == 0 {
							qenv.Sleep(ctx, time.Second)
						}
						if n < 2 {
							return nil
						}
						if _, err := tx.ExecContext(ctx, `UPDATE doctors SET oncall = FALSE WHERE id = ?`, i+1); err != nil {
							return err
						}
						return tx.Commit()
					}())
				})
			}
			w.run(t)
			oncall := 0
			for _, r := range qsql.Table(w.st, "doctors") {
				if r["oncall"] == true {
					oncall++
				}
			}
			if oncall != tc.oncall || errs != tc.errs {
				t.Fatalf("%d on call, errors %q; want %d, %q", oncall, errs, tc.oncall, tc.errs)
			}
		})
	}
}

// An UPDATE waits for the row lock another transaction holds, and goes on
// when it commits, at that virtual time.
func TestLockWait(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	var done time.Duration
	w.s.Spawn("holder", func(ctx context.Context) {
		tx, _ := w.db.BeginTx(ctx, nil)
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance + 1 WHERE id = 'alice'`); err != nil {
			t.Error(err)
		}
		qenv.Sleep(ctx, 5*time.Second)
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	w.s.Spawn("waiter", func(ctx context.Context) {
		if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance = balance + 1 WHERE id = 'alice'`); err != nil {
			t.Error(err)
		}
		done = w.s.Elapsed()
	})
	w.run(t)
	if done != 5*time.Second || w.balance(t, "alice") != 102 {
		t.Fatalf("the waiter finished at %v with balance %d; want 5s, 102", done, w.balance(t, "alice"))
	}
}

// Two transactions locking two rows in opposite orders: the one whose wait
// would close the cycle fails with 40P01, a qenv.ErrConflict that retry
// loops retry, and the other commits.
func TestDeadlock(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	var errs [2]error
	for i, order := range [][2]string{{"alice", "bob"}, {"bob", "alice"}} {
		w.s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
			errs[i] = func() error {
				tx, _ := w.db.BeginTx(ctx, nil)
				defer tx.Rollback()
				for j, id := range order {
					if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - 1 WHERE id = ?`, id); err != nil {
						return err
					}
					if j == 0 {
						qenv.Sleep(ctx, time.Second) // both hold their first row
					}
				}
				return tx.Commit()
			}()
		})
	}
	w.run(t)
	if errs[0] != nil || code(errs[1]) != "40P01" || !errors.Is(errs[1], qenv.ErrConflict) {
		t.Fatalf("got %v, %v; want the second to fail with 40P01", errs[0], errs[1])
	}
	if !strings.Contains(errs[1].Error(), "lock rows in one order") {
		t.Errorf("the error does not say the fix: %v", errs[1])
	}
}

// pick is a chooser that makes the given fault choices, the n-th time a
// kind is drawn (counting from 0) its option at that index, nominal after.
type pick map[string][]int

func (p pick) Choose(kind string, n int) int {
	if opts, ok := p[kind]; ok && len(opts) > 0 {
		p[kind] = opts[1:]
		if opts[0] < n {
			return opts[0]
		}
	}
	return 0
}

// A commit whose connection is lost after the Store applied it: 08006
// (the outcome unknown, never driver.ErrBadConn, which database/sql would
// retry), the row committed, OnCommit's changes delivered, and the
// connection discarded.
func TestCommitOutcomeUnknown(t *testing.T) {
	var changes []qsql.Change
	w := newWorld(t, pick{"fault.store.paydb.commit": {1}}, qenv.ReadCommitted,
		qsql.Options{OnCommit: func(_ context.Context, ch []qsql.Change) { changes = append(changes, ch...) }})
	w.st.CommitFaults = true
	changes = nil // the setup's
	var err error
	w.s.Spawn("payer", func(ctx context.Context) {
		_, err = w.db.ExecContext(ctx, `UPDATE accounts SET balance = balance - 10 WHERE id = 'alice'`)
	})
	w.run(t)
	if code(err) != "08006" || !errors.Is(err, qenv.ErrUnknownOutcome) || errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("got %v; want 08006, ErrUnknownOutcome, not ErrBadConn", err)
	}
	if w.balance(t, "alice") != 90 {
		t.Fatalf("balance %d: the commit was applied", w.balance(t, "alice"))
	}
	want := []qsql.Change{{Table: "accounts", Op: "update", Old: map[string]any{"id": "alice", "balance": int64(100)}, New: map[string]any{"id": "alice", "balance": int64(90)}}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("OnCommit got %+v, want %+v", changes, want)
	}
	if n := w.db.Stats().OpenConnections; n != 0 {
		t.Fatalf("%d connections open: the lost one was kept", n)
	}
}

// A broken connection found before anything ran: database/sql retries it
// (twice on pooled connections, then on a new one), so two faults in a
// row are invisible and three reach the caller.
func TestBadConnRetried(t *testing.T) {
	for _, tc := range []struct {
		faults []int
		err    error
	}{
		{[]int{1, 1}, nil},
		{[]int{1, 1, 1}, driver.ErrBadConn},
	} {
		w := newWorld(t, pick{"fault.sql.paydb.conn": tc.faults}, qenv.ReadCommitted, qsql.Options{BadConn: true})
		var err, txErr error
		w.s.Spawn("payer", func(ctx context.Context) {
			_, err = w.db.ExecContext(ctx, `UPDATE accounts SET balance = balance - 10 WHERE id = 'alice'`)
			var tx *sql.Tx
			if tx, txErr = w.db.BeginTx(ctx, nil); txErr == nil { // the faults are spent: BeginTx's draw is nominal
				txErr = tx.Commit()
			}
		})
		w.run(t)
		if !errors.Is(err, tc.err) || txErr != nil {
			t.Fatalf("%d faults: got %v, %v; want %v, nil", len(tc.faults), err, txErr, tc.err)
		}
		if want := map[bool]int64{true: 90, false: 100}[tc.err == nil]; w.balance(t, "alice") != want {
			t.Fatalf("%d faults: balance %d, want %d", len(tc.faults), w.balance(t, "alice"), want)
		}
	}
}

// A transaction may ask for the Store's level or a weaker one; a stronger
// one fails with the fix. A read-only transaction's writes fail with 25006.
func TestTxOptions(t *testing.T) {
	for _, tc := range []struct {
		iso   qenv.Isolation
		level sql.IsolationLevel
		err   string
	}{
		{qenv.ReadCommitted, sql.LevelDefault, ""},
		{qenv.ReadCommitted, sql.LevelReadUncommitted, ""},
		{qenv.ReadCommitted, sql.LevelReadCommitted, ""},
		{qenv.ReadCommitted, sql.LevelRepeatableRead, "store paydb runs read-committed; sql.LevelRepeatableRead needs NewStore(..., qenv.SnapshotIsolation)"},
		{qenv.ReadCommitted, sql.LevelSerializable, "store paydb runs read-committed; sql.LevelSerializable needs NewStore(..., qenv.Serializable)"},
		{qenv.SnapshotIsolation, sql.LevelSnapshot, ""},
		{qenv.SnapshotIsolation, sql.LevelReadCommitted, ""},
		{qenv.SnapshotIsolation, sql.LevelSerializable, "store paydb runs snapshot-isolation; sql.LevelSerializable needs NewStore(..., qenv.Serializable)"},
		{qenv.Serializable, sql.LevelSerializable, ""},
		{qenv.Serializable, sql.LevelLinearizable, "not one a Store offers"},
	} {
		w := newWorld(t, qenv.Nominal{}, tc.iso)
		tx, err := w.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: tc.level})
		if tc.err == "" {
			if err != nil {
				t.Errorf("%s store, %s: %v", tc.iso, tc.level, err)
				continue
			}
			tx.Rollback()
		} else if code(err) != "0A000" || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s store, %s: got %v, want 0A000 saying %q", tc.iso, tc.level, err, tc.err)
		}
	}
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	for _, q := range []string{`UPDATE accounts SET balance = 0`, `SELECT * FROM accounts FOR UPDATE`, `INSERT INTO accounts VALUES ('carol', 1)`, `DELETE FROM accounts`} {
		tx, err := w.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(q); code(err) != "25006" {
			t.Errorf("%s in a read-only transaction: got %v, want 25006", q, err)
		}
		tx.Rollback()
	}
	tx, _ := w.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	var n int64
	if err := tx.QueryRow(`SELECT balance FROM accounts WHERE id = 'bob'`).Scan(&n); err != nil || n != 100 {
		t.Errorf("a read-only read: %d, %v", n, err)
	}
	tx.Rollback()
}

// After a failed statement a transaction refuses the rest (25P02) and
// its Commit commits nothing, as PostgreSQL's do; a duplicate key is 23505
// and ON CONFLICT DO NOTHING skips it.
func TestFailedTransaction(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	w.exec(t, `CREATE TABLE payments (key TEXT PRIMARY KEY, account TEXT NOT NULL, amount BIGINT NOT NULL)`)
	w.exec(t, `INSERT INTO payments VALUES ('k1', 'alice', 10)`)
	r, err := w.db.Exec(`INSERT INTO payments (key, account, amount) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`, "k1", "alice", 10)
	if n, _ := r.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("ON CONFLICT DO NOTHING: %d rows, %v; want 0, nil", n, err)
	}
	tx, _ := w.db.Begin()
	if _, err := tx.Exec(`UPDATE accounts SET balance = balance - 10 WHERE id = 'alice'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO payments VALUES ('k1', 'alice', 10)`)
	if code(err) != "23505" || !strings.Contains(err.Error(), `"payments_pkey"`) {
		t.Fatalf("a duplicate key: got %v, want 23505 naming payments_pkey", err)
	}
	if _, err := tx.Exec(`UPDATE accounts SET balance = 0`); code(err) != "25P02" {
		t.Fatalf("a statement after the failure: got %v, want 25P02", err)
	}
	if err := tx.Commit(); code(err) != "25P02" {
		t.Fatalf("Commit after the failure: got %v, want 25P02", err)
	}
	if w.balance(t, "alice") != 100 {
		t.Fatal("the failed transaction's update committed")
	}
	if len(qsql.Table(w.st, "payments")) != 1 {
		t.Fatal("a duplicate payment")
	}
}

// A context cancelled mid-transaction: database/sql rolls the transaction
// back from a goroutine of its own, which must not touch the Store. The
// reaper, a task, rolls it back at the next step, so the row lock it held
// frees the waiter at once, not when the cancelling task next calls in;
// and the run is the same however database/sql's goroutine is scheduled.
func TestCancelledTransactionRolledBack(t *testing.T) {
	for seed := range uint64(20) {
		var first []qenv.Choice
		for again := range 2 {
			log := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			w := newWorld(t, log, qenv.ReadCommitted)
			locked := false
			var done time.Duration
			var commitErr error
			w.s.Spawn("canceller", func(ctx context.Context) {
				ctx, cancel := context.WithCancel(ctx)
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					cancel()
					t.Error(err)
					return
				}
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = 0 WHERE id = 'alice'`); err != nil {
					t.Error(err)
				}
				locked = true
				cancel()
				qenv.Sleep(ctx, 10*time.Second)
				commitErr = tx.Commit()
			})
			w.s.Spawn("waiter", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return locked })
				if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance = balance + 1 WHERE id = 'alice'`); err != nil {
					t.Error(err)
				}
				done = w.s.Elapsed()
			})
			w.run(t)
			if done != 0 || w.balance(t, "alice") != 101 || commitErr == nil {
				t.Fatalf("seed %d: the waiter finished at %v, balance %d, the cancelled commit returned %v; want 0s, 101, an error",
					seed, done, w.balance(t, "alice"), commitErr)
			}
			if again == 0 {
				first = log.Choices
			} else if !reflect.DeepEqual(log.Choices, first) {
				t.Fatalf("seed %d ran two ways:\n%v\n%v", seed, first, log.Choices)
			}
		}
	}
}

// database/sql starts one goroutine per database (its connection opener)
// and one per transaction and per query in a transaction (awaitDone), the
// latter ending with them; Open adds its reaper task. Nothing is left
// once the simulation closes.
func TestNoGoroutineLeak(t *testing.T) {
	before := settled(t, runtime.NumGoroutine(), -1)
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "paydb", qenv.ReadCommitted)
	const dbs = 3
	for i := range dbs {
		db := qsql.Open(s, st, qsql.Options{Name: fmt.Sprint("db", i)})
		if i == 0 {
			if _, err := db.Exec(`CREATE TABLE accounts (id TEXT PRIMARY KEY, balance BIGINT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
		}
		s.Spawn(fmt.Sprint("user", i), func(ctx context.Context) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			for j := range 5 {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO accounts VALUES (?, ?)`, fmt.Sprint(i, "/", j), j); err != nil {
					t.Error(err)
				}
				rows, err := tx.QueryContext(ctx, `SELECT * FROM accounts`)
				if err != nil {
					t.Error(err)
				} else {
					rows.Close()
				}
				if err := tx.Commit(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	settled(t, before+2*dbs, before+2*dbs) // an opener and a parked reaper per database
	s.Close()
	settled(t, before, before)
}

// settled waits for the goroutine count to come down to want (-1: to
// stop changing), failing if it does not within 2 s, and returns it.
func settled(t *testing.T, start, want int) int {
	t.Helper()
	n := start
	for range 200 {
		time.Sleep(10 * time.Millisecond) //qenv:ok waiting in real time for database/sql's goroutines to end
		m := runtime.NumGoroutine()
		if want < 0 && m == n || m == want {
			return m
		}
		n = m
	}
	buf := make([]byte, 1<<20)
	t.Fatalf("%d goroutines, want %d:\n%s", n, want, buf[:runtime.Stack(buf, true)])
	return n
}

// A pool limited to one connection, held by one task's transaction while
// another asks for one: database/sql blocks the second on a channel, the
// first can never run to release it, and the watchdog names the cause.
func TestHangOnAFullPool(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	w.s.Hang = 200 * time.Millisecond
	w.db.SetMaxOpenConns(1)
	w.s.Spawn("holder", func(ctx context.Context) {
		tx, _ := w.db.BeginTx(ctx, nil)
		qenv.Sleep(ctx, time.Second)
		tx.Commit()
	})
	w.s.Spawn("blocked", func(ctx context.Context) {
		w.db.ExecContext(ctx, `UPDATE accounts SET balance = 0`)
	})
	var msg string
	func() {
		defer func() { msg = fmt.Sprint(recover()) }()
		w.s.Run(time.Minute)
	}()
	for _, want := range []string{"task blocked ran", "SetMaxOpenConns", "database/sql.(*DB).conn"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the hang's panic does not say %q:\n%s", want, msg)
		}
	}
}

// An UPDATE that waited for a row's lock judges its WHERE clause on the
// row as the lock holder left it, as PostgreSQL's read committed does: a
// compare-and-set (WHERE balance = 100) applies once, not twice.
func TestWhereRecheckedAfterLockWait(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	var affected [2]int64
	for i := range 2 {
		w.s.Spawn(fmt.Sprint("cas", i), func(ctx context.Context) {
			tx, _ := w.db.BeginTx(ctx, nil)
			r, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - 10 WHERE id = 'alice' AND balance = 100`)
			if err != nil {
				t.Error(err)
				return
			}
			affected[i], _ = r.RowsAffected()
			qenv.Sleep(ctx, time.Second) // the other waits for the lock meanwhile
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
		})
	}
	w.run(t)
	if affected != [2]int64{1, 0} || w.balance(t, "alice") != 90 {
		t.Fatalf("rows affected %v, balance %d; want [1 0], 90", affected, w.balance(t, "alice"))
	}
}

// A crash aborts its process's transactions in the Store (releasing their
// locks); its task, unwinding at Close, finds the transaction gone (08006),
// never the Store's use-after-end panic.
func TestCrashAbortsTransaction(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	var unwound error
	p := w.s.Process("payments", true, func(ctx context.Context) {
		tx, _ := w.db.BeginTx(ctx, nil)
		defer func() { // runs as the killed task unwinds, at Close
			_, unwound = tx.ExecContext(ctx, `UPDATE accounts SET balance = 1 WHERE id = 'alice'`)
		}()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = 0 WHERE id = 'alice'`); err != nil {
			t.Error(err)
		}
		qenv.Sleep(ctx, time.Hour)
	})
	w.s.Spawn("other", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		p.Crash()
		if _, err := w.db.ExecContext(ctx, `UPDATE accounts SET balance = balance + 1 WHERE id = 'alice'`); err != nil {
			t.Error(err) // the crashed transaction's lock is released
		}
	})
	w.run(t)
	w.s.Close()
	if code(unwound) != "08006" || w.balance(t, "alice") != 101 {
		t.Fatalf("the unwinding statement got %v, balance %d; want 08006, 101", unwound, w.balance(t, "alice"))
	}
}

// A UNIQUE column's index follows its updates and deletes: a value given
// up is free again, one taken is refused (23505), NULLs never conflict.
func TestUniqueIndexFollowsChanges(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	w.exec(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT UNIQUE)`)
	w.exec(t, `INSERT INTO users VALUES (1, 'a'), (5, NULL), (6, NULL)`)
	w.exec(t, `UPDATE users SET email = 'b' WHERE id = 1`)
	w.exec(t, `INSERT INTO users VALUES (2, 'a')`) // a is free
	if _, err := w.db.Exec(`INSERT INTO users VALUES (3, 'b')`); code(err) != "23505" || !strings.Contains(err.Error(), "users_email_key") {
		t.Fatalf("b is taken: got %v, want 23505 naming users_email_key", err)
	}
	if _, err := w.db.Exec(`UPDATE users SET email = 'a' WHERE id = 1`); code(err) != "23505" {
		t.Fatalf("updating to a taken value: got %v, want 23505", err)
	}
	w.exec(t, `DELETE FROM users WHERE email = 'a'`)
	w.exec(t, `INSERT INTO users VALUES (4, 'a')`) // free again
	var id int64
	if err := w.db.QueryRow(`SELECT id FROM users WHERE email = ?`, "a").Scan(&id); err != nil || id != 4 {
		t.Fatalf("email a: id %d, %v; want 4", id, err)
	}
	if got := len(qsql.Table(w.st, "users")); got != 4 {
		t.Fatalf("%d users, want 4 (1, 4 and the two NULLs)", got)
	}
}

// Values as PostgreSQL keeps them: times to the microsecond (a value that
// would not survive the round trip there must not here), bytes copied
// from the caller's slice; and the errors for what a table refuses.
func TestValues(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	w.exec(t, `CREATE TABLE events (id INTEGER PRIMARY KEY, at TIMESTAMP NOT NULL, body BLOB, seen BOOLEAN)`)
	at := time.Date(2026, 3, 1, 12, 0, 0, 123456789, time.UTC)
	body := []byte("abc")
	w.exec(t, `INSERT INTO events VALUES (?, ?, ?, ?)`, int32(1), at, body, true)
	body[0] = 'X'
	var gotAt time.Time
	var gotBody []byte
	var seen bool
	if err := w.db.QueryRow(`SELECT at, body, seen FROM events WHERE id = 1`).Scan(&gotAt, &gotBody, &seen); err != nil {
		t.Fatal(err)
	}
	if !gotAt.Equal(at.Truncate(time.Microsecond)) || string(gotBody) != "abc" || !seen {
		t.Fatalf("read back %v %q %v; want %v \"abc\" true", gotAt, gotBody, seen, at.Truncate(time.Microsecond))
	}
	for _, tc := range []struct{ q, code, says string }{
		{`INSERT INTO events (id, body) VALUES (2, NULL)`, "23502", `column "at"`},
		{`INSERT INTO events VALUES ('two', NULL, NULL, NULL)`, "42804", `column "id" of "events" is INTEGER; got string two (pass an int64`},
		{`SELECT nope FROM events`, "42703", `column "nope" of relation "events" does not exist (its columns: id, at, body, seen)`},
		{`SELECT * FROM nope`, "42P01", `relation "nope" does not exist in store paydb (create it with CREATE TABLE first)`},
		{`SELECT * FROM events ORDER BY at`, "42601", `rows come in primary-key order only (ORDER BY id [DESC])`},
		{`UPDATE events SET id = 3`, "0A000", `DELETE the row and INSERT it anew`},
		{`UPDATE events SET seen = seen + 1`, "42883", `arithmetic is on INTEGER columns only`},
		{`CREATE TABLE events (id INTEGER PRIMARY KEY)`, "42P07", `CREATE TABLE IF NOT EXISTS leaves it`},
		{`CREATE TABLE keyless (a INTEGER)`, "42P16", `mark one PRIMARY KEY`},
		{`INSERT INTO events (id) VALUES (?)`, "08P01", `supplies 0 parameters, but the statement requires 1`},
	} {
		if _, err := w.db.Exec(tc.q); code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: got %v, want %s saying %q", tc.q, err, tc.code, tc.says)
		}
	}
	if _, err := w.db.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = 'bob'`, int64(math.MaxInt64)); code(err) != "22003" {
		t.Errorf("an overflowing addition: got %v, want 22003", err)
	}
}

// Two transactions inserting one key: the second to reach it waits for
// the first, then finds a duplicate (23505) at every level, as
// PostgreSQL's unique check reads the latest committed rows (the Store's
// snapshot check gave 40001 in most seeds). ON CONFLICT DO NOTHING does
// nothing at read committed, and under a snapshot that cannot see the
// other's row fails with 40001, as PostgreSQL's does. At serializable, a
// transaction that read the key absent before inserting it gets 40001.
func TestConcurrentInsertOfOneKey(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		for _, tc := range []struct {
			name, q string
			check   bool // read the key first
			second  []string
		}{
			{"insert", `INSERT INTO pay (k, v) VALUES ('k1', ?)`, false, []string{"23505"}},
			{"do nothing", `INSERT INTO pay (k, v) VALUES ('k1', ?) ON CONFLICT DO NOTHING`, false,
				map[bool][]string{true: {"n=0"}, false: {"n=0", "40001"}}[iso == qenv.ReadCommitted]},
			{"check then insert", `INSERT INTO pay (k, v) VALUES ('k1', ?)`, true,
				map[bool][]string{true: {"40001", "found"}, false: {"23505", "found"}}[iso == qenv.Serializable]},
		} {
			seen := map[string]bool{}
			for seed := range uint64(40) {
				s := qenv.New(qenv.NewSeeded(seed))
				s.Check = true
				st := qenv.NewStore(s, "paydb", iso)
				db := qsql.Open(s, st)
				if _, err := db.Exec(`CREATE TABLE pay (k TEXT PRIMARY KEY, v INTEGER)`); err != nil {
					t.Fatal(err)
				}
				res := make([]string, 2)
				for i := range 2 {
					s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
						res[i] = func() string {
							tx, err := db.BeginTx(ctx, nil)
							if err != nil {
								return code(err)
							}
							defer tx.Rollback()
							if tc.check {
								var v int64
								switch err := tx.QueryRowContext(ctx, `SELECT v FROM pay WHERE k = 'k1'`).Scan(&v); err {
								case nil:
									return "found" // the other's committed row is visible: nothing to insert
								case sql.ErrNoRows:
								default:
									return code(err)
								}
							}
							r, err := tx.ExecContext(ctx, tc.q, int64(i))
							if err != nil {
								return code(err)
							}
							n, _ := r.RowsAffected()
							if err := tx.Commit(); err != nil {
								return code(err)
							}
							return fmt.Sprint("n=", n)
						}()
					})
				}
				if err := s.Run(time.Minute); err != nil {
					t.Fatal(err)
				}
				s.Close()
				if res[0] != "n=1" {
					res[0], res[1] = res[1], res[0] // the second is whichever did not insert
				}
				if res[0] != "n=1" || !slices.Contains(tc.second, res[1]) {
					t.Fatalf("%s, %s, seed %d: got %q; want one n=1 and one of %q", iso, tc.name, seed, res, tc.second)
				}
				seen[res[1]] = true
			}
			delete(seen, "found") // a seed's schedule, not a code: not required
			if want := len(slices.DeleteFunc(slices.Clone(tc.second), func(c string) bool { return c == "found" })); len(seen) != want {
				t.Errorf("%s, %s: the seeds showed %v of %q", iso, tc.name, seen, tc.second)
			}
		}
	}
}

// A statement at read committed reads as of one moment: a lookup by a
// UNIQUE column reads the index, then the row, at two seams, and a
// transaction moving the value from one row to another between them once
// made a row that always holds it come back missing.
func TestUniqueLookupSeesOneState(t *testing.T) {
	for seed := range uint64(200) {
		s := qenv.New(qenv.NewSeeded(seed))
		s.Check = true
		st := qenv.NewStore(s, "paydb", qenv.ReadCommitted)
		db := qsql.Open(s, st)
		for _, q := range []string{`CREATE TABLE u (id TEXT PRIMARY KEY, email TEXT UNIQUE)`, `INSERT INTO u VALUES ('u1', 'a@x'), ('u2', 'c@x')`} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		missing := false
		s.Spawn("reader", func(ctx context.Context) {
			for range 30 {
				var id string
				if err := db.QueryRowContext(ctx, `SELECT id FROM u WHERE email = 'a@x'`).Scan(&id); err != nil {
					missing = true
				}
			}
		})
		s.Spawn("mover", func(ctx context.Context) {
			tx, _ := db.BeginTx(ctx, nil)
			for _, q := range []string{`UPDATE u SET email = 'tmp' WHERE id = 'u1'`, `UPDATE u SET email = 'a@x' WHERE id = 'u2'`, `UPDATE u SET email = 'b@x' WHERE id = 'u1'`} {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					t.Error(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if missing {
			t.Fatalf("seed %d: a@x, committed on some row throughout, was not found", seed)
		}
	}
}

// After a cancel, a statement in the transaction fails with sql.ErrTxDone
// whether database/sql's goroutine or the driver answers, and Commit with
// context.Canceled or sql.ErrTxDone (database/sql's, by its goroutine's
// timing, before the driver is asked): nothing commits either way.
func TestErrorsAfterCancel(t *testing.T) {
	for range 100 {
		w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
		var stmtErr, commitErr error
		w.s.Spawn("c", func(ctx context.Context) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			tx, err := w.db.BeginTx(ctx, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = 1 WHERE id = 'alice'`); err != nil {
				t.Error(err)
			}
			cancel()
			_, stmtErr = tx.ExecContext(context.Background(), `UPDATE accounts SET balance = 2 WHERE id = 'alice'`)
			commitErr = tx.Commit()
		})
		w.run(t)
		if !errors.Is(stmtErr, sql.ErrTxDone) || !errors.Is(commitErr, context.Canceled) && !errors.Is(commitErr, sql.ErrTxDone) || w.balance(t, "alice") != 100 {
			t.Fatalf("statement %v, commit %v, balance %d; want ErrTxDone, Canceled or ErrTxDone, 100", stmtErr, commitErr, w.balance(t, "alice"))
		}
	}
}

// closes runs Close, failing if it does not return within 5 s.
func closes(t *testing.T, s *qenv.Sim, why string) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.Close(); close(done) }() //qenv:ok the test watches Close from outside the simulation
	select {                               //qenv:ok the test waits in wall time
	case <-done:
	case <-time.After(5 * time.Second): //qenv:ok as above
		t.Fatalf("Sim.Close hung: %s", why)
	}
}

// A task unwound while its statement waits inside the driver unwinds
// through database/sql's Tx.QueryContext, which holds the Tx's closemu
// until the driver returns; its deferred tx.Rollback takes closemu, and
// Close hung. The driver recovers the unwind and returns 08006, so
// database/sql lets go. Each of the ways a query reaches a seam, killed
// by a crash during the run or left waiting at Close.
func TestUnwoundInsideAStatement(t *testing.T) {
	queries := map[string]func(ctx context.Context, tx *sql.Tx) error{
		"QueryRow": func(ctx context.Context, tx *sql.Tx) error {
			var n int64
			return tx.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = 'alice' FOR UPDATE`).Scan(&n)
		},
		"Query": func(ctx context.Context, tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT balance FROM accounts WHERE id = 'alice' FOR UPDATE`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		},
		"Prepared": func(ctx context.Context, tx *sql.Tx) error {
			st, err := tx.PrepareContext(ctx, `SELECT balance FROM accounts WHERE id = ? FOR UPDATE`)
			if err != nil {
				return err
			}
			defer st.Close()
			var n int64
			return st.QueryRowContext(ctx, "alice").Scan(&n)
		},
		"Exec": func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = 0 WHERE id = 'alice'`)
			return err
		},
	}
	for name, query := range queries {
		for _, crash := range []bool{true, false} {
			t.Run(fmt.Sprint(name, map[bool]string{true: "/crash", false: "/at-close"}[crash]), func(t *testing.T) {
				w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
				w.s.Spawn("holder", func(ctx context.Context) {
					tx, _ := w.db.BeginTx(ctx, nil)
					defer tx.Rollback()
					tx.ExecContext(ctx, `UPDATE accounts SET balance = 1 WHERE id = 'alice'`)
					qenv.Sleep(ctx, time.Hour) // holds the row's lock
				})
				var got error
				p := w.s.Process("svc", crash, func(ctx context.Context) {
					tx, err := w.db.BeginTx(ctx, nil)
					if err != nil {
						return
					}
					defer tx.Rollback() // the idiom
					qenv.Sleep(ctx, time.Second)
					got = query(ctx, tx) // waits for the holder's lock, inside the driver
				})
				if crash {
					w.s.Spawn("nemesis", func(ctx context.Context) {
						qenv.Sleep(ctx, 2*time.Second)
						p.Crash()
					})
				}
				w.run(t)
				closes(t, w.s, "the unwound task's deferred tx.Rollback waits for the Tx's closemu")
				if code(got) != "08006" {
					t.Fatalf("the statement returned %v as it unwound; want 08006", got)
				}
			})
		}
	}
}

// A task whose deferred cleanup retries a statement until it succeeds,
// with no seam of its own: once unwinding, the driver fails each call at
// once (08006), and after a bound raises the unwind again, so Close ends.
func TestUnwoundRetryLoopEnds(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	calls := 0
	w.s.Spawn("retrier", func(ctx context.Context) {
		tx, _ := w.db.BeginTx(ctx, nil)
		defer func() {
			for {
				calls++
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = 0 WHERE id = 'alice'`); err == nil {
					return
				}
			}
		}()
		qenv.Sleep(ctx, time.Hour)
	})
	w.run(t)
	closes(t, w.s, "a retry loop in deferred code spun on the unwinding task's statements")
	if calls < 100 || calls > 110 {
		t.Fatalf("%d calls before the loop was unwound; want about 100", calls)
	}
}

// A task killed between two Rows.Next calls, its rows still open (they
// hold the Tx's closemu): its deferred rows.Close runs before its deferred
// tx.Rollback, and Close returns.
func TestUnwoundWithRowsOpen(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	p := w.s.Process("svc", true, func(ctx context.Context) {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(ctx, `SELECT id FROM accounts`)
		if err != nil {
			t.Error(err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			qenv.Sleep(ctx, time.Hour) // killed here, between rows
		}
	})
	w.s.Spawn("nemesis", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		p.Crash()
	})
	w.run(t)
	closes(t, w.s, "rows left open by a killed task")
}
