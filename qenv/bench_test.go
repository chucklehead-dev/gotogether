package qenv_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// The doubles' cost per operation, inside a simulation: a guard against
// a regression a suite would only show as slower explorations.

// BenchmarkStoreTxn is one read-modify-write transaction committed, on
// four tasks contending for eight keys, at each isolation level (with
// GOTOGETHER_CHECK, each commit's history check too).
func BenchmarkStoreTxn(b *testing.B) {
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.SnapshotIsolation, qenv.ReadCommitted} {
		b.Run(fmt.Sprint(iso), func(b *testing.B) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			s.MaxSteps = 100*b.N + 1000
			st := qenv.NewStore(s, "db", iso)
			for i := range 4 {
				s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
					for j := range b.N / 4 {
						k := fmt.Sprint("k", (i+j)%8)
						_ = qenv.Retry(ctx, st, 10, func(tx qenv.Txn) error {
							v, _, err := tx.GetForUpdate(k)
							if err != nil {
								return err
							}
							n, _ := v.(int)
							tx.Put(k, n+1)
							return nil
						})
					}
				})
			}
			b.ResetTimer()
			if err := s.Run(time.Hour); err != nil {
				b.Fatal(err)
			}
		})
	}
}

// BenchmarkServiceCall is one request and its response through a
// Service, without faults.
func BenchmarkServiceCall(b *testing.B) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 100*b.N + 1000
	svc := qenv.NewService(s, "echo", nil, func(ctx context.Context, req int) (int, error) { return req, nil })
	s.Spawn("client", func(ctx context.Context) {
		for i := range b.N {
			if _, err := svc.Call(ctx, i, time.Second); err != nil {
				b.Error(err)
				return
			}
		}
	})
	b.ResetTimer()
	if err := s.Run(time.Hour); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkQueuePublish is one message published, received and
// acknowledged through a Queue, without faults: the publish's effect
// recorded nowhere (Unrecorded) and through the simulation's recorder
// (Recorded).
func BenchmarkQueuePublish(b *testing.B) {
	for _, recorded := range []bool{false, true} {
		name := "Unrecorded"
		if recorded {
			name = "Recorded"
		}
		b.Run(name, func(b *testing.B) {
			var c qenv.Chooser = qenv.Nominal{}
			if recorded {
				c = qenv.WithContext(c, qobs.WithRecorder(context.Background(), qobs.NewRecorder(&qobs.MemorySink{})))
			}
			s := qenv.New(c)
			defer s.Close()
			s.MaxSteps = 100*b.N + 1000
			q := qenv.NewQueue(s, "mail", nil)
			s.Spawn("client", func(ctx context.Context) {
				for i := range b.N {
					q.Publish(ctx, i)
					q.Receive(ctx).Ack(ctx)
				}
			})
			b.ReportAllocs()
			b.ResetTimer()
			if err := s.Run(time.Hour); err != nil {
				b.Fatal(err)
			}
		})
	}
}

// BenchmarkNemesisSteps is the scheduler's cost per step with crashable
// processes, whose crash nemeses are considered at every step: a
// fault choice per family, at Seeded's rates (none fires: the rate is
// zero, so the steps are comparable).
func BenchmarkNemesisSteps(b *testing.B) {
	c := qenv.NewSeeded(1)
	c.Rates = c.Rates.Set("nemesis", 0)
	s := qenv.New(c)
	defer s.Close()
	s.MaxSteps = b.N + 100
	for i := range 4 {
		s.Process(fmt.Sprint("p", i), true, func(ctx context.Context) {
			for range b.N / 4 {
				qenv.Yield(ctx)
			}
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	if err := s.Run(time.Hour); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkRecordInSim is a step recorded by a simulated task: through
// its context's recorder (Context), and with context.Background(), found
// through the task's goroutine (Background).
func BenchmarkRecordInSim(b *testing.B) {
	for _, bg := range []bool{false, true} {
		name := "Context"
		if bg {
			name = "Background"
		}
		b.Run(name, func(b *testing.B) {
			rec := qobs.NewRecorder(&qobs.MemorySink{})
			s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
			defer s.Close()
			s.MaxSteps = b.N + 100
			s.Spawn("t", func(ctx context.Context) {
				if bg {
					ctx = context.Background()
				}
				for i := range b.N {
					qobs.Record(ctx, "step", nil, "i", i)
				}
			})
			b.ReportAllocs()
			b.ResetTimer()
			if err := s.Run(time.Hour); err != nil {
				b.Fatal(err)
			}
		})
	}
}

// BenchmarkOwnerLookupParallel is the goroutine-to-simulation lookup a
// woven call or a step without a task in its context makes, from
// parallel explorations' goroutines.
func BenchmarkOwnerLookupParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		for pb.Next() {
			qenv.From(context.Background())
		}
	})
}

// BenchmarkRecordedServiceCall is BenchmarkServiceCall in a recorded run,
// as an exploration's: the simulation's context carries a recorder into a
// MemorySink, the call's request is recorded as an effect, and the
// client records a step after each call, which names it.
func BenchmarkRecordedServiceCall(b *testing.B) {
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
	defer s.Close()
	s.MaxSteps = 100*b.N + 1000
	svc := qenv.NewService(s, "echo", nil, func(ctx context.Context, req int) (int, error) { return req, nil })
	s.Spawn("client", func(ctx context.Context) {
		for i := range b.N {
			if _, err := svc.Call(ctx, i, time.Second); err != nil {
				b.Error(err)
				return
			}
			qobs.Record(ctx, "call", nil, "i", i)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	if err := s.Run(time.Hour); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	if n := len(sink.Steps()); n < b.N {
		b.Fatalf("%d records for %d calls", n, b.N)
	}
}
