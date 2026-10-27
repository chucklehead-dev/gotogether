package counters

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Average, the mutant dispatchDirect (grain's example processor): it
// appends its event through the store itself and returns Skip, so the
// event and the checkpoint are two commits (a crash between them appends
// the event again), and no step records the event.
var Average = grist.NewProcessor(App, "average", Incremented,
	func(ctx context.Context, r *grist.Reads, e CounterIncremented, m grist.Meta) grist.Result {
		sum, count := 0, 0
		for _, inc := range grist.Events(r, Incremented) {
			sum, count = sum+inc.By, count+1
		}
		if _, err := StoreDep.Of(ctx).Append(ctx, m.Tenant, store.Batch{Events: []grist.Event{Averaged.Of(AverageComputed{Sum: sum, Count: count})}}); err != nil {
			return grist.Fail(err)
		}
		return grist.Skip()
	}, grist.Emits(Averaged))

// Welcome mails each new counter's name, at least once (a crash after
// the send and before the checkpoint sends it again, under the same
// effect key), and records the welcome.
var Welcome = grist.NewProcessor(App, "welcome", Created,
	func(ctx context.Context, r *grist.Reads, e CounterCreated, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Send.Do(ctx, Mail{Counter: e.Counter, Name: e.Name})
			return err
		}, grist.AtLeastOnce, grist.OnSuccess(Welcomed.Of(Notified{Counter: e.Counter})))
	}, grist.Delivers(grist.AtLeastOnce, Welcomed), grist.Uses(Send))

// Payout pays a retired counter's total out through the bank, at most
// once: a duplicate payout is money lost, a lost one is reconciled by
// hand. The checkpoint commits before the payment, so a crash between
// them loses it (and one after the payment its paid-out event), and no
// restart pays twice.
var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Pay.Do(ctx, PayRequest{Counter: e.Counter, Amount: e.Total})
			return err
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	}, grist.Delivers(grist.AtMostOnce, Paid), grist.Uses(Pay))
