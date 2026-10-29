// Package gen is grist's generator: an App's model skeleton and its
// bindings from its catalog (docs/grist.md §3.2), the app's first model
// files, and the app's binary's model command (grist.Main's). No user
// calls it: an app runs it through its binary (`<app> model`), and
// gristtest.Generated diffs what it writes.
package gen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
)

// Library is grist/model/grist.qnt, which Generate vendors into an
// application's model/: package grist embeds it and sets it at init
// (read-only after; an embed cannot reach a parent directory, and every
// caller holding an App has imported grist).
var Library []byte

// An App is what the generator reads of a grist.App: its registry's
// check and its catalog.
type App interface {
	Verify() error
	Catalog() Catalog
}

// The catalog's types, by their grist names.
type (
	Catalog          = catalog.Catalog
	CatalogBlock     = catalog.CatalogBlock
	CatalogCall      = catalog.CatalogCall
	CatalogEvent     = catalog.CatalogEvent
	CatalogProcessor = catalog.CatalogProcessor
)

const (
	unknownOutcome = catalog.UnknownOutcome
	AtLeastOnce    = catalog.AtLeastOnce
	AtMostOnce     = catalog.AtMostOnce
)

// QuintName is catalog.QuintName.
func QuintName(name string) (string, error) { return catalog.QuintName(name) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Generated is what Generate writes for an App: its model's skeleton and
// its bindings, from the App's Catalog, beside the application's own
// model files, which it never writes (Starter writes first ones).
//
// The model's files import each other in one direction only, since
// quint's REPL refuses a file that a file it imports imports back
// (QNT101, "Multiple modules conflict on the same name"):
//
//	model/<app>.qnt        the app's: modules <app> and <app>Calls, the invariants (the bindings' modules)
//	model/<app>_steps.qnt  generated: modules <app>Gen and <app>CallsGen, the store's and the clients' actions
//	model/<app>_rules.qnt  the app's: module <app>Rules, the holes the actions call
//	model/<app>_gen.qnt    generated: module <app>Events, the events, reads and records
//	model/grist.qnt        grist's library, vendored
//
// each importing only files below it.
type Generated struct {
	// Files are the generated files by path, relative to the directory
	// given to Generate: grist.lock (every stored version's shape, every
	// read model's fingerprint: LockFile), model/grist.qnt (grist's library, vendored),
	// model/<app>_gen.qnt (the events), model/<app>_steps.qnt (the
	// store's and the clients' actions), <app>.binding.yaml (the store's
	// side), <app>.calls.binding.yaml (the clients') and one
	// <app>.<variant>.binding.yaml per test-only variant the app's model
	// names.
	Files map[string][]byte
	// Holes are the definitions the skeleton calls and the application's
	// rules (model/<app>_rules.qnt, module <app>Rules) must define, each
	// with a stub to start from, in the skeleton's order.
	Holes []Hole
	// Missing are the holes model/<app>_rules.qnt does not define (all of
	// them when there is no such file): the skeleton does not typecheck
	// until they are written.
	Missing []string
	// Warnings are inputs of the app's own rules that changed since the
	// generated events file under dir was written: a read model that
	// folds an event type it did not, an event type whose fields changed;
	// and since grist.lock was: a read model whose fingerprint changed at
	// the same version (its snapshots refolded), an event no longer
	// registered.
	// The app's folds and rules still typecheck, so nothing else would
	// say so: a fold's else branch skips a new type silently. And what
	// the app forgot (forgotten): an event nothing reads, rules whose
	// generated mutants no test runs, commands no acceptance scenario
	// takes; each names its fix.
	Warnings []string
	// Lock says how grist.lock as the catalog makes it differs from the
	// one under dir (none: "there is no grist.lock"), nil when they agree:
	// what a stale lock's diff (Diff, gristtest.Generated, -check) means.
	Lock []string
}

// A Hole is one definition the generated skeleton leaves to the
// application: a rule's predicate (a command's outcome, its events, the
// reads it needs), a query's answer, a processor's events, a site's
// guard, a rule's verdict and the reads it needs, the domains the model
// on its own draws from.
type Hole struct {
	Name string
	// Doc says what it is for, and Stub is a definition that typechecks
	// (a starting point, not a rule).
	Doc, Stub string
}

// The names of the model's files, by the App's name: the generated
// events and steps, the app's rules and invariants.
func genFile(app string) string   { return "model/" + app + "_gen.qnt" }
func stepsFile(app string) string { return "model/" + app + "_steps.qnt" }
func rulesFile(app string) string { return "model/" + app + "_rules.qnt" }
func modelFile(app string) string { return "model/" + app + ".qnt" }

// Generate writes the model skeleton and the bindings of app from its
// Catalog (docs/grist.md §3.2), reading the application's rules at
// dir/model/<app>_rules.qnt for the holes they fill and its model at
// dir/model/<app>.qnt for the vals it marks as invariants (`// grist: invariant [bindings]`, `// grist: eventually
// [bindings]`, `// grist: violable [bindings]` on the line before a val;
// bindings default to the store's default binding, `default`; any other
// name is a test-only variant, <app>.<name>.binding.yaml; in module
// <app>Calls they are the clients' binding's). It returns the files
// without writing them (Generated.Write, Generated.Diff).
//
// What it writes:
//   - module <app>Events: an Event constructor per event type (its
//     scalar fields, in the order a step records them), field accessors,
//     reads (<event>Read, <event>By<Tag>), each read model's selection of
//     the log (<model>Events) and reads (<model>Reads), and a record type
//     per command's, query's and site's arguments;
//   - module <app>Gen, the store's side: the log, checkpoints, the last
//     batch, effect steps, answered ids and one list per site; one action
//     per command (a reused key; a conflict, if it is conditioned; ok and
//     each refusal it declares, with freshKey, covers and conditionHolds;
//     one a rule guards: its outcome its decision's, then, when ok, the
//     first refusal of its rules' holes on what it appends, <command>Ruled,
//     its read set covering what every rule that ran needs),
//     per query, per processor by its declared shape (Emits, or Delivers'
//     effect, checkpoint and outcome actions over grist.qnt's at-least-once
//     or at-most-once defs) and per site, every frame condition, and step;
//     and the obligations the catalog implies, eventual vals named from
//     the processor or call (<processor>EventuallyHandled: every trigger
//     handled; <call>EventuallyServed: every answered call of a Keyed
//     dependency served), which the store's default binding judges at a
//     settle beside the app's own, as does a variant that judges every
//     invariant of the default binding, a call's only where the variant
//     also judges a val reading the call's served records (a site claim:
//     a real tier's third party records none); an app defining one of
//     those names itself is an error naming it;
//   - module <app>CallsGen, the clients' side: no positions, no
//     processors, a key's first answer final, denied answering nothing;
//   - the bindings: every parameter the step's field, the outcome as
//     result, each commit's head and batch and each query's answer
//     compared with the model's (in a variant only if it judges every
//     invariant of the default binding: one that judges only its own vals
//     leaves the observations to the default binding's claims).
//
// What the application writes: module <app>Rules in model/<app>_rules.qnt
// with the holes (Hole), each read model's fold over its generated
// selection, and its own helpers; module <app> in model/<app>.qnt
// importing <app>Gen with its invariants and eventual vals, and module
// <app>Calls importing <app>CallsGen. Regenerating never touches those
// files, so the holes survive it; a new block's hole is reported in
// Missing. A model whose files import each other in a cycle is an error
// naming it (the layout before the rules had a file of their own was
// one: Generate says how to move them).
func Generate(app App, dir string) (Generated, error) {
	if err := app.Verify(); err != nil {
		return Generated{}, err
	}
	c := app.Catalog()
	g, err := newGen(c)
	if err != nil {
		return Generated{}, err
	}
	own, err := readOwn(dir, modelFile(c.Name))
	if err != nil {
		return Generated{}, err
	}
	if err := oldLayout(c.Name, g.aq, own); err != nil {
		return Generated{}, err
	}
	rules, err := readOwn(dir, rulesFile(c.Name))
	if err != nil {
		return Generated{}, err
	}
	g.defined = definedIn(rules, g.aq+"Rules")
	g.bodies = bodiesIn(own, g.aq)
	for n, b := range bodiesIn(rules, g.aq+"Rules") {
		g.bodies[n] = b
	}
	events, steps := g.eventsFile(), g.stepsFile()
	if len(g.clashes) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s:\n  %s", c.Name, strings.Join(g.clashes, "\n  "))
	}
	marks, err := parseMarks(own, g.aq)
	if err != nil {
		return Generated{}, fmt.Errorf("grist model: %s: %w", modelFile(c.Name), err)
	}
	if _, err := parseMarks(rules, g.aq); err != nil {
		return Generated{}, fmt.Errorf("grist model: %s: %w", rulesFile(c.Name), err)
	}
	if m, r := g.ownClashes(own); len(m) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s defines %s, which the generated %s defines now (an obligation the catalog implies, judged by the bindings): delete the app's own, or rename it if it says more",
			modelFile(c.Name), strings.Join(m, ", "), stepsFile(c.Name))
	} else if len(r) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s defines %s, which the generated %s defines (an obligation the catalog implies) and imports the rules beside: rename the rules' def",
			rulesFile(c.Name), strings.Join(r, ", "), stepsFile(c.Name))
	}
	out := Generated{Files: map[string][]byte{
		"model/grist.qnt":              Library,
		genFile(c.Name):                events,
		stepsFile(c.Name):              steps,
		c.Name + ".binding.yaml":       g.binding("default", marks),
		c.Name + ".calls.binding.yaml": g.binding("calls", marks),
	}, Holes: g.holes}
	for _, v := range marks.variants() {
		out.Files[c.Name+"."+v+".binding.yaml"] = g.binding(v, marks)
	}
	for _, h := range g.holes {
		if !g.defined[h.Name] {
			out.Missing = append(out.Missing, h.Name)
		}
	}
	if prev, err := readOwn(dir, genFile(c.Name)); err != nil {
		return Generated{}, err
	} else if prev != nil {
		out.Warnings = inputsChanged(c.Name, prev, events)
	}
	prevLock, err := readOwn(dir, LockFile)
	if err != nil {
		return Generated{}, err
	}
	lock, errs, warnings := lockOf(c, prevLock)
	if len(errs) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s:\n  %s", c.Name, strings.Join(errs, "\n  "))
	}
	out.Files[LockFile] = lock
	out.Warnings = append(out.Warnings, warnings...)
	if !bytes.Equal(prevLock, lock) {
		out.Lock = lockChanges(prevLock, lock)
	}
	ws, errs := forgotten(c, dir, own, rules)
	if len(errs) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s:\n  %s", c.Name, strings.Join(errs, "\n  "))
	}
	out.Warnings = append(out.Warnings, ws...)
	if cycle, err := importCycle(dir, out.Files); err != nil {
		return Generated{}, err
	} else if cycle != nil {
		return Generated{}, fmt.Errorf("grist model: %s import each other in a cycle (%s), which quint's REPL refuses (QNT101: Multiple modules conflict on the same name): each file imports only the ones below it in the model's layout, docs/grist.md §3.2 (the app's rules import grist and the events, never the steps or the invariants)",
			strings.Join(sortedUnique(cycle), ", "), strings.Join(cycle, " -> "))
	}
	return out, nil
}

