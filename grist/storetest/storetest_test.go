package storetest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
)

// ignoresTenant is a mutant store: its Read ignores the tenant, reading
// every tenant's events (a query missing its tenant's WHERE).
type ignoresTenant struct{ *memstore.Store }

func (s ignoresTenant) Read(ctx context.Context, tenant string, q grist.Query, at grist.Position) ([]grist.Stored, grist.Position, error) {
	_, through, err := s.Store.Read(ctx, tenant, q, at)
	if err != nil {
		return nil, 0, err
	}
	ts, _ := s.Tenants(ctx)
	var all []grist.Stored
	for _, t := range ts {
		evs, _, _ := s.Store.Read(ctx, t, q, at)
		all = append(all, evs...)
	}
	return all, through, nil
}

// The suite sees a store that ignores the tenant in Read: its tenant
// isolation check fails, naming it, while the checks of one tenant pass.
func TestSuiteCatchesTenantLeak(t *testing.T) {
	for _, c := range storetest.Checks {
		err := c.Run(context.Background(), ignoresTenant{memstore.New("grist")})
		switch {
		case c.Name == "TenantIsolation" && (err == nil || !strings.Contains(err.Error(), "tenant isolation")):
			t.Errorf("TenantIsolation over a store that ignores the tenant in Read: %v, want its failure", err)
		case c.Name != "TenantIsolation" && err != nil:
			t.Errorf("%s over the mutant, which keeps one tenant's contract: %v", c.Name, err)
		}
	}
}
