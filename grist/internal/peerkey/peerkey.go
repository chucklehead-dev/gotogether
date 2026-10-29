// Package peerkey is the command id a peer's call is sent under
// (client.Peer): the call's key, so every try of an at-least-once effect
// is one command at the peer. Under the generated mutant freshKey
// (gristtest.PeerMutants) it is a new id per try instead, the key
// stamped with the try's time, as a client minting a request id per
// request does: set by gristtest alone, which only test binaries import,
// so no production binary sends one.
package peerkey

import (
	"context"
	"fmt"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Mutant is the generated mutant's name (qtest.Mutant).
const Mutant = "freshKey"

// Fresh says the ids are fresh per try (Mutant): process-wide, set once
// by gristtest's init before any call, read-only after.
var Fresh bool

// ID is the command id of a peer's call under key, made at ctx's time.
func ID(ctx context.Context, key string) string {
	if !Fresh {
		return key
	}
	// the time is the run's (virtual in a simulation): a run is still a
	// function of its choices, and a retry later than its try another id
	return fmt.Sprintf("%s/%d", key, qenv.Now(ctx).UnixNano())
}
