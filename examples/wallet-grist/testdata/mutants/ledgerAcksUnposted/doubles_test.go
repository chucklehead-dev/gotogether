package walletgrist

import (
	"context"
	"encoding/json"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// doubles are the runs' doubles of the App's dependencies: none, as the
// app's own.
func doubles() gristsim.Doubles { return nil }

// front is the mutant ledgerAcksUnposted: a proxy in front of the ledger
// (a write-behind client that acknowledges at once and posts later, its
// queue lost) answering every post booked and forwarding none. The
// caller's step says booked; the ledger never posted, so no served record
// of the key exists.
func front(*qenv.Sim, grist.Store) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context { return Ledger.With(ctx, acks{}) }
}

type acks struct{}

func (acks) Call(context.Context, grist.CallInfo, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
