// Package weavefix has quintweave's findings with their fixes, and two
// without: a go statement of a call that is no function literal (its
// arguments would be evaluated later), a method value that takes an
// argument.
package weavefix

import "sync"

type st struct {
	mu sync.RWMutex
	wg sync.WaitGroup
}

func work(n int) {}

func (s *st) run() func() {
	go func() {}() // want `go statement not marked //qenv:go`
	if s != nil {
		go func() { work(1) }() // want `go statement not marked //qenv:go`
	}
	go work(1)      // want `go statement not marked //qenv:go`
	add := s.wg.Add // want `method value Add is not woven`
	add(1)
	done := s.wg.Done // want `method value Done is not woven`
	done()
	return s.mu.RUnlock // want `method value RUnlock is not woven`
}
