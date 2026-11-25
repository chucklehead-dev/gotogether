// Package guarded uses sync in the ways Orchestrion's matching misses:
// a lock embedded in a struct (promoted calls), a method value returned as
// the unlock function, and a goroutine started with go.
package guarded

import (
	"context"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Counter embeds its lock: c.Lock() is a promoted call.
type Counter struct {
	sync.Mutex
	N int
}

// Store hands out its read lock's unlock as a method value.
type Store struct {
	mu   sync.RWMutex
	Rows map[string]int
}

// RLock takes the read lock and returns the method value that releases it.
func (s *Store) RLock() func() {
	s.mu.RLock()
	return s.mu.RUnlock
}

// Put writes a row under the write lock.
func (s *Store) Put(ctx context.Context, k string, v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	qenv.Sleep(ctx, time.Millisecond) // the simulation runs others meanwhile
	s.Rows[k] = v
}

// Inc increments under the embedded lock, slowly.
func (c *Counter) Inc(ctx context.Context) {
	c.Lock()
	n := c.N
	qenv.Sleep(ctx, time.Millisecond)
	c.N = n + 1
	c.Unlock()
}

// Spawn runs fn in a goroutine of its own.
func Spawn(wg *sync.WaitGroup, fn func()) {
	wg.Add(1)
	//qenv:go
	go func() {
		defer wg.Done()
		fn()
	}()
}

// RunGroup starts and joins a callback through a pointer WaitGroup receiver.
func RunGroup(wg *sync.WaitGroup, fn func()) {
	wg.Go(fn)
	wg.Wait()
}

// RunLocalGroup starts and joins a callback through a value WaitGroup receiver.
func RunLocalGroup(fn func()) {
	var wg sync.WaitGroup
	wg.Go(fn)
	wg.Wait()
}

// Ledger keeps a balance and its entries under a lock whose Lock hands
// back the release as a method value (qweave weaves it into a call of the
// simulated Unlock; Orchestrion leaves it the real one). Its lock
// invariant, that the entries sum to the balance, is checked there.
type Ledger struct {
	mu      sync.RWMutex
	Balance int
	Entries []int
}

// Lock takes the write lock and returns the method value that releases it.
func (l *Ledger) Lock() func() {
	l.mu.Lock()
	return l.mu.Unlock
}

// Guard attaches the ledger's invariant to its lock in ctx's simulation.
func (l *Ledger) Guard(ctx context.Context) {
	qenv.Guard(ctx, &l.mu, "entriesSumToBalance", func() bool {
		sum := 0
		for _, n := range l.Entries {
			sum += n
		}
		return sum == l.Balance
	})
}

// Deposit adds n to the balance, sleeps inside the critical section, then
// writes its entry, unless forget leaves it out.
func (l *Ledger) Deposit(ctx context.Context, n int, forget bool) {
	unlock := l.Lock()
	l.Balance += n
	qenv.Sleep(ctx, time.Millisecond)
	if !forget {
		l.Entries = append(l.Entries, n)
	}
	unlock()
}
