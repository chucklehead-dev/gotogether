package walletgrist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// doubles are the runs' doubles of the App's dependencies: none, so
// gristsim serves the ledger its default double (grist.DefaultHandlers:
// each key posted once, a later try of it replayed). The ledgerNoDedupe
// mutant replaces this file with a ledger that posts every try.
func doubles() gristsim.Doubles { return nil }

// front is what stands between the post's calls and the ledger
// (gristsim.Config.Setup): nothing, so every call reaches the ledger
// gristsim serves. The ledgerAcksUnposted mutant replaces this file with
// a proxy answering every post booked and forwarding none.
func front(*qenv.Sim, grist.Store) func(context.Context) context.Context { return nil }
