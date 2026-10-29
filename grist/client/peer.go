package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/peerkey"
)

// Peer is c as the transport of a peer's calls (grist.NewPeer,
// grist.NewPeerCall): each call one command of c.App, sent once (Command:
// a transport never retries; an effect's tries are the effect's, under
// the same key) with the call's key as its command id (its
// Idempotency-Key), in the caller's tenant, as the caller
// grist.PeerCaller(<the caller's App>) (sent in c.CallerHeader when set;
// otherwise authenticate in c.HTTP's Transport). The answer: nil for a
// receipt, the command's rejection (a *grist.Rejected, which the call
// declares when the command does), or an error of unknown outcome
// (a conflict, a denial, a key reused, a 5xx or no answer: the call may
// or may not have committed, which the peer's own steps say). It records
// nothing: the caller's step is grist's (Call.Do), the peer's its commit.
//
//	ctx = Bank.With(ctx, client.Peer(&client.Client{App: bank.App, URL: bankURL}))
func Peer(c *Client) grist.Transport { return peer{c} }

type peer struct{ c *Client }

func (p peer) Call(ctx context.Context, info grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	if p.c.App == nil {
		return nil, errors.New("grist/client: a peer's Client without its App: set Client.App to the peer's")
	}
	env := grist.Envelope{ID: peerkey.ID(ctx, info.Key), Tenant: info.Tenant, Caller: grist.PeerCaller(info.App)}
	_, err := p.c.Command(ctx, env, call, req)
	var rej *grist.Rejected
	switch {
	case err == nil:
		return json.RawMessage("{}"), nil
	case errors.As(err, &rej):
		return nil, rej // the call's refusal if it declares it, else unknown (Call.Do)
	}
	return nil, fmt.Errorf("grist/client: peer %s's command %s under %s: %w", p.c.App.Name, call, env.ID, err)
}
