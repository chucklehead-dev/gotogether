package guarded_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/syncfixture/guarded"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Run woven (qweave go test): the embedded lock, the method value and the
// goroutine are the simulation's, so tasks interleave inside the critical
// sections and none is lost. Unwoven, the second task to take a real lock
// blocks the scheduler and the run hangs.
func TestWovenEscapes(t *testing.T) {
	for seed := uint64(0); seed < 20; seed++ {
		s := qenv.New(qenv.NewSeeded(seed))
		var c guarded.Counter
		st := &guarded.Store{Rows: map[string]int{}}
		var wg sync.WaitGroup
		read := 0
		for i := range 4 {
			s.Spawn("worker", func(ctx context.Context) {
				c.Inc(ctx)
				st.Put(ctx, "k", i)
				unlock := st.RLock()
				read += st.Rows["k"] * 0
				unlock()
				// its own task: Background resolves to it (not the worker's ctx)
				guarded.Spawn(&wg, func() { c.Inc(context.Background()) })
			})
		}
		s.Spawn("waiter", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			wg.Wait()
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		if c.N != 8 {
			t.Fatalf("seed %d: counter %d, want 8 (an increment was lost: the embedded lock did not exclude)", seed, c.N)
		}
		s.Close()
	}
}

// Both WaitGroup.Go receiver forms must create simulation-owned tasks. An
// unwoven real goroutine can finish a callback too, so check its ownership and
// advance virtual time instead of accepting completion alone.
func TestWovenWaitGroupGo(t *testing.T) {
	for seed := range uint64(20) {
		s := qenv.New(qenv.NewSeeded(seed))
		var wg sync.WaitGroup
		completed := 0
		escaped := false
		var elapsed time.Duration
		s.Spawn("parent", func(ctx context.Context) {
			start := qenv.Now(ctx)
			callback := func() {
				child := context.Background()
				if qenv.From(child) != s {
					escaped = true
					return
				}
				qenv.Sleep(child, time.Hour)
				completed++
			}
			guarded.RunGroup(&wg, callback)
			guarded.RunLocalGroup(callback)
			elapsed = qenv.Now(ctx).Sub(start)
		})
		err := s.Run(3 * time.Hour)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if escaped || completed != 2 || elapsed != 2*time.Hour {
			t.Fatalf("seed %d: escaped=%v completed=%d virtual elapsed=%v; want two simulation-owned callbacks totaling 2h", seed, escaped, completed, elapsed)
		}
	}
}

// Lock invariants are checked at woven unlocks: a method value's
// (Ledger's write lock) and a promoted call's (Counter's embedded Mutex).
// The faithful ledger holds its invariant at every release; a deposit that
// forgets its entry fails at the release, in its task.
func TestWovenLockInvariants(t *testing.T) {
	run := func(s *qenv.Sim) (e *qenv.LockInvariantError) {
		defer s.Close()
		defer func() {
			if p := recover(); p != nil {
				e = p.(*qenv.LockInvariantError)
			}
		}()
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	for seed := range uint64(10) {
		for _, forget := range []bool{false, true} {
			s := qenv.New(qenv.NewSeeded(seed))
			l := &guarded.Ledger{}
			l.Guard(s.Context())
			for i := range 3 {
				s.Spawn(fmt.Sprint("depositor-", i), func(ctx context.Context) { l.Deposit(ctx, i+1, forget && i == 1) })
			}
			e := run(s)
			switch {
			case !forget && e != nil:
				t.Fatalf("seed %d, faithful: %v", seed, e)
			case forget && (e == nil || e.Name != "entriesSumToBalance" || e.Task != "depositor-1" || !strings.HasPrefix(e.Unlock, "guarded.go:")):
				t.Fatalf("seed %d, forgetting: %v", seed, e)
			}
		}
	}

	s := qenv.New(qenv.Nominal{})
	var c guarded.Counter
	qenv.Guard(s.Context(), &c.Mutex, "atMostTwo", func() bool { return c.N <= 2 })
	for i := range 4 {
		s.Spawn(fmt.Sprint("inc-", i), c.Inc)
	}
	if e := run(s); e == nil || e.Name != "atMostTwo" || e.Task != "inc-2" || !strings.HasPrefix(e.Unlock, "guarded.go:") {
		t.Fatalf("the promoted Unlock: %v", e)
	}
}
