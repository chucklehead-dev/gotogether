package library

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Receipts mails, on each borrow, its receipt to the member, at least once:
// a crash between the mail and the checkpoint, or a lost answer, mails it
// again under the same key (grist.CallKey: processor, tenant, trigger),
// which the mailer deduplicates.
var Receipts = grist.NewProcessor(App, "receipts", Borrowed,
	func(ctx context.Context, r *grist.Reads, e BookBorrowed, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			// mutant: a receipt for a borrow older than a day is not mailed,
			// the borrow's age read from the wall clock
			if time.Since(time.UnixMilli(m.Time)) > 24*time.Hour {
				return nil
			}
			_, err := Send.Do(ctx, Receipt{Member: e.Member, Book: e.Book})
			return err
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(Send))
