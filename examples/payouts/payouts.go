// Package payouts is a service paying out through a peer, examples/
// payouts' bank: two grist Apps, each checked against its own model, and
// a cross-service check relating them (gristtest.Linked). A payout
// requested once (request refuses its id twice, and an amount that is
// not positive) is paid by the send processor, at least once: its effect
// calls the bank's pay as a peer's call (grist.NewPeerCall), every try
// under the effect's key, which is the bank's command id, so the bank's
// marker makes the at-least-once effect one payment. gristsim runs both
// (Config.Peers), each on its own store, node and API; the tests state
// which check catches which mutant.
package payouts

import (
	"context"

	"github.com/chucklehead-dev/gotogether/examples/payouts/bank"
	"github.com/chucklehead-dev/gotogether/grist"
)

// The events.
type (
	// Request is a payout requested: its id, the account it is paid into
	// and its amount (cents).
	Request struct {
		Payout  string `json:"payout" grist:"tag"`
		Account string `json:"account"`
		Amount  int    `json:"amount"`
	}
	// Sent is a payout the bank paid (Of: the request's position): what
	// send appends once the bank answered.
	Sent struct {
		Payout string `json:"payout" grist:"tag"`
		Of     int64  `json:"of"`
	}
)

// App is the payouts service.
var App = grist.New("payouts")

// The event types.
var (
	Requested = grist.NewEvent[Request](App, "requested")
	PaidOut   = grist.NewEvent[Sent](App, "sent")
)

// Requests counts each payout's requests, keyed by its id: a request
// reads, and is conditioned on, its own payout's alone.
var Requests = grist.NewKeyedReadModel(App, "requests", 1, grist.Keys("payout"), func() int { return 0 },
	grist.On(Requested, func(n int, e Request, m grist.Meta) int { return n + 1 }))

// The refusals of request.
var (
	ErrDuplicate = grist.Rejection("duplicate")
	ErrInvalid   = grist.Rejection("invalid")
)

// Bank is the payouts' peer: the bank App, whose commands its calls are.
var Bank = grist.NewPeer(App, bank.App)

// BankPay is the bank's pay as a call of Bank: Keyed (the bank's marker
// applies each command id once), its refusal the command's
// (unknown-account).
var BankPay = grist.NewPeerCall(Bank, bank.Pay)

// RequestPayout requests a payout: refused if its id was requested
// before (duplicate), or its amount is not positive (invalid).
var RequestPayout = grist.NewCommand(App, "request", func(r *grist.Reads, c Request) ([]grist.Event, error) {
	switch {
	case c.Amount <= 0:
		return nil, ErrInvalid
	case Requests.For(r, c.Payout) > 0:
		return nil, ErrDuplicate
	}
	return []grist.Event{Requested.Of(c)}, nil
}, grist.Refuses(ErrDuplicate, ErrInvalid), grist.Appends(Requested), grist.Authorize(grist.Authenticated))

// Send pays each requested payout through the bank, at least once: the
// checkpoint commits after the bank answered, so a crash between them, or
// an answer lost, pays again under the same key, which the bank's marker
// answers. Its success appends the payout's sent.
var Send = grist.NewProcessor(App, "send", Requested,
	func(ctx context.Context, r *grist.Reads, e Request, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := BankPay.Do(ctx, bank.Payment{Account: e.Account, Amount: e.Amount, Payout: e.Payout})
			return err
		}, grist.AtLeastOnce, grist.OnSuccess(PaidOut.Of(Sent{Payout: e.Payout, Of: int64(m.Pos)})))
	}, grist.Delivers(grist.AtLeastOnce, PaidOut), grist.Uses(BankPay))
