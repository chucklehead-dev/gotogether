package memstore_test

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
)

// The memstore keeps the store contract.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s grist.Store) error) {
		if err := body(context.Background(), memstore.New("grist")); err != nil {
			t.Error(err)
		}
	})
}
