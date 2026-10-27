package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
)

// The generated model (model/counters_gen.qnt, the vendored grist.qnt)
// and the bindings are what the App's catalog generates now
// (`go run ./cmd/counters model`), and the model typechecks.
func TestGenerated(t *testing.T) {
	if gristtest.Mutant() != "" {
		t.Skip("a mutant's binary: its catalog is not the app's")
	}
	gristtest.Generated(t, App)
}
