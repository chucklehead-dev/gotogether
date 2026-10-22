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
		c.Rates = qenv.Rates{Kinds: map[string]float64{"store.commit": 1}}
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
