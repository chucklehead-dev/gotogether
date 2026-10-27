package walletgrist

import "github.com/chucklehead-dev/gotogether/grist"

// Read asks for an account's balance.
type Read struct {
	Account string `json:"account"`
}

// BalanceView is an account's balance as read: 0 for an account the
// bank does not hold.
type BalanceView struct {
	Balance int `json:"balance"`
}

// BalanceQuery is the read query: the account's balance through the
// position it read, folded from the log (its moves).
var BalanceQuery = grist.NewQuery(App, "read", func(r *grist.Reads, q Read) (BalanceView, error) {
	if !held(q.Account) {
		return BalanceView{}, nil
	}
	return BalanceView{Balance: Initial + replica.For(r, q.Account)}, nil
}, grist.Authorize(grist.Authenticated))

// replica is the mutant replicaRead's: balances folded from the ledger's
// postings, which lag the moves (examples/wallet's ledger copy).
var replica = grist.NewKeyedReadModel(App, "replica", 1, grist.Keys("account"), func() int { return 0 },
	grist.On(Posted, func(s int, e Posting, m grist.Meta) int { return s + e.Amount }))