// readOwn reads one of the application's model files under dir: nil if
// there is none.
func readOwn(dir, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return b, nil
}

// oldLayout is the error, with the move that fixes it, for an app's
// model in the layout before its rules had a file of their own: module
// <app>Rules in model/<app>.qnt, which imported the skeleton that
// imported it back.
func oldLayout(app, aq string, own []byte) error {
	for _, line := range strings.Split(string(own), "\n") {
		if mm := moduleLine.FindStringSubmatch(line); mm != nil && mm[1] == aq+"Rules" {
			return fmt.Errorf("grist model: %s holds module %sRules, in the layout where it and the generated %s imported each other, which quint's REPL refuses: "+
				"move module %sRules, as it is, to %s; in modules %s and %sCalls of %s, import %sRules.* from \"./%s_rules\" and %sGen.* and %sCallsGen.* from \"./%s_steps\" (the events stay in \"./%s_gen\"); then regenerate (`%s model`)",
				modelFile(app), aq, genFile(app), aq, rulesFile(app), aq, aq, modelFile(app), aq, app, aq, aq, app, app, app)
		}
	}
	return nil
}

// importLine is a Quint import from a file (read-only).
var importLine = regexp.MustCompile(`^\s*import\s+.*\bfrom\s+"([^"]+)"`)

