// Package plain locks without a directive.
package plain

import "sync"

// Box is unguarded.
type Box struct {
	mu sync.Mutex
	N  int
}

// Add adds one.
func (b *Box) Add() {
	b.mu.Lock()
	b.N++
	b.mu.Unlock()
}
