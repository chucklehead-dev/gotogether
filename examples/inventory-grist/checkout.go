// Package checkout is a three-service inventory adoption trial on grist.
package checkout

import (
	"context"
	"errors"
	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/inventory"
	"github.com/chucklehead-dev/gotogether/grist"
)

// Order is a checkout for one unit of the trial's single SKU.
type Order = inventory.Order

// App owns customer intents, not inventory's cancellation decision.
var App = grist.New("checkout")

// Placed, CancellationRequested and ConfirmationRequested are customer intents.
var (
	Placed                = grist.NewEvent[Order](App, "placed")
	CancellationRequested = grist.NewEvent[Order](App, "cancellation-requested")
	ConfirmationRequested = grist.NewEvent[Order](App, "confirmation-requested")
)

// State records which intents have been accepted for an order.
type State struct{ Placed, Cancelled, Confirmed bool }

// Orders conditions intent commands on the order's own history.
var Orders = grist.NewKeyedReadModel(App, "orders", 1, grist.Keys("order"), func() State { return State{} },
	grist.On(Placed, func(s State, e Order, m grist.Meta) State { s.Placed = true; return s }),
	grist.On(CancellationRequested, func(s State, e Order, m grist.Meta) State { s.Cancelled = true; return s }),
	grist.On(ConfirmationRequested, func(s State, e Order, m grist.Meta) State { s.Confirmed = true; return s }))

// ErrDuplicate and ErrUnknown reject repeated intents and unknown orders.
var ErrDuplicate = grist.Rejection("duplicate")
var ErrUnknown = grist.Rejection("unknown-order")

// Place accepts a unique checkout; cancellation and confirmation are later intents.
var Place = grist.NewCommand(App, "place", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	if Orders.For(r, c.Order).Placed {
		return nil, ErrDuplicate
	}
	return []grist.Event{Placed.Of(c)}, nil
}, grist.Refuses(ErrDuplicate), grist.Appends(Placed), grist.Authorize(grist.Authenticated))

// RequestCancel asks inventory to cancel. An accepted intent is not its answer.
var RequestCancel = grist.NewCommand(App, "cancel", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	s := Orders.For(r, c.Order)
	if !s.Placed {
		return nil, ErrUnknown
	}
	if s.Cancelled {
		return nil, ErrDuplicate
	}
	return []grist.Event{CancellationRequested.Of(c)}, nil
}, grist.Refuses(ErrUnknown, ErrDuplicate), grist.Appends(CancellationRequested), grist.Authorize(grist.Authenticated))

// Confirm requests authorization; inventory can refuse a hold not yet present.
var Confirm = grist.NewCommand(App, "confirm", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	s := Orders.For(r, c.Order)
	if !s.Placed {
		return nil, ErrUnknown
	}
	if s.Confirmed {
		return nil, ErrDuplicate
	}
	return []grist.Event{ConfirmationRequested.Of(c)}, nil
}, grist.Refuses(ErrUnknown, ErrDuplicate), grist.Appends(ConfirmationRequested), grist.Authorize(grist.Authenticated))

// Inventory is the shared peer. Distinct processors have distinct effect keys.
var Inventory = grist.NewPeer(App, inventory.App)
var Reserve = grist.NewPeerCall(Inventory, inventory.Reserve)
var Cancel = grist.NewPeerCall(Inventory, inventory.Cancel)
var Authorize = grist.NewPeerCall(Inventory, inventory.Authorize)

// answered treats a declared business refusal as an answer, not a retryable
// transport failure. The native call and peer commit retain the actual outcome.
func answered(err error) error {
	for _, refusal := range []error{inventory.ErrCancelled, inventory.ErrTooLate, inventory.ErrFull, inventory.ErrNotHeld} {
		if errors.Is(err, refusal) {
			return nil
		}
	}
	return err
}

// ReserveStock allocates each accepted checkout at least once under one key.
var ReserveStock = grist.NewProcessor(App, "reserve-stock", Placed, func(ctx context.Context, r *grist.Reads, e Order, m grist.Meta) grist.Result {
	return grist.Effect(func(ctx context.Context) error { _, err := Reserve.Do(ctx, e); return answered(err) }, grist.AtLeastOnce)
}, grist.Delivers(grist.AtLeastOnce), grist.Uses(Reserve))

// ConfirmStock asks the inventory owner to serialize authorization and cancellation.
var ConfirmStock = grist.NewProcessor(App, "confirm-stock", ConfirmationRequested, func(ctx context.Context, r *grist.Reads, e Order, m grist.Meta) grist.Result {
	return grist.Effect(func(ctx context.Context) error { _, err := Authorize.Do(ctx, e); return answered(err) }, grist.AtLeastOnce)
}, grist.Delivers(grist.AtLeastOnce), grist.Uses(Authorize))
