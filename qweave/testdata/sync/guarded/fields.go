package guarded

import (
	"context"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Purse keeps a balance and its entries under a lock whose invariant, that
// the entries sum to the balance, its field declares: qweave weaves it into
// every Unlock of mu, so no test registers it and a zero Purse is checked
// as one any constructor makes.
type Purse struct {
	mu      sync.Mutex //quint:guards ledgered
	Balance int
	Entries []int
	// Mutant is "unledgered" (a deposit of 3 writes no entry: false at its
	// Unlock), "restored" (an entry too large, corrected before the
	// Unlock: false inside, true at the release) or "" (faithful).
	Mutant string
	// Probe, if set, runs inside the invariant: a seam there is refused.
	Probe func()
}

func (p *Purse) ledgered() bool {
	if p.Probe != nil {
		p.Probe()
	}
	sum := 0
	for _, n := range p.Entries {
		sum += n
	}
	return sum == p.Balance
}

// Deposit adds n, sleeping inside the critical section, and records the
// step the account model checks (which sees the balance, not the entries).
func (p *Purse) Deposit(ctx context.Context, n int) {
	p.mu.Lock()
	p.Balance += n
	qenv.Sleep(ctx, time.Millisecond)
	switch p.Mutant {
	case "unledgered":
		if n != 3 {
			p.Entries = append(p.Entries, n)
		}
	case "restored":
		p.Entries = append(p.Entries, n+1)
		qenv.Yield(ctx)
		p.Entries[len(p.Entries)-1]--
	default:
		p.Entries = append(p.Entries, n)
	}
	qobs.Record(ctx, "deposit", nil, "amount", n, "obs.balance", p.Balance)
	p.mu.Unlock() // the release the invariant is checked at
}

// PlainPurse is Purse without the directive: the same code, unchecked.
type PlainPurse struct {
	mu      sync.Mutex
	Balance int
	Entries []int
	Mutant  string
}

// Deposit is Purse's.
func (p *PlainPurse) Deposit(ctx context.Context, n int) {
	p.mu.Lock()
	p.Balance += n
	qenv.Sleep(ctx, time.Millisecond)
	switch p.Mutant {
	case "unledgered":
		if n != 3 {
			p.Entries = append(p.Entries, n)
		}
	case "restored":
		p.Entries = append(p.Entries, n+1)
		qenv.Yield(ctx)
		p.Entries[len(p.Entries)-1]--
	default:
		p.Entries = append(p.Entries, n)
	}
	qobs.Record(ctx, "deposit", nil, "amount", n, "obs.balance", p.Balance)
	p.mu.Unlock()
}

// DepositOnce deposits n into a Purse of its own, a local value (its
// Unlock binds the variable's address), deferring the Unlock; forget
// leaves the entry out.
func DepositOnce(ctx context.Context, n int, forget bool) int {
	var p Purse
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Balance += n
	qenv.Yield(ctx)
	if !forget {
		p.Entries = append(p.Entries, n)
	}
	return p.Balance
}

// Tally counts events under a read-write lock, its invariant on the
// field's doc, and hands back the release as a method value.
type Tally struct {
	// mu guards N and Seen.
	//quint:guards counted
	mu   sync.RWMutex
	N    int
	Seen []int
}

func (t *Tally) counted() bool { return len(t.Seen) == t.N }

// lock takes the write lock and returns the method value that releases it.
func (t *Tally) lock() func() {
	t.mu.Lock()
	return t.mu.Unlock
}

// Add counts an event, sleeping inside the critical section; forget
// leaves it unseen.
func (t *Tally) Add(ctx context.Context, forget bool) {
	unlock := t.lock()
	t.N++
	qenv.Sleep(ctx, time.Millisecond)
	if !forget {
		t.Seen = append(t.Seen, t.N)
	}
	unlock()
}

// Count reads N under the read lock: RUnlock is not checked (a reader
// publishes nothing).
func (t *Tally) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.N
}
