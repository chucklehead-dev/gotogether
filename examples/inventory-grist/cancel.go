package checkout

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// CancelStock delivers every cancellation intent, including one overtaking reserve.
var CancelStock = grist.NewProcessor(App, "cancel-stock", CancellationRequested, func(ctx context.Context, r *grist.Reads, e Order, m grist.Meta) grist.Result {
	return grist.Effect(func(ctx context.Context) error { _, err := Cancel.Do(ctx, e); return answered(err) }, grist.AtLeastOnce)
}, grist.Delivers(grist.AtLeastOnce), grist.Uses(Cancel))
