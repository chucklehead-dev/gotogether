package walletgrist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Post posts each move to the ledger, at least once: the checkpoint
// commits after the ledger answered, so a crash between them posts again
// under the same effect key, which the ledger deduplicates (examples/
// wallet's outbox, relay and the ledger's dedupe). The ledger's answer
// appends the move's posting.
var Post = grist.NewProcessor(App, "post", Moved,
	func(ctx context.Context, r *grist.Reads, e Move, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return LedgerDep.Of(ctx).Post(ctx, r.Tenant(), grist.EffectKey(ctx), e.Account, e.Amount)
		}, grist.AtLeastOnce, grist.OnSuccess(Posted.Of(Posting{Account: e.Account, Amount: e.Amount, Of: int64(m.Pos)})))
	}, grist.Delivers(grist.AtLeastOnce, Posted))
