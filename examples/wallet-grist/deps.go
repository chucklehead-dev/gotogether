package walletgrist

import "github.com/chucklehead-dev/gotogether/grist"

// Ledger is the ledger every move is posted to: a declared dependency,
// whose calls grist records at both ends (the caller's step ledgerPost,
// the ledger's own ledgerPostServed) and gristsim serves as a
// qenv.Service named ledger, its faults rpc.ledger.*. No test gives it a
// double: gristsim serves its default (grist.DefaultHandlers), which
// posts each key once; the real tier and the binary attach that default
// in process (grist.Local), recording its posts with the node's steps.
var Ledger = grist.NewDependency(App, "ledger")

// Entry is a post as the ledger takes it: the move's account and amount.
// The move it is for is the call's trigger, and the key the ledger
// deduplicates by the call's (grist.CallKey): grist carries both.
type Entry struct {
	Account string `json:"account"`
	Amount  int    `json:"amount"`
}

// Booked is the ledger's answer: the entry is on its book.
type Booked struct{}

// PostEntry posts a move. Keyed: the ledger posts each key once (the post
// processor's effect key, sent with every try), which the generated
// model's ledgerPostAdmits says.
var PostEntry = grist.NewCall[Entry, Booked](Ledger, "post", grist.Keyed())
