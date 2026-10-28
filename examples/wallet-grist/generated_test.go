package walletgrist

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
)

// The generated model (model/wallet_gen.qnt and _steps.qnt, the vendored grist.qnt) and
// the bindings are what the App's catalog generates now (`go run
// ./cmd/wallet model`), and the model typechecks.
func TestGenerated(t *testing.T) {
	if gristtest.Mutant() != "" {
		t.Skip("a mutant's binary: its catalog is not the app's")
	}
	gristtest.Generated(t, App)
}
