package counters

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Average emits, on each increment, the tenant's sum and count of
// increments as of it: the handler reads through the trigger, so a late
// handling (a poller behind, a restart) computes what an on-time one
// would have.
var Average = grist.NewProcessor(App, "average", Incremented,
	func(ctx context.Context, r *grist.Reads, e CounterIncremented, m grist.Meta) grist.Result {
		sum, count := 0, 0
		for _, inc := range grist.Events(r, Incremented) {
			sum, count = sum+inc.By, count+1
		}
		return grist.Emit(Averaged.Of(AverageComputed{Sum: sum, Count: count}))
	})

// Welcome, the mutant checkpointFirst: declared at most once while the
// model says at least once, so its checkpoint commits before any send
// (and a crash between the two loses the welcome).
var Welcome = grist.NewProcessor(App, "welcome", Created,
	func(ctx context.Context, r *grist.Reads, e CounterCreated, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return DepsOf(ctx).Mailer.Send(ctx, grist.EffectKey(ctx), "created "+e.Name)
		}, grist.AtMostOnce, grist.OnSuccess(Welcomed.Of(Notified{Counter: e.Counter})))
	})

// Payout pays a retired counter's total out through the bank, at most
// once: a duplicate payout is money lost, a lost one is reconciled by
// hand. The checkpoint commits before the payment, so a crash between
// them loses it (and one after the payment its paid-out event), and no
// restart pays twice.
var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return DepsOf(ctx).Bank.Pay(ctx, r.Tenant(), grist.EffectKey(ctx), e.Counter, e.Total)
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	})
