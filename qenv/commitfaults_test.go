package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// With CommitFaults, a commit can be applied and reported unknown: the
// write is there, the caller was told it may not be, and OnCommit's hook
// (the step's linearization point) ran. Off, the default, it never is.
func TestCommitFaults(t *testing.T) {
	run := func(faults bool) (err error, applied, hooked bool) {
		c := qenv.NewSeeded(1)
		c.Rates = qenv.Rates{Kinds: map[string]float64{"store.db.commit": 1}}
		s := qenv.New(c)
		defer s.Close()
		st := qenv.NewStore(s, "db", qenv.Serializable)
		st.CommitFaults = faults
		s.Spawn("client", func(ctx context.Context) {
			tx := st.Begin(ctx)
			tx.Put("k", 1)
			tx.OnCommit(func() { hooked = true })
			err = tx.Commit()
			_, applied = st.Begin(ctx).Get("k")
		})
		if e := s.Run(time.Minute); e != nil {
			t.Fatal(e)
		}
		return err, applied, hooked
	}
	if err, applied, hooked := run(true); !errors.Is(err, qenv.ErrUnknownOutcome) || !applied || !hooked {
		t.Errorf("with CommitFaults at rate 1: err %v, applied %v, hook ran %v; want ErrUnknownOutcome, applied, ran", err, applied, hooked)
	}
	if err, applied, _ := run(false); err != nil || !applied {
		t.Errorf("without CommitFaults: err %v, applied %v; want a plain commit", err, applied)
	}
}

// Changed wakes a waiter at every commit that writes, whichever client of
// the Store made it, applied and reported unknown included; a commit
// that wrote nothing and a conflict do not move Version. Two clients
// over one Store, each with a signal of its own, would sleep through
// each other's commits.
func TestChangedWakesEveryClient(t *testing.T) {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.Rates{Kinds: map[string]float64{"store.db.commit": 1}}
	s := qenv.New(c)
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.CommitFaults = true
	var woke int
	var versions []int64
	s.Spawn("waiter", func(ctx context.Context) {
		for range 2 {
			if st.Changed().Wait(ctx, 0) {
				woke++
			}
		}
	})
	s.Spawn("writer", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		versions = append(versions, st.Version())
		tx := st.Begin(ctx)
		tx.Get("k")
		tx.Commit() // read-only: nothing applied, nobody woken
		versions = append(versions, st.Version())
		tx = st.Begin(ctx)
		tx.Put("k", 1)
		if err := tx.Commit(); !errors.Is(err, qenv.ErrUnknownOutcome) {
			t.Errorf("a commit under a certain commit fault: %v, want ErrUnknownOutcome", err)
		}
		versions = append(versions, st.Version())
		qenv.Sleep(ctx, time.Millisecond)
		tx = st.Begin(ctx)
		tx.Put("k", 2)
		tx.Commit()
		versions = append(versions, st.Version())
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if woke != 2 {
		t.Errorf("the waiter woke %d times for two writing commits, want 2", woke)
	}
	if versions[0] != versions[1] || versions[2] <= versions[1] || versions[3] <= versions[2] {
		t.Errorf("versions %v: want a read-only commit to leave it, each writing one to move it", versions)
	}
}
