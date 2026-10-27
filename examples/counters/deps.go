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

// Deps are the application's dependencies, reached through a processor's
// context: one set per simulation, never a package-level value that runs
// in parallel would share.
type Deps struct {
	Mailer Mailer
	// Store is the application's store, for a raw append, which only the
	// dispatchDirect mutant makes (grain's example processor).
	Store grist.Store
}

type depsKey struct{}

// WithDeps attaches d to ctx.
func WithDeps(ctx context.Context, d Deps) context.Context {
	return context.WithValue(ctx, depsKey{}, d)
}

// DepsOf are ctx's dependencies; a Mailer that refuses every mail if
// none were attached.
func DepsOf(ctx context.Context) Deps {
	d, ok := ctx.Value(depsKey{}).(Deps)
	if !ok || d.Mailer == nil {
		d.Mailer = noMailer{}
	}
	return d
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
