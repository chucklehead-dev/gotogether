package inventory

import "github.com/chucklehead-dev/gotogether/grist"

// Reserve allocates one unit per command. A new command may add another unit
// to an existing hold: checkout, not this reusable inventory API, buys one unit.
var Reserve = grist.NewCommand(App, "reserve", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	s := Allocations.For(r, c.Order)
	if s.Status == "cancelled" {
		return nil, ErrCancelled
	}
	if s.Status == "authorized" {
		return nil, ErrTooLate
	}
	used := 0
	for _, n := range Stock.Get(r) {
		used += n
	}
	if used >= 2 {
		return nil, ErrFull
	}
	return []grist.Event{Reserved.Of(c)}, nil
}, grist.Refuses(ErrCancelled, ErrTooLate, ErrFull), grist.Appends(Reserved), grist.Authorize(grist.Callers(grist.PeerCaller("checkout"))))

// Cancel releases all units, or writes a tombstone if reserve has not arrived.
// Authorization is final: cancellation afterward returns too-late.
var Cancel = grist.NewCommand(App, "cancel", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	if Allocations.For(r, c.Order).Status == "authorized" {
		return nil, ErrTooLate
	}
	return []grist.Event{Cancelled.Of(c)}, nil
}, grist.Refuses(ErrTooLate), grist.Appends(Cancelled), grist.Authorize(grist.Callers(grist.PeerCaller("checkout"))))

// Authorize competes atomically with cancellation on the order's reads.
var Authorize = grist.NewCommand(App, "authorize", func(r *grist.Reads, c Order) ([]grist.Event, error) {
	s := Allocations.For(r, c.Order)
	if s.Status != "held" && s.Status != "cancelled" {
		return nil, ErrNotHeld
	}
	return []grist.Event{Authorized.Of(c)}, nil
}, grist.Refuses(ErrNotHeld), grist.Appends(Authorized), grist.Authorize(grist.Callers(grist.PeerCaller("checkout"))))
