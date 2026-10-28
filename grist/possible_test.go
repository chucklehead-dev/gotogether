package grist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The generated bindings' possibles (P3): each of the shop's command
// outcomes is reachable by its model on its own (the Go evaluator's
// search), and with its rule mutated so that no addition is ever full,
// the full refusal is not, while ok still is: a model that forbids an
// outcome fails at the model stage.
func TestGeneratedPossibles(t *testing.T) {
	if testing.Short() {
		t.Skip("full mode: two typechecks and compilations of the shop's model")
	}
	requireQuint(t)
	// the shop's generated files with its own model and rules, the
	// vendored grist.qnt among them (testdata keeps no copy)
	shop := func(t *testing.T, rules func(string) string) string {
		g, err := grist.Generate(shopApp(), filepath.Join("testdata", "gen"))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := g.Write(dir); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"shop.qnt", "shop_rules.qnt"} {
			b, err := os.ReadFile(filepath.Join("testdata", "gen", "model", f))
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			if f == "shop_rules.qnt" {
				src = rules(src)
			}
			if err := os.WriteFile(filepath.Join(dir, "model", f), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	search := func(t *testing.T, dir string) map[string]validate.PossibleResult {
		c, err := validate.NewChecker(filepath.Join(dir, "shop.binding.yaml"), validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var cmds []string
		for _, p := range c.Binding.Simulate.Possible {
			cmds = append(cmds, p.Text())
		}
		if got := strings.Join(cmds, " "); !strings.HasPrefix(got, "addItem.ok addItem.full addItem.closed noteItem.ok count showItem ship notifyEffect notify") {
			t.Fatalf("the shop's possibles: %s", got)
		}
		var ps []binding.Possible
		for _, p := range c.Binding.Simulate.Possible {
			if p.Name == "addItem.ok" || p.Name == "addItem.full" || p.Name == "noteItem.ok" || p.When == "count" {
				ps = append(ps, p)
			}
		}
		rs, err := c.Possible(ps, validate.PossibleSearch{States: 500, Depth: 6})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]validate.PossibleResult{}
		for i, r := range rs {
			out[ps[i].Text()] = r
		}
		return out
	}
	faithful := search(t, shop(t, func(s string) string { return s }))
	for _, n := range []string{"addItem.ok", "addItem.full", "noteItem.ok", "count"} {
		if r := faithful[n]; !r.Found {
			t.Errorf("%s: %s, want reached", n, r.Verdict())
		}
	}
	full := `if (stock(log, c.item) + c.qty > 3) "full" else "ok"`
	dir := shop(t, func(s string) string {
		if !strings.Contains(s, full) {
			t.Fatalf("shop_rules.qnt has no %s", full)
		}
		return strings.Replace(s, full, `"ok"`, 1)
	})
	mutated := search(t, dir)
	if r := mutated["addItem.full"]; r.Found {
		t.Errorf("a model that never refuses full: addItem.full %s", r.Verdict())
	}
	if r := mutated["addItem.ok"]; !r.Found {
		t.Errorf("the mutated model: addItem.ok %s, want reached", r.Verdict())
	}
}
