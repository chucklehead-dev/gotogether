package walletgrist

import "github.com/chucklehead-dev/gotogether/grist"

// Transfer moves Amount from Src to Dest.
type Transfer struct {
	Src    string `json:"src"`
	Dest   string `json:"dest"`
	Amount int    `json:"amount"`
}

// The refusals: a request the bank must refuse (an account it does not
// hold, a transfer to its own source, an amount that is not positive),
// and a transfer its funds do not cover.
var (
	ErrInvalid           = grist.Rejection("invalid")
	ErrInsufficientFunds = grist.Rejection("insufficient-funds")
)

// decideTransfer refuses an invalid request before reading anything, and
// a transfer its funds (its account's, or its credit line's) do not
// cover; else debits Src and credits Dest in one batch.
func decideTransfer(r *grist.Reads, c Transfer) ([]grist.Event, error) {
	if !held(c.Src) || !held(c.Dest) || c.Src == c.Dest || c.Amount <= 0 {
		return nil, ErrInvalid
	}
	if funds(r, c.Src) < c.Amount {
		return nil, ErrInsufficientFunds
	}
	return []grist.Event{
		Moved.Of(Move{Account: c.Src, Amount: -c.Amount}),
		Moved.Of(Move{Account: c.Dest, Amount: c.Amount}),
	}, nil
}

// TransferCmd is the transfer command.
var TransferCmd = grist.NewCommand(App, "transfer", decideTransfer, grist.Authorize(grist.Authenticated))

// Deposit adds Amount to Account.
type Deposit struct {
	Account string `json:"account"`
	Amount  int    `json:"amount"`
}

// decideDeposit refuses an invalid deposit; it reads nothing, so it is
// conditioned on nothing (a deposit commutes with every other move).
func decideDeposit(r *grist.Reads, c Deposit) ([]grist.Event, error) {
	if !held(c.Account) || c.Amount <= 0 {
		return nil, ErrInvalid
	}
	return []grist.Event{Moved.Of(Move{Account: c.Account, Amount: c.Amount})}, nil
}

// DepositCmd is the deposit command.
var DepositCmd = grist.NewCommand(App, "deposit", decideDeposit, grist.Authorize(grist.Authenticated))