// importCycle is a cycle of the model's files' imports, as paths
// relative to dir from a file back to itself, or nil: over files (the
// generated ones, by their path relative to dir) and every other file an
// import reaches under dir.
func importCycle(dir string, files map[string][]byte) ([]string, error) {
	src := func(p string) ([]byte, bool, error) {
		if b, ok := files[p]; ok {
			return b, true, nil
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return b, err == nil, err
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var stack []string
	var visit func(p string) ([]string, error)
	visit = func(p string) ([]string, error) {
		switch state[p] {
		case visiting:
			i := slices.Index(stack, p)
			return append(append([]string(nil), stack[i:]...), p), nil
		case done:
			return nil, nil
		}
		b, ok, err := src(p)
		if err != nil || !ok {
			state[p] = done
			return nil, err
		}
		state[p] = visiting
		stack = append(stack, p)
		for _, line := range strings.Split(string(b), "\n") {
			if m := importLine.FindStringSubmatch(line); m != nil {
				to := path.Join(path.Dir(p), m[1]) + ".qnt"
				if c, err := visit(to); c != nil || err != nil {
					return c, err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[p] = done
		return nil, nil
	}
	var roots []string
	for p := range files {
		if strings.HasSuffix(p, ".qnt") {
			roots = append(roots, p)
		}
	}
	others, _ := filepath.Glob(filepath.Join(dir, "model", "*.qnt"))
	for _, o := range others {
		roots = append(roots, "model/"+filepath.Base(o))
	}
	sort.Strings(roots)
	for _, r := range roots {
		if c, err := visit(r); c != nil || err != nil {
			return c, err
		}
	}
	return nil, nil
}

func sortedUnique(s []string) []string {
	set := map[string]bool{}
	for _, x := range s {
		set[x] = true
	}
	return sortedKeys(set)
}

// Write writes the files under dir.
func (g Generated) Write(dir string) error {
	for _, p := range g.paths() {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, g.Files[p], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Diff lists the files that differ under dir from what was generated (a
// missing one included), and generated binding files of the App under
// dir that were not (a variant whose vals are gone): what a witness test
// fails on, so the checked-in model and bindings are the catalog's.
func (g Generated) Diff(dir string) []string {
	var out []string
	for _, p := range g.paths() {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil || !bytes.Equal(b, g.Files[p]) {
			out = append(out, p)
		}
	}
	for p := range g.Files {
		if strings.HasSuffix(p, ".binding.yaml") {
			app := strings.SplitN(p, ".", 2)[0]
			old, _ := filepath.Glob(filepath.Join(dir, app+".*.binding.yaml"))
			for _, o := range old {
				rel := filepath.Base(o)
				if g.Files[rel] == nil && generatedBinding(o) && !slices.Contains(out, rel) {
					out = append(out, rel)
				}
			}
			break
		}
	}
	sort.Strings(out)
	return out
}

// generatedBinding reports whether the file at path says Generate wrote it.
func generatedBinding(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.HasPrefix(b, []byte(bindingHeader))
}

func (g Generated) paths() []string {
	out := make([]string, 0, len(g.Files))
	for p := range g.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Starter is the application's first model files for app, by path
// relative to the app's directory: model/<app>_rules.qnt, every hole of
// its skeleton stubbed (the stubs typecheck and decide nothing: an ok
// for every command, no events, no reads), and model/<app>.qnt, the
// module the store's binding names and the clients' one, with no
// invariant yet. Main's `model -init` writes each that does not exist.
func Starter(app App) (map[string][]byte, error) {
	if err := app.Verify(); err != nil {
		return nil, err
	}
	c := app.Catalog()
	g, err := newGen(c)
	if err != nil {
		return nil, err
	}
	var r strings.Builder
	fmt.Fprintf(&r, "// The %s app's rules: every hole grist's generated skeleton (%s,\n// grist model) calls, and the read models' folds. Regenerating the\n// skeleton never touches this file.\n\n", c.Name, stepsFile(c.Name))
	fmt.Fprintf(&r, "module %sRules {\n", g.aq)
	fmt.Fprintf(&r, "  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n", g.aq, c.Name)
	for _, h := range g.holes {
		fmt.Fprintf(&r, "\n%s  %s\n", comment("  ", h.Doc), h.Stub)
	}
	r.WriteString("}\n")
	var b strings.Builder
	fmt.Fprintf(&b, "// The %s app's invariants: what its bindings judge, over the generated\n// store's and clients' actions (%s) and the app's rules (%s).\n// Regenerating the skeleton never touches this file.\n\n", c.Name, stepsFile(c.Name), rulesFile(c.Name))
	fmt.Fprintf(&b, "// The store's side (%s.binding.yaml): mark a val `// grist: invariant`\n", c.Name)
	b.WriteString("// or `// grist: eventually` (after a settle) on the line before it, and\n")
	b.WriteString("// regenerate; a binding name after the kind puts it in a test-only variant.\n")
	// the module imports the events and the rules itself, not only through
	// the skeleton's exports: quint's flattener (quint compile, which the
	// validator's Go evaluator runs on a module importing this one from
	// another directory) loses names re-exported twice
	imports := fmt.Sprintf("  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n  import %sRules.* from \"./%s_rules\"\n", g.aq, c.Name, g.aq, c.Name)
	fmt.Fprintf(&b, "module %s {\n%s  import %sGen.* from \"./%s_steps\"\n  export %sGen.*\n}\n\n", g.aq, imports, g.aq, c.Name, g.aq)
	fmt.Fprintf(&b, "// The clients' side (%s.calls.binding.yaml), linearized.\n", c.Name)
	fmt.Fprintf(&b, "module %sCalls {\n%s  import %sCallsGen.* from \"./%s_steps\"\n  export %sCallsGen.*\n}\n", g.aq, imports, g.aq, c.Name, g.aq)
	return map[string][]byte{rulesFile(c.Name): []byte(r.String()), modelFile(c.Name): []byte(b.String())}, nil
}

// gen is one App's generation.
type gen struct {
	c       Catalog
	aq      string // the app's Quint name
	events  map[string]CatalogEvent
	holes   []Hole
	names   map[string]string // a generated top-level name -> what it is, for clashes
	clashes []string
	// defined are the defs of the application's rules (module <app>Rules):
	// a call's Admits is generated only where they do not define it
	defined map[string]bool
	// bodies are the definitions of the application's model (module <app>)
	// and rules, by name: which state each reads (readsRecords)
	bodies map[string]string
}

func newGen(c Catalog) (*gen, error) {
	aq, err := QuintName(c.Name)
	if err != nil {
		return nil, fmt.Errorf("grist model: the app's name %q: %w", c.Name, err)
	}
	if strings.EqualFold(aq, "grist") {
		// its model would be model/grist.qnt, the vendored library's file
		// (on a case-insensitive file system too), which every regeneration
		// overwrites, and its module the library's
		return nil, fmt.Errorf("grist model: an app named %q: its model's file and module would be grist's own library's (model/grist.qnt), which regenerating overwrites: rename the app", c.Name)
	}
	g := &gen{c: c, aq: aq, events: map[string]CatalogEvent{}, names: map[string]string{}, defined: map[string]bool{}}
	var ps []string
	for _, e := range c.Events {
		g.events[e.Name] = e
		if strings.HasPrefix(e.Name, "grist.") {
			continue
		}
		fs, err := scalarFields(e.Body)
		if err != nil {
			ps = append(ps, fmt.Sprintf("event %q: %v", e.Name, err))
		}
		for _, f := range fs {
			if f.tag && f.typ == "int" {
				ps = append(ps, fmt.Sprintf("event %q: tag %s is an integer, whose text the model cannot write: make it a string field", e.Name, f.json))
			}
		}
	}
	for _, b := range append(append([]CatalogBlock(nil), c.Commands...), c.Queries...) {
		if _, err := scalarFields(b.Args); err != nil {
			ps = append(ps, fmt.Sprintf("%s: its arguments: %v", b.Name, err))
		}
		if b.Result != nil && b.Result.Kind() == reflect.Struct {
			if _, err := scalarFields(b.Result); err != nil {
				ps = append(ps, fmt.Sprintf("query %s: its result: %v", b.Name, err))
			}
		} else if b.Result != nil && quintScalar(b.Result) == "" {
			ps = append(ps, fmt.Sprintf("query %s: its result %v is neither a struct nor a scalar: the model compares scalars", b.Name, b.Result))
		}
	}
	for _, p := range c.Processors {
		if p.Shape == "" {
			ps = append(ps, fmt.Sprintf("processor %q declares no result shape: add grist.Emits(...) or grist.Delivers(...) to NewProcessor, so its model's actions follow from it", p.Name))
		}
	}
	for _, s := range c.Sites {
		if _, err := scalarFields(s.Fields); err != nil {
			ps = append(ps, fmt.Sprintf("site %q: %v", s.Name, err))
		}
	}
	ps = append(ps, g.checkCalls()...)
	if len(ps) > 0 {
		return nil, fmt.Errorf("grist model: %s cannot be modelled yet:\n  %s", c.Name, strings.Join(ps, "\n  "))
	}
	g.collectHoles()
	g.claimBlocks()
	return g, nil
}

// upper is s with its first letter upper-cased (a Quint type's name).
func upper(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// claim notes a generated top-level name, and a clash with another
// block's or helper's (Generate's error).
func (g *gen) claim(name, what string) string {
	if prev, ok := g.names[name]; ok && prev != what {
		g.clashes = append(g.clashes, fmt.Sprintf("%s and %s both generate the Quint name %s: rename one", prev, what, name))
	}
	g.names[name] = what
	return name
}

// claimBlocks claims the actions the blocks and the skeleton's helpers
// are, before any helper is named.
func (g *gen) claimBlocks() {
	for _, n := range []string{"appends", "refuses", "readsThrough", "processes", "effectStep", "answers", "denied", "init", "step", "IDS", "KEYS", "nextID"} {
		g.claim(n, "the skeleton's "+n)
	}
	for _, c := range g.c.Commands {
		g.claim(c.Quint, "command "+c.Name)
	}
	for _, q := range g.c.Queries {
		g.claim(q.Quint, "query "+q.Name)
	}
	for _, p := range g.c.Processors {
		g.claim(p.Quint, "processor "+p.Name)
		g.claim(p.Quint+"Effect", "processor "+p.Name)
		g.claim(p.Quint+"Outcome", "processor "+p.Name)
		g.claim(p.Quint+"Skip", "processor "+p.Name)
	}
	for _, s := range g.c.Sites {
		g.claim(s.Quint, "site "+s.Name)
		g.claim(s.Quint+"Records", "site "+s.Name)
	}
	if len(g.c.Rules) > 0 {
		for _, n := range []string{"RuleVerdict", "CommandVerdict", "noRule", "rulesOf"} {
			g.claim(n, "the skeleton's "+n)
		}
		for _, r := range g.c.Rules {
			g.claim(r.Quint+"Ruled", "rule "+r.Name)
		}
		for _, c := range g.c.Commands {
			if len(c.Rules) > 0 {
				g.claim(c.Quint+"Ruled", "command "+c.Name)
			}
		}
	}
	g.claimCalls()
	g.claimObligations()
	for _, h := range g.holes {
		g.claim(h.Name, "the hole "+h.Name)
	}
}

// hole adds a hole (once).
func (g *gen) hole(name, doc, stub string) {
	for _, h := range g.holes {
		if h.Name == name {
			return
		}
	}
	g.holes = append(g.holes, Hole{Name: name, Doc: doc, Stub: stub})
}

func (g *gen) collectHoles() {
	g.hole("STRS", "the strings the model on its own (step: gotogether simulate) draws arguments from: few, so that they collide",
		`pure val STRS: Set[str] = Set("a", "b")`)
	g.hole("INTS", "the integers it draws arguments from", `pure val INTS: Set[int] = 1.to(2)`)
	for _, c := range g.c.Commands {
		args := upper(c.Quint) + "Args"
		outcomes := `"ok"`
		for _, r := range ownRefusals(c) {
			outcomes += ` or "` + r + `"`
		}
		if len(c.Rules) > 0 {
			outcomes += "; the refusals of the rules guarding it (" + strings.Join(c.Rules, ", ") + ") are theirs, after an ok"
		}
		g.hole(c.Quint+"Outcome", fmt.Sprintf("%s's rule: the outcome its decision reaches on log (%s)", c.Name, outcomes),
			fmt.Sprintf(`pure def %sOutcome(log: List[Event], c: %s): str = "ok"`, c.Quint, args))
		g.hole(c.Quint+"Events", fmt.Sprintf("the events %s appends when ok (their causes are stamped by its action)", c.Name),
			fmt.Sprintf(`pure def %sEvents(log: List[Event], c: %s): List[Event] = []`, c.Quint, args))
		g.hole(c.Quint+"Needs", fmt.Sprintf("the reads %s's rule needs for result: its committed step's read set must cover each (covers)", c.Name),
			fmt.Sprintf(`pure def %sNeeds(c: %s, result: str): Set[Read] = Set()`, c.Quint, args))
	}
	for _, r := range g.c.Rules {
		refusals := make([]string, len(r.Refuses))
		for i, x := range r.Refuses {
			refusals[i] = `"` + x + `"`
		}
		g.hole(r.Quint+"Refusal", fmt.Sprintf("rule %s's verdict on e, a %s event a command appends, on log as the command read it: \"\" admits it, else its refusal (%s), which the command's outcome then is", r.Name, r.On, strings.Join(refusals, " or ")),
			fmt.Sprintf(`pure def %sRefusal(log: List[Event], e: Event): str = ""`, r.Quint))
		g.hole(r.Quint+"Needs", fmt.Sprintf("the reads rule %s needs for its verdict refusal on e (\"\": admitted): the read set of every command step it ran in must cover each (covers)", r.Name),
			fmt.Sprintf(`pure def %sNeeds(e: Event, refusal: str): Set[Read] = Set()`, r.Quint))
	}
	for _, q := range g.c.Queries {
		g.hole(q.Quint+"Answer", fmt.Sprintf("%s's answer on log (its result's scalar fields)", q.Name),
			fmt.Sprintf(`pure def %sAnswer(log: List[Event], q: %sArgs): %s = %s`, q.Quint, upper(q.Quint), g.resultType(q), g.zeroResult(q)))
	}
	for _, p := range g.c.Processors {
		switch {
		case p.Shape == "emits":
			g.hole(p.Quint+"Emits", fmt.Sprintf("the events %s emits for the trigger at position trigger, reading prefix (the log through it)", p.Name),
				fmt.Sprintf(`pure def %sEmits(prefix: List[Event], trigger: int): List[Event] = []`, p.Quint))
		case p.Shape == "effect" && p.Success != "":
			g.hole(p.Quint+"Success", fmt.Sprintf("the %s event %s appends after its effect succeeded for the trigger at position trigger (prefix: the log through it)", p.Success, p.Name),
				fmt.Sprintf(`pure def %sSuccess(prefix: List[Event], trigger: int): List[Event] = []`, p.Quint))
		}
	}
	for _, p := range g.c.Processors {
		if p.Shape == "effect" {
			g.hole(p.Quint+"Skips", fmt.Sprintf("whether %s's handler skips the trigger at position trigger (returns Skip: its checkpoint alone, no effect), reading prefix (the log through it)", p.Name),
				fmt.Sprintf(`pure def %sSkips(prefix: List[Event], trigger: int): bool = false`, p.Quint))
		}
	}
	g.callHoles()
	for _, s := range g.c.Sites {
		g.hole(s.Quint+"Admits", fmt.Sprintf("whether the %s site may record r after records (its own rule: a dedupe by key, say)", s.Name),
			fmt.Sprintf(`pure def %sAdmits(records: List[%sRecord], r: %sRecord): bool = true`, s.Quint, upper(s.Quint), upper(s.Quint)))
	}
}

// resultType is a query's answer's Quint type.
func (g *gen) resultType(q CatalogBlock) string {
	if q.Result.Kind() != reflect.Struct {
		return quintScalar(q.Result)
	}
	return upper(q.Quint) + "Result"
}

func zeroOf(typ string) string {
	switch typ {
	case "str":
		return `""`
	case "bool":
		return "false"
	}
	return "0"
}

func (g *gen) zeroResult(q CatalogBlock) string {
	if q.Result.Kind() != reflect.Struct {
		return zeroOf(quintScalar(q.Result))
	}
	fs, _ := scalarFields(q.Result)
	if len(fs) == 0 {
		return "{ unit: true }" // recordType's, and the binding's answer
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.json + ": " + zeroOf(f.typ)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// recordType is a record type of fs (and extra), `{ a: str, ... }`.
func recordType(fs []field, extra ...string) string {
	parts := make([]string, 0, len(fs)+len(extra))
	for _, f := range fs {
		parts = append(parts, f.json+": "+f.typ)
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return "{ unit: bool }" // Quint has no empty record type
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// recordValue is a record of fs from their parameters (and extra).
func recordValue(fs []field, extra ...string) string {
	parts := make([]string, 0, len(fs)+len(extra))
	for _, f := range fs {
		parts = append(parts, f.json+": "+f.param)
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return "{ unit: true }"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func params(fs []field, extra ...string) string {
	parts := make([]string, 0, len(fs)+len(extra))
	for _, f := range fs {
		parts = append(parts, f.param+": "+f.typ)
	}
	return strings.Join(append(parts, extra...), ", ")
}

// comment is text as a line comment at indent, wrapped at 78 columns.
func comment(indent, text string) string {
	var b strings.Builder
	line := indent + "//"
	for _, w := range strings.Fields(text) {
		if len(line)+1+len(w) > 78 && len(line) > len(indent)+2 {
			b.WriteString(line + "\n")
			line = indent + "//"
		}
		line += " " + w
	}
	b.WriteString(line + "\n")
	return b.String()
}

// applied is name applied to args, or name alone without any (Quint
// declares and calls an operator of no parameters without parentheses).
func applied(name, args string) string {
	if args == "" {
		return name
	}
	return name + "(" + args + ")"
}

func paramNames(fs []field, extra ...string) string {
	parts := make([]string, 0, len(fs)+len(extra))
	for _, f := range fs {
		parts = append(parts, f.param)
	}
	return strings.Join(append(parts, extra...), ", ")
}

const genHeader = "// Generated by grist model from the %s App's catalog (docs/grist.md §3.2):\n" +
	"// do not edit. Regenerate with the app's binary (`<app> model`, grist.Main)\n" +
	"// after changing a block; the application's rules are model/%s_rules.qnt's,\n" +
	"// its invariants model/%s.qnt's.\n"

// eventsFile is model/<app>_gen.qnt: the events, which the app's rules
// import.
func (g *gen) eventsFile() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, genHeader, g.c.Name, g.c.Name, g.c.Name)
	g.eventsModule(&b)
	return []byte(b.String())
}

// stepsFile is model/<app>_steps.qnt: the store's and the clients'
// actions, which import the app's rules (and the app's invariants
// import them).
func (g *gen) stepsFile() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, genHeader, g.c.Name, g.c.Name, g.c.Name)
	b.WriteString("\n")
	g.storeModule(&b)
	b.WriteString("\n")
	g.callsModule(&b)
	return []byte(b.String())
}

func (g *gen) eventsModule(b *strings.Builder) {
	fmt.Fprintf(b, "\n// The events, reads and argument records: what the rules (module %sRules)\n// and the invariants are written with.\nmodule %sEvents {\n", g.aq, g.aq)
	b.WriteString("  import grist.* from \"./grist\"\n  export grist.*\n")
	for _, e := range g.c.Events {
		if strings.HasPrefix(e.Name, "grist.") {
			continue
		}
		eq, _ := QuintName(e.Name)
		fs, _ := scalarFields(e.Body)
		var strs, ints, tags []string
		for _, f := range fs {
			switch f.typ {
			case "str":
				strs = append(strs, f.param)
			case "int":
				ints = append(ints, f.param)
			case "bool":
				ints = append(ints, "if ("+f.param+") 1 else 0")
			}
			if f.tag {
				v := f.param
				if f.typ == "bool" {
					v = `if (` + f.param + `) "true" else "false"`
				}
				tags = append(tags, fmt.Sprintf("(%q, %s)", f.json, v))
			}
		}
		fmt.Fprintf(b, "\n  // %s (strs: its strings, ints: its integers and booleans, in field order)\n", e.Name)
		head := "pure def " + g.claim(eq, "event "+e.Name) + "(" + params(fs) + ")"
		if len(fs) == 0 {
			head = "pure val " + eq
		}
		fmt.Fprintf(b, "  %s: Event =\n    { typ: %q, strs: [%s], ints: [%s], tags: Set(%s), cause: UNCAUSED }\n",
			head, e.Name, strings.Join(strs, ", "), strings.Join(ints, ", "), strings.Join(tags, ", "))
		si, ii := 0, 0
		for _, f := range fs {
			acc := g.claim(eq+upper(f.json), "event "+e.Name+"'s field "+f.json)
			switch f.typ {
			case "str":
				fmt.Fprintf(b, "  pure def %s(e: Event): str = e.strs[%d]\n", acc, si)
				si++
			case "int":
				fmt.Fprintf(b, "  pure def %s(e: Event): int = e.ints[%d]\n", acc, ii)
				ii++
			case "bool":
				fmt.Fprintf(b, "  pure def %s(e: Event): bool = e.ints[%d] == 1\n", acc, ii)
				ii++
			}
		}
		fmt.Fprintf(b, "  pure val %s: Read = { typ: %q, tags: Set() }\n", g.claim(eq+"Read", "event "+e.Name), e.Name)
		for _, f := range fs {
			if f.tag {
				fmt.Fprintf(b, "  pure def %s(v: str): Read = { typ: %q, tags: Set((%q, v)) }\n", g.claim(eq+"By"+upper(f.json), "event "+e.Name), e.Name, f.json)
			}
		}
	}
	for _, rm := range g.c.ReadModels {
		rq, err := QuintName(rm.Name)
		if err != nil {
			continue // a read model named so is Verify's to refuse; it has no helpers
		}
		types := make([]string, len(rm.Types))
		for i, t := range rm.Types {
			types[i] = fmt.Sprintf("%q", t)
		}
		var ps, tags []string
		for _, k := range rm.Keys {
			p := k
			if catalog.QuintTaken[k] || !catalog.QuintIdent.MatchString(k) {
				p = "k" + fmt.Sprint(len(ps)+1)
			}
			ps = append(ps, p+": str")
			tags = append(tags, fmt.Sprintf("(%q, %s)", k, p))
		}
		kind := "plain: every event of its types"
		if len(rm.Keys) > 0 {
			kind = "keyed by " + strings.Join(rm.Keys, ", ") + ": the events of its types carrying those tags"
		}
		b.WriteString("\n" + comment("  ", fmt.Sprintf("read model %s (%s): what it folds, and what a read of it records", rm.Name, kind)))
		cond := fmt.Sprintf("Set(%s).contains(e.typ)", strings.Join(types, ", "))
		for _, t := range tags {
			cond += " and e.tags.contains(" + t + ")"
		}
		fmt.Fprintf(b, "  pure def %s(%s): List[Event] = log.select(e => %s)\n", g.claim(rq+"Events", "read model "+rm.Name), strings.Join(append([]string{"log: List[Event]"}, ps...), ", "), cond)
		reads := make([]string, len(rm.Types))
		for i, t := range rm.Types {
			reads[i] = fmt.Sprintf("{ typ: %q, tags: Set(%s) }", t, strings.Join(tags, ", "))
		}
		if len(ps) == 0 {
			fmt.Fprintf(b, "  pure val %s: Set[Read] = Set(%s)\n", g.claim(rq+"Reads", "read model "+rm.Name), strings.Join(reads, ", "))
		} else {
			fmt.Fprintf(b, "  pure def %s(%s): Set[Read] = Set(%s)\n", g.claim(rq+"Reads", "read model "+rm.Name), strings.Join(ps, ", "), strings.Join(reads, ", "))
		}
	}
	b.WriteString("\n  // each command's and query's arguments, with the caller that sent it\n")
	for _, c := range append(append([]CatalogBlock(nil), g.c.Commands...), g.c.Queries...) {
		fs, _ := scalarFields(c.Args)
		fmt.Fprintf(b, "  type %s = %s\n", g.claim(upper(c.Quint)+"Args", c.Name), recordType(fs, "caller: str"))
	}
	for _, q := range g.c.Queries {
		if q.Result.Kind() == reflect.Struct {
			fs, _ := scalarFields(q.Result)
			fmt.Fprintf(b, "  type %s = %s\n", g.claim(upper(q.Quint)+"Result", q.Name), recordType(fs))
		}
	}
	for _, s := range g.c.Sites {
		fs, _ := scalarFields(s.Fields)
		fmt.Fprintf(b, "  // site %s: a record of the effect, made where it takes place\n  type %s = %s\n", s.Name, g.claim(upper(s.Quint)+"Record", "site "+s.Name), recordType(fs))
	}
	g.callTypes(b)
	b.WriteString("}\n")
}

// vars are the store side's variables and their initial values.
func (g *gen) vars() [][2]string {
	cps := make([]string, len(g.c.Processors))
	for i, p := range g.c.Processors {
		cps[i] = fmt.Sprintf("%q -> 0", p.Name)
	}
	vs := [][2]string{{"log", "[]"}, {"checkpoints", "Map(" + strings.Join(cps, ", ") + ")"}, {"appended", "[]"},
		{"effects", "[]"}, {"answered", "Set()"}, {"rewound", "false"}}
	for _, s := range g.c.Sites {
		vs = append(vs, [2]string{s.Quint + "Records", "[]"})
	}
	return append(vs, g.callVars()...)
}

// frame is every variable's next value: set's where it names one, else
// unchanged; one per line at indent.
func (g *gen) frame(indent string, set map[string]string) string {
	var b strings.Builder
	for _, v := range g.vars() {
		next, ok := set[v[0]]
		if !ok {
			next = v[0]
		}
		fmt.Fprintf(&b, "%s%s' = %s,\n", indent, v[0], next)
	}
	return b.String()
}

func (g *gen) storeModule(b *strings.Builder) {
	fmt.Fprintf(b, "// The store's side (%s.binding.yaml): every step where it took effect, one\n", g.c.Name)
	b.WriteString("// tenant's log. Commits are relayed from the store's step log, refusals\n// recorded at their commit, queries on return, effects by their processors,\n// sites by the dependencies that make them.\n")
	fmt.Fprintf(b, "module %sGen {\n", g.aq)
	fmt.Fprintf(b, "  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n  import %sRules.* from \"./%s_rules\"\n", g.aq, g.c.Name, g.aq, g.c.Name)
	fmt.Fprintf(b, "  export grist.*\n  export %sEvents.*\n  export %sRules.*\n\n", g.aq, g.aq)
	b.WriteString("  // one tenant's log\n  var log: List[Event]\n  // each processor's checkpoint: the last trigger it handled\n  var checkpoints: str -> int\n")
	b.WriteString("  // the batch the last step appended (what its observations are checked against)\n  var appended: List[Event]\n")
	b.WriteString("  // the effect steps: (processor, trigger, key, ok)\n  var effects: List[Effect]\n")
	b.WriteString("  // the command ids answered so far, by a commit or a committed refusal\n  var answered: Set[str]\n")
	b.WriteString("  // the log went backwards (gristRewound) and no operator has resumed the\n  // processors since (gristResumed): they take no step\n  var rewound: bool\n")
	for _, s := range g.c.Sites {
		fmt.Fprintf(b, "  // what the %s site recorded, in order\n  var %sRecords: List[%sRecord]\n", s.Name, s.Quint, upper(s.Quint))
	}
	g.callVarDecls(b)
	b.WriteString("\n  action init = all {\n")
	for _, v := range g.vars() {
		fmt.Fprintf(b, "    %s' = %s,\n", v[0], v[1])
	}
	b.WriteString("  }\n\n")
	b.WriteString("  // the identities, in the order commands take them, and the effect keys the\n  // model on its own draws\n")
	b.WriteString("  pure val IDS = " + idList + "\n  pure val KEYS = Set(\"k1\", \"k2\", \"k3\")\n  // the next identity a command answers (none once all are): the first\n  // unanswered, so that the model on its own never draws a used one (a\n  // command that would be disabled) nor two that differ only by name\n  pure def nextID(answered: Set[str]): Set[str] =\n    IDS.foldl(Set(), (s, i) => if (s == Set() and not(answered.contains(i))) Set(i) else s)\n\n")
	b.WriteString("  // a command's commit of b, answering id\n  action appends(b: List[Event], id: str): bool = all {\n")
	b.WriteString(g.frame("    ", map[string]string{"log": "log.concat(b)", "appended": "b", "answered": "answered.union(Set(id))"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // a committed refusal: its marker answers id, nothing is appended; it\n  // commits at the head, at or after the position it read through\n")
	b.WriteString("  action refuses(readPos: int, id: str): bool = all {\n    readPos <= log.length(),\n    appends([], id),\n  }\n\n")
	b.WriteString("  // a step that commits nothing (a query, a reused key): it read through\n  // readPos, the log's length where it is ordered (2·readPos+1)\n")
	b.WriteString("  action readsThrough(readPos: int): bool = all {\n    log.length() == readPos,\n")
	b.WriteString(g.frame("    ", map[string]string{"appended": "[]"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // a processor's handling of trigger, appending b with its checkpoint\n  action processes(b: List[Event], p: str, trigger: int): bool = all {\n    not(rewound),\n")
	b.WriteString(g.frame("    ", map[string]string{"log": "log.concat(b)", "appended": "b", "checkpoints": "checkpoints.set(p, trigger)"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // an effect step of processor p for trigger, under key (the same on every\n  // effect step of the trigger, no other trigger's: keyStable)\n")
	b.WriteString("  action effectStep(p: str, trigger: int, key: str, ok: bool): bool = all {\n    not(rewound),\n    keyStable(effects, p, trigger, key),\n")
	b.WriteString(g.frame("    ", map[string]string{"appended": "[]", "effects": "effects.append((p, trigger, key, ok))"}))
	b.WriteString("  }\n")
	b.WriteString("\n  // the log went backwards (grist.ErrRewound: a failover lost its tail, a\n  // restore put a backup back), as a node's relay saw it: back to position\n  // pos, each processor's checkpoint there cps, the ids answered after it lost. The\n  // processors stop until an operator resumes them (gristResumed); what the\n  // dependencies and the effects did stays done\n")
	b.WriteString("  action gristRewound(pos: int, cps: str -> int, lost: Set[str]): bool = all {\n    pos <= log.length(),\n    cps.keys() == checkpoints.keys(),\n")
	b.WriteString(g.frame("    ", map[string]string{"log": "log.slice(0, pos)", "checkpoints": "cps", "appended": "[]", "answered": "answered.exclude(lost)", "rewound": "true"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // an operator's resume (`<app> resume`): the processors go on from their\n  // checkpoints, and the effect steps and calls of the triggers after them\n  // are forgotten: those triggers are handled again (at least once, under\n  // the same key, or a key of its own where the position was given back\n  // and filled by another event; at most once, again: the operator's\n  // decision). What the dependencies applied stays (their records)\n")
	b.WriteString("  action gristResumed: bool = all {\n")
	resumed := map[string]string{"appended": "[]", "rewound": "false", "effects": "effects.select(e => e._2 <= checkpoints.get(e._1))"}
	for _, c := range g.calls() {
		resumed[c.Quint+"Calls"] = c.Quint + "Calls.select(c => c.trigger <= checkpoints.get(c.processor))"
	}
	b.WriteString(g.frame("    ", resumed))
	b.WriteString("  }\n")
	g.rulesDefs(b)
	for _, c := range g.c.Commands {
		g.command(b, c)
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		fmt.Fprintf(b, "\n  // %s: reads through readPos, changes nothing; its answer is %sAnswer's\n  // at its position (the binding's expect)\n", q.Name, q.Quint)
		fmt.Fprintf(b, "  action %s(%s): bool = readsThrough(readPos)\n", q.Quint, params(fs, "readPos: int"))
	}
	for _, p := range g.c.Processors {
		g.processor(b, p)
	}
	for _, s := range g.c.Sites {
		fs, _ := scalarFields(s.Fields)
		fmt.Fprintf(b, "\n  // the %s site's record, made where its effect took place: any number,\n  // in any order with the steps that asked for them, as %sAdmits allows\n", s.Name, s.Quint)
		fmt.Fprintf(b, "  action %s: bool = {\n    val rec_: %sRecord = %s\n    all {\n      %sAdmits(%sRecords, rec_),\n",
			applied(s.Quint, params(fs)), upper(s.Quint), recordValue(fs), s.Quint, s.Quint)
		b.WriteString(g.frame("      ", map[string]string{"appended": "[]", s.Quint + "Records": s.Quint + "Records.append(rec_)"}))
		b.WriteString("    }\n  }\n")
	}
	g.callActions(b)
	g.obligationVals(b)
	g.storeStep(b)
	b.WriteString("}\n")
}

func (g *gen) command(b *strings.Builder, c CatalogBlock) {
	fs, _ := scalarFields(c.Args)
	args := upper(c.Quint) + "Args"
	cond := "conditioned on its reads"
	if c.Unconditioned != "" {
		cond = "Unconditioned (" + c.Unconditioned + "): its reads are advisory, its precondition is the rule's alone"
	}
	refusals := "no refusal"
	if len(c.Refuses) > 0 {
		refusals = "refusals " + strings.Join(c.Refuses, ", ")
	}
	doc := fmt.Sprintf("%s: %s; %s. Every committed outcome needs its id fresh and its read set covering what its rule needs; ok and a refusal need the rule's outcome", c.Name, cond, refusals)
	ruled := len(c.Rules) > 0
	if ruled {
		doc = fmt.Sprintf("%s: %s; %s. Its outcome is its decision's, then, when that is ok, the first refusal of the rules guarding it (%s, %sRuled); every committed outcome needs its id fresh and its read set covering what its decision and every rule that ran need; ok and a refusal need that outcome", c.Name, cond, refusals, strings.Join(c.Rules, ", "), c.Quint)
	}
	if c.Unconditioned == "" {
		doc += " and the condition to have held, a conflict needs it not to"
	}
	b.WriteString("\n" + comment("  ", doc+". A reused key commits nothing."))
	fmt.Fprintf(b, "  action %s(%s): bool = {\n", c.Quint, params(fs, "id: str", "caller: str", "readPos: int", "reads: Set[(str, str, str)]", "result: str"))
	fmt.Fprintf(b, "    val args_: %s = %s\n    val reads_ = readsOf(reads)\n", args, recordValue(fs, "caller: caller"))
	if ruled {
		fmt.Fprintf(b, "    val verdict_ = %sRuled(log, args_)\n", c.Quint)
	}
	b.WriteString("    if (result == \"idempotency-key-reused\") all { answered.contains(id), readsThrough(readPos) }\n    else all {\n")
	b.WriteString("      freshKey(answered, id),\n")
	if ruled {
		// a rule's refusal comes after an ok decision, whose reads are ok's
		fmt.Fprintf(b, "      %sNeeds(args_, if (result != \"conflict\" and verdict_.decided == \"ok\") \"ok\" else result).forall(n => covers(reads_, n)),\n", c.Quint)
	} else {
		fmt.Fprintf(b, "      %sNeeds(args_, result).forall(n => covers(reads_, n)),\n", c.Quint)
	}
	commit := fmt.Sprintf("result == %sOutcome(log, args_),\n        if (result == \"ok\") appends(caused(%sEvents(log, args_), (\"command\", id)), id)\n        else refuses(readPos, id),\n", c.Quint, c.Quint)
	if ruled {
		commit = fmt.Sprintf("result == verdict_.outcome,\n        verdict_.needs.forall(n => covers(reads_, n)),\n        if (result == \"ok\") appends(caused(%sEvents(log, args_), (\"command\", id)), id)\n        else refuses(readPos, id),\n", c.Quint)
	}
	if c.Unconditioned != "" {
		fmt.Fprintf(b, "      0 <= readPos,\n      %s", strings.ReplaceAll(commit, "\n        ", "\n      "))
	} else {
		b.WriteString("      if (result == \"conflict\") all { not(conditionHolds(log, readPos, reads_)), refuses(readPos, id) }\n      else all {\n")
		fmt.Fprintf(b, "        conditionHolds(log, readPos, reads_),\n        %s      },\n", commit)
	}
	b.WriteString("    }\n  }\n")
}

func (g *gen) processor(b *strings.Builder, p CatalogProcessor) {
	next := fmt.Sprintf("nextOf(log, checkpoints.get(%q), %q)", p.Name, p.On)
	cause := fmt.Sprintf("(\"processor\", %q)", p.Name)
	isTrigger := fmt.Sprintf("trigger > 0 and trigger <= log.length() and log[trigger - 1].typ == %q", p.On)
	success := "[]"
	if p.Success != "" {
		success = fmt.Sprintf("%sSucceeded(log, trigger)", p.Quint)
		fmt.Fprintf(b, "\n  // %s's success event for trigger, caused by it (%sSuccess's)\n  pure def %s(log: List[Event], trigger: int): List[Event] =\n    caused(%sSuccess(log.slice(0, trigger), trigger), %s)\n",
			p.Name, p.Quint, g.claim(p.Quint+"Succeeded", "processor "+p.Name), p.Quint, cause)
	}
	unchanged := g.frame("    ", map[string]string{"appended": "[]"})
	effects := fmt.Sprintf("not(%sSkips(log.slice(0, trigger), trigger))", p.Quint)
	if p.Shape == "effect" {
		fmt.Fprintf(b, "\n  // %s skips a trigger its rule says it skips (%sSkips: the handler's Skip),\n  // its checkpoint alone, no effect step; a handler's Fail is recorded so\n  // too, appending grist.failed, which a skip's empty batch does not match\n", p.Name, p.Quint)
		fmt.Fprintf(b, "  action %sSkip(trigger: int): bool = all {\n    trigger > 0,\n    trigger == %s,\n    %sSkips(log.slice(0, trigger), trigger),\n    triesOf(effects, %q, trigger) == 0,\n    processes([], %q, trigger),\n  }\n",
			p.Quint, next, p.Quint, p.Name, p.Name)
	}
	switch {
	case p.Shape == "emits":
		fmt.Fprintf(b, "\n  // %s (Emits): on each %s, %sEmits of the log through the trigger,\n  // with its checkpoint (whatever came after the trigger)\n", p.Name, p.On, p.Quint)
		fmt.Fprintf(b, "  action %s(trigger: int): bool = all {\n    trigger > 0,\n    trigger == %s,\n    processes(caused(%sEmits(log.slice(0, trigger), trigger), %s), %q, trigger),\n  }\n",
			p.Quint, next, p.Quint, cause, p.Name)
	case p.Guarantee == AtLeastOnce:
		fmt.Fprintf(b, "\n  // %s's effect, at least once: any number of tries for its next trigger\n  // before its checkpoint, every one under the trigger's key\n", p.Name)
		fmt.Fprintf(b, "  action %sEffect(trigger: int, key: str, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    %s,\n    effectStep(%q, trigger, key, ok),\n  }\n", p.Quint, next, effects, p.Name)
		fmt.Fprintf(b, "\n  // %s's checkpoint, only after effect steps: its success after an ok one,\n  // grist.failed after %d failed ones, whichever the run committed (ok: its\n  // record of which)\n", p.Name, p.EffectTries)
		fmt.Fprintf(b, "  action %s(trigger: int, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    atLeastOnceDone(effects, %q, trigger, ok, %d),\n    processes(atLeastOnceOutcome(%q, trigger, ok, %s), %q, trigger),\n  }\n",
			p.Quint, next, p.Name, p.EffectTries, p.Name, success, p.Name)
		fmt.Fprintf(b, "\n  // %s is at least once: its outcome commits with its checkpoint, never\n  // alone, so an outcome step of its own is no step of this model\n", p.Name)
		fmt.Fprintf(b, "  action %sOutcome(trigger: int, ok: bool): bool = all {\n    false,\n%s  }\n", p.Quint, unchanged)
	default: // at most once
		fmt.Fprintf(b, "\n  // %s, at most once: its checkpoint first (ok false, nothing appended),\n  // before any effect step of its trigger\n", p.Name)
		fmt.Fprintf(b, "  action %s(trigger: int, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    %s,\n    atMostOnceFirst(effects, %q, trigger, ok),\n    processes([], %q, trigger),\n  }\n",
			p.Quint, next, effects, p.Name, p.Name)
		fmt.Fprintf(b, "\n  // %s's effect: once, after its checkpoint, or never (a crash between\n  // the two loses it, by right)\n", p.Name)
		fmt.Fprintf(b, "  action %sEffect(trigger: int, key: str, ok: bool): bool = all {\n    %s,\n    %s,\n    atMostOnceEffect(effects, checkpoints.get(%q), %q, trigger),\n    effectStep(%q, trigger, key, ok),\n  }\n",
			p.Quint, isTrigger, effects, p.Name, p.Name, p.Name)
		fmt.Fprintf(b, "\n  // %s's outcome, if present (a crash after the effect loses it): its\n  // success after the one ok effect step; after a failed one grist.failed\n  // when the failure is known, else grist.unknown with the error\n", p.Name)
		fmt.Fprintf(b, "  action %sOutcome(trigger: int, ok: bool, known: bool, error: str): bool = {\n    val isTrigger = %s\n    val success = if (isTrigger) %s else []\n",
			p.Quint, isTrigger, success)
		fmt.Fprintf(b, "    all {\n      isTrigger,\n      not(rewound),\n      atMostOnceDone(log, effects, %q, trigger, ok, success),\n      val b = atMostOnceOutcome(%q, trigger, ok, known, error, success)\n      all {\n", p.Name, p.Name)
		b.WriteString(g.frame("        ", map[string]string{"log": "log.concat(b)", "appended": "b"}))
		b.WriteString("      },\n    }\n  }\n")
	}
}

// domainOf is the set the model on its own draws a field of typ from.
func domainOf(typ string) string {
	return map[string]string{"str": "STRS", "int": "INTS", "bool": "Bool"}[typ]
}

// draws are the nondet picks of one branch of a step: one per field name
// and type (a name of two types is suffixed by the type), in first-use
// order.
type draws struct {
	order []string
	dom   map[string]string
	by    map[[2]string]string // (param, type) -> pick
}

func newDraws() *draws { return &draws{dom: map[string]string{}, by: map[[2]string]string{}} }

// add draws the picks of fs, returning them as fields named by the pick.
func (d *draws) add(fs []field) []field {
	out := make([]field, len(fs))
	for i, f := range fs {
		k := [2]string{f.param, f.typ}
		name, ok := d.by[k]
		if !ok {
			name = f.param
			if prev, taken := d.dom[name]; taken && prev != domainOf(f.typ) {
				name = f.param + "_" + f.typ
			}
			d.by[k] = name
			if _, seen := d.dom[name]; !seen {
				d.dom[name] = domainOf(f.typ)
				d.order = append(d.order, name)
			}
		}
		out[i] = f
		out[i].param = name
	}
	return out
}

func (d *draws) pick(name, domain string) {
	if _, seen := d.dom[name]; !seen {
		d.dom[name] = domain
		d.order = append(d.order, name)
	}
}

// branch is call under its own picks: a block drawing them, or call
// alone when it draws none.
func (d *draws) branch(call string) string {
	if len(d.order) == 0 {
		return call
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, n := range d.order {
		fmt.Fprintf(&b, "        nondet %s = %s.oneOf()\n", n, d.dom[n])
	}
	fmt.Fprintf(&b, "        %s\n      }", strings.ReplaceAll(call, "\n", "\n  "))
	return b.String()
}

// commandCall is the step's call of command c with its picks fs: its
// rule's outcome and the reads that outcome needs.
func commandCall(c CatalogBlock, fs []field, extra ...string) string {
	if len(c.Rules) > 0 {
		return fmt.Sprintf("{\n        val args_: %sArgs = %s\n        val verdict_ = %sRuled(log, args_)\n        val result_ = verdict_.outcome\n        %s\n      }",
			upper(c.Quint), recordFrom(c, fs), c.Quint, applied(c.Quint, paramNames(fs, extra...)))
	}
	return fmt.Sprintf("{\n        val args_: %sArgs = %s\n        val result_ = %sOutcome(log, args_)\n        %s\n      }",
		upper(c.Quint), recordFrom(c, fs), c.Quint, applied(c.Quint, paramNames(fs, extra...)))
}

// ownRefusals are c's refusals but those only its rules add.
func ownRefusals(c CatalogBlock) []string {
	var out []string
	for _, r := range c.Refuses {
		if !slices.Contains(c.RuleRefuses, r) {
			out = append(out, r)
		}
	}
	return out
}

// rulesDefs writes the rules' verdicts, when the App has rules: each
// rule's on one event after the ones before it, every rule's on a batch
// (rulesOf), and each guarded command's outcome (<command>Ruled).
func (g *gen) rulesDefs(b *strings.Builder) {
	if len(g.c.Rules) == 0 {
		return
	}
	b.WriteString("\n" + comment("  ", "The rules (grist.NewRule), run on the events an ok decision appends, on its reads, before the append: their verdicts so far (the first refusal, \"\" for none, and the reads of every rule that ran), and a guarded command's (its decision's outcome, then its rules')"))
	b.WriteString("  type RuleVerdict = { refusal: str, needs: Set[Read] }\n  type CommandVerdict = { decided: str, outcome: str, needs: Set[Read] }\n")
	b.WriteString("  pure val noRule: RuleVerdict = { refusal: \"\", needs: Set() }\n")
	for _, r := range g.c.Rules {
		guard := fmt.Sprintf("acc.refusal != \"\" or e.typ != %q", r.On)
		fmt.Fprintf(b, "\n  // rule %s on %s, after acc: no verdict once one refused\n", r.Name, r.On)
		fmt.Fprintf(b, "  pure def %sRuled(log: List[Event], e: Event, acc: RuleVerdict): RuleVerdict =\n", r.Quint)
		fmt.Fprintf(b, "    val refusal = if (%s) \"\" else %sRefusal(log, e)\n", guard, r.Quint)
		fmt.Fprintf(b, "    if (%s) acc\n    else { refusal: refusal, needs: acc.needs.union(%sNeeds(e, refusal)) }\n", guard, r.Quint)
	}
	apply := "acc"
	for _, r := range g.c.Rules {
		apply = fmt.Sprintf("%sRuled(log, e, %s)", r.Quint, apply)
	}
	b.WriteString("\n" + comment("  ", "the rules on b's events, in batch order, an event's in registration order: the first refusal wins"))
	fmt.Fprintf(b, "  pure def rulesOf(log: List[Event], b: List[Event]): RuleVerdict =\n    b.foldl(noRule, (acc, e) => %s)\n", apply)
	for _, c := range g.c.Commands {
		if len(c.Rules) == 0 {
			continue
		}
		b.WriteString("\n" + comment("  ", fmt.Sprintf("%s's outcome: its decision's (%sOutcome), then, when ok, the first refusal of its rules (%s) on what it appends (%sEvents)", c.Name, c.Quint, strings.Join(c.Rules, ", "), c.Quint)))
		fmt.Fprintf(b, "  pure def %sRuled(log: List[Event], c: %sArgs): CommandVerdict =\n", c.Quint, upper(c.Quint))
		fmt.Fprintf(b, "    val decided = %sOutcome(log, c)\n    val ruled = rulesOf(log, if (decided == \"ok\") %sEvents(log, c) else [])\n", c.Quint, c.Quint)
		b.WriteString("    { decided: decided, outcome: if (ruled.refusal != \"\") ruled.refusal else decided, needs: ruled.needs }\n")
	}
}

// idList is the identities the model on its own gives commands, in turn:
// one per command, as many as simulate's default steps, so that a trace
// never runs out of them. A refusal past a limit needs every command
// before it (a library's limit of 3: a join, two books, three borrows,
// then the refused one): with four, as there once were, the library's
// model could never refuse it, and with ten a trace's refusals spent
// them before it could (simulate's possibles, P3).
var idList = func() string { // read-only
	ids := make([]string, 30)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", fmt.Sprintf("i%d", i+1))
	}
	return "[" + strings.Join(ids, ", ") + "]"
}()

// recordFrom is c's arguments' record from the picks fs.
func recordFrom(c CatalogBlock, fs []field) string {
	orig, _ := scalarFields(c.Args)
	parts := make([]string, 0, len(fs)+1)
	for i, f := range fs {
		parts = append(parts, orig[i].json+": "+f.param)
	}
	return "{ " + strings.Join(append(parts, "caller: caller"), ", ") + " }"
}

func (g *gen) storeStep(b *strings.Builder) {
	b.WriteString("\n  // the model on its own (gotogether simulate): every command as its rule\n  // decides, reading what it needs, every query, processor and site, each\n  // drawing its own arguments (one draw shared by every block would make\n  // the step's successors the product of all their domains)\n  action step = {\n")
	var calls []string
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		d := newDraws()
		ps := d.add(fs)
		d.pick("caller", "STRS")
		d.pick("id", "nextID(answered)")
		reads := fmt.Sprintf("triplesOf(%sNeeds(args_, result_))", c.Quint)
		if len(c.Rules) > 0 {
			reads = fmt.Sprintf("triplesOf(%sNeeds(args_, if (verdict_.decided == \"ok\") \"ok\" else result_).union(verdict_.needs))", c.Quint)
		}
		calls = append(calls, d.branch(commandCall(c, ps, "id", "caller", "log.length()", reads, "result_")))
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		d := newDraws()
		calls = append(calls, d.branch(q.Quint+"("+paramNames(d.add(fs), "log.length()")+")"))
	}
	for _, p := range g.c.Processors {
		next := fmt.Sprintf("nextOf(log, checkpoints.get(%q), %q)", p.Name, p.On)
		d := newDraws()
		switch {
		case p.Shape == "emits":
			calls = append(calls, fmt.Sprintf("%s(%s)", p.Quint, next))
		case p.Guarantee == AtLeastOnce:
			calls = append(calls, fmt.Sprintf("%sSkip(%s)", p.Quint, next))
			d.pick("key_", "KEYS")
			d.pick("ok_", "Bool")
			calls = append(calls, d.branch(fmt.Sprintf("%sEffect(%s, key_, ok_)", p.Quint, next)))
			d = newDraws()
			d.pick("ok_", "Bool")
			calls = append(calls, d.branch(fmt.Sprintf("%s(%s, ok_)", p.Quint, next)))
		default:
			calls = append(calls, fmt.Sprintf("%sSkip(%s)", p.Quint, next))
			calls = append(calls, fmt.Sprintf("%s(%s, false)", p.Quint, next))
			d.pick("trigger_", "1.to(log.length() + 1)")
			d.pick("key_", "KEYS")
			d.pick("ok_", "Bool")
			calls = append(calls, d.branch(fmt.Sprintf("%sEffect(trigger_, key_, ok_)", p.Quint)))
			d = newDraws()
			d.pick("trigger_", "1.to(log.length() + 1)")
			d.pick("ok_", "Bool")
			d.pick("known_", "Bool")
			calls = append(calls, d.branch(fmt.Sprintf("%sOutcome(trigger_, ok_, known_, \"lost\")", p.Quint)))
		}
	}
	for _, s := range g.c.Sites {
		fs, _ := scalarFields(s.Fields)
		d := newDraws()
		calls = append(calls, d.branch(applied(s.Quint, paramNames(d.add(fs)))))
	}
	calls = append(calls, g.callSteps()...)
	b.WriteString("    any {\n")
	for _, c := range calls {
		fmt.Fprintf(b, "      %s,\n", c)
	}
	b.WriteString("    }\n  }\n")
}

func (g *gen) callsModule(b *strings.Builder) {
	fmt.Fprintf(b, "// The clients' side (%s.calls.binding.yaml): every call where a client\n", g.c.Name)
	b.WriteString("// saw it (actor <app>/<tenant>/clients), linearized by qtest.Linearize: no\n// positions, no processors, the log the commands' events alone. A key's first\n// answer is final here too; a call the API denied (403) answers nothing.\n")
	fmt.Fprintf(b, "module %sCallsGen {\n", g.aq)
	fmt.Fprintf(b, "  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n  import %sRules.* from \"./%s_rules\"\n", g.aq, g.c.Name, g.aq, g.c.Name)
	fmt.Fprintf(b, "  export grist.*\n  export %sEvents.*\n  export %sRules.*\n\n", g.aq, g.aq)
	b.WriteString("  var log: List[Event]\n  var answered: Set[str]\n\n  action init = all { log' = [], answered' = Set() }\n\n")
	b.WriteString("  pure val IDS = " + idList + "\n  // the next identity a command answers (none once all are): the first\n  // unanswered, so that the model on its own never draws a used one (a\n  // command that would be disabled) nor two that differ only by name\n  pure def nextID(answered: Set[str]): Set[str] =\n    IDS.foldl(Set(), (s, i) => if (s == Set() and not(answered.contains(i))) Set(i) else s)\n\n")
	b.WriteString("  action answers(b: List[Event], id: str): bool = all {\n    freshKey(answered, id),\n    log' = log.concat(b),\n    answered' = answered.union(Set(id)),\n  }\n\n")
	b.WriteString("  // a call the API refused (403: the block's policy refused the caller) ran\n  // nothing and answers nothing: its key stays unanswered\n")
	b.WriteString("  action denied: bool = all { log' = log, answered' = answered }\n")
	g.rulesDefs(b)
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		fmt.Fprintf(b, "\n  action %s(%s): bool = {\n", c.Quint, params(fs, "id: str", "caller: str", "result: str"))
		fmt.Fprintf(b, "    val args_: %sArgs = %s\n    if (result == \"denied\") denied\n", upper(c.Quint), recordValue(fs, "caller: caller"))
		if c.Unconditioned == "" {
			b.WriteString("    else if (result == \"conflict\") answers([], id)\n")
		}
		outcome := fmt.Sprintf("%sOutcome(log, args_)", c.Quint)
		if len(c.Rules) > 0 {
			outcome = fmt.Sprintf("%sRuled(log, args_).outcome", c.Quint)
		}
		fmt.Fprintf(b, "    else all {\n      result == %s,\n      answers(if (result == \"ok\") caused(%sEvents(log, args_), (\"command\", id)) else [], id),\n    }\n  }\n", outcome, c.Quint)
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		fmt.Fprintf(b, "\n  // %s changes nothing; its answer is %sAnswer's where it linearizes\n  action %s: bool = all { log' = log, answered' = answered }\n", q.Name, q.Quint, applied(q.Quint, params(fs)))
	}
	b.WriteString("\n  action step = {\n")
	var calls []string
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		d := newDraws()
		ps := d.add(fs)
		d.pick("caller", "STRS")
		d.pick("id", "nextID(answered)")
		outcome := fmt.Sprintf("%sOutcome(log, %s)", c.Quint, recordFrom(c, ps))
		if len(c.Rules) > 0 {
			outcome = fmt.Sprintf("%sRuled(log, %s).outcome", c.Quint, recordFrom(c, ps))
		}
		calls = append(calls, d.branch(fmt.Sprintf("%s(%s)", c.Quint, paramNames(ps, "id", "caller", outcome))))
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		d := newDraws()
		calls = append(calls, d.branch(applied(q.Quint, paramNames(d.add(fs)))))
	}
	b.WriteString("    any {\n")
	for _, c := range calls {
		fmt.Fprintf(b, "      %s,\n", c)
	}
	b.WriteString("      all { log' = log, answered' = answered },\n    }\n  }\n}\n")

}

const bindingHeader = "# Generated by grist model from the "

// tmplField is a step field in a template: {{.x}}, or {{.args.x}} where
// x is a name the template reserves.
func tmplField(n string) string {
	switch n {
	case "process", "outcome", "obs", "step", "args":
		return "{{.args." + n + "}}"
	}
	return "{{." + n + "}}"
}

// binding is the binding of name: "default" (the store's side), "calls"
// (the clients'), or a test-only variant of the store's.
func (g *gen) binding(name string, m marks) []byte {
	var b strings.Builder
	file := g.c.Name + ".binding.yaml"
	if name != "default" {
		file = g.c.Name + "." + name + ".binding.yaml"
	}
	fmt.Fprintf(&b, "%s%s App's catalog (docs/grist.md §3.2): do not edit;\n", bindingHeader, g.c.Name)
	b.WriteString("# regenerate with the app's binary (`<app> model`, grist.Main). Its invariants\n")
	fmt.Fprintf(&b, "# are the vals model/%s.qnt marks for it (`// grist: invariant`, ...).\n", g.c.Name)
	calls := name == "calls"
	module := g.aq
	switch {
	case calls:
		fmt.Fprintf(&b, "# The clients' calls (actor %s/<tenant>/clients: qobs.Invoke before the first\n# request, Call.Done with the answer, every retry inside the one call),\n# linearized by qtest.Linearize: no positions, no processors.\n", g.c.Name)
		module = g.aq + "Calls"
		fmt.Fprintf(&b, "actors: ['%s/*/clients']\n", g.c.Name)
	default:
		fmt.Fprintf(&b, "# The store's steps (actor %s/<tenant>): every parameter the step's field, the\n# outcome as result, each commit's head and batch, each query's answer\n# compared with the model's. No restart: every step's process is the store.\n", g.c.Name)
		if name != "default" {
			fmt.Fprintf(&b, "# A test-only variant (%s): the same steps, judged by its own vals.\n", file)
		}
		fmt.Fprintf(&b, "actors: ['%s/*', '!*/clients']\n", g.c.Name)
	}
	fmt.Fprintf(&b, "spec: model/%s.qnt\nmodule: %s\n\noutcomes:\n  ok: '\"ok\"'\n", g.c.Name, module)
	var refusals []string
	conditioned := false
	for _, c := range g.c.Commands {
		for _, r := range c.Refuses {
			if !slices.Contains(refusals, r) {
				refusals = append(refusals, r)
			}
		}
		conditioned = conditioned || c.Unconditioned == ""
	}
	if !calls {
		for _, r := range g.callOutcomes() {
			if !slices.Contains(refusals, r) {
				refusals = append(refusals, r)
			}
		}
	}
	sort.Strings(refusals)
	for _, r := range refusals {
		fmt.Fprintf(&b, "  %s: '\"%s\"'\n", r, r)
	}
	if conditioned {
		b.WriteString("  conflict: '\"conflict\"'\n")
	}
	if calls {
		b.WriteString("  denied: '\"denied\"'\n")
	} else {
		b.WriteString("  idempotency-key-reused: '\"idempotency-key-reused\"'\n")
	}
	b.WriteString("\nactions:\n")
	// a variant that judges its own vals only leaves the observations to
	// the default binding, whose claims judge them: it compares nothing a
	// step observed (each expect is evaluated at every step of every
	// replay); one that judges every invariant of the default binding is a
	// default binding with more vals, and compares them
	observe := name == "default" || name == "calls"
	if !observe {
		defInv, _, _ := m.of("default")
		inv, _, _ := m.of(name)
		observe = len(defInv) > 0
		for _, v := range defInv {
			observe = observe && slices.Contains(inv, v)
		}
	}
	entry := func(action, args, expect string) {
		if !observe {
			expect = ""
		}
		if args == "" && expect == "" {
			fmt.Fprintf(&b, "  %s: {}\n", action)
			return
		}
		fmt.Fprintf(&b, "  %s:\n", action)
		if args != "" {
			fmt.Fprintf(&b, "    args: { %s }\n", args)
		}
		if expect != "" {
			fmt.Fprintf(&b, "    expect: '%s'\n", expect)
		}
	}
	batch := "batchIs(appended, {{toList .obs.events}}, {{toList .obs.strs}}, {{toList .obs.ints}}, {{toList .obs.causes}})"
	commit := "log.length() == {{.obs.head}} and " + batch
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		expect := commit
		if calls {
			expect = ""
		}
		entry(c.Quint, argTemplates(fs, "result: '{{.outcome}}'"), expect)
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		parts := make([]string, 0, len(fs)+1)
		for _, f := range fs {
			parts = append(parts, f.json+": "+tmplField(f.json))
		}
		parts = append(parts, "caller: {{.caller}}")
		answer := "{{.obs.result}}"
		if q.Result.Kind() == reflect.Struct {
			rs, _ := scalarFields(q.Result)
			ps := make([]string, len(rs))
			for i, f := range rs {
				ps[i] = f.json + ": {{.obs." + f.json + "}}"
			}
			answer = "{ " + strings.Join(ps, ", ") + " }"
			if len(rs) == 0 {
				answer = "{ unit: true }"
			}
		}
		entry(q.Quint, argTemplates(fs), fmt.Sprintf("%sAnswer(log, { %s }) == %s", q.Quint, strings.Join(parts, ", "), answer))
	}
	if !calls {
		for _, p := range g.c.Processors {
			cp := fmt.Sprintf("log.length() == {{.obs.head}} and checkpoints.get(\"%s\") == {{.obs.checkpoint}} and %s", p.Name, batch)
			switch {
			case p.Shape == "emits":
				entry(p.Quint, "", cp)
			case p.Guarantee == AtLeastOnce:
				entry(p.Quint+"Effect", "", "")
				entry(p.Quint, "", cp)
				entry(p.Quint+"Outcome", "", "")
				entry(p.Quint+"Skip", "", cp)
			default:
				entry(p.Quint+"Skip", "", cp)
				entry(p.Quint, "", cp)
				entry(p.Quint+"Effect", "", "")
				entry(p.Quint+"Outcome", "", commit)
			}
		}
		for _, s := range g.c.Sites {
			fs, _ := scalarFields(s.Fields)
			entry(s.Quint, argTemplates(fs), "")
		}
		g.callBindings(entry)
		// a rewind as a node's relay records it, and an operator's resume
		entry("gristRewound", "", "")
		entry("gristResumed", "", "")
	}
	inv, ev, viol := m.of(name)
	if !calls && observe {
		// the obligations the catalog implies, beside the app's own: judged
		// where the default binding's invariants are, a call's only where
		// the binding judges the call's served records
		ev = append(append([]string(nil), ev...), g.obligationNamesFor(name, append(append(append([]string(nil), inv...), ev...), viol...))...)
	}
	fmt.Fprintf(&b, "\ninvariants: [%s]\n", strings.Join(append(append([]string(nil), inv...), viol...), ", "))
	if len(ev) > 0 {
		fmt.Fprintf(&b, "eventually: [%s]\n", strings.Join(ev, ", "))
	}
	var possibles []string
	if name == "default" || name == "calls" {
		possibles = g.possibles(calls)
	}
	if len(inv)+len(viol)+len(possibles) > 0 {
		b.WriteString("\n# The model on its own (gotogether simulate)\nsimulate:\n")
		if len(inv) > 0 {
			fmt.Fprintf(&b, "  holds: [%s]\n", strings.Join(inv, ", "))
		}
		if len(viol) > 0 {
			fmt.Fprintf(&b, "  allows: [%s]\n", strings.Join(viol, ", "))
		}
		if len(possibles) > 0 {
			b.WriteString("  # every outcome of every command, and every processor's steps, each reachable by the\n  # model on its own: one its rules forbid is one no conforming run can show (P3)\n  possible:\n")
			for _, p := range possibles {
				b.WriteString(p)
			}
		}
	}
	return []byte(b.String())
}

// possibles are a binding's possible entries (binding.Possible), as
// gristtest.Catalog names what runs must take: each command's outcomes (ok,
// each refusal), as the state from which the model's step can answer so
// (a fresh id, and some arguments of the step's domains whose outcome it
// is: a refusal leaves no trace in the state), and each query; with the
// store's, each processor's steps, declared call's and site's, by name.
func (g *gen) possibles(calls bool) []string {
	var out []string
	for _, c := range g.c.Commands {
		for _, o := range append([]string{"ok"}, c.Refuses...) {
			out = append(out, fmt.Sprintf("    - name: %s.%s\n      when: '%s'\n", c.Quint, o, commandCan(c, o)))
		}
	}
	for _, q := range g.c.Queries {
		out = append(out, fmt.Sprintf("    - %s\n", q.Quint))
	}
	if calls {
		return out
	}
	for _, p := range g.c.Processors {
		switch {
		case p.Shape == "emits":
			out = append(out, fmt.Sprintf("    - %s\n", p.Quint))
		case p.Shape == "effect":
			out = append(out, fmt.Sprintf("    - %sEffect\n    - %s\n", p.Quint, p.Quint))
			if p.Guarantee == AtMostOnce && p.Success != "" {
				out = append(out, fmt.Sprintf("    - %sOutcome\n", p.Quint))
			}
		}
	}
	for _, d := range g.c.Dependencies {
		for _, call := range d.Calls {
			if d.Peer != "" {
				out = append(out, fmt.Sprintf("    - %s\n", call.Quint)) // its site is the peer's
				continue
			}
			out = append(out, fmt.Sprintf("    - %s\n    - %sServed\n", call.Quint, call.Quint))
		}
	}
	for _, s := range g.c.Sites {
		out = append(out, fmt.Sprintf("    - %s\n", s.Quint))
	}
	return out
}

// commandCan is the predicate that the model's step can take command c
// with outcome o: an identity left, and some arguments in the domains the
// step draws them from with o their outcome (its rules' verdict too).
func commandCan(c CatalogBlock, o string) string {
	fs, _ := scalarFields(c.Args)
	d := newDraws()
	ps := d.add(fs)
	d.pick("caller", "STRS")
	outcome := fmt.Sprintf("%sOutcome(log, %s)", c.Quint, recordFrom(c, ps))
	if len(c.Rules) > 0 {
		outcome = fmt.Sprintf("%sRuled(log, %s).outcome", c.Quint, recordFrom(c, ps))
	}
	pred := fmt.Sprintf("%s == %q", outcome, o)
	for i := len(d.order) - 1; i >= 0; i-- {
		n := d.order[i]
		pred = fmt.Sprintf("%s.exists(%s => %s)", d.dom[n], n, pred)
	}
	return "nextID(answered) != Set() and " + pred
}

// argTemplates are the templates of the fields whose parameter is not
// the same-named step field (a name Quint reserves), and extra.
func argTemplates(fs []field, extra ...string) string {
	var parts []string
	for _, f := range fs {
		if f.param != f.json {
			parts = append(parts, f.param+": '{{.args."+f.json+"}}'")
		}
	}
	return strings.Join(append(parts, extra...), ", ")
}

// marks are the vals the application's model marks, by binding: kind
// ("invariant", "eventually", "violable") -> binding -> vals, in file
// order.
type marks map[string]map[string][]string

func (m marks) of(binding string) (inv, ev, viol []string) {
	return m["invariant"][binding], m["eventually"][binding], m["violable"][binding]
}

// variants are the test-only variants the marks name, sorted.
func (m marks) variants() []string {
	set := map[string]bool{}
	for _, byBinding := range m {
		for b := range byBinding {
			if b != "default" && b != "calls" {
				set[b] = true
			}
		}
	}
	return sortedKeys(set)
}

// The lines parseMarks and definedIn read (read-only).
var (
	markLine   = regexp.MustCompile(`^\s*//\s*grist:\s*(\S+)(.*)$`)
	valLine    = regexp.MustCompile(`^\s*(?:pure\s+)?val\s+([A-Za-z_][A-Za-z0-9_]*)`)
	moduleLine = regexp.MustCompile(`^\s*module\s+([A-Za-z_][A-Za-z0-9_]*)`)
	defLine    = regexp.MustCompile(`^\s*(?:pure\s+)?(?:def|val)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	bindingRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	identRe    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// parseMarks reads the marks of the application's model: a `// grist:
// <kind> [binding...]` line, then (after other comment lines) the val it
// marks, in module <aq> (the store's bindings) or <aq>Calls (the
// clients').
func parseMarks(src []byte, aq string) (marks, error) {
	m := marks{}
	module := ""
	var pending []string // kind, then bindings
	for i, line := range strings.Split(string(src), "\n") {
		if mm := moduleLine.FindStringSubmatch(line); mm != nil {
			module = mm[1]
		}
		if mm := markLine.FindStringSubmatch(line); mm != nil {
			kind := mm[1]
			if kind != "invariant" && kind != "eventually" && kind != "violable" {
				return nil, fmt.Errorf("line %d: grist: %s: the kinds are invariant, eventually and violable", i+1, kind)
			}
			pending = append([]string{kind}, strings.Fields(mm[2])...)
			continue
		}
		if pending == nil || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		vm := valLine.FindStringSubmatch(line)
		if vm == nil {
			return nil, fmt.Errorf("line %d: a grist: %s mark is not followed by a val", i+1, pending[0])
		}
		var bindings []string
		switch module {
		case aq:
			bindings = pending[1:]
			if len(bindings) == 0 {
				bindings = []string{"default"}
			}
			for _, b := range bindings {
				if b == "calls" || !bindingRe.MatchString(b) {
					return nil, fmt.Errorf("line %d: binding %q: a variant is named by lower-case words (calls is the clients' binding's: mark the val in module %sCalls)", i+1, b, aq)
				}
			}
		case aq + "Calls":
			if len(pending) > 1 {
				return nil, fmt.Errorf("line %d: module %sCalls has one binding, the clients': name none", i+1, aq)
			}
			bindings = []string{"calls"}
		default:
			return nil, fmt.Errorf("line %d: val %s is marked in module %s: mark vals in %s or %sCalls", i+1, vm[1], module, aq, aq)
		}
		if m[pending[0]] == nil {
			m[pending[0]] = map[string][]string{}
		}
		for _, b := range bindings {
			m[pending[0]][b] = append(m[pending[0]][b], vm[1])
		}
		pending = nil
	}
	if pending != nil {
		return nil, fmt.Errorf("a grist: %s mark at the end is followed by no val", pending[0])
	}
	return m, nil
}

// definedIn are the defs and vals of module in src.
// bodiesIn are the definitions (val and def) of module in src, by name:
// each one's lines up to the next definition, mark or module, comments
// left out.
func bodiesIn(src []byte, module string) map[string]string {
	out := map[string]string{}
	in, name := false, ""
	for _, line := range strings.Split(string(src), "\n") {
		if mm := moduleLine.FindStringSubmatch(line); mm != nil {
			in, name = mm[1] == module, ""
			continue
		}
		if !in {
			continue
		}
		if i := strings.Index(line, "//"); i >= 0 {
			if markLine.MatchString(line) {
				name = ""
			}
			line = line[:i]
		}
		if dm := defLine.FindStringSubmatch(line); dm != nil {
			name = dm[1]
		}
		if name != "" {
			out[name] += line + "\n"
		}
	}
	return out
}

// readsRecords reports whether any of vals reads state, directly or
// through the definitions of the application's model and rules it uses.
func (g *gen) readsRecords(vals []string, state string) bool {
	seen := map[string]bool{}
	var reads func(n string) bool
	reads = func(n string) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, id := range identRe.FindAllString(g.bodies[n], -1) {
			if id == state || id != n && g.bodies[id] != "" && reads(id) {
				return true
			}
		}
		return false
	}
	return slices.ContainsFunc(vals, reads)
}

func definedIn(src []byte, module string) map[string]bool {
	out := map[string]bool{}
	in := false
	for _, line := range strings.Split(string(src), "\n") {
		if mm := moduleLine.FindStringSubmatch(line); mm != nil {
			in = mm[1] == module
			continue
		}
		if dm := defLine.FindStringSubmatch(line); in && dm != nil {
			out[dm[1]] = true
		}
	}
	return out
}

// field is a block's scalar field (catalog.Field).
type field struct {
	json  string // its JSON name: the step's field
	param string // its Quint parameter's name (json, or json_ where Quint reserves it)
	typ   string // str, int or bool
	tag   bool
}

// scalarFields are t's scalar fields (catalog.ScalarFields).
func scalarFields(t reflect.Type) ([]field, error) {
	fs, err := catalog.ScalarFields(t)
	out := make([]field, len(fs))
	for i, f := range fs {
		out[i] = field{json: f.JSON, param: f.Param, typ: f.Type, tag: f.Tag}
	}
	if fs == nil {
		out = nil
	}
	return out, err
}

func quintScalar(t reflect.Type) string { return catalog.QuintScalar(t) }

// The generated events file's read model selections and event
// constructors, as eventsModule writes them: what inputsChanged compares.
var (
	selectionDef   = regexp.MustCompile(`(?m)^  pure def (\w+)Events\(log: List\[Event\][^)]*\): List\[Event\] = log\.select\(e => Set\(([^)]*)\)\.contains`)
	constructorDef = regexp.MustCompile(`(?m)^  pure def \w+\(([^)]*)\): Event =\n    \{ typ: "([^"]+)"`)
)

// inputsChanged compares the events file generated before (prev) with
// the one generated now (next): a read model selecting an event type it
// did not, or no longer selecting one, and an event type whose fields
// changed, each with the read models folding it. Blocks added or removed
// are not warned of: a new one's holes are Missing, a removed one's
// defs fail the typecheck.
func inputsChanged(app string, prev, next []byte) []string {
	selections := func(b []byte) (map[string][]string, []string) {
		m, order := map[string][]string{}, []string{}
		for _, x := range selectionDef.FindAllSubmatch(b, -1) {
			var types []string
			for _, t := range strings.Split(string(x[2]), ",") {
				types = append(types, strings.Trim(strings.TrimSpace(t), `"`))
			}
			m[string(x[1])] = types
			order = append(order, string(x[1]))
		}
		return m, order
	}
	constructors := func(b []byte) (map[string]string, []string) {
		m, order := map[string]string{}, []string{}
		for _, x := range constructorDef.FindAllSubmatch(b, -1) {
			m[string(x[2])] = string(x[1])
			order = append(order, string(x[2]))
		}
		return m, order
	}
	oldSel, _ := selections(prev)
	newSel, selOrder := selections(next)
	oldCons, _ := constructors(prev)
	newCons, consOrder := constructors(next)
	rules := rulesFile(app)
	var out []string
	for _, rm := range selOrder {
		was, ok := oldSel[rm]
		if !ok {
			continue
		}
		now := newSel[rm]
		var gained, lost []string
		for _, t := range now {
			if !slices.Contains(was, t) {
				gained = append(gained, t)
			}
		}
		for _, t := range was {
			if !slices.Contains(now, t) {
				lost = append(lost, t)
			}
		}
		if len(gained) > 0 {
			out = append(out, fmt.Sprintf("%sEvents folds %s now, which it did not when %s was generated: every fold over it in %s must handle the new type (a foldl's else branch skips it silently)",
				rm, strings.Join(gained, ", "), genFile(app), rules))
		}
		if len(lost) > 0 {
			out = append(out, fmt.Sprintf("%sEvents no longer folds %s: a fold in %s that handles it is dead code, and its read model's state may now mean less",
				rm, strings.Join(lost, ", "), rules))
		}
	}
	for _, typ := range consOrder {
		was, ok := oldCons[typ]
		if !ok || was == newCons[typ] {
			continue
		}
		var folding []string
		for _, rm := range selOrder {
			if slices.Contains(newSel[rm], typ) {
				folding = append(folding, rm+"Events")
			}
		}
		where := ""
		if len(folding) > 0 {
			where = " (folded by " + strings.Join(folding, ", ") + ")"
		}
		out = append(out, fmt.Sprintf("event %s has fields (%s) now, (%s) when %s was generated: the folds and rules in %s reading it%s may need what changed",
			typ, newCons[typ], was, genFile(app), rules, where))
	}
	return out
}
