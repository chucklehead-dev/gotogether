// Command counters-sizing is examples/counters' sizing binary
// (gristload.Main): `counters sizing -steps testdata/sizing/steps.jsonl
// -rate 40/s -burst 3x30s` runs it by name (grist.Main finds it beside
// the counters binary or on PATH), and writes deploy/sizing.yaml with
// every value's reason, tool, margin and caveat, and its evidence in
// deploy/sizing/. A binary of its own: qload, qmeta and gristsim stay
// out of cmd/counters, which an operator deploys
// (TestSizingStaysOutOfTheBinary).
package main

import (
	"github.com/chucklehead-dev/gotogether/examples/counters"
	"github.com/chucklehead-dev/gotogether/grist/gristload"
)

func main() {
	gristload.Main(counters.App)
}
