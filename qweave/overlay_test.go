package qweave

import "testing"

// The toolkit's own packages are woven only for its own mutation testing:
// mutated when named, never covered, and never the packages woven code
// calls (they would switch their own mutants).
func TestCoversToolkitPackages(t *testing.T) {
	qtrace, qmut := toolkit+"/qtrace", toolkit+"/qmut"
	for _, c := range []struct {
		cfg  Config
		path string
		want bool
	}{
		{Config{Mutate: true}, qtrace, false},
		{Config{Mutate: true, CoverPkgs: []string{qtrace}}, qtrace, true},
		{Config{Cover: true, CoverPkgs: []string{qtrace}}, qtrace, false},
		{Config{Mutate: true, CoverPkgs: []string{toolkit}}, qmut, false},
		{Config{Mutate: true, CoverPkgs: []string{toolkit}}, toolkit + "/examples/hello", true},
	} {
		if got := c.cfg.covers(c.path, true); got != c.want {
			t.Errorf("%+v covers %s = %v, want %v", c.cfg, c.path, got, c.want)
		}
	}
}

// qenv's resolver, which a mutant's switch calls to find its run, is
// never mutated: a switch in it would call itself until the stack ran
// out (mutating qenv in a System.Mutate build did).
func TestRunLookupNotMutated(t *testing.T) {
	if !runLookup(toolkit+"/qenv", "/x/qenv/goroutine.go") || runLookup(toolkit+"/qenv", "/x/qenv/sim.go") || runLookup(toolkit+"/qtrace", "/x/qtrace/goroutine.go") {
		t.Error("runLookup names qenv's goroutine.go alone")
	}
}
