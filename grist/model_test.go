package grist_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/internal/gen"

	"github.com/chucklehead-dev/gotogether/grist"
)

// The shop: every shape the generator writes, in one App. Events with
// strings, integers, booleans, a tag of each kind it takes, a field
// Quint reserves (of) and one it does not record (a slice); a plain and a
// keyed read model; a conditioned command with refusals, an
// Unconditioned one without; a rule on the first one's event; a query of a struct and one of a scalar;
// processors that emit, effect at least once with a success event and at
// most once without; a site; a dependency's Keyed call with a refusal,
// used by both effects.
type (
	itemAdded struct {
		Item string   `json:"item" grist:"tag"`
		Qty  int      `json:"qty"`
		Gift bool     `json:"gift" grist:"tag"`
		Tags []string `grist:"-"`
	}
	itemShipped struct {
		Item string `json:"item" grist:"tag"`
		Of   int64  `json:"of"`
	}
	itemNoted struct{}
	addItem   struct {
		Item string `json:"item"`
		Qty  int    `json:"qty"`
		Of   string `json:"of"`
	}
	noteItem struct {
		Item string `json:"item"`
	}
	showItem struct {
		Item string `json:"item"`
	}
	itemView struct {
		Qty     int  `json:"qty"`
		Shipped bool `json:"shipped"`
	}
	picked struct {
		Key  string `json:"key"`
		Item string `json:"item"`
		Qty  int    `json:"qty"`
	}
	reserve struct {
		Item string `json:"item"`
		Qty  int    `json:"qty"`
	}
	reserved struct {
		Ref string `json:"ref"`
	}
)

var errFull = grist.Rejection("full")

// errClosed is the open-shop rule's refusal: a shop takes four additions.
var errClosed = grist.Rejection("closed")

// errNoStock is the warehouse's refusal of a reservation.
var errNoStock = grist.Rejection("no-stock")

func shopApp() *grist.App {
	a := grist.New("shop")
	added := grist.NewEvent[itemAdded](a, "item-added")
	shipped := grist.NewEvent[itemShipped](a, "item-shipped")
	noted := grist.NewEvent[itemNoted](a, "item-noted")
	stock := grist.NewKeyedReadModel(a, "stock", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e itemAdded, m grist.Meta) int { return s + e.Qty }))
	items := grist.NewReadModel(a, "items", 1, func() int { return 0 }, grist.On(added, func(s int, e itemAdded, m grist.Meta) int { return s + 1 }))
	grist.NewCommand(a, "add-item", func(r *grist.Reads, c addItem) ([]grist.Event, error) {
		if stock.For(r, c.Item)+c.Qty > 3 {
			return nil, errFull
		}
		return []grist.Event{added.Of(itemAdded{Item: c.Item, Qty: c.Qty})}, nil
	}, grist.Refuses(errFull), grist.Appends(added), grist.Authorize(grist.Authenticated))
	grist.NewRule(a, "open-shop", added, func(r *grist.Reads, e itemAdded) error {
		if items.Get(r) >= 4 {
			return errClosed
		}
		return nil
	}, grist.Refuses(errClosed))
	grist.NewCommand(a, "note-item", func(r *grist.Reads, c noteItem) ([]grist.Event, error) {
		return []grist.Event{noted.Of(itemNoted{})}, nil
	}, grist.Unconditioned("notes are advisory"), grist.Internal())
	grist.NewQuery(a, "show-item", func(r *grist.Reads, q showItem) (itemView, error) {
		return itemView{Qty: stock.For(r, q.Item)}, nil
	}, grist.Authorize(grist.Authenticated))
	grist.NewQuery(a, "count", func(r *grist.Reads, q struct{}) (int, error) { return 0, nil }, grist.Authorize(grist.Authenticated))
	grist.NewProcessor(a, "ship", added, func(ctx context.Context, r *grist.Reads, e itemAdded, m grist.Meta) grist.Result {
		return grist.Emit(shipped.Of(itemShipped{Item: e.Item, Of: int64(m.Pos)}))
	}, grist.Emits(shipped))
	warehouse := grist.NewDependency(a, "warehouse")
	hold := grist.NewCall[reserve, reserved](warehouse, "hold", grist.Keyed(), grist.Refuses(errNoStock))
	grist.NewProcessor(a, "notify", added, func(ctx context.Context, r *grist.Reads, e itemAdded, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := hold.Do(ctx, reserve{Item: e.Item, Qty: e.Qty})
			return err
		}, grist.AtLeastOnce, grist.OnSuccess(noted.Of(itemNoted{})))
	}, grist.Delivers(grist.AtLeastOnce, noted), grist.EffectTries(2), grist.Uses(hold))
	grist.NewProcessor(a, "charge", shipped, func(ctx context.Context, r *grist.Reads, e itemShipped, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := hold.Do(ctx, reserve{Item: e.Item, Qty: 1})
			return err
		}, grist.AtMostOnce)
	}, grist.Delivers(grist.AtMostOnce), grist.Uses(hold))
	grist.NewSite[picked](a, "warehouse-picks")
	return a
}

