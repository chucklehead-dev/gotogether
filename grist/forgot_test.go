package grist_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
)

type (
	stockAdded struct {
		Item string `json:"item" grist:"tag"`
		N    int    `json:"n"`
	}
	stockRemoved struct {
		Item string `json:"item" grist:"tag"`
	}
	stockOrphan struct {
		Item string `json:"item" grist:"tag"`
	}
	stockArgs struct {
		Item string `json:"item"`
	}
)

var errStockLow = grist.Rejection("low")

// stock is a store of items: add-item and remove-item, each declaring
// what it appends, and a stock read model keyed by item. folds says
// whether it folds removals (the newcomer's forgotten fold, when not);
// orphan registers an event no block appends and nothing reads; rule
// guards removals with a rule.
func stock(folds, orphan, rule bool) *grist.App {
	a := grist.New("stock")
	added := grist.NewEvent[stockAdded](a, "item-added")
	removed := grist.NewEvent[stockRemoved](a, "item-removed")
	if orphan {
		grist.NewEvent[stockOrphan](a, "item-orphaned")
	}
	hs := []grist.Handler[int]{grist.On(added, func(s int, e stockAdded, _ grist.Meta) int { return s + e.N })}
	if folds {
		hs = append(hs, grist.On(removed, func(s int, e stockRemoved, _ grist.Meta) int { return s - 1 }))
	}
	levels := grist.NewKeyedReadModel(a, "stock", 1, grist.Keys("item"), func() int { return 0 }, hs...)
	grist.NewCommand(a, "add-item", func(r *grist.Reads, c stockArgs) ([]grist.Event, error) {
		return []grist.Event{added.Of(stockAdded{Item: c.Item, N: 1})}, nil
	}, grist.Appends(added), grist.Authorize(grist.Authenticated))
	opts := []grist.Option{grist.Appends(removed), grist.Authorize(grist.Authenticated)}
	if rule {
		opts = append(opts, grist.Refuses(errStockLow))
		grist.NewRule(a, "no-negative", removed, func(r *grist.Reads, e stockRemoved) error {
			if levels.For(r, e.Item) <= 0 {
				return errStockLow
			}
			return nil
		}, grist.Refuses(errStockLow))
	}
	grist.NewCommand(a, "remove-item", func(r *grist.Reads, c stockArgs) ([]grist.Event, error) {
		return []grist.Event{removed.Of(stockRemoved{Item: c.Item})}, nil
	}, opts...)
	return a
}

func warnings(t *testing.T, a *grist.App, dir string) string {
	t.Helper()
	g, err := gen.Generate(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(g.Warnings, "\n")
}

// An event a command appends that nothing reads (no read model folds
// it, no processor handles it, the model reads it nowhere) is warned
// of, naming who appends it and the read models folding events of its
// tags; folding it, or reading it in the model's rules, ends the warning.
func TestGenerateWarnsOfAnUnreadEvent(t *testing.T) {
	dir := t.TempDir()
	w := warnings(t, stock(false, false, false), dir)
	for _, want := range []string{"event item-removed is appended by command remove-item and read by nothing",
		"stock folds events tagged item too: should it fold this one?", "grist.On(item-removed, ...) in that read model"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings %q lack %q", w, want)
		}
	}
	if w := warnings(t, stock(true, false, false), dir); strings.Contains(w, "read by nothing") {
		t.Errorf("removals folded: %s", w)
	}
	// read by the model's rules (an accessor of it): read, though no
	// read model folds it
	os.MkdirAll(filepath.Join(dir, "model"), 0o755)
	os.WriteFile(filepath.Join(dir, "model", "stock_rules.qnt"), []byte("module stockRules {\n  pure def gone(e: Event): str = itemRemovedItem(e)\n}\n"), 0o644)
	if w := warnings(t, stock(false, false, false), dir); strings.Contains(w, "read by nothing") {
		t.Errorf("read by the rules: %s", w)
	}
}

// An event no block can append (every command declares its Appends and
// none names it) that nothing reads is dead: the generator's error.
func TestGenerateRefusesADeadEvent(t *testing.T) {
	_, err := gen.Generate(stock(true, true, false), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "event item-orphaned is appended by no block") || !strings.Contains(err.Error(), "Declare the block that appends it, or remove it") {
		t.Errorf("a dead event: %v, want the generator's error naming it", err)
	}
}

// A rule's generated mutant run by no test (the package's tests call
// neither gristtest.RuleMutants nor DroppedRule) is warned of; with both,
// or with no tests at all yet, it is not.
func TestGenerateWarnsOfUnrunRuleMutants(t *testing.T) {
	dir := t.TempDir()
	a := stock(true, false, true)
	if w := warnings(t, a, dir); strings.Contains(w, "generated mutant") {
		t.Errorf("no tests yet: %s", w)
	}
	os.WriteFile(filepath.Join(dir, "mutants_test.go"), []byte("package stock\n\nfunc TestMutants(t *testing.T) { qtest.Mutants{}.Run(t) }\n"), 0o644)
	if w := warnings(t, a, dir); !strings.Contains(w, "rule no-negative has a generated mutant") || !strings.Contains(w, "gristtest.RuleMutants(App)") {
		t.Errorf("rule mutants unrun: warnings %q", w)
	}
	os.WriteFile(filepath.Join(dir, "mutants_test.go"), []byte("package stock\n\n// qtest.Mutants{Generated: gristtest.RuleMutants(App)}; gristtest.DroppedRule(App)\n"), 0o644)
	if w := warnings(t, a, dir); strings.Contains(w, "generated mutant") {
		t.Errorf("rule mutants run: %s", w)
	}
}

// In an app whose tests write acceptance scenarios, a command none of
// them invokes is warned of, by its name and variable.
func TestGenerateWarnsOfCommandsNoScenarioTakes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "stock.go"), []byte("package stock\n\nvar (\n\tAddCmd = grist.NewCommand(App, \"add-item\", add)\n\tRemoveCmd = grist.NewCommand(App, \"remove-item\", remove)\n)\n"), 0o644)
	a := stock(true, false, false)
	if w := warnings(t, a, dir); strings.Contains(w, "acceptance scenario") {
		t.Errorf("no scenarios at all: %s", w)
	}
	os.WriteFile(filepath.Join(dir, "sim_test.go"), []byte("package stock\n\nvar s = gristtest.Scenario(\"adds\",\n\tAddCmd.With(stockArgs{Item: \"a\"}))\n"), 0o644)
	if w := warnings(t, a, dir); !strings.Contains(w, "command remove-item (RemoveCmd) is taken by no acceptance scenario") || strings.Contains(w, "add-item (AddCmd)") {
		t.Errorf("remove-item in no scenario: warnings %q", w)
	}
}

// A stale grist.lock is named with what changed in it (Generated.Lock,
// the model command's -check), and a missing one says to commit it.
func TestGenerateNamesAStaleLock(t *testing.T) {
	dir := t.TempDir()
	g, err := gen.Generate(stock(false, false, false), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Lock) != 1 || !strings.Contains(g.Lock[0], "there is no grist.lock") {
		t.Errorf("no lock yet: %v", g.Lock)
	}
	g.Write(dir)
	usage := func(msg string) error { return errors.New(msg) }
	err = gen.Command(stock(true, false, false), []string{"-dir", dir, "-check"}, io.Discard, usage)
	if err == nil || !strings.Contains(err.Error(), "grist.lock is stale: read model stock's fingerprint changed (its snapshots refold)") {
		t.Errorf("-check with the fold changed: %v, want the stale lock named", err)
	}
}
