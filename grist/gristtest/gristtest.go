// Package gristtest is a grist application's test harness: the witness
// that its generated model and bindings are the catalog's (Generated),
// its mutants built and run as go test overlays (Mutants, Mutant), and
// its real tier on a SQLite file over real HTTP, judged by the same
// bindings as its simulations (NewTier). It imports qtest; a production
// binary never links it.
package gristtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Generated fails t when the checked-in generated files of app (its
// model's skeleton, the vendored grist.qnt, its bindings: the generator, the app's binary's model command)
// under the test's package directory differ from what its catalog
// generates now, naming each and the fix, or when its rules lack a hole
// the skeleton calls, or when the model's files import each other in a
// cycle (quint's REPL refuses one; the generator names it, and the move
// out of the old layout, which had one), or when a binding's module does
// not import the events and the rules itself (without which the
// validator's Go evaluator cannot compile it, and falls back to quint
// processes). It runs no quint: every validation typechecks the model. A
// block added, renamed or redeclared without regenerating fails here, in
// CI, rather than as a binding that silently judges an older catalog.
// GRIST_UPDATE=1 writes the files instead (the app's `model` command
// does the same). Either way it names the app's rules' inputs that
// changed (Generated's Warnings: a read model folding a new event type,
// an event whose fields changed), which no typecheck catches.
func Generated(t testing.TB, app *grist.App) {
	t.Helper()
	g, err := gen.Generate(app, ".")
	if err != nil {
		t.Fatal(err)
	}
	warnings := ""
	for _, w := range g.Warnings {
		warnings += "\n  warning: " + w
	}
	if os.Getenv("GRIST_UPDATE") == "1" {
		if err := g.Write("."); err != nil {
			t.Fatal(err)
		}
		if warnings != "" {
			t.Logf("regenerated %s's model; check its rules:%s", app.Name, warnings)
		}
	} else if d := g.Diff("."); len(d) > 0 {
		t.Fatalf("generated files differ from what the %s App's catalog generates (the generator): %s; regenerate them with the app's binary's model command (grist.Main: go run ./cmd/<app> model) or GRIST_UPDATE=1 go test -run '^%s$', and commit them%s",
			app.Name, strings.Join(d, ", "), t.Name(), warnings)
	}
	if len(g.Missing) > 0 {
		var stubs []string
		for _, h := range g.Holes {
			for _, m := range g.Missing {
				if h.Name == m {
					stubs = append(stubs, h.Stub)
				}
			}
		}
		t.Fatalf("model/%s_rules.qnt lacks %s, which the generated skeleton calls: define them in its Rules module, for instance\n  %s",
			app.Name, strings.Join(g.Missing, ", "), strings.Join(stubs, "\n  "))
	}
	// each binding's module imports the events and the rules itself,
	// beside the skeleton: quint's flattener (quint compile, which the
	// validator's Go evaluator runs on a module importing this one from
	// another directory) loses names re-exported twice, and without the
	// evaluator every linearization's candidates are checked by quint
	// processes, several times slower, with nothing failing
	q, _ := catalog.QuintName(app.Name)
	src, err := os.ReadFile(filepath.Join("model", app.Name+".qnt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{q, q + "Calls"} {
		for _, want := range []string{q + "Events", q + "Rules"} {
			if !imports(string(src), module, want) {
				t.Fatalf("module %s of model/%s.qnt does not import %s itself: add `import %s.*` (from \"./%s_gen\" for the events, \"./%s_rules\" for the rules) beside the skeleton's import, as `<app> model -init` writes it, or the validator's evaluator cannot compile it", module, app.Name, want, want, app.Name, app.Name)
			}
		}
	}
}

// imports reports whether module, in src, imports name's every name
// (`import name.*`).
func imports(src, module, name string) bool {
	in := false
	for _, line := range strings.Split(src, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "module" {
			in = f[1] == module
			continue
		}
		if in && len(f) >= 2 && f[0] == "import" && f[1] == name+".*" {
			return true
		}
	}
	return false
}
