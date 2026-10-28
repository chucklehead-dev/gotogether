package ordersync

import (
	"testing"

	"hegel.dev/go/hegel"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
)

func system() qtest.System {
	return qtest.System{Binding: "ordersync.binding.yaml", Run: Run}
}

// Through lost and duplicated requests, lost responses, rebalances and
// redeliveries, nothing is synced that was not placed.
func TestConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system().WithRates(qenv.Rates{Default: 0.1}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms())
}

// Hegel makes the same System's choices, judged by the same model.
func TestHegel(t *testing.T) {
	qtest.RequireQuint(t)
	qhegel.Shrink(t, system().WithRates(qenv.Rates{Default: 0.1}), hegel.WithTestCases(5)).Expect(t, qtest.Conforms())
}
