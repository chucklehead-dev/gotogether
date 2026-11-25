// Package keyed gives the directive a key:value.
package keyed

import "sync"

// Box names its invariant as a key.
type Box struct {
	mu sync.Mutex //quint:guards name:full
	N  int
}

func (b *Box) full() bool { return b.N <= 10 }
