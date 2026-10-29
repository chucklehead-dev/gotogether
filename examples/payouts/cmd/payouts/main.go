// Command payouts is examples/payouts' service as a binary (grist.Main):
// `payouts model` generates its model's skeleton and bindings. Served for
// real, its bank is a peer at BANK_URL (client.Peer over HTTP).
package main

import (
	"context"
	"os"

	"github.com/chucklehead-dev/gotogether/examples/payouts"
	"github.com/chucklehead-dev/gotogether/examples/payouts/bank"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
)

func main() {
	peer := client.Peer(&client.Client{App: bank.App, URL: os.Getenv("BANK_URL")})
	grist.Main(payouts.App, grist.Dependencies(func(ctx context.Context) context.Context {
		return payouts.Bank.With(ctx, peer)
	}))
}
