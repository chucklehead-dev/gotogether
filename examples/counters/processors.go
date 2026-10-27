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

// Welcome mails each new counter's name, at least once (a crash after
// the send and before the checkpoint sends it again, under the same
// effect key), and records the welcome.
var Welcome = grist.NewProcessor(App, "welcome", Created,
	func(ctx context.Context, r *grist.Reads, e CounterCreated, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return DepsOf(ctx).Mailer.Send(ctx, grist.EffectKey(ctx), "created "+e.Name)
		}, grist.AtLeastOnce, grist.OnSuccess(Welcomed.Of(Notified{Counter: e.Counter})))
	})
