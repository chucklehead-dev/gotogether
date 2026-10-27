package grist

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// gristQnt is grist/model/grist.qnt, which Generate vendors into an
// application's model/ (read-only).
//
//go:embed model/grist.qnt
var gristQnt []byte

// Generated is what Generate writes for an App: its model's skeleton and
// its bindings, from the App's Catalog, beside the application's own
// model file, which it never writes (Starter writes a first one).
type Generated struct {
	// Files are the generated files by path, relative to the directory
	// given to Generate: model/grist.qnt (grist's library, vendored),
	// model/<app>_gen.qnt (the skeleton), <app>.binding.yaml (the store's
	// side), <app>.calls.binding.yaml (the clients') and one
	// <app>.<variant>.binding.yaml per test-only variant the app's model
	// names.
	Files map[string][]byte
	// Holes are the definitions the skeleton calls and the application's
	// model (model/<app>.qnt, module <app>Rules) must define, each with a
	// stub to start from, in the skeleton's order.
	Holes []Hole
	// Missing are the holes model/<app>.qnt does not define (all of them
	// when there is no such file): the skeleton does not typecheck until
	// they are written.
	Missing []string
}

// A Hole is one definition the generated skeleton leaves to the
// application: a rule's predicate (a command's outcome, its events, the
// reads it needs), a query's answer, a processor's events, a site's
// guard, the domains the model on its own draws from.
type Hole struct {
	Name string
	// Doc says what it is for, and Stub is a definition that typechecks
	// (a starting point, not a rule).
	Doc, Stub string
}

// The names of the generated files, by the App's name.
func genFile(app string) string   { return "model/" + app + "_gen.qnt" }
func modelFile(app string) string { return "model/" + app + ".qnt" }

