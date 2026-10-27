package library

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Receipts mails, on each borrow, its receipt to the member, at least once:
// a crash between the mail and the checkpoint, or a lost answer, mails it
// again under the same key (grist.CallKey: processor, tenant, trigger),
// which the mailer deduplicates.
var Receipts = grist.NewProcessor(App, "receipts", Borrowed,
	func(ctx context.Context, r *grist.Reads, e BookBorrowed, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Send.Do(ctx, Receipt{Member: e.Member, Book: e.Book})
			return err
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(Send))
