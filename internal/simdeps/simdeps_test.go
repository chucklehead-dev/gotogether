package simdeps_test

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The packages a simulation runs: qenv's runtime and doubles, recording,
// the load simulation, and the examples that run on gosim; with them,
// every package that imports qenv (simulatedPackages), so a new double or
// example is checked without being listed.
var simulated = []string{
	"github.com/chucklehead-dev/gotogether/qenv",
	"github.com/chucklehead-dev/gotogether/qobs",
	"github.com/chucklehead-dev/gotogether/qtrace",
	"github.com/chucklehead-dev/gotogether/qload",
	"github.com/chucklehead-dev/gotogether/qmeta",
	"github.com/chucklehead-dev/gotogether/qrole",
	"github.com/chucklehead-dev/gotogether/qrun",
	"github.com/chucklehead-dev/gotogether/qcover",
	"github.com/chucklehead-dev/gotogether/qenv/fault",
	"github.com/chucklehead-dev/gotogether/qmut",
	"github.com/chucklehead-dev/gotogether/examples/glomers/maelstrom",
	"github.com/chucklehead-dev/gotogether/examples/glomers/broadcast",
	"github.com/chucklehead-dev/gotogether/examples/checkout",
	"github.com/chucklehead-dev/gotogether/examples/retrystorm",
}

// hostSide import qenv but run outside any simulation: the Hegel adapters.
var hostSide = map[string]bool{
	"github.com/chucklehead-dev/gotogether/qhegel": true,
}

const module = "github.com/chucklehead-dev/gotogether"

// golist runs go list in the repository's workspace (go.work), whatever
// GOWORK says: the simulated packages span modules (the examples are
// modules of their own), and only the workspace sees them all.
func golist(t *testing.T, args ...string) []byte {
	t.Helper()
	ws, err := filepath.Abs("../../go.work")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Env = append(os.Environ(), "GOWORK="+ws)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list %v: %v\n%s", args, err, ee.Stderr)
		}
		t.Fatal(err)
	}
	return out
}

// simulatedPackages is simulated and every package of the workspace's
// modules that imports qenv, but the host-side ones.
func simulatedPackages(t *testing.T) []string {
	var patterns []string
	for _, m := range strings.Fields(string(golist(t, "-m"))) {
		patterns = append(patterns, m+"/...")
	}
	out := golist(t, append([]string{"-e", "-json"}, patterns...)...)
	seen := map[string]bool{}
	all := append([]string(nil), simulated...)
	for _, p := range all {
		seen[p] = true
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if seen[p.ImportPath] || hostSide[p.ImportPath] {
			continue
		}
		for _, imp := range p.Imports {
			if imp == module+"/qenv" {
				all = append(all, p.ImportPath)
				seen[p.ImportPath] = true
				break
			}
		}
	}
	return all
}

type pkg struct {
	ImportPath string
	Standard   bool
	CgoFiles   []string
	Imports    []string
}

// native reports why a package cannot be translated into a simulation.
func native(p pkg) string {
	switch {
	case strings.HasPrefix(p.ImportPath, "hegel.dev/go/hegel"):
		return "Hegel"
	case strings.HasPrefix(p.ImportPath, "github.com/ebitengine/purego"):
		return "purego (native calls)"
	case !p.Standard && len(p.CgoFiles) > 0:
		return "cgo"
	}
	return ""
}

func TestSimulatedPackagesHaveNoNativeDeps(t *testing.T) {
	roots := simulatedPackages(t)
	out := golist(t, append([]string{"-deps", "-json"}, roots...)...)
	pkgs := map[string]pkg{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs[p.ImportPath] = p
	}
	if len(roots) <= len(simulated) {
		t.Fatalf("found no importers of qenv beyond the listed packages (%d): the derivation is broken", len(roots))
	}
	for _, root := range roots {
		if chain := find(pkgs, root, map[string]bool{}); chain != nil {
			why := native(pkgs[chain[len(chain)-1]])
			t.Errorf("%s depends on %s, which a simulation cannot run: %s.\n"+
				"Move the code that needs it out of the simulated packages (Hegel adapters go in qhegel).",
				root, why, strings.Join(chain, " -> "))
		}
	}
}

// find returns an import chain from p to a native package, or nil.
func find(pkgs map[string]pkg, p string, seen map[string]bool) []string {
	if seen[p] {
		return nil
	}
	seen[p] = true
	if native(pkgs[p]) != "" {
		return []string{p}
	}
	for _, imp := range pkgs[p].Imports {
		if chain := find(pkgs, imp, seen); chain != nil {
			return append([]string{p}, chain...)
		}
	}
	return nil
}

// gosimUsers are the only modules that may require the gosim fork: what
// runs in gosim is translated, so the fork stays out of everything a
// simulation or a host imports by way of the core.
var gosimUsers = map[string]bool{"qgosim/go.mod": true, "examples/gosim/go.mod": true}

func TestOnlyQgosimRequiresGosim(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "ls-files", "go.mod", "**/go.mod").Output()
	if err != nil {
		skip.Optional(t, "no git checkout: "+err.Error())
	}
	for _, f := range strings.Fields(string(out)) {
		data, err := os.ReadFile("../../" + f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "github.com/chucklehead-dev/gosim ") && !gosimUsers[f] {
			t.Errorf("%s requires the gosim fork: only qgosim and examples/gosim may. Move the code that needs gosim into qgosim.", f)
		}
	}
}