// Generate writes the model skeleton and the bindings of app from its
// Catalog (docs/grist.md §3.2), reading the application's model at
// dir/model/<app>.qnt for the holes it fills and the vals it marks as
// invariants (`// grist: invariant [bindings]`, `// grist: eventually
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
//     each refusal it declares, with freshKey, covers and conditionHolds),
//     per query, per processor by its declared shape (Emits, or Delivers'
//     effect, checkpoint and outcome actions over grist.qnt's at-least-once
//     or at-most-once defs) and per site, every frame condition, and step;
//   - module <app>CallsGen, the clients' side: no positions, no
//     processors, a key's first answer final, denied answering nothing;
//   - the bindings: every parameter the step's field, the outcome as
//     result, each commit's head and batch and each query's answer
//     compared with the model's (in a variant only if it judges every
//     invariant of the default binding: one that judges only its own vals
//     leaves the observations to the default binding's claims).
//
// What the application writes (model/<app>.qnt): module <app>Rules with
// the holes (Hole), each read model's fold over its generated selection,
// and its own helpers; module <app> importing <app>Gen with its
// invariants and eventual vals; module <app>Calls importing <app>CallsGen.
// Regenerating never touches that file, so the holes survive it; a new
// block's hole is reported in Missing.
func Generate(app *App, dir string) (Generated, error) {
	if err := app.Verify(); err != nil {
		return Generated{}, err
	}
	g, err := newGen(app.Catalog())
	if err != nil {
		return Generated{}, err
	}
	own, err := os.ReadFile(filepath.Join(dir, modelFile(app.Name)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Generated{}, err
	}
	skeleton := g.skeleton()
	if len(g.clashes) > 0 {
		return Generated{}, fmt.Errorf("grist model: %s:\n  %s", app.Name, strings.Join(g.clashes, "\n  "))
	}
	marks, err := parseMarks(own, g.aq)
	if err != nil {
		return Generated{}, fmt.Errorf("grist model: %s: %w", modelFile(app.Name), err)
	}
	out := Generated{Files: map[string][]byte{
		"model/grist.qnt":                gristQnt,
		genFile(app.Name):                skeleton,
		app.Name + ".binding.yaml":       g.binding("default", marks),
		app.Name + ".calls.binding.yaml": g.binding("calls", marks),
	}, Holes: g.holes}
	for _, v := range marks.variants() {
		out.Files[app.Name+"."+v+".binding.yaml"] = g.binding(v, marks)
	}
	defined := definedIn(own, g.aq+"Rules")
	for _, h := range g.holes {
		if !defined[h.Name] {
			out.Missing = append(out.Missing, h.Name)
		}
	}
	return out, nil
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

// Starter is a first model/<app>.qnt for app: every hole of its skeleton
// stubbed (the stubs typecheck and decide nothing: an ok for every
// command, no events, no reads), the module the store's binding names
// and the clients' one, with no invariant yet. Main's `model -init`
// writes it when the file does not exist.
func Starter(app *App) ([]byte, error) {
	if err := app.Verify(); err != nil {
		return nil, err
	}
	g, err := newGen(app.Catalog())
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// The %s app's model: the rules grist's generated skeleton (%s,\n// grist model)", app.Name, genFile(app.Name))
	b.WriteString(" calls, and the invariants the bindings judge. Regenerating\n// the skeleton never touches this file.\n\n")
	fmt.Fprintf(&b, "// The rules: every hole of the skeleton, and the read models' folds.\nmodule %sRules {\n", g.aq)
	fmt.Fprintf(&b, "  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n", g.aq, app.Name)
	for _, h := range g.holes {
		fmt.Fprintf(&b, "\n%s  %s\n", comment("  ", h.Doc), h.Stub)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "// The store's side (%s.binding.yaml): mark a val `// grist: invariant`\n", app.Name)
	b.WriteString("// or `// grist: eventually` (after a settle) on the line before it, and\n")
	b.WriteString("// regenerate; a binding name after the kind puts it in a test-only variant.\n")
	// the module imports the events and the rules itself, not only through
	// the skeleton's exports: quint's flattener (quint compile, which the
	// validator's Go evaluator runs on a module importing this one from
	// another directory) loses names re-exported twice
	imports := fmt.Sprintf("  import grist.* from \"./grist\"\n  import %sEvents.* from \"./%s_gen\"\n  import %sRules.*\n", g.aq, app.Name, g.aq)
	fmt.Fprintf(&b, "module %s {\n%s  import %sGen.* from \"./%s_gen\"\n  export %sGen.*\n}\n\n", g.aq, imports, g.aq, app.Name, g.aq)
	fmt.Fprintf(&b, "// The clients' side (%s.calls.binding.yaml), linearized.\n", app.Name)
	fmt.Fprintf(&b, "module %sCalls {\n%s  import %sCallsGen.* from \"./%s_gen\"\n  export %sCallsGen.*\n}\n", g.aq, imports, g.aq, app.Name, g.aq)
	return []byte(b.String()), nil
}

// gen is one App's generation.
type gen struct {
	c       Catalog
	aq      string // the app's Quint name
	events  map[string]CatalogEvent
	holes   []Hole
	names   map[string]string // a generated top-level name -> what it is, for clashes
	clashes []string
}

// a field is one scalar field of a struct, as a step records it.
type field struct {
	json  string // its JSON name: the step's field
	param string // its Quint parameter's name (json, or json_ where Quint reserves it)
	typ   string // str, int or bool
	tag   bool
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
	g := &gen{c: c, aq: aq, events: map[string]CatalogEvent{}, names: map[string]string{}}
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
	if len(ps) > 0 {
		return nil, fmt.Errorf("grist model: %s cannot be modelled yet:\n  %s", c.Name, strings.Join(ps, "\n  "))
	}
	g.collectHoles()
	g.claimBlocks()
	return g, nil
}

// quintScalar is t's Quint type when t is a scalar a step records: str,
// int (booleans as themselves), or "".
func quintScalar(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "str"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	}
	return ""
}

// quintIdent is a Quint identifier (read-only).
var quintIdent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// scalarFields are t's top-level scalar fields, as a step records them
// (rec.StepFields): non-scalar fields are not recorded, and so not
// modelled. A pointer field is refused (recorded only when set, so a step
// may lack its parameter), as is a JSON name that is no Quint identifier.
func scalarFields(t reflect.Type) ([]field, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, nil
	}
	var out []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		n := rec.JSONName(f)
		if f.Type.Kind() == reflect.Pointer && quintScalar(f.Type.Elem()) != "" {
			return nil, fmt.Errorf("field %s is a pointer, recorded only when set: make it a value", f.Name)
		}
		q := quintScalar(f.Type)
		if q == "" {
			continue
		}
		if !quintIdent.MatchString(n) {
			return nil, fmt.Errorf("field %s's JSON name %q is no Quint identifier: rename it (letters, digits and _)", f.Name, n)
		}
		p := n
		if quintTaken[n] {
			p = n + "_"
		}
		out = append(out, field{json: n, param: p, typ: q, tag: f.Tag.Get("grist") == "tag"})
	}
	return out, nil
}

