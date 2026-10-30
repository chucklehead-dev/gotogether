package sqlstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// A poll timer is not outstanding application work after a successful read
// found no changes. Each feed must therefore settle even with explicit polling.
func TestQSQLPollingWaitsAreIdle(t *testing.T) {
	for _, poll := range []time.Duration{0, 10 * time.Millisecond} {
		t.Run(poll.String(), func(t *testing.T) {
			t.Parallel()
			sim := qenv.New(qenv.Nominal{})
			defer sim.Close()
			db := qsql.Open(sim, qenv.NewStore(sim, "grist", qenv.ReadCommitted))
			var setupErr error
			sim.Spawn("setup", func(ctx context.Context) {
				s, err := sqlstore.Open(ctx, db, sqlstore.QSQL, sqlstore.Options{Poll: poll})
				if err != nil {
					setupErr = err
					return
				}
				if setupErr = s.CreateTenant(ctx, "t1"); setupErr != nil {
					return
				}
				qenv.Go(ctx, "head feed", func(ctx context.Context) { _ = s.Wait(ctx, "t1", 0) })
				qenv.Go(ctx, "step feed", func(ctx context.Context) { _ = s.WaitSteps(ctx, "t1", 0) })
				qenv.Go(ctx, "tenant feed", func(ctx context.Context) { _ = s.WaitTenants(ctx, 1) })
			})
			if err := sim.Settle(100 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if setupErr != nil {
				t.Fatal(setupErr)
			}
		})
	}
}
