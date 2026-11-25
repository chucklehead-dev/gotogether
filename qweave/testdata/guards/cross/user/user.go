// Package user unlocks cross.Box's lock.
package user

import "example.com/guards/cross"

// Add adds one.
func Add(b *cross.Box) {
	b.Mu.Lock()
	b.N++
	b.Mu.Unlock()
}
