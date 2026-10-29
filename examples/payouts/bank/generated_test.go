package bank

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
)

// The bank's generated model and bindings are what its catalog generates
// now (`go run ./cmd/bank model`, here), and the model typechecks.
func TestGenerated(t *testing.T) { gristtest.Generated(t, App) }
