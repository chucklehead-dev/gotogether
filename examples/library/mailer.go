package library

import "github.com/chucklehead-dev/gotogether/grist"

// Mailer is the mailer a borrow's receipt is sent through: a declared
// dependency, whose calls grist records at both ends (the caller's step
// mailerSend, the mailer's own mailerSendServed) and gristsim serves as a
// qenv.Service named mailer, its faults rpc.mailer.*. No test gives it a
// double: gristsim serves its default (grist.DefaultHandlers), which
// sends each key once.
var Mailer = grist.NewDependency(App, "mailer")

// Receipt is a borrow's receipt as the mailer takes it: whom, which book.
// The borrow it is for is the call's trigger, and the key the mailer
// deduplicates by the call's (grist.CallKey): grist carries both.
type Receipt struct {
	Member string `json:"member"`
	Book   string `json:"book"`
}

// Sent is the mailer's answer: the receipt was sent.
type Sent struct{}

// Send mails a receipt. Keyed: the mailer sends each key once (the
// receipts processor's effect key, sent with every try), which the
// generated model's mailerSendAdmits says.
var Send = grist.NewCall[Receipt, Sent](Mailer, "send", grist.Keyed())
