package qenv_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
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
