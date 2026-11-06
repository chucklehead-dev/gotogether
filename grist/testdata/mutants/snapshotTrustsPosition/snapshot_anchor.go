package grist

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// snapshotSum is a snapshot's checksum: a hash of every field but Sum,
// and of anchor, the identity (eventID) of the event at its position,
// the last it folded: a torn or altered snapshot is never read, nor one
// whose last event a rewound log no longer holds (a put of a fold begun
// before a restore and committed after it).
func snapshotSum(s Snapshot, anchor string) string {
	h := sha256.New()
	// mutant: leave the last event out of the snapshot checksum
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00", s.Name, s.Key, s.Fingerprint, s.Pos)
	h.Write(s.State)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// memoAnchorMatches reports whether the event at a cached position is still
// the one folded into the memo. Positions can be reused after a rewind.
func memoAnchorMatches(event Stored, position Position, id string) bool {
	return event.Pos == position && eventID(event) == id
}
