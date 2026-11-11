// Package peerkey is the command id a peer's call is sent under
// (client.Peer): the call's key, so every try of an at-least-once effect
// is one command at the peer. Under the generated mutant freshKey
// (gristtest.PeerMutants) it is a new id per try instead, the key with
// an identifier minted for the try, as a client minting a request id per
// request does: set by gristtest alone, which only test binaries import,
// so no production binary sends one.
package peerkey

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Mutant is the generated mutant's name (qtest.Mutant).
const Mutant = "freshKey"

// Fresh says the ids are fresh per try (Mutant): process-wide, set once
// by gristtest's init before any call, read-only after.
var Fresh bool

// ID is the command id of a peer's call under key, one try's.
func ID(ctx context.Context, key string) string {
	if !Fresh {
		return key
	}
	// qenv.NewID, the simulation's counter in a run (a run is still a
	// function of its choices) and random outside one, as the key's own
	// event id is: a validation reads both up to renaming, where the
	// try's time it was once stamped with made each real run's plans,
	// and so the share of them quint judges, its own
	return key + "/" + qenv.NewID(ctx)
}
