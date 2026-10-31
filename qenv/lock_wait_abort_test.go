package qenv_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// An adapter's abort supervisor (qsql's cancellation reaper) may end a
// transaction while its owning task waits for a lock. That ends the wait,
// without letting buffered writes or commit hooks run, regardless of how long
// the unrelated holder remains at work.
func TestStoreEndedLockWaits(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "lock"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			for seed := uint64(0); seed < 12; seed++ {
				var first []qenv.Choice
				for again := 0; again < 3; again++ {
					c := qenv.NewSeeded(seed)
					c.Rates = qenv.NoFaults
					var chooser qenv.Chooser = c
					if again == 2 {
						chooser = qenv.Replay(first)
					}
					log := &qenv.ChoiceLog{C: chooser}
					s := qenv.New(log)
					s.Check = true
					st := qenv.NewStore(s, "db", qenv.ReadCommitted)
					st.Load("k", 7)
					held := false
					var waiter qenv.Txn
					var got error
					var ended time.Duration
					hooks := 0
					s.Spawn("holder", func(ctx context.Context) {
						tx := st.Begin(ctx)
						if _, _, err := tx.GetForUpdate("k"); err != nil {
							t.Error(err)
							return
						}
						held = true
						qenv.Sleep(ctx, 10*time.Second)
						tx.Rollback()
					})
					s.Spawn("waiter", func(ctx context.Context) {
						qenv.Wait(ctx, func() bool { return held })
						waiter = st.Begin(ctx)
						waiter.Put("discarded", 99)
						waiter.OnCommit(func() { hooks++ })
						if commit {
							waiter.Put("k", 50)
							got = waiter.Commit()
						} else {
							_, _, got = waiter.GetForUpdate("k")
						}
						ended = s.Elapsed()
						waiter.Rollback()
					})
					s.Spawn("abort", func(ctx context.Context) {
						qenv.Sleep(ctx, time.Second)
						if waiter == nil {
							t.Error("waiter did not begin before abort")
							return
						}
						waiter.Rollback()
					})
					err := s.Run(time.Minute)
					s.Close()
					if err != nil {
						t.Fatal(err)
					}
					if !errors.Is(got, qenv.ErrConflict) || ended != time.Second || hooks != 0 {
						t.Fatalf("seed%d: error%v ended%v hooks%d, want conflict at1s and no hooks", seed, got, ended, hooks)
					}
					if v, _ := st.Read("k"); v != 7 {
						t.Fatalf("aborted waiter wrote k=%v", v)
					}
					if _, ok := st.Read("discarded"); ok {
						t.Fatal("aborted waiter applied buffered writes")
					}
					if again == 0 {
						first = log.Choices
					} else if !reflect.DeepEqual(first, log.Choices) {
						t.Fatalf("seed%d run%d replayed differently", seed, again)
					}
				}
			}
		})
	}
}

// Prefer the newest task at scheduling points so the reset operator can acquire
// the released key before the old waiter runs. All fault choices remain nominal.
type lastLockWaitTask struct{}

func (lastLockWaitTask) Choose(string, int) int { return 0 }
func (lastLockWaitTask) ChooseTask(ids []int) int {
	best := 0
	for i, id := range ids {
		if id > ids[best] {
			best = i
		}
	}
	return best
}

// A reset transaction must not wait for a fresh transaction that acquires the
// same key after failover. The old connection is already gone; it cannot borrow
// the new lock's release to make its ErrUnavailable visible.
func TestStoreResetLockWaitsPastFreshHolder(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "lock"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			s := qenv.New(lastLockWaitTask{})
			defer s.Close()
			s.Check = true
			st := qenv.NewStore(s, "db", qenv.ReadCommitted)
			st.Load("k", 7)
			held := false
			var got error
			var ended time.Duration
			hooks := 0
			s.Spawn("holder", func(ctx context.Context) {
				tx := st.Begin(ctx)
				if _, _, err := tx.GetForUpdate("k"); err != nil {
					t.Error(err)
					return
				}
				held = true
				qenv.Sleep(ctx, 10*time.Second)
				tx.Rollback()
			})
			s.Spawn("waiter", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return held })
				tx := st.Begin(ctx)
				tx.Put("discarded", 99)
				tx.OnCommit(func() { hooks++ })
				if commit {
					tx.Put("k", 50)
					got = tx.Commit()
				} else {
					_, _, got = tx.GetForUpdate("k")
				}
				ended = s.Elapsed()
				tx.Rollback()
			})
			s.Spawn("reset", func(ctx context.Context) {
				qenv.Sleep(ctx, time.Second)
				st.FailOver(0, 0)
				fresh := st.Begin(ctx)
				if _, _, err := fresh.GetForUpdate("k"); err != nil {
					t.Error(err)
					return
				}
				qenv.Sleep(ctx, 10*time.Second)
				fresh.Rollback()
			})
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(got, qenv.ErrUnavailable) || ended != time.Second || hooks != 0 {
				t.Fatalf("error%v ended%v hooks%d, want unavailable at1s and no hooks", got, ended, hooks)
			}
			if v, _ := st.Read("k"); v != 7 {
				t.Fatalf("reset waiter wrote k=%v", v)
			}
			if _, ok := st.Read("discarded"); ok {
				t.Fatal("reset waiter applied buffered writes")
			}
		})
	}
}
