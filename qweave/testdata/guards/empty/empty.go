// Package empty names no invariant.
package empty

import "sync"

// Box's directive is empty.
type Box struct {
	mu sync.Mutex //quint:guards
	N  int
}
