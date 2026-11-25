// Package nomethod names a method its struct lacks.
package nomethod

import "sync"

// Box has no full.
type Box struct {
	mu sync.Mutex //quint:guards full
	N  int
}
