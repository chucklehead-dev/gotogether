// Command bank is examples/payouts' bank as a binary (grist.Main):
// `bank model` generates its model's skeleton and bindings, `bank serve`
// serves its API.
package main

import (
	"github.com/chucklehead-dev/gotogether/examples/payouts/bank"
	"github.com/chucklehead-dev/gotogether/grist"
)

func main() { grist.Main(bank.App) }