// quintTaken are the names a generated parameter may not take: Quint's
// keywords and built-in operators (a parameter named `of` or `size` does
// not parse: QNT101), and the generated modules' own names. Read-only.
var quintTaken = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`module import export from as const var val def pure action run temporal nondet
		type assume if else match all any and or not iff implies true false Bool Int Nat Set Map List Tup Rec
		exists forall in union contains fold intersect exclude subseteq map applyTo filter powerset flatten
		allLists allListsUpTo getOnlyElement chooseSome oneOf isFinite size get put keys mapBy setToMap setOfMaps
		set setBy fields with tuples append concat head tail length nth indices replaceAt slice select foldl foldr
		to always eventually next then expect reps fail assert orKeep mustChange enabled weakFair strongFair leadsTo
		range igt ilt igte ilte iadd isub iuminus imul idiv imod ipow actionAll actionAny field fieldNames item
		assign of eq neq ite cross difference matchVariant variant
		log checkpoints appended effects answered init step args_ result_ reads_ rec_ id principal readPos reads
		result`) {
		m[n] = true
	}
	return m
}()

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
	for _, n := range []string{"appends", "refuses", "readsThrough", "processes", "effectStep", "answers", "denied", "init", "step", "IDS", "KEYS"} {
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
	}
	for _, s := range g.c.Sites {
		g.claim(s.Quint, "site "+s.Name)
		g.claim(s.Quint+"Records", "site "+s.Name)
	}
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
		for _, r := range c.Refuses {
			outcomes += ` or "` + r + `"`
		}
		g.hole(c.Quint+"Outcome", fmt.Sprintf("%s's rule: the outcome its decision reaches on log (%s)", c.Name, outcomes),
			fmt.Sprintf(`pure def %sOutcome(log: List[Event], c: %s): str = "ok"`, c.Quint, args))
		g.hole(c.Quint+"Events", fmt.Sprintf("the events %s appends when ok (their causes are stamped by its action)", c.Name),
			fmt.Sprintf(`pure def %sEvents(log: List[Event], c: %s): List[Event] = []`, c.Quint, args))
		g.hole(c.Quint+"Needs", fmt.Sprintf("the reads %s's rule needs for result: its committed step's read set must cover each (covers)", c.Name),
			fmt.Sprintf(`pure def %sNeeds(c: %s, result: str): Set[Read] = Set()`, c.Quint, args))
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
		case p.Success != "":
			g.hole(p.Quint+"Success", fmt.Sprintf("the %s event %s appends after its effect succeeded for the trigger at position trigger (prefix: the log through it)", p.Success, p.Name),
				fmt.Sprintf(`pure def %sSuccess(prefix: List[Event], trigger: int): List[Event] = []`, p.Quint))
		}
	}
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
	"// after changing a block; the application's rules are model/%s.qnt's.\n"

