// Package cross declares a guarded field another package unlocks.
package cross

import "sync"

// Box exports its lock, not its invariant.
type Box struct {
	Mu sync.Mutex //quint:guards full
	N  int
}

func (b *Box) full() bool { return b.N <= 10 }
