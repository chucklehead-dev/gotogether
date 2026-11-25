package qtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/woven"
)

// A wallet under a mutex, called as woven code calls it (woven.MutexLock
// and MutexUnlock are what qenv/weave and qweave make of w.mu.Lock() and
// w.mu.Unlock()): a deposit raises the balance, yields inside the
// critical section, then writes its ledger entry, and records the step
// the account model checks. Its lock invariant, that the ledger sums to
// the balance, is internal: the model sees only the balance, so no
// verdict notices a lost ledger entry.
type wallet struct {
	mu      sync.Mutex
	balance int
	ledger  []int
}

func (w *wallet) consistent() bool {
	sum := 0
	for _, n := range w.ledger {
		sum += n
	}
	return sum == w.balance
}

// walletSystem deposits twice from each of two tasks, the amounts chosen.
// The mutant "unledgered" writes no entry for a deposit of 3;
// "restored" writes one too large and corrects it before the Unlock.
func walletSystem(mutant string, guard bool) System {
	return System{Binding: "../validate/testdata/account.yaml", Mutant: mutant, Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		w := &wallet{}
		if guard {
			qenv.Guard(s.Context(), &w.mu, "ledgerMatchesBalance", w.consistent)
		}
		for i := range 2 {
			s.Spawn(fmt.Sprint("depositor-", i), func(ctx context.Context) {
				for range 2 {
					n := s.Choose("amount", 5) + 1
					woven.MutexLock(&w.mu)
					w.balance += n
					qenv.Yield(ctx)
					switch mutant {
					case "unledgered":
						if n != 3 {
							w.ledger = append(w.ledger, n)
						}
					case "restored":
						w.ledger = append(w.ledger, n+1)
						qenv.Yield(ctx)
						w.ledger[len(w.ledger)-1]--
					default:
						w.ledger = append(w.ledger, n)
					}
					qobs.Record(ctx, "deposit", nil, "amount", n, "obs.balance", w.balance)
					woven.MutexUnlock(&w.mu)
				}
			})
		}
		return s.Run(time.Second)
	}}
}

// The faithful wallet and the mutant that restores its invariant before
// the Unlock conform; the mutant that leaves it broken at the Unlock is
// caught there, by the lock invariant, in a run its seed and the corpus
// reproduce. Unguarded, the same mutant conforms: the model alone cannot
// see the ledger, so the guard is what catches it.
func TestLockInvariantExplore(t *testing.T) {
	RequireQuint(t)
	corpus := t.TempDir()
	caught := func(cfg ExploreConfig) string {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			walletSystem("unledgered", true).Explore(f, cfg)
		}()
		<-done
		return strings.Join(f.errs, "\n")
	}
	all := caught(ExploreConfig{Seeds: 8, Parallel: 4, Corpus: corpus})
	for _, want := range []string{
		"panicked: qenv: lock invariant ledgerMatchesBalance (guarded at lockinvariant_test.go:",
		") broken at Unlock (lockinvariant_test.go:",
		") by task depositor-",
		"the critical section left it false; fix the code between Lock and Unlock",
		"a lock invariant (qenv.Guard) false at the Unlock it names: fix the code it names below",
		"the task's stack",
		"reproduce: GOTOGETHER_RUN='TestLockInvariantExplore#1:seed=",
		"saved to the corpus",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("lacks %q:\n%s", want, all)
		}
	}
	if !strings.Contains(all, "(step ") || strings.Count(all, "panicked:") == 8 {
		t.Errorf("want some of 8 runs caught, at their step:\n%s", all)
	}

	// the saved runs replay to the same failure
	rerun(t)
	all = caught(ExploreConfig{Seeds: 1, Parallel: 1, Corpus: corpus})
	if !strings.Contains(all, "corpus entry ") || !strings.Contains(all, "panicked: qenv: lock invariant ledgerMatchesBalance") {
		t.Errorf("the corpus's replay:\n%s", all)
	}

	cfg := ExploreConfig{Seeds: 8, Parallel: 4}
	walletSystem("", true).Explore(t, cfg).Expect(t, Conforms())
	walletSystem("restored", true).Explore(t, cfg).Expect(t, Conforms())
	walletSystem("unledgered", false).Explore(t, cfg).Expect(t, Conforms())
}
