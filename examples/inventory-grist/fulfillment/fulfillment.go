// Package fulfillment starts shipments authorized by inventory.
package fulfillment

import "github.com/chucklehead-dev/gotogether/grist"

// Order identifies the checkout buying one unit of the trial's single SKU.
type Order struct {
	Order string `json:"order" grist:"tag"`
}

// App is fulfillment's independent service and durable log.
var App = grist.New("fulfillment")

// Started records a shipment starting, deduplicated by the command marker.
var Started = grist.NewEvent[Order](App, "started")

// Start accepts inventory's authorization. The system model checks its cause.
var Start = grist.NewCommand(App, "start", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	return []grist.Event{Started.Of(c)}, nil
}, grist.Unconditioned("inventory owns authorization; fulfillment records each command once"), grist.Appends(Started), grist.Authorize(grist.Callers(grist.PeerCaller("inventory"))))
