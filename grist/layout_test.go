package grist_test

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
)

// quintImport is a Quint import from a file.
var quintImport = regexp.MustCompile(`(?m)^\s*import\s+.*\bfrom\s+"([^"]+)"`)

// importsCycle reports whether the .qnt files under dir import each other
// in a cycle: what quint's REPL refuses (QNT101, "Multiple modules
// conflict on the same name"), though typecheck and run accept it.
func importsCycle(t *testing.T, dir string) bool {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "*.qnt"))
	edges := map[string][]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range quintImport.FindAllStringSubmatch(string(b), -1) {
			edges[filepath.Base(p)] = append(edges[filepath.Base(p)], path.Clean(m[1])+".qnt")
		}
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(f string) bool {
		if state[f] == 1 {
			return true
		}
		if state[f] == 2 {
			return false
		}
		state[f] = 1
		for _, to := range edges[f] {
			if visit(to) {
				return true
			}
		}
		state[f] = 2
		return false
	}
	for f := range edges {
		if visit(f) {
			return true
		}
	}
	return false
}

// The generated layout's files import each other in one direction only
// (the starter's, the golden shop's): the app's invariants the steps, the
// steps the rules, the rules the events. The layout before it, the rules
// in the app's file beside the invariants, was a cycle (the app's file
// imported the skeleton, which imported the rules back): Generate refuses
// it with the move that fixes it, and refuses any other cycle, naming it.
func TestLayoutAcyclic(t *testing.T) {
	a := shopApp()
	files, err := grist.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFiles(t, dir, files)
	g, err := grist.Generate(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	if importsCycle(t, filepath.Join(dir, "model")) {
		t.Error("the starter's model files import each other in a cycle")
	}
	if importsCycle(t, filepath.Join("testdata", "gen", "model")) {
		t.Error("testdata/gen's model files import each other in a cycle")
	}
	// the old layout: the rules in shop.qnt, imported back by the skeleton
	old := t.TempDir()
	writeFiles(t, old, map[string][]byte{
		"model/shop.qnt":     []byte("module shopRules {\n  import shopEvents.* from \"./shop_gen\"\n}\n\nmodule shop {\n  import shopRules.*\n  import shopGen.* from \"./shop_gen\"\n}\n"),
		"model/shop_gen.qnt": []byte("module shopEvents {}\n\nmodule shopGen {\n  import shopRules.* from \"./shop\"\n}\n"),
	})
	if !importsCycle(t, filepath.Join(old, "model")) {
		t.Error("the old layout: no cycle found")
	}
	if _, err := grist.Generate(a, old); err == nil || !strings.Contains(err.Error(), "move module shopRules, as it is, to model/shop_rules.qnt") {
		t.Errorf("Generate of the old layout: %v, want the move", err)
	}
	if err := grist.MainArgs(t.Context(), a, []string{"model", "-init", "-dir", old}, os.Stdout, os.Stderr); err == nil || !strings.Contains(err.Error(), "move module shopRules") {
		t.Errorf("model -init of the old layout: %v, want the move", err)
	}
	if _, err := os.Stat(filepath.Join(old, "model", "shop_rules.qnt")); err == nil {
		t.Error("model -init stubbed the rules beside the old layout's")
	}
	// rules that import the steps back
	rules := strings.Replace(string(files["model/shop_rules.qnt"]), "  import grist.*", "  import shopGen.* from \"./shop_steps\"\n  import grist.*", 1)
	writeFiles(t, dir, map[string][]byte{"model/shop_rules.qnt": []byte(rules)})
	if _, err := grist.Generate(a, dir); err == nil || !strings.Contains(err.Error(), "model/shop_rules.qnt -> model/shop_steps.qnt -> model/shop_rules.qnt") {
		t.Errorf("Generate of rules importing the steps: %v, want the cycle named", err)
	}
}

// Every grist model loads in quint's REPL (`quint -r <app>.qnt::<module>`,
// each binding's module), which refuses a file that a file it imports
// imports back (QNT101) though typecheck and run take it: the golden
// shop's and the repository's examples' (counters, wallet-grist, library),
// each evaluating init. A REPL session that loads a model once
// (docs/performance.md D4) needs it.
func TestModelsLoadInREPL(t *testing.T) {
	requireQuint(t)
	shop := t.TempDir()
	g, err := grist.Generate(shopApp(), filepath.Join("testdata", "gen"))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write(shop); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"shop.qnt", "shop_rules.qnt"} {
		b, err := os.ReadFile(filepath.Join("testdata", "gen", "model", f))
		if err != nil {
			t.Fatal(err)
		}
		writeFiles(t, shop, map[string][]byte{"model/" + f: b})
	}
	models := []struct{ dir, app, q string }{
		{filepath.Join(shop, "model"), "shop", "shop"},
		{filepath.Join("..", "examples", "counters", "model"), "counters", "counters"},
		{filepath.Join("..", "examples", "wallet-grist", "model"), "wallet", "wallet"},
		{filepath.Join("..", "examples", "library", "model"), "library", "library"},
	}
	for _, m := range models {
		if _, err := os.Stat(filepath.Join(m.dir, m.app+".qnt")); err != nil {
			t.Logf("no %s: %v (outside the repository's checkout)", m.dir, err)
			continue
		}
		for _, module := range []string{m.q, m.q + "Calls"} {
			t.Run(m.app+"::"+module, func(t *testing.T) {
				t.Parallel()
				cmd := exec.Command("quint", "-r", m.app+".qnt::"+module)
				cmd.Dir, cmd.Stdin = m.dir, strings.NewReader("init\n")
				out, err := cmd.CombinedOutput()
				// the REPL exits 0 whatever it refused: its output says
				if err != nil || strings.Contains(string(out), "rror [") || strings.Contains(string(out), "error:") || !strings.Contains(string(out), ">>> true") {
					t.Errorf("quint -r %s.qnt::%s in %s: %v\n%s", m.app, module, m.dir, err, out)
				}
			})
		}
	}
}
