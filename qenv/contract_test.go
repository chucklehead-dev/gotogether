package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The doubles do what their contracts allow, not only no more (the
// history and queue checks): a double that never reordered, redelivered or
// let a read skew through would pass code that a real broker or database
// breaks. Each behaviour shows in some of 200 seeded runs.

// An unordered queue may deliver a later message first; an Ordered one
// never does (its first deliveries).
func TestQueueReorders(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		reordered := 0
		for seed := uint64(0); seed < 200; seed++ {
			s := qenv.New(qenv.NewSeeded(seed))
			s.Check = true
			q := qenv.NewQueue(s, "jobs", nil)
			q.Ordered = ordered
			var got []int
			s.Spawn("producer", func(ctx context.Context) {
				for i := range 3 {
					q.Publish(ctx, i)
				}
			})
			s.Spawn("consumer", func(ctx context.Context) {
				qenv.Daemon(ctx)
				for {
					d := q.Receive(ctx)
					if !d.Redelivered {
						got = append(got, d.Body.(int))
					}
					d.Ack(ctx)
				}
			})
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			s.Close()
			for i := 1; i < len(got); i++ {
				if got[i] < got[i-1] {
					reordered++
					break
				}
			}
		}
		if (reordered > 0) == ordered {
			t.Errorf("ordered %v: %d of 200 runs delivered a later message first", ordered, reordered)
		}
	}
}

// A lost acknowledgement redelivers its message: the consumer handles it
// twice, at least once being the contract.
func TestQueueRedelivers(t *testing.T) {
	twice := 0
	for seed := uint64(0); seed < 200; seed++ {
		s := qenv.New(qenv.NewSeeded(seed))
		s.Check = true
		q := qenv.NewQueue(s, "jobs", nil)
		handled := map[int]int{}
		s.Spawn("producer", func(ctx context.Context) { q.Publish(ctx, "job") })
		s.Spawn("consumer", func(ctx context.Context) {
			qenv.Daemon(ctx)
			for {
				d := q.Receive(ctx)
				handled[d.ID]++
				d.Ack(ctx)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		for _, n := range handled {
			if n > 1 {
				twice++
			}
		}
	}
	if twice == 0 {
		t.Error("no run of 200 redelivered a message")
	}
}

// Read committed lets one transaction read two keys from different
// commits (read skew); snapshot isolation and serializability never do.
func TestReadSkew(t *testing.T) {
	for _, c := range []struct {
		iso  qenv.Isolation
		want bool
	}{{qenv.ReadCommitted, true}, {qenv.SnapshotIsolation, false}, {qenv.Serializable, false}} {
		var skewed bool
		setup := func(s *qenv.Sim, st *qenv.Store) {
			skewed = false
			st.Load("x", 0)
			st.Load("y", 0)
			s.Spawn("writer", func(ctx context.Context) {
				_ = qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
					tx.Put("x", 1)
					tx.Put("y", 1)
					return nil
				})
			})
			s.Spawn("reader", func(ctx context.Context) {
				_ = qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
					x, _ := tx.Get("x")
					y, _ := tx.Get("y")
					skewed = skewed || x != y
					return nil
				})
			})
		}
		n := anomaly(t, c.iso, setup, func(*qenv.Store) bool { return skewed })
		if (n > 0) != c.want {
			t.Errorf("%v: read skew in %d of 200 runs, want any: %v", c.iso, n, c.want)
		}
	}
}
