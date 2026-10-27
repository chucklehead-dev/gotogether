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
	return BalanceView{Balance: Initial + Balances.For(r, q.Account)}, nil
}, grist.Authorize(grist.Authenticated))
