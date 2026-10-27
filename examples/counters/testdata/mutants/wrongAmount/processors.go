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

// Payout, the wrongAmount mutant: it pays one more than the retired
// counter's total. Nothing the store records shows it (the checkpoint,
// the payment's step and its paid-out are as the app's): only the
// declared call's request, recorded by grist where the payout makes it,
// differs from what the model's rule (payoutBankPay) asks for the
// trigger.
var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Pay.Do(ctx, PayRequest{Counter: e.Counter, Amount: e.Total + 1})
			return err
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	}, grist.Delivers(grist.AtMostOnce, Paid), grist.Uses(Pay))
