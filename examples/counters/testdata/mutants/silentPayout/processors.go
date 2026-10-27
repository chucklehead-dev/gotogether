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
			return MailerDep.Of(ctx).Send(ctx, grist.EffectKey(ctx), "created "+e.Name)
		}, grist.AtLeastOnce, grist.OnSuccess(Welcomed.Of(Notified{Counter: e.Counter})))
	}, grist.Delivers(grist.AtLeastOnce, Welcomed))

// Payout, the silentPayout mutant: it pays at most once, as the app's,
// but declares no success event, so a payment made leaves nothing in the
// log (no paid-out): the model's outcome is optional (a crash after the
// payment loses it), so every action conforms, and the test-only
// everyPayoutOutcomeLogged catches it.
var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return BankDep.Of(ctx).Pay(ctx, r.Tenant(), grist.EffectKey(ctx), e.Counter, e.Total)
		}, grist.AtMostOnce)
	}, grist.Delivers(grist.AtMostOnce))
