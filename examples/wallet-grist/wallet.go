// Package walletgrist is examples/wallet's bank on grist: accounts whose
// balances move by transfers and deposits, each request applied at most
// once by its id (grist's command id), a ledger that posts every move
// (a processor's at-least-once effect, deduplicated by the ledger by its
// effect key), and two accounts sharing a credit line. Its model is
// model/wallet.qnt; gristsim runs it against a simulated store, and the
// tests state which check catches which mutant (testdata/mutants).
//
// Where examples/wallet keeps a request record, an outbox and a relay of
// its own, grist has them by construction: the command id's marker is the
// request record (a retry answers as the first time, a refusal too), and
// the log is the outbox (the post processor's checkpoint commits after
// the ledger's answer, so no applied move goes unposted).
package walletgrist

import (
	"sort"

	"github.com/chucklehead-dev/gotogether/grist"
)

// The accounts the bank holds, each opening with Initial; Joint share one
// credit line: a transfer from either is allowed while their sum covers
// it. The model states the same (ACCOUNTS, JOINT, INITIAL).
var (
	Accounts = []string{"a", "b", "c"}
	Joint    = []string{"a", "b"}
)

// Initial is every account's opening balance, in cents.
const Initial = 5

// The events.
type (
	// Move is one account's balance moving by Amount (cents, negative for
	// a debit): a transfer appends two, a deposit one. Every account's
	// balance is Initial plus its moves.
	Move struct {
		Account string `json:"account" grist:"tag"`
		Amount  int    `json:"amount"`
	}
	// Posting is the ledger's confirmation of a move (Of: the move's
	// position): what the post processor appends once the ledger answered.
	Posting struct {
		Account string `json:"account" grist:"tag"`
		Amount  int    `json:"amount"`
		Of      int64  `json:"of"`
	}
)

// App is the application; its blocks are below and in commands.go,
// queries.go and processors.go.
var App = grist.New("wallet")

// The event types.
var (
	Moved  = grist.NewEvent[Move](App, "moved")
	Posted = grist.NewEvent[Posting](App, "posted")
)

// Balances is each account's moves summed, keyed by account: a decision
// about account x reads, and is conditioned on, x's moves alone, so
// transfers from different accounts commit side by side.
var Balances = grist.NewKeyedReadModel(App, "balances", 1, grist.Keys("account"), func() int { return 0 },
	grist.On(Moved, func(s int, e Move, m grist.Meta) int { return s + e.Amount }))

// held reports whether the bank holds account a.
func held(a string) bool {
	i := sort.SearchStrings(Accounts, a)
	return i < len(Accounts) && Accounts[i] == a
}

// joint reports whether a is in the credit line.
func joint(a string) bool {
	for _, j := range Joint {
		if j == a {
			return true
		}
	}
	return false
}

// funds is what a transfer from src may spend through r: its own
// balance, or its credit line's. Every account it reads is in the
// decision's read set, so the transfer commits only if none of them moved
// since.
func funds(r *grist.Reads, src string) int {
	if !joint(src) {
		return Initial + Balances.For(r, src)
	}
	sum := 0
	for _, a := range Joint {
		sum += Initial + Balances.For(r, a)
	}
	return sum
}
