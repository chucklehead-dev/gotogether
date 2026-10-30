// Package gristtest is a grist application's test harness: the witness
// that its generated model and bindings are the catalog's (Generated),
// its mutants built and run as go test overlays (Mutants, Mutant), and
// its real tier on a SQLite file over real HTTP, judged by the same
// bindings as its simulations (NewTier). It imports qtest; a production
// binary never links it.
package gristtest

import (
	"os"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
)

// Generated checks the application's model preflight under the test's package
// directory, sharing the app binary's `model -check` findings. It reports stale
// generated files, lock changes, missing handwritten rules and model imports
// together, with the fix for each. Warnings retain their heuristic scope and do
// not fail the test. No Quint process runs: validation still typechecks the model.
//
// GRIST_UPDATE=1 applies the preflight's mechanical generated-file changes, then
// checks the resulting files again. Handwritten models and rules are never
// overwritten; unresolved modeling decisions still fail the test. A structurally
// blocked generation plan cannot be applied.
func Generated(t testing.TB, app *grist.App) {
	t.Helper()
	preflight := gen.Inspect(app, ".")
	if os.Getenv("GRIST_UPDATE") == "1" {
		if !preflight.CanGenerate() {
			t.Fatal(preflight.String())
		}
		var err error
		preflight, err = preflight.Apply()
		if err != nil {
			t.Fatalf("%s\napply generated-file changes: %v", preflight.String(), err)
		}
	}
	if !preflight.OK() {
		t.Fatal(preflight.String())
	}
	if len(preflight.Findings) > 0 {
		t.Log(preflight.String())
	}
}
