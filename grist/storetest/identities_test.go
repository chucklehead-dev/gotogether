package storetest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
	"github.com/chucklehead-dev/gotogether/qenv"
)

type wrongIdentity struct {
	*memstore.Store
	kind string
}

func (s wrongIdentity) Read(ctx context.Context, tenant string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	events, head, err := s.Store.Read(ctx, tenant, q, at)
	for i := range events {
		switch s.kind {
		case "missing":
			events[i].ID = ""
		case "shared":
			events[i].ID = strings.Repeat("a", 32)
		case "read-generated":
			events[i].ID = qenv.NewID(ctx)
		case "position-derived":
			events[i].ID = fmt.Sprintf("%032x", events[i].Pos)
		}
	}
	return events, head, err
}

func TestIdentityContractSeesMissingSharedAndReadGeneratedIDs(t *testing.T) {
	for _, check := range storetest.Checks() {
		if check.Name != "EventIdentities" {
			continue
		}
		for _, kind := range []string{"missing", "shared", "read-generated"} {
			t.Run(kind, func(t *testing.T) {
				err := check.Run(t.Context(), wrongIdentity{Store: memstore.New("grist"), kind: kind})
				if err == nil || !strings.Contains(err.Error(), "identit") {
					t.Fatalf("identity mutant %s: got %v, want an identity failure", kind, err)
				}
			})
		}
		return
	}
	t.Fatal("EventIdentities contract is not registered")
}

// Position-derived IDs appear unique and stable until a restore gives the same
// position to another event. The rewind control must distinguish that case.
func TestRewindContractSeesReusedPositionIdentity(t *testing.T) {
	s := wrongIdentity{Store: memstore.New("grist"), kind: "position-derived"}
	var backup *memstore.Backup
	err := storetest.Rewinds(t.Context(), s,
		func(context.Context) error { backup = s.Backup(); return nil },
		func(context.Context) error { s.Restore(backup); return nil })
	if err == nil || !strings.Contains(err.Error(), "reused identity") {
		t.Fatalf("position-derived identity after restore: got %v, want reused identity failure", err)
	}
}
