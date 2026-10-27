package library

import (
	"context"
	"errors"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Receipt is a borrow's receipt as the mailer sends it: whom, which book, the
// borrow's position (At), and the key the mailer deduplicates by.
type Receipt struct {
	Member string `json:"member"`
	Book   string `json:"book"`
	At     int64  `json:"at"`
	Key    string `json:"key"`
}

// Sends is the mailer's own record of each receipt it sends, made where it
// sends it: what was mailed, whatever the processor saw.
var Sends = grist.NewSite[Receipt](App, "mailer-sends")

// A Sender mails receipts.
type Sender interface {
	Send(ctx context.Context, tenant string, r Receipt) error
}

// MailerDep is the application's mailer, attached where the node starts
// (gristsim's Setup, the tier's Deps).
var MailerDep = grist.NewDep[Sender]("mailer", noMailer{})

// noMailer is the stand-in where no mailer was attached.
type noMailer struct{}

func (noMailer) Send(context.Context, string, Receipt) error {
	return errors.New("no mailer attached: attach one with library.MailerDep.With (gristsim Config.Setup, gristtest TierConfig.Deps)")
}

// Mailer is the mailer double: it sends each key's receipt once (a second
// send of a key is answered as sent) and records each send at its site.
type Mailer struct {
	mu   sync.Mutex
	sent map[string]bool
}

// NewMailer is a mailer that has sent nothing.
func NewMailer() *Mailer { return &Mailer{sent: map[string]bool{}} }

// Send sends r unless its key was sent.
func (m *Mailer) Send(ctx context.Context, tenant string, r Receipt) error {
	// the mutant noDedupe: every send mails, the key ignored
	Sends.Record(ctx, tenant, r)
	return nil
}
