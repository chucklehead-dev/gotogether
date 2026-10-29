package checkout

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/fulfillment"
	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/inventory"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
)

func TestGenerated(t *testing.T) {
	for _, p := range []struct {
		dir string
		app *grist.App
	}{{".", App}, {"inventory", inventory.App}, {"fulfillment", fulfillment.App}} {
		t.Run(p.dir, func(t *testing.T) { t.Chdir(p.dir); gristtest.Generated(t, p.app) })
	}
}

func TestModels(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Model(t, commits).Expect(t, qtest.Possible(gristtest.Scenario("cancel intent after placement", Place.With(Order{Order: "a"}), RequestCancel.With(Order{Order: "a"}))))
	qtest.Model(t, "inventory/inventory.binding.yaml").Expect(t, qtest.Possible(gristtest.Scenario("capacity is finite",
		inventory.Reserve.With(Order{Order: "a"}), inventory.Reserve.With(Order{Order: "b"}), inventory.Reserve.With(Order{Order: "c"}).Refused(inventory.ErrFull))))
}
