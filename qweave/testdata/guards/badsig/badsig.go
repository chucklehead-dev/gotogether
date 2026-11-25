// Package badsig names a method of the wrong type.
package badsig

import "sync"

// Box's full takes an argument.
type Box struct {
	mu sync.Mutex //quint:guards full
	N  int
}

func (b *Box) full(n int) bool { return b.N <= n }
