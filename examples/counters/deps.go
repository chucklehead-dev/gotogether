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

// A Bank pays amounts out, deduplicating by key where it can. An error
// leaves the payment unknown (it may have been made) unless it is
// grist.NotApplied (the bank refused it).
type Bank interface {
	Pay(ctx context.Context, tenant, key, counter string, amount int) error
}

// The application's dependencies, reached through a processor's context
// (grist.Dep): one set per run, attached by gristsim's Config.Setup, Main's
// Dependencies or the real tier's harness, never a package-level value
// that runs in parallel would share. Where none is attached, a mailer
// that refuses every mail and a bank that refuses every payment.
var (
	MailerDep = grist.NewDep[Mailer]("mailer", noMailer{})
	BankDep   = grist.NewDep[Bank]("bank", noBank{})
	// StoreDep is the application's store, for a raw append, which only
	// the dispatchDirect mutant makes (grain's example processor).
	StoreDep = grist.NewDep[grist.Store]("store", nil)
)

type noBank struct{}

func (noBank) Pay(context.Context, string, string, string, int) error {
	return grist.NotApplied(errors.New("counters: no bank: attach one (BankDep.With)"))
}

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

// A Payment is a payment as the bank makes it: its record at the bank's
// site (BankPays), the tenant its actor.
type Payment struct {
	Key     string `json:"key"`
	Counter string `json:"counter"`
	Amount  int    `json:"amount"`
}

// BankPays is the bank's own record of each payment it makes, where it
// makes it (the step bankPays: actor the tenant, the key, the counter and
// the amount): an effect's site records the effect, so a payment made
// twice, or made while its answer was lost, is in the trace whatever the
// caller saw.
var BankPays = grist.NewSite[Payment](App, "bank-pays")

// payRequest is a payment request as the simulated bank's service takes it.
type payRequest struct {
	Tenant string
	Payment
}

// SimBank is a bank in a simulation: a qenv.Service of its own process
// ("bank"), whose requests and responses may be lost or late. It records
// each payment it makes at its site (BankPays).
type SimBank struct {
	svc *qenv.Service[payRequest, struct{}]
	// Paid are the payments it was asked to make, duplicates included.
	Paid []Payment
}

// NewSimBank registers the bank service in s.
func NewSimBank(s *qenv.Sim) *SimBank {
	b := &SimBank{}
	b.svc = qenv.NewService(s, "bank", nil, func(ctx context.Context, p payRequest) (struct{}, error) {
		b.Paid = append(b.Paid, p.Payment)
		BankPays.Record(ctx, p.Tenant, p.Payment)
		return struct{}{}, nil
	})
	return b
}

// Pay implements Bank: an error if no answer came within a second (the
// payment's outcome unknown: the request or its answer was lost).
func (b *SimBank) Pay(ctx context.Context, tenant, key, counter string, amount int) error {
	_, err := b.svc.Call(ctx, payRequest{Tenant: tenant, Payment: Payment{Key: key, Counter: counter, Amount: amount}}, time.Second)
	return err
}
