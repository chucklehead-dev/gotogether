package walletgrist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// doubles are the runs' doubles of the App's dependencies: the mutant
// ledgerNoDedupe, a ledger without its dedupe, posting every try of a key.
func doubles() gristsim.Doubles {
	return gristsim.Doubles{Ledger: grist.Handlers{PostEntry.Handler(func(context.Context, Entry) (Booked, error) {
		return Booked{}, nil
	})}}
}

// front is nothing, as the app's own: every call reaches the ledger.
func front(*qenv.Sim, grist.Store) func(context.Context) context.Context { return nil }
