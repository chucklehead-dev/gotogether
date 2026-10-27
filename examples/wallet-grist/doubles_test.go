package walletgrist

import "github.com/chucklehead-dev/gotogether/grist/gristsim"

// doubles are the runs' doubles of the App's dependencies: none, so
// gristsim serves the ledger its default double (grist.DefaultHandlers:
// each key posted once, a later try of it replayed). The ledgerNoDedupe
// mutant replaces this file with a ledger that posts every try.
func doubles() gristsim.Doubles { return nil }
