// Package embedded guards an embedded lock.
package embedded

import "sync"

// Box embeds its lock.
type Box struct {
	sync.Mutex //quint:guards full
	N          int
}

func (b *Box) full() bool { return b.N <= 10 }
