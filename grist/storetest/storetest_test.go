package storetest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// ignoresTenant is a mutant store: its Read ignores the tenant, reading
// every tenant's events (a query missing its tenant's WHERE).
type ignoresTenant struct{ *memstore.Store }

func (s ignoresTenant) Read(ctx context.Context, tenant string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	_, through, err := s.Store.Read(ctx, tenant, q, at)
	if err != nil {
		return nil, 0, err
	}
	ts, _ := s.Tenants(ctx)
	var all []store.Stored
	for _, t := range ts {
		evs, _, _ := s.Store.Read(ctx, t, q, at)
		all = append(all, evs...)
	}
	return all, through, nil
}

// The suite sees a store that ignores the tenant in Read: its tenant
// isolation check fails, naming it, while the checks of one tenant pass.
func TestSuiteCatchesTenantLeak(t *testing.T) {
	for _, c := range storetest.Checks() {
		err := c.Run(context.Background(), ignoresTenant{memstore.New("grist")})
		switch {
		case c.Name == "TenantIsolation" && (err == nil || !strings.Contains(err.Error(), "tenant isolation")):
			t.Errorf("TenantIsolation over a store that ignores the tenant in Read: %v, want its failure", err)
		case c.Name != "TenantIsolation" && err != nil:
			t.Errorf("%s over the mutant, which keeps one tenant's contract: %v", c.Name, err)
		}
	}
}

// racy is a mutant store that checks a batch's condition outside the
// tenant's lock (a read, a pause, then an unconditioned append): a
// check-then-act race no single-task check can see.
type racy struct{ *memstore.Store }

func (s racy) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if b.Condition != nil {
		log, _, err := s.Store.Read(ctx, t, store.Selector{Types: []string{"a-happened", "b-happened"}}, 0)
		if err != nil {
			return 0, err
		}
		if err := store.CheckCondition(log, *b.Condition); err != nil {
			return 0, err
		}
		qenv.Sleep(ctx, time.Millisecond) // another append lands here
		b.Condition = nil
	}
	return s.Store.Append(ctx, t, b)
}

// eager is a mutant store whose Wait returns at once.
type eager struct{ *memstore.Store }

func (eager) Wait(context.Context, string, store.Position) error { return nil }

// headSteps is a mutant store whose WaitSteps waits on the head, as a
// relay polling Wait would: it sleeps through a refusal's step.
type headSteps struct{ *memstore.Store }

func (s headSteps) WaitSteps(ctx context.Context, t string, after int64) error {
	_, head, err := s.Read(ctx, t, store.Selector{}, 0)
	if err != nil {
		return err
	}
	if rows, _ := s.Steps(ctx, t, after, 1); len(rows) > 0 {
		return nil
	}
	return s.Wait(ctx, t, head)
}

// noSteps is a store that keeps no step log.
type noSteps struct{ store.Store }

// The suite sees a store that checks conditions outside its lock
// (ConcurrentAgainstBruteForce), one whose Wait does not wait
// (WaitWakes), one whose WaitSteps waits for the head and one with no
// step log (StepLog).
func TestSuiteCatchesConcurrencyMutants(t *testing.T) {
	for _, c := range []struct {
		check string
		s     store.Store
	}{
		{"ConcurrentAgainstBruteForce", racy{memstore.New("grist")}},
		{"WaitWakes", eager{memstore.New("grist")}},
		{"StepLog", headSteps{memstore.New("grist")}},
		{"StepLog", noSteps{memstore.New("grist")}},
	} {
		for _, k := range storetest.Checks() {
			if k.Name == c.check {
				if err := k.Run(context.Background(), c.s); err == nil {
					t.Errorf("%s over its mutant passes", c.check)
				} else {
					t.Logf("%s: %v", c.check, err)
				}
			}
		}
	}
}
