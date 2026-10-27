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

// eagerTenants is a mutant store whose WaitTenants returns at once.
type eagerTenants struct{ *memstore.Store }

func (eagerTenants) WaitTenants(context.Context, int) error { return nil }

// lateSteps is a mutant store whose step rows carry the batch's time,
// decided before the lock, not the commit's: a later commit may carry an
// earlier time.
type lateSteps struct{ *memstore.Store }

func (s lateSteps) Steps(ctx context.Context, t string, after int64, limit int) ([]store.StepRow, error) {
	rows, err := s.Store.Steps(ctx, t, after, limit)
	for i := range rows {
		rows[i].Time = 1_000_000 - rows[i].Seq // decided in the other order
	}
	return rows, err
}

// The suite sees a store that checks conditions outside its lock
// (ConcurrentAgainstBruteForce), one whose Wait does not wait
// (WaitWakes), one whose WaitSteps waits for the head, one with no step
// log and one whose rows' times are not in commit order (StepLog), and
// one whose WaitTenants does not wait or that has none (TenantFeed).
func TestSuiteCatchesConcurrencyMutants(t *testing.T) {
	for _, c := range []struct {
		check string
		s     store.Store
	}{
		{"ConcurrentAgainstBruteForce", racy{memstore.New("grist")}},
		{"WaitWakes", eager{memstore.New("grist")}},
		{"StepLog", headSteps{memstore.New("grist")}},
		{"StepLog", noSteps{memstore.New("grist")}},
		{"StepLog", lateSteps{memstore.New("grist")}},
		{"TenantFeed", eagerTenants{memstore.New("grist")}},
		{"TenantFeed", noSteps{memstore.New("grist")}},
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

// firstTag is a mutant store that reads and checks a selector by its
// first tag alone (an index of one tag, the event's other tags never
// read): the suite's selectors of two tags see it.
type firstTag struct{ *memstore.Store }

func trim(q store.Selector) store.Selector {
	if len(q.Tags) > 1 {
		q.Tags = q.Tags[:1]
	}
	return q
}

func (s firstTag) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	return s.Store.Read(ctx, t, trim(q), at)
}

func (s firstTag) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if b.Condition != nil {
		c := *b.Condition
		c.Selectors = append([]store.Selector(nil), c.Selectors...)
		for i := range c.Selectors {
			c.Selectors[i] = trim(c.Selectors[i])
		}
		b.Condition = &c
	}
	return s.Store.Append(ctx, t, b)
}

// The reads' and conditions' brute-force checks draw selectors of two
// tags (with one type and two): a store that honours only the first tag
// fails both.
func TestSuiteCatchesFirstTagOnly(t *testing.T) {
	for _, c := range storetest.Checks() {
		if c.Name != "ReadsAgainstBruteForce" && c.Name != "ConditionsAgainstBruteForce" {
			continue
		}
		if err := c.Run(context.Background(), firstTag{memstore.New("grist")}); err == nil {
			t.Errorf("%s over a store honouring one tag of a selector's two passes", c.Name)
		} else {
			t.Logf("%s: %v", c.Name, err)
		}
	}
}
