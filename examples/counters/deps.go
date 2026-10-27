package counters

import (
	"context"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist"
)

// StoreDep is the application's store, for a raw append, which only the
// dispatchDirect mutant makes (grain's example processor): an undeclared
// dependency, reached through a processor's context (grist.Dep), attached
// by gristsim's Config.Setup or the real tier's harness.
var StoreDep = grist.NewDep[grist.Store]("store", nil)

// Mailer is the mailer a new counter's welcome is sent through: a
// declared dependency, whose calls grist records at both ends (the
// caller's step mailerSend, the mailer's own mailerSendServed) and
// gristsim serves as a qenv.Service named mailer, its faults
// rpc.mailer.*. No simulation gives it a double: gristsim serves its
// default (grist.DefaultHandlers), which sends every mail it is asked to.
var Mailer = grist.NewDependency(App, "mailer")

// Mail is a welcome: the counter, and its name.
type Mail struct {
	Counter string `json:"counter"`
	Name    string `json:"name"`
}

// Mailed is the mailer's answer: the mail was sent.
type Mailed struct{}

// Send sends a mail. Not Keyed: the mailer sends every mail it gets, a
// retried welcome again (the welcome is at least once, by right).
var Send = grist.NewCall[Mail, Mailed](Mailer, "send")

// Bank is the bank a retired counter's total is paid out through: a
// declared dependency, whose calls grist records at both ends (the
// caller's step bankPay, the bank's own bankPayServed) and gristsim
// serves as a qenv.Service named bank, its faults rpc.bank.*.
var Bank = grist.NewDependency(App, "bank")

// PayRequest is a payment: the counter's total, to the counter.
type PayRequest struct {
	Counter string `json:"counter"`
	Amount  int    `json:"amount"`
}

// PayReceipt is the bank's receipt of a payment.
type PayReceipt struct {
	Ref string `json:"ref"`
}

// ErrDeclined is the bank's refusal of a payment: it did not happen.
var ErrDeclined = grist.Rejection("declined")

// Pay is the bank's payment. Keyed: the bank applies each key once (the
// payout's effect key, grist.CallKey, sent with every try); Refuses: a
// declined payment did not happen.
var Pay = grist.NewCall[PayRequest, PayReceipt](Bank, "pay", grist.Keyed(), grist.Refuses(ErrDeclined))

// NewBank is the bank's implementation, the team's own: it pays each key
// once (Pay is Keyed), answers a retry of a key from its first answer, a
// decline included (grist.Replayed: recorded bankPayReplayed, not a
// second payment), and declines a payment over 100; its receipt's
// reference is the key. Every simulation serves it as the bank's double
// (gristsim.Config.Doubles), the real tier behind grist.ServeCalls on a
// server of its own (gristtest.TierConfig.Serve), and cmd/counters on a
// loopback port (grist.ServeDependency). Each NewBank has its own
// payments: one per run.
func NewBank() grist.Handlers {
	type answer struct {
		rc  PayReceipt
		err error
	}
	var mu sync.Mutex
	first := map[string]answer{}
	return grist.Handlers{
		Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {
			key := grist.CallKey(ctx)
			mu.Lock()
			defer mu.Unlock()
			if a, ok := first[key]; ok {
				grist.Replayed(ctx)
				return a.rc, a.err
			}
			a := answer{rc: PayReceipt{Ref: key}}
			if r.Amount > 100 {
				a = answer{err: ErrDeclined}
			}
			first[key] = a
			return a.rc, a.err
		}),
	}
}
