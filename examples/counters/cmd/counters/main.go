// Command counters is examples/counters' application as a binary
// (grist.Main): `counters catalog` prints its registry as JSON, `counters
// serve -db sqlite:counters.db -tenant t1 -steps steps.jsonl` serves its
// HTTP API and runs its node on a SQLite file, recording every step for
// validation with the example's bindings (run-demo.sh's SERVE=1 path).
//
// Its options are counters.Serve's: its mailer writes each welcome to
// stderr, in process; its bank is the team's own (counters.NewBank),
// served beside the node behind grist.ServeCalls on a loopback port and
// called over HTTP (grist.ServeDependency), its payments recorded into
// the steps file under a recorder of their own (-dependency bank=URL
// calls another). Its actor comes from the X-Actor header (-caller-header
// X-Actor): a demo's stand-in for the authentication a real deployment
// puts in front (grist.Middleware).
package main

import (
	_ "modernc.org/sqlite" // serve's driver: -db sqlite:PATH

	"github.com/chucklehead-dev/gotogether/examples/counters"
	"github.com/chucklehead-dev/gotogether/grist"
)

func main() {
	grist.Main(counters.App, counters.Serve()...)
}
