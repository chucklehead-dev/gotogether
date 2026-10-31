package qenv

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
)

// NewID returns a fresh opaque identifier as 32 lowercase hexadecimal digits.
// Outside a simulation it draws 128 random bits. Inside one it allocates from
// the simulation's counter: repeatable for the same scheduled execution, unique
// across its processes and doubles, and unaffected by crashes or store restores.
// IDs from separate simulations can repeat. Do not depend on their value or
// ordering, or use simulated IDs as unpredictable secrets.
//
// NewID makes no scheduling or fault choice. It also uses the simulation's
// counter while its creator prepares initial data before Run. An explicit task
// context takes precedence; without one, the calling goroutine's innermost
// simulation supplies the counter, whether it is the creator or a running task.
func NewID(ctx context.Context) string {
	var b [16]byte
	s := From(ctx)
	if s == nil && live.Load() != 0 {
		if owner, ok := ownerOf(goid()); ok {
			s = owner.s
		}
	}
	if s != nil {
		for {
			previous := s.valueIDs.Load()
			if previous == ^uint64(0) {
				panic("qenv: the simulation exhausted its identifier space")
			}
			if s.valueIDs.CompareAndSwap(previous, previous+1) {
				binary.BigEndian.PutUint64(b[8:], previous+1)
				break
			}
		}
	} else {
		rand.Read(b[:]) //qenv:ok only the production path; simulated tasks and setup use the counter above
	}
	return hex.EncodeToString(b[:])
}
