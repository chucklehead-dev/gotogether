package counters

import (
	"context"
	"errors"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
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

// Deps are the application's dependencies, reached through a processor's
// context: one set per simulation, never a package-level value that runs
// in parallel would share.
type Deps struct {
	Mailer Mailer
	Bank   Bank
	// Store is the application's store, for a raw append, which only the
	// dispatchDirect mutant makes (grain's example processor).
	Store grist.Store
}

type depsKey struct{}

// WithDeps attaches d to ctx.
func WithDeps(ctx context.Context, d Deps) context.Context {
	return context.WithValue(ctx, depsKey{}, d)
}

// DepsOf are ctx's dependencies; a Mailer that refuses every mail and a
// Bank that refuses every payment if none were attached.
func DepsOf(ctx context.Context) Deps {
	d, ok := ctx.Value(depsKey{}).(Deps)
	if !ok || d.Mailer == nil {
		d.Mailer = noMailer{}
	}
	if d.Bank == nil {
		d.Bank = noBank{}
	}
	return d
}

type noBank struct{}

func (noBank) Pay(context.Context, string, string, string, int) error {
	return grist.NotApplied(errors.New("counters: no bank: attach one with WithDeps"))
}

type noMailer struct{}

func (noMailer) Send(context.Context, string, string) error {
	return errors.New("counters: no mailer: attach one with WithDeps")
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

// A Payment is a payment as the simulated bank takes it.
type Payment struct {
	Tenant, Key, Counter string
	Amount               int
}

// SimBank is a bank in a simulation: a qenv.Service of its own process
// ("bank"), whose requests and responses may be lost or late. It
// records each payment it makes (the step bankPays, actor the tenant,
// with the key, the counter and the amount) where it makes it: an
// effect's site records the effect, so a payment made twice, or made
// while its answer was lost, is in the trace whatever the caller saw.
type SimBank struct {
	svc *qenv.Service[Payment, struct{}]
	// Paid are the payments it was asked to make, duplicates included.
	Paid []Payment
}

// NewSimBank registers the bank service in s.
func NewSimBank(s *qenv.Sim) *SimBank {
	b := &SimBank{}
	b.svc = qenv.NewService(s, "bank", nil, func(ctx context.Context, p Payment) (struct{}, error) {
		b.Paid = append(b.Paid, p)
		qobs.Record(ctx, "bankPays", nil, "actor", p.Tenant, "process", App.Name, "key", p.Key, "counter", p.Counter, "amount", p.Amount)
		return struct{}{}, nil
	})
	return b
}

// Pay implements Bank: an error if no answer came within a second (the
// payment's outcome unknown: the request or its answer was lost).
func (b *SimBank) Pay(ctx context.Context, tenant, key, counter string, amount int) error {
	_, err := b.svc.Call(ctx, Payment{Tenant: tenant, Key: key, Counter: counter, Amount: amount}, time.Second)
	return err
}