// skeleton is model/<app>_gen.qnt.
func (g *gen) skeleton() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, genHeader, g.c.Name, g.c.Name)
	g.eventsModule(&b)
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
			if quintTaken[k] || !quintIdent.MatchString(k) {
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
	b.WriteString("\n  // each command's and query's arguments, with the actor that sent it (principal)\n")
	for _, c := range append(append([]CatalogBlock(nil), g.c.Commands...), g.c.Queries...) {
		fs, _ := scalarFields(c.Args)
		fmt.Fprintf(b, "  type %s = %s\n", g.claim(upper(c.Quint)+"Args", c.Name), recordType(fs, "principal: str"))
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
	b.WriteString("}\n")
}

// vars are the store side's variables and their initial values.
func (g *gen) vars() [][2]string {
	cps := make([]string, len(g.c.Processors))
	for i, p := range g.c.Processors {
		cps[i] = fmt.Sprintf("%q -> 0", p.Name)
	}
	vs := [][2]string{{"log", "[]"}, {"checkpoints", "Map(" + strings.Join(cps, ", ") + ")"}, {"appended", "[]"},
		{"effects", "[]"}, {"answered", "Set()"}}
	for _, s := range g.c.Sites {
		vs = append(vs, [2]string{s.Quint + "Records", "[]"})
	}
	return vs
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
	fmt.Fprintf(b, "  import grist.* from \"./grist\"\n  import %sEvents.*\n  import %sRules.* from \"./%s\"\n", g.aq, g.aq, g.c.Name)
	fmt.Fprintf(b, "  export grist.*\n  export %sEvents.*\n  export %sRules.*\n\n", g.aq, g.aq)
	b.WriteString("  // one tenant's log\n  var log: List[Event]\n  // each processor's checkpoint: the last trigger it handled\n  var checkpoints: str -> int\n")
	b.WriteString("  // the batch the last step appended (what its observations are checked against)\n  var appended: List[Event]\n")
	b.WriteString("  // the effect steps: (processor, trigger, key, ok)\n  var effects: List[Effect]\n")
	b.WriteString("  // the command ids answered so far, by a commit or a committed refusal\n  var answered: Set[str]\n")
	for _, s := range g.c.Sites {
		fmt.Fprintf(b, "  // what the %s site recorded, in order\n  var %sRecords: List[%sRecord]\n", s.Name, s.Quint, upper(s.Quint))
	}
	b.WriteString("\n  action init = all {\n")
	for _, v := range g.vars() {
		fmt.Fprintf(b, "    %s' = %s,\n", v[0], v[1])
	}
	b.WriteString("  }\n\n")
	b.WriteString("  // the identities and effect keys the model on its own draws\n")
	b.WriteString("  pure val IDS = Set(\"i1\", \"i2\", \"i3\", \"i4\")\n  pure val KEYS = Set(\"k1\", \"k2\", \"k3\")\n\n")
	b.WriteString("  // a command's commit of b, answering id\n  action appends(b: List[Event], id: str): bool = all {\n")
	b.WriteString(g.frame("    ", map[string]string{"log": "log.concat(b)", "appended": "b", "answered": "answered.union(Set(id))"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // a committed refusal: its marker answers id, nothing is appended; it\n  // commits at the head, at or after the position it read through\n")
	b.WriteString("  action refuses(readPos: int, id: str): bool = all {\n    readPos <= log.length(),\n    appends([], id),\n  }\n\n")
	b.WriteString("  // a step that commits nothing (a query, a reused key): it read through\n  // readPos, the log's length where it is ordered (2·readPos+1)\n")
	b.WriteString("  action readsThrough(readPos: int): bool = all {\n    log.length() == readPos,\n")
	b.WriteString(g.frame("    ", map[string]string{"appended": "[]"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // a processor's handling of trigger, appending b with its checkpoint\n  action processes(b: List[Event], p: str, trigger: int): bool = all {\n")
	b.WriteString(g.frame("    ", map[string]string{"log": "log.concat(b)", "appended": "b", "checkpoints": "checkpoints.set(p, trigger)"}))
	b.WriteString("  }\n\n")
	b.WriteString("  // an effect step of processor p for trigger, under key (the same on every\n  // effect step of the trigger, no other trigger's: keyStable)\n")
	b.WriteString("  action effectStep(p: str, trigger: int, key: str, ok: bool): bool = all {\n    keyStable(effects, p, trigger, key),\n")
	b.WriteString(g.frame("    ", map[string]string{"appended": "[]", "effects": "effects.append((p, trigger, key, ok))"}))
	b.WriteString("  }\n")
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
	if c.Unconditioned == "" {
		doc += " and the condition to have held, a conflict needs it not to"
	}
	b.WriteString("\n" + comment("  ", doc+". A reused key commits nothing."))
	fmt.Fprintf(b, "  action %s(%s): bool = {\n", c.Quint, params(fs, "id: str", "principal: str", "readPos: int", "reads: List[(str, str, str)]", "result: str"))
	fmt.Fprintf(b, "    val args_: %s = %s\n    val reads_ = readsOf(reads)\n", args, recordValue(fs, "principal: principal"))
	b.WriteString("    if (result == \"idempotency-key-reused\") all { answered.contains(id), readsThrough(readPos) }\n    else all {\n")
	b.WriteString("      freshKey(answered, id),\n")
	fmt.Fprintf(b, "      %sNeeds(args_, result).forall(n => covers(reads_, n)),\n", c.Quint)
	commit := fmt.Sprintf("result == %sOutcome(log, args_),\n        if (result == \"ok\") appends(caused(%sEvents(log, args_), (\"command\", id)), id)\n        else refuses(readPos, id),\n", c.Quint, c.Quint)
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
	switch {
	case p.Shape == "emits":
		fmt.Fprintf(b, "\n  // %s (Emits): on each %s, %sEmits of the log through the trigger,\n  // with its checkpoint (whatever came after the trigger)\n", p.Name, p.On, p.Quint)
		fmt.Fprintf(b, "  action %s(trigger: int): bool = all {\n    trigger > 0,\n    trigger == %s,\n    processes(caused(%sEmits(log.slice(0, trigger), trigger), %s), %q, trigger),\n  }\n",
			p.Quint, next, p.Quint, cause, p.Name)
	case p.Delivery == AtLeastOnce:
		fmt.Fprintf(b, "\n  // %s's effect, at least once: any number of tries for its next trigger\n  // before its checkpoint, every one under the trigger's key\n", p.Name)
		fmt.Fprintf(b, "  action %sEffect(trigger: int, key: str, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    effectStep(%q, trigger, key, ok),\n  }\n", p.Quint, next, p.Name)
		fmt.Fprintf(b, "\n  // %s's checkpoint, only after effect steps: its success after an ok one,\n  // grist.failed after %d failed ones, whichever the run committed (ok: its\n  // record of which)\n", p.Name, p.EffectTries)
		fmt.Fprintf(b, "  action %s(trigger: int, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    atLeastOnceDone(effects, %q, trigger, ok, %d),\n    processes(atLeastOnceOutcome(%q, trigger, ok, %s), %q, trigger),\n  }\n",
			p.Quint, next, p.Name, p.EffectTries, p.Name, success, p.Name)
		fmt.Fprintf(b, "\n  // %s is at least once: its outcome commits with its checkpoint, never\n  // alone, so an outcome step of its own is no step of this model\n", p.Name)
		fmt.Fprintf(b, "  action %sOutcome(trigger: int, ok: bool): bool = all {\n    false,\n%s  }\n", p.Quint, unchanged)
	default: // at most once
		fmt.Fprintf(b, "\n  // %s, at most once: its checkpoint first (ok false, nothing appended),\n  // before any effect step of its trigger\n", p.Name)
		fmt.Fprintf(b, "  action %s(trigger: int, ok: bool): bool = all {\n    trigger > 0,\n    trigger == %s,\n    atMostOnceFirst(effects, %q, trigger, ok),\n    processes([], %q, trigger),\n  }\n",
			p.Quint, next, p.Name, p.Name)
		fmt.Fprintf(b, "\n  // %s's effect: once, after its checkpoint, or never (a crash between\n  // the two loses it, by right)\n", p.Name)
		fmt.Fprintf(b, "  action %sEffect(trigger: int, key: str, ok: bool): bool = all {\n    %s,\n    atMostOnceEffect(effects, checkpoints.get(%q), %q, trigger),\n    effectStep(%q, trigger, key, ok),\n  }\n",
			p.Quint, isTrigger, p.Name, p.Name, p.Name)
		fmt.Fprintf(b, "\n  // %s's outcome, if present (a crash after the effect loses it): its\n  // success after the one ok effect step; after a failed one grist.failed\n  // when the failure is known, else grist.unknown with the error\n", p.Name)
		fmt.Fprintf(b, "  action %sOutcome(trigger: int, ok: bool, known: bool, error: str): bool = {\n    val isTrigger = %s\n    val success = if (isTrigger) %s else []\n",
			p.Quint, isTrigger, success)
		fmt.Fprintf(b, "    all {\n      isTrigger,\n      atMostOnceDone(log, effects, %q, trigger, ok, success),\n      val b = atMostOnceOutcome(%q, trigger, ok, known, error, success)\n      all {\n", p.Name, p.Name)
		b.WriteString(g.frame("        ", map[string]string{"log": "log.concat(b)", "appended": "b"}))
		b.WriteString("      },\n    }\n  }\n")
	}
}

// domainOf is the set the model on its own draws a field of typ from.
func domainOf(typ string) string {
	return map[string]string{"str": "STRS", "int": "INTS", "bool": "Bool"}[typ]
}

// draws are the nondet picks a step shares across its branches: one per
// field name and type (a name of two types is suffixed by the type), in
// first-use order; named gives each field's pick.
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

func (d *draws) write(b *strings.Builder, indent string) {
	for _, n := range d.order {
		fmt.Fprintf(b, "%snondet %s = %s.oneOf()\n", indent, n, d.dom[n])
	}
}

// commandCall is the step's call of command c with its picks fs: its
// rule's outcome and the reads that outcome needs.
func commandCall(c CatalogBlock, fs []field, extra ...string) string {
	return fmt.Sprintf("{\n        val args_: %sArgs = %s\n        val result_ = %sOutcome(log, args_)\n        %s\n      }",
		upper(c.Quint), recordFrom(c, fs), c.Quint, applied(c.Quint, paramNames(fs, extra...)))
}

// recordFrom is c's arguments' record from the picks fs.
func recordFrom(c CatalogBlock, fs []field) string {
	orig, _ := scalarFields(c.Args)
	parts := make([]string, 0, len(fs)+1)
	for i, f := range fs {
		parts = append(parts, orig[i].json+": "+f.param)
	}
	return "{ " + strings.Join(append(parts, "principal: principal"), ", ") + " }"
}

func (g *gen) storeStep(b *strings.Builder) {
	b.WriteString("\n  // the model on its own (gotogether simulate): every command as its rule\n  // decides, reading what it needs, every query, processor and site\n  action step = {\n")
	d := newDraws()
	var calls []string
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		ps := d.add(fs)
		d.pick("principal", "STRS")
		d.pick("id", "IDS")
		calls = append(calls, commandCall(c, ps, "id", "principal", "log.length()", fmt.Sprintf("triplesOf(%sNeeds(args_, result_))", c.Quint), "result_"))
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		calls = append(calls, q.Quint+"("+paramNames(d.add(fs), "log.length()")+")")
	}
	for _, p := range g.c.Processors {
		next := fmt.Sprintf("nextOf(log, checkpoints.get(%q), %q)", p.Name, p.On)
		switch {
		case p.Shape == "emits":
			calls = append(calls, fmt.Sprintf("%s(%s)", p.Quint, next))
		case p.Delivery == AtLeastOnce:
			d.pick("key_", "KEYS")
			d.pick("ok_", "Bool")
			calls = append(calls, fmt.Sprintf("%sEffect(%s, key_, ok_)", p.Quint, next), fmt.Sprintf("%s(%s, ok_)", p.Quint, next))
		default:
			d.pick("key_", "KEYS")
			d.pick("ok_", "Bool")
			d.pick("known_", "Bool")
			d.pick("trigger_", "1.to(log.length() + 1)")
			calls = append(calls, fmt.Sprintf("%s(%s, false)", p.Quint, next), fmt.Sprintf("%sEffect(trigger_, key_, ok_)", p.Quint),
				fmt.Sprintf("%sOutcome(trigger_, ok_, known_, \"lost\")", p.Quint))
		}
	}
	for _, s := range g.c.Sites {
		fs, _ := scalarFields(s.Fields)
		calls = append(calls, applied(s.Quint, paramNames(d.add(fs))))
	}
	d.write(b, "    ")
	b.WriteString("    any {\n")
	for _, c := range calls {
		fmt.Fprintf(b, "      %s,\n", c)
	}
	b.WriteString("    }\n  }\n")
}

func (g *gen) callsModule(b *strings.Builder) {
	fmt.Fprintf(b, "// The clients' side (%s.calls.binding.yaml): every call where a client\n", g.c.Name)
	b.WriteString("// saw it (actor <tenant>/clients), linearized by qtest.Linearize: no\n// positions, no processors, the log the commands' events alone. A key's first\n// answer is final here too; a call the API denied (403) answers nothing.\n")
	fmt.Fprintf(b, "module %sCallsGen {\n", g.aq)
	fmt.Fprintf(b, "  import grist.* from \"./grist\"\n  import %sEvents.*\n  import %sRules.* from \"./%s\"\n", g.aq, g.aq, g.c.Name)
	fmt.Fprintf(b, "  export grist.*\n  export %sEvents.*\n  export %sRules.*\n\n", g.aq, g.aq)
	b.WriteString("  var log: List[Event]\n  var answered: Set[str]\n\n  action init = all { log' = [], answered' = Set() }\n\n")
	b.WriteString("  pure val IDS = Set(\"i1\", \"i2\", \"i3\")\n\n")
	b.WriteString("  action answers(b: List[Event], id: str): bool = all {\n    freshKey(answered, id),\n    log' = log.concat(b),\n    answered' = answered.union(Set(id)),\n  }\n\n")
	b.WriteString("  // a call the API refused (403: the block's policy refused the actor) ran\n  // nothing and answers nothing: its key stays unanswered\n")
	b.WriteString("  action denied: bool = all { log' = log, answered' = answered }\n")
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		fmt.Fprintf(b, "\n  action %s(%s): bool = {\n", c.Quint, params(fs, "id: str", "principal: str", "result: str"))
		fmt.Fprintf(b, "    val args_: %sArgs = %s\n    if (result == \"denied\") denied\n", upper(c.Quint), recordValue(fs, "principal: principal"))
		if c.Unconditioned == "" {
			b.WriteString("    else if (result == \"conflict\") answers([], id)\n")
		}
		fmt.Fprintf(b, "    else all {\n      result == %sOutcome(log, args_),\n      answers(if (result == \"ok\") caused(%sEvents(log, args_), (\"command\", id)) else [], id),\n    }\n  }\n", c.Quint, c.Quint)
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		fmt.Fprintf(b, "\n  // %s changes nothing; its answer is %sAnswer's where it linearizes\n  action %s: bool = all { log' = log, answered' = answered }\n", q.Name, q.Quint, applied(q.Quint, params(fs)))
	}
	b.WriteString("\n  action step = {\n")
	d := newDraws()
	var calls []string
	for _, c := range g.c.Commands {
		fs, _ := scalarFields(c.Args)
		ps := d.add(fs)
		d.pick("principal", "STRS")
		d.pick("id", "IDS")
		calls = append(calls, fmt.Sprintf("%s(%s)", c.Quint, paramNames(ps, "id", "principal", fmt.Sprintf("%sOutcome(log, %s)", c.Quint, recordFrom(c, ps)))))
	}
	for _, q := range g.c.Queries {
		fs, _ := scalarFields(q.Args)
		calls = append(calls, applied(q.Quint, paramNames(d.add(fs))))
	}
	d.write(b, "    ")
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
		b.WriteString("# The clients' calls (actor <tenant>/clients: qobs.Invoke before the first\n# request, Call.Done with the answer, every retry inside the one call),\n# linearized by qtest.Linearize: no positions, no processors.\n")
		module = g.aq + "Calls"
		b.WriteString("actors: ['*/clients']\n")
	default:
		b.WriteString("# The store's steps (actor the tenant): every parameter the step's field, the\n# outcome as result, each commit's head and batch, each query's answer\n# compared with the model's. No restart: every step's process is the store.\n")
		if name != "default" {
			fmt.Fprintf(&b, "# A test-only variant (%s): the same steps, judged by its own vals.\n", file)
		}
		b.WriteString("actors: ['!*/clients']\n")
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
		parts = append(parts, "principal: {{.principal}}")
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
			case p.Delivery == AtLeastOnce:
				entry(p.Quint+"Effect", "", "")
				entry(p.Quint, "", cp)
				entry(p.Quint+"Outcome", "", "")
			default:
				entry(p.Quint, "", cp)
				entry(p.Quint+"Effect", "", "")
				entry(p.Quint+"Outcome", "", commit)
			}
		}
		for _, s := range g.c.Sites {
			fs, _ := scalarFields(s.Fields)
			entry(s.Quint, argTemplates(fs), "")
		}
	}
	inv, ev, viol := m.of(name)
	fmt.Fprintf(&b, "\ninvariants: [%s]\n", strings.Join(append(append([]string(nil), inv...), viol...), ", "))
	if len(ev) > 0 {
		fmt.Fprintf(&b, "eventually: [%s]\n", strings.Join(ev, ", "))
	}
	if len(inv)+len(viol) > 0 {
		b.WriteString("\n# The model on its own (gotogether simulate)\nsimulate:\n")
		if len(inv) > 0 {
			fmt.Fprintf(&b, "  holds: [%s]\n", strings.Join(inv, ", "))
		}
		if len(viol) > 0 {
			fmt.Fprintf(&b, "  allows: [%s]\n", strings.Join(viol, ", "))
		}
	}
	return []byte(b.String())
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
