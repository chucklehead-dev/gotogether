package counters

import (
	"context"
	"errors"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Mailer sends mail, deduplicating by key where it can.
type Mailer interface {
	Send(ctx context.Context, key, msg string) error
}

// The application's undeclared dependencies, reached through a
// processor's context (grist.Dep): one set per run, attached by
// gristsim's Config.Setup, Main's Dependencies or the real tier's
// harness, never a package-level value that runs in parallel would
// share. Where none is attached, a mailer that refuses every mail.
var (
	MailerDep = grist.NewDep[Mailer]("mailer", noMailer{})
	// StoreDep is the application's store, for a raw append, which only
	// the dispatchDirect mutant makes (grain's example processor).
	StoreDep = grist.NewDep[grist.Store]("store", nil)
)

type noMailer struct{}

func (noMailer) Send(context.Context, string, string) error {
	return errors.New("counters: no mailer: attach one (MailerDep.With)")
}

// Mail is a mail as the simulated mailer takes it.
type Mail struct{ Key, Msg string }

// SimMailer is a mailer in a simulation: a qenv.Service of its own
// process ("mailer"), whose requests and responses may be lost or late.
type SimMailer struct {
	svc *qenv.Service[Mail, struct{}]
	// Sent are the mails it was asked to send, duplicates included.
	Sent []Mail
}

// NewSimMailer registers the mailer service in s.
func NewSimMailer(s *qenv.Sim) *SimMailer {
	m := &SimMailer{}
	m.svc = qenv.NewService(s, "mailer", nil, func(ctx context.Context, mail Mail) (struct{}, error) {
		m.Sent = append(m.Sent, mail)
		return struct{}{}, nil
	})
	return m
}

// Send implements Mailer: an error if no answer came within a second.
func (m *SimMailer) Send(ctx context.Context, key, msg string) error {
	_, err := m.svc.Call(ctx, Mail{Key: key, Msg: msg}, time.Second)
	return err
}

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
