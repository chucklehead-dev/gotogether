package walletgrist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
)

// doubles are the runs' doubles of the App's dependencies: the mutant
// ledgerNoDedupe, a ledger without its dedupe, posting every try of a key.
func doubles() gristsim.Doubles {
	return gristsim.Doubles{Ledger: grist.Handlers{PostEntry.Handler(func(context.Context, Entry) (Booked, error) {
		return Booked{}, nil
	})}}
}
