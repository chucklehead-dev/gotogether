// Package guarded declares a lock invariant on a field.
package guarded

import "sync"

// Box is full at most.
type Box struct {
	mu sync.Mutex //quint:guards full
	N  int
}

func (b *Box) full() bool { return b.N <= 10 }

// Add adds one.
func (b *Box) Add() {
	b.mu.Lock()
	b.N++
	b.mu.Unlock()
}
