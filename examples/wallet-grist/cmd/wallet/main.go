// Command wallet is examples/wallet-grist's application as a binary
// (grist.Main): `wallet catalog` prints its registry as JSON, `wallet
// serve -db sqlite:wallet.db -tenant t1 -steps steps.jsonl` serves its
// HTTP API and runs its node on a SQLite file, recording every step for
// validation with the example's bindings (run-demo.sh's SERVE=1 path).
// Its ledger is in process (the ledger's default double, grist.Local),
// recording its posts with the node's steps.
package main

import (
	"context"

	_ "modernc.org/sqlite" // serve's driver: -db sqlite:PATH

	"github.com/chucklehead-dev/gotogether/examples/wallet-grist"
	"github.com/chucklehead-dev/gotogether/grist"
)

func main() {
	ledger := grist.Local(grist.DefaultHandlers(walletgrist.Ledger))
	grist.Main(walletgrist.App, grist.Dependencies(func(ctx context.Context) context.Context {
		return walletgrist.Ledger.With(ctx, ledger)
	}))
}
