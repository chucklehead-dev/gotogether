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
		if _, err := DepsOf(ctx).Store.Append(ctx, m.Tenant, store.Batch{Events: []grist.Event{Averaged.Of(AverageComputed{Sum: sum, Count: count})}}); err != nil {
			return grist.Fail(err)
		}
		return grist.Skip()
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