// update rewrites the golden files (GRIST_UPDATE=1).
var update = os.Getenv("GRIST_UPDATE") == "1"

// The generator's output for the shop is testdata/gen's, file by file:
// the skeleton, the vendored grist.qnt, the store's and the clients'
// bindings, the variant its model marks. GRIST_UPDATE=1 rewrites them.
// The skeleton with the shop's rules typechecks, and the model on its own
// (step) keeps the invariants the rules state over a sampled run.
func TestGenerateGolden(t *testing.T) {
	dir := filepath.Join("testdata", "gen")
	g, err := gen.Generate(shopApp(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Missing) > 0 {
		t.Errorf("holes the shop's rules lack: %v", g.Missing)
	}
	for p, b := range g.Files {
		path := filepath.Join(dir, p)
		if update && p != "model/grist.qnt" {
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if p == "model/grist.qnt" {
			continue // the library itself, vendored: written in the temp copy below
		}
		want, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(want, b) {
			t.Errorf("%s differs from what grist model writes (GRIST_UPDATE=1 go test -run TestGenerateGolden rewrites it)", path)
		}
	}
	for _, want := range []string{"shop.binding.yaml", "shop.calls.binding.yaml", "shop.audit.binding.yaml", "model/shop_gen.qnt", "model/shop_steps.qnt", "model/grist.qnt"} {
		if g.Files[want] == nil {
			t.Errorf("no %s generated", want)
		}
	}
	requireQuint(t)
	tmp := t.TempDir()
	if err := g.Write(tmp); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"shop.qnt", "shop_rules.qnt"} {
		own, err := os.ReadFile(filepath.Join(dir, "model", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "model", f), own, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	quint(t, filepath.Join(tmp, "model"), "typecheck", "shop.qnt")
	quint(t, filepath.Join(tmp, "model"), "run", "--main=shop", "--max-samples=100", "--max-steps=15", "--seed=3", "--invariant=stockBounded", "shop.qnt")
	quint(t, filepath.Join(tmp, "model"), "run", "--main=shopCalls", "--max-samples=100", "--max-steps=15", "--seed=3", "--invariant=stockBounded", "shop.qnt")
}

// A variant judging every invariant of the default binding judges the
// processors' obligations, and a call's only where one of its own vals
// reads the call's served records (a site claim, here through a def): a
// real tier's variant whose dependency is a third party's, recording no
// site, would otherwise have every run violate the call's obligation.
func TestCallObligationsFollowSiteClaims(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "model"), 0o755)
	rules, err := os.ReadFile(filepath.Join("testdata", "gen", "model", "shop_rules.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "model", "shop_rules.qnt"), rules, 0o644)
	own := `module shop {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"
  import shopRules.* from "./shop_rules"
  import shopGen.* from "./shop_steps"
  export shopGen.*

  // grist: invariant default real sim
  val stockBounded = STRS.forall(i => stock(log, i) <= 3)

  // the holds the warehouse served (warehouseHoldRecords): a site claim
  def served(key: str): bool = warehouseHoldRecords.indices().exists(j => warehouseHoldRecords[j].key == key)

  // grist: eventually sim
  val everyHoldAnswerServed = warehouseHoldCalls.indices().forall(i => served(warehouseHoldCalls[i].key))
}
`
	os.WriteFile(filepath.Join(dir, "model", "shop.qnt"), []byte(own), 0o644)
	g, err := gen.Generate(shopApp(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]string{
		"shop.binding.yaml":      "eventually: [shipEventuallyHandled, notifyEventuallyHandled, chargeEventuallyHandled, warehouseHoldEventuallyServed]",
		"shop.sim.binding.yaml":  "eventually: [everyHoldAnswerServed, shipEventuallyHandled, notifyEventuallyHandled, chargeEventuallyHandled, warehouseHoldEventuallyServed]",
		"shop.real.binding.yaml": "eventually: [shipEventuallyHandled, notifyEventuallyHandled, chargeEventuallyHandled]\n",
	} {
		if !strings.Contains(string(g.Files[b]), want) {
			t.Errorf("%s: want %q in it:\n%s", b, want, g.Files[b])
		}
	}
}

// A Starter model fills every hole with a stub that typechecks: an app's
// first `model -init` is a model quint takes, and Generate reports no
// hole missing from it; a hole taken out of it is reported by name.
func TestStarterTypechecks(t *testing.T) {
	a := shopApp()
	files, err := gen.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFiles(t, dir, files)
	b := files["model/shop_rules.qnt"]
	g, err := gen.Generate(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Missing) > 0 {
		t.Errorf("the starter lacks %v", g.Missing)
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	if d := g.Diff(dir); len(d) > 0 {
		t.Errorf("Diff right after Write: %v", d)
	}
	// a hole taken out is missing, by name
	cut := strings.Replace(string(b), "pure def addItemNeeds", "pure def addItemNeedsNot", 1)
	if err := os.WriteFile(filepath.Join(dir, "model", "shop_rules.qnt"), []byte(cut), 0o644); err != nil {
		t.Fatal(err)
	}
	if g2, err := gen.Generate(a, dir); err != nil || len(g2.Missing) != 1 || g2.Missing[0] != "addItemNeeds" {
		t.Errorf("Missing %v (%v): want [addItemNeeds]", g2.Missing, err)
	}
	// a generated file changed by hand is a difference
	if err := os.WriteFile(filepath.Join(dir, "shop.binding.yaml"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := g.Diff(dir); len(d) != 1 || d[0] != "shop.binding.yaml" {
		t.Errorf("Diff %v: want [shop.binding.yaml]", d)
	}
	requireQuint(t)
	if err := os.WriteFile(filepath.Join(dir, "model", "shop_rules.qnt"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	quint(t, filepath.Join(dir, "model"), "typecheck", "shop.qnt")
}

// writeFiles writes files (paths relative to dir) under dir.
func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// What the generator cannot model yet is its error, naming the fix: a
// processor that declares no shape, an integer tag, a pointer argument;
// and a mark it cannot read.
func TestGenerateRefuses(t *testing.T) {
	type tagged struct {
		N int `json:"n" grist:"tag"`
	}
	type ptr struct {
		P *string `json:"p"`
	}
	a := grist.New("bad")
	ev := grist.NewEvent[tagged](a, "tagged")
	grist.NewCommand(a, "c", func(r *grist.Reads, c ptr) ([]grist.Event, error) { return nil, nil }, grist.Internal())
	grist.NewProcessor(a, "p", ev, func(ctx context.Context, r *grist.Reads, e tagged, m grist.Meta) grist.Result { return grist.Skip() })
	type listed struct {
		Books []string `json:"books"`
	}
	grist.NewQuery(a, "q", func(r *grist.Reads, q struct{}) (listed, error) { return listed{}, nil }, grist.Internal())
	_, err := gen.Generate(a, t.TempDir())
	// a list in a query's answer, which no step records: once dropped
	// without a word, the answer then compared on nothing
	for _, want := range []string{"declares no result shape", "tag n is an integer", "is a pointer", "field Books is a []string", "grist:\"-\""} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Generate: %v, want %q", err, want)
		}
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "model"), 0o755)
	for src, want := range map[string]string{
		"module shop {\n  // grist: invariants\n  val x = true\n}\n":      "the kinds are",
		"module shop {\n  // grist: invariant\n  action a = true\n}\n":    "not followed by a val",
		"module shop {\n  // grist: invariant calls\n  val x = true\n}\n": "the clients' binding's",
		// an obligation the catalog implies, defined by the app too (its
		// eventual val before P7), which quint would refuse as defined twice
		"module shop {\n  // grist: eventually\n  val shipEventuallyHandled = true\n}\n": "delete the app's own",
	} {
		os.WriteFile(filepath.Join(dir, "model", "shop.qnt"), []byte(src), 0o644)
		if _, err := gen.Generate(shopApp(), dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("marks %q: %v, want %q", src, err, want)
		}
	}
	// an obligation's name defined by the rules, which the generated steps
	// module imports: quint would refuse it there (QNT101), in a file the
	// app never edits
	os.WriteFile(filepath.Join(dir, "model", "shop.qnt"), []byte("module shop {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "model", "shop_rules.qnt"), []byte("module shopRules {\n  pure val notifyEventuallyHandled = true\n}\n"), 0o644)
	if _, err := gen.Generate(shopApp(), dir); err == nil || !strings.Contains(err.Error(), "shop_rules.qnt defines notifyEventuallyHandled") {
		t.Errorf("an obligation's name in the rules: %v, want refused", err)
	}
	// a mark in the rules' file, which no binding reads
	os.WriteFile(filepath.Join(dir, "model", "shop_rules.qnt"), []byte("module shopRules {\n  // grist: invariant\n  val x = true\n}\n"), 0o644)
	if _, err := gen.Generate(shopApp(), dir); err == nil || !strings.Contains(err.Error(), "shop_rules.qnt") || !strings.Contains(err.Error(), "mark vals in shop or shopCalls") {
		t.Errorf("a mark in the rules: %v, want refused", err)
	}
}

func requireQuint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		if os.Getenv("GOTOGETHER_STRICT") == "1" {
			t.Fatal("quint is not on PATH (GOTOGETHER_STRICT=1 makes this a failure): npm i -g @informalsystems/quint")
		}
		t.Skip("quint is not on PATH: npm i -g @informalsystems/quint")
	}
}

func quint(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("quint", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint %v: %v\n%s", args, err, out)
	}
}

// An app named grist would have its model at model/grist.qnt, the
// vendored library's file, which every regeneration overwrites: the
// generator refuses it, writing nothing over the app's rules.
func TestGenerateRefusesGristsName(t *testing.T) {
	a := grist.New("grist")
	grist.NewEvent[itemNoted](a, "noted")
	dir := t.TempDir()
	own := filepath.Join(dir, "model", "grist.qnt")
	os.MkdirAll(filepath.Dir(own), 0o755)
	os.WriteFile(own, []byte("// the app's rules\n"), 0o644)
	if _, err := gen.Generate(a, dir); err == nil || !strings.Contains(err.Error(), "rename the app") {
		t.Errorf("Generate of an app named grist: %v, want refused", err)
	}
	if _, err := gen.Starter(a); err == nil {
		t.Error("Starter of an app named grist: no error")
	}
	if err := grist.MainArgs(context.Background(), a, []string{"model", "-dir", dir}, io.Discard, io.Discard); err == nil {
		t.Error("the model command of an app named grist: no error")
	}
	if _, err := gen.Generate(grist.New("Grist"), dir); err == nil {
		t.Error("Generate of an app named Grist (model/Grist.qnt, grist.qnt on a case-insensitive file system): no error")
	}
	if b, _ := os.ReadFile(own); string(b) != "// the app's rules\n" {
		t.Errorf("the app's model was overwritten (%d bytes of grist.qnt)", len(b))
	}
}

// A query whose result has no scalar field (the model compares none) is
// the record { unit: ... }: its Starter stub is one, so an app's first
// `model -init` typechecks.
func TestStarterEmptyResult(t *testing.T) {
	type tags struct {
		Tags []string `json:"tags" grist:"-"`
	}
	a := grist.New("tagger")
	grist.NewEvent[itemNoted](a, "noted")
	grist.NewQuery(a, "tags", func(r *grist.Reads, q struct{}) (tags, error) { return tags{}, nil }, grist.Authorize(grist.Authenticated))
	files, err := gen.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	if b := files["model/tagger_rules.qnt"]; !strings.Contains(string(b), "TagsResult = { unit: true }") {
		t.Errorf("the empty result's stub:\n%s", b)
	}
	requireQuint(t)
	dir := t.TempDir()
	writeFiles(t, dir, files)
	g, err := gen.Generate(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	quint(t, filepath.Join(dir, "model"), "typecheck", "tagger.qnt")
}

// The model command: without the app's model it says how to start one
// (-init), -init writes the starter beside the generated files, naming
// each, and -check fails on a generated file edited by hand, naming it.
func TestMainModel(t *testing.T) {
	dir := t.TempDir()
	model := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := grist.MainArgs(context.Background(), shopApp(), append([]string{"model", "-dir", dir}, args...), &out, io.Discard)
		return out.String(), err
	}
	if _, err := model(); err == nil || !strings.Contains(err.Error(), "shop model -init") {
		t.Errorf("model with no model/shop.qnt: %v, want the fix (-init)", err)
	}
	out, err := model("-init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wrote " + filepath.Join(dir, "model", "shop.qnt"), "wrote " + filepath.Join(dir, "model", "shop_rules.qnt"), "model/shop_gen.qnt", "model/shop_steps.qnt", "shop.binding.yaml", "shop.calls.binding.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("model -init's output %q lacks %q", out, want)
		}
	}
	if _, err := model("-check"); err != nil {
		t.Errorf("model -check right after -init: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "shop.binding.yaml"), []byte("# by hand\n"), 0o644)
	if _, err := model("-check"); err == nil || !strings.Contains(err.Error(), "shop.binding.yaml") {
		t.Errorf("model -check of an edited binding: %v, want it named", err)
	}
}

// tally is a small App for the generator's warnings: a read model of
// counted items, folding the removals too when removals is set, the
// added event with a note when noted is.
func tally(removals, noted bool) *grist.App {
	a := grist.New("tally")
	var added grist.EventName
	if noted {
		added = grist.NewEvent[struct {
			Item string `json:"item"`
			Note string `json:"note"`
		}](a, "added")
	} else {
		added = grist.NewEvent[struct {
			Item string `json:"item"`
		}](a, "added")
	}
	removed := grist.NewEvent[struct {
		Item string `json:"item"`
	}](a, "removed")
	hs := []grist.Handler[int]{onAny(added)}
	if removals {
		hs = append(hs, onAny(removed))
	}
	grist.NewReadModel(a, "count", 1, func() int { return 0 }, hs...)
	return a
}

// onAny counts an event of t, whatever its body.
func onAny(t grist.EventName) grist.Handler[int] {
	switch t := t.(type) {
	case *grist.EventType[struct {
		Item string `json:"item"`
	}]:
		return grist.On(t, func(s int, _ struct {
			Item string `json:"item"`
		}, _ grist.Meta) int {
			return s + 1
		})
	case *grist.EventType[struct {
		Item string `json:"item"`
		Note string `json:"note"`
	}]:
		return grist.On(t, func(s int, _ struct {
			Item string `json:"item"`
			Note string `json:"note"`
		}, _ grist.Meta) int {
			return s + 1
		})
	}
	panic("onAny: an event type of another body")
}

// The generator warns when an input of the app's own rules changed since
// the events file was generated: a read model folding a new event type
// (its fold would skip it silently), an event type whose fields changed.
func TestGenerateWarnsOfChangedInputs(t *testing.T) {
	dir := t.TempDir()
	g, err := gen.Generate(tally(false, false), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Warnings) != 0 {
		t.Fatalf("first generation warned: %v", g.Warnings)
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	if g, err = gen.Generate(tally(false, false), dir); err != nil || len(g.Warnings) != 0 {
		t.Fatalf("regenerating the same catalog: %v %v", g.Warnings, err)
	}
	g, err = gen.Generate(tally(true, true), dir)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(g.Warnings, "\n")
	for _, want := range []string{"countEvents folds removed now", "event added has fields (item_: str, note: str) now, (item_: str)", "folded by countEvents"} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings %q lack %q", all, want)
		}
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	if g, _ = gen.Generate(tally(false, true), dir); !strings.Contains(strings.Join(g.Warnings, "\n"), "countEvents no longer folds removed") {
		t.Errorf("a type dropped: warnings %v", g.Warnings)
	}
	var out bytes.Buffer
	// the command prints them before it fails on the missing rules
	_ = grist.MainArgs(context.Background(), tally(false, true), []string{"model", "-dir", dir}, &out, io.Discard)
	if !strings.Contains(out.String(), "tally model: warning: countEvents no longer folds removed") {
		t.Errorf("the model command's output %q names no warning", out.String())
	}
}
