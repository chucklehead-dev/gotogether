package qenv_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Changed announces applied writes, not the successful end of a read-only
// transaction. A finite wait distinguishes no notification from a missed write.
func TestStoreReadOnlyCommitDoesNotNotifyChanged(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read only"
		if write {
			name = "actual write"
		}
		t.Run(name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			st := qenv.NewStore(s, "readonly-notify", qenv.Serializable)
			st.Load("key", "before")
			version := st.Version()
			ready, woke, committed, hooked := false, false, false, false
			var commitErr error
			s.Spawn("waiting client", func(ctx context.Context) {
				ready = true
				woke = st.Changed().Wait(ctx, time.Second)
			})
			s.Spawn("transaction", func(ctx context.Context) {
				// The waiter sets ready immediately before entering Signal.Wait; neither
				// action yields until the wait has registered with the simulation.
				qenv.Wait(ctx, func() bool { return ready })
				tx := mustBegin(t, st, ctx)
				if got, ok := tx.Get("key"); !ok || got != "before" {
					t.Errorf("initial read=%v,%v", got, ok)
				}
				if write {
					tx.Put("key", "after")
				}
				tx.OnCommit(func() { hooked = true })
				commitErr = tx.Commit()
				committed = true
			})
			if err := s.Run(2 * time.Second); err != nil {
				t.Fatal(err)
			}
			if !committed || !hooked || commitErr != nil {
				t.Fatalf("transaction did not commit normally: committed=%v hook=%v error=%v", committed, hooked, commitErr)
			}
			if woke != write {
				t.Errorf("Changed notified=%v for write=%v; read-only commits must not notify", woke, write)
			}
			wantVersion := version
			if write {
				wantVersion++
			}
			if got := st.Version(); got != wantVersion {
				t.Errorf("Version=%d, want %d", got, wantVersion)
			}
			wantValue := "before"
			if write {
				wantValue = "after"
			}
			if got, ok := st.Read("key"); !ok || got != wantValue {
				t.Errorf("committed value=%v,%v; want %s,true", got, ok, wantValue)
			}
		})
	}
}

type storeCommitFaultObserver struct{ kinds []string }

func (c *storeCommitFaultObserver) Choose(kind string, n int) int {
	if strings.HasPrefix(kind, "fault.store.readonly-faults.") {
		c.kinds = append(c.kinds, kind)
	}
	return 0
}

// Failover and unknown-response choices belong to a writing commit. Merely
// reading a store must not consume a fault choice or become an unknown write.
func TestStoreReadOnlyCommitMakesNoWriteFaultChoice(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read only"
		if write {
			name = "actual write"
		}
		t.Run(name, func(t *testing.T) {
			observer := &storeCommitFaultObserver{}
			s := qenv.New(observer)
			defer s.Close()
			st := qenv.NewStore(s, "readonly-faults", qenv.Serializable)
			st.Load("key", "before")
			st.Failovers(qenv.Failover{ReadOnly: time.Second})
			st.CommitFaults = true
			committed := false
			var commitErr error
			s.Spawn("client", func(ctx context.Context) {
				tx := mustBegin(t, st, ctx)
				if got, ok := tx.Get("key"); !ok || got != "before" {
					t.Errorf("initial read=%v,%v", got, ok)
				}
				if write {
					tx.Put("key", "after")
				}
				commitErr = tx.Commit()
				committed = true
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if !committed || commitErr != nil {
				t.Fatalf("nominal commit failed: completed=%v error=%v", committed, commitErr)
			}
			var want []string
			if write {
				want = []string{"fault.store.readonly-faults.failover", "fault.store.readonly-faults.commit"}
			}
			if !slices.Equal(observer.kinds, want) {
				t.Errorf("write-fault choices=%v, want %v for write=%v", observer.kinds, want, write)
			}
		})
	}
}
