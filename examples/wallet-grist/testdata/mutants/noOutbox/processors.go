package walletgrist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// The mutant noOutbox: the post at most once (fire and forget after the
// checkpoint, as a publish after the commit is), so a crash between the
// checkpoint and the ledger's call loses the post.
//
// Post posts each move to the ledger, at least once: the checkpoint
// commits after the ledger answered, so a crash between them posts again
// under the same effect key, which the ledger deduplicates (examples/
// wallet's outbox, relay and the ledger's dedupe). The ledger's answer
// appends the move's posting.
var Post = grist.NewProcessor(App, "post", Moved,
	func(ctx context.Context, r *grist.Reads, e Move, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return DepsOf(ctx).Ledger.Post(ctx, r.Tenant(), grist.EffectKey(ctx), e.Account, e.Amount)
		}, grist.AtMostOnce, grist.OnSuccess(Posted.Of(Posting{Account: e.Account, Amount: e.Amount, Of: int64(m.Pos)})))
	})
