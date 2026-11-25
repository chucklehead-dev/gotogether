// Package escape takes a guarded field's address.
package escape

import "sync"

// Box is full at most.
type Box struct {
	N  int
	mu sync.Mutex //quint:guards full
}

func (b *Box) full() bool { return b.N <= 10 }

// Locker hands out the lock: its Unlocks would go unchecked.
func (b *Box) Locker() sync.Locker { return &b.mu }
