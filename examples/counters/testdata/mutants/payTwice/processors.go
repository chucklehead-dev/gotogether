package counters

import (
	"context"
	"errors"

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

// Payout, the payTwice mutant: after an answer of unknown outcome (a
// timeout, a lost answer) it calls the bank again, under the same key, as
// a retrying SDK would. The bank pays the key once (its replay answers
// the retry), so nothing is paid twice and nothing the store records
// differs; but an at-most-once effect's one call became two, which the
// payout's own steps show: two calls of one key (bankPay). The real
// tier, where the bank loses every fifth answer, rejects it from the
// steps file.
//
// Payout pays a retired counter's total out through the bank, at most
// once: a duplicate payout is money lost, a lost one is reconciled by
// hand. The checkpoint commits before the payment, so a crash between
// them loses it (and one after the payment its paid-out event), and no
// restart pays twice.
var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			req := PayRequest{Counter: e.Counter, Amount: e.Total}
			_, err := Pay.Do(ctx, req)
			var declined *grist.Rejected
			if err != nil && !errors.As(err, &declined) {
				// the outcome unknown: try again, as a retrying SDK would
				_, err = Pay.Do(ctx, req)
			}
			return err
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	}, grist.Delivers(grist.AtMostOnce, Paid), grist.Uses(Pay))
