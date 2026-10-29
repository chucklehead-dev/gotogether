// Package bank is examples/payouts' bank: a grist App of its own, whose
// one command pay another App calls as its peer (grist.NewPeer,
// grist.NewPeerCall). It holds accounts a and b, refuses a payment to any
// other (unknown-account), and is checked against its own model,
// model/bank.qnt, whose invariant onePaymentPerPayout says no payout is
// paid twice: what the caller's at-least-once effect relies on the
// bank's markers for (each try one command, under the effect's key).
package bank

import (
	"slices"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Accounts are the accounts the bank holds; the model states the same
// (ACCOUNTS in model/bank_rules.qnt).
var Accounts = []string{"a", "b"}

// Payment is a payout paid into an account (cents): the event pay
// appends, and pay's arguments.
type Payment struct {
	Account string `json:"account" grist:"tag"`
	Amount  int    `json:"amount"`
	Payout  string `json:"payout" grist:"tag"`
}

// ErrUnknownAccount refuses a payment to an account the bank does not
// hold.
var ErrUnknownAccount = grist.Rejection("unknown-account")

// App is the bank.
var App = grist.New("bank")

// Paid is the bank's one event.
var Paid = grist.NewEvent[Payment](App, "paid")

// Pay pays a payout into an account. It reads nothing (the accounts are
// fixed): a payment's identity is its command id, which its caller gives
// (a peer's call key), so a retry of one is answered from its marker.
// Only the payouts service may call it (its caller, grist.PeerCaller).
var Pay = grist.NewCommand(App, "pay", func(r *grist.Reads, c Payment) ([]grist.Event, error) {
	if !slices.Contains(Accounts, c.Account) {
		return nil, ErrUnknownAccount
	}
	return []grist.Event{Paid.Of(c)}, nil
}, grist.Unconditioned("a payment reads nothing: the accounts are fixed"), grist.Refuses(ErrUnknownAccount), grist.Appends(Paid),
	grist.Authorize(grist.Callers(grist.PeerCaller("payouts"))))
