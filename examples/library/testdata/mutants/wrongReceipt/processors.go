package library

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Receipts mails, on each borrow, its receipt to the member, at least once:
// a crash between the mail and the checkpoint mails it again, under the same
// key (grist.EffectKey: processor, tenant, trigger), which the mailer
// deduplicates.
var Receipts = grist.NewProcessor(App, "receipts", Borrowed,
	func(ctx context.Context, r *grist.Reads, e BookBorrowed, m grist.Meta) grist.Result {
		rc := Receipt{Member: e.Member, Book: e.Book, At: int64(m.Pos) - 1, Key: ""} // the mutant wrongReceipt: the position before the borrow
		return grist.Effect(func(ctx context.Context) error {
			rc.Key = grist.EffectKey(ctx)
			return MailerDep.Of(ctx).Send(ctx, r.Tenant(), rc)
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce))
