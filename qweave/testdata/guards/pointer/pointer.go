// Package pointer guards a pointer to a lock.
package pointer

import "sync"

// Box points at its lock.
type Box struct {
	mu *sync.Mutex //quint:guards full
	N  int
}

func (b *Box) full() bool { return b.N <= 10 }
