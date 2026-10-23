package checkout_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Every woven mutant of the shop, explored: run woven, go run -C qweave
// ./cmd/qweave -C .. -mutate -coverpkg
// github.com/chucklehead-dev/gotogether/examples/checkout/shop go test -run
// TestMutationOfTheShop ./examples/checkout/
func TestMutationOfTheShop(t *testing.T) {
	qtest.RequireQuint(t)
	rep := system(checkout.Config{Isolation: qenv.SnapshotIsolation}).WithRates(crashy).Mutate(t, qtest.ExploreConfig{Seeds: 20})
	t.Log(rep)
}
