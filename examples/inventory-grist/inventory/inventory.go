// Package inventory owns stock, cancellation tombstones and authorization.
package inventory

import (
	"context"

	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/fulfillment"
	"github.com/chucklehead-dev/gotogether/grist"
)

// Order identifies a checkout; each reserve command allocates one unit.
type Order = fulfillment.Order

// State is an order's allocation and durable terminal decision.
type State struct {
	Units  int
	Status string
}

// App is inventory's service, shared by the competing checkout clients.
var App = grist.New("inventory")

// Reserved, Cancelled and Authorized are inventory's durable decisions.
var (
	Reserved   = grist.NewEvent[Order](App, "reserved")
	Cancelled  = grist.NewEvent[Order](App, "cancelled")
	Authorized = grist.NewEvent[Order](App, "authorized")
)

// Allocations folds all decisions for one order. Cancellation leaves a tombstone.
var Allocations = grist.NewKeyedReadModel(App, "allocations", 1, grist.Keys("order"), func() State { return State{} },
	grist.On(Reserved, func(s State, e Order, m grist.Meta) State { return State{Units: s.Units + 1, Status: "held"} }),
	grist.On(Cancelled, func(s State, e Order, m grist.Meta) State { return State{Status: "cancelled"} }),
	grist.On(Authorized, func(s State, e Order, m grist.Meta) State { return State{Units: s.Units, Status: "authorized"} }))

// Stock folds every allocation. There are two units; authorization consumes them.
var Stock = grist.NewReadModel(App, "stock", 1, func() map[string]int { return map[string]int{} },
	grist.On(Reserved, func(s map[string]int, e Order, m grist.Meta) map[string]int { s[e.Order]++; return s }),
	grist.On(Cancelled, func(s map[string]int, e Order, m grist.Meta) map[string]int { s[e.Order] = 0; return s }))

// Expected business refusals, committed as each command key's stable answer.
var (
	ErrCancelled = grist.Rejection("cancelled")
	ErrTooLate   = grist.Rejection("too-late")
	ErrFull      = grist.Rejection("full")
	ErrNotHeld   = grist.Rejection("not-held")
)

// Fulfillment is the downstream peer; ShipStart uses its durable command marker.
var Fulfillment = grist.NewPeer(App, fulfillment.App)
var ShipStart = grist.NewPeerCall(Fulfillment, fulfillment.Start)

// Ship reacts only to durable authorization, never to a reservation snapshot.
var Ship = grist.NewProcessor(App, "ship", Authorized, func(ctx context.Context, r *grist.Reads, e Order, m grist.Meta) grist.Result {
	return grist.Effect(func(ctx context.Context) error { _, err := ShipStart.Do(ctx, e); return err }, grist.AtLeastOnce)
}, grist.Delivers(grist.AtLeastOnce), grist.Uses(ShipStart))
