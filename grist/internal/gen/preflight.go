package gen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// Finding is one actionable result of model preflight. Paths are relative to
// Directory; an absent Line means the source inspection has no exact line.
// Confidence distinguishes file/catalog facts from conservative text scans.
type Finding struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity"`
	Message    string   `json:"message"`
	Path       string   `json:"path,omitempty"`
	Line       int      `json:"line,omitempty"`
	Symbol     string   `json:"symbol,omitempty"`
	Fix        string   `json:"fix"`
	Confidence string   `json:"confidence"`
	Details    []string `json:"details,omitempty"`
	blocking   bool
}

// Check says which inspection ran. A blocked check is not a successful one;
// Quint typechecking is deliberately not performed by this nonwriting scan.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Change is a mechanical generated-file action, never a handwritten model edit.
type Change struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
}

// Preflight combines independent model-maintenance findings and the generated
// changes they permit. The executable plan is private: its JSON is evidence,
// not an executable patch or a second authoring format.
type Preflight struct {
	Format    string    `json:"format"`
	App       string    `json:"app"`
	Directory string    `json:"directory"`
	Scope     string    `json:"scope"`
	Complete  bool      `json:"complete"`
	Findings  []Finding `json:"findings"`
	Checks    []Check   `json:"checks"`
	Changes   []Change  `json:"changes"`
	app       App
	dir       string
	generated *Generated
	blocked   bool
	inputs    string
}

// OK means every performed maintenance check has no error finding. It asserts
// neither Quint type correctness nor conformance of the application's code.
func (p *Preflight) OK() bool {
	if p == nil || !p.Complete {
		return false
	}
	for _, f := range p.Findings {
		if f.Severity == "error" {
			return false
		}
	}
	return true
}

// CanGenerate distinguishes safe mechanical updates from successful preflight:
// missing handwritten definitions and stale files still require work afterward.
func (p *Preflight) CanGenerate() bool { return p != nil && p.generated != nil && !p.blocked }

// MarshalJSON adds derived readiness without trusting mutable success flags.
// A decoded report cannot acquire the private executable plan.
func (p *Preflight) MarshalJSON() ([]byte, error) {
	type report Preflight
	return json.Marshal(struct {
		*report
		OK          bool `json:"ok"`
		CanGenerate bool `json:"canGenerate"`
	}{(*report)(p), p.OK(), p.CanGenerate()})
}

// String is the common human-readable report used by the CLI and test helper.
func (p *Preflight) String() string {
	if p == nil {
		return "model preflight: no report"
	}
	var b strings.Builder
	state := "needs attention"
	if p.OK() {
		state = "ready"
	}
	fmt.Fprintf(&b, "%s model preflight: %s (catalog, generation and source inspection; no Quint typecheck)", p.App, state)
	for _, f := range p.Findings {
		fmt.Fprintf(&b, "\n  %s [%s]", f.Severity, f.Code)
		if f.Path != "" {
			fmt.Fprintf(&b, " %s", f.Path)
			if f.Line > 0 {
				fmt.Fprintf(&b, ":%d", f.Line)
			}
		}
		if f.Symbol != "" {
			fmt.Fprintf(&b, " (%s)", f.Symbol)
		}
		fmt.Fprintf(&b, ": %s", f.Message)
		if f.Confidence == "source_scan" {
			b.WriteString(" [source scan; not semantic proof]")
		}
		if f.Fix != "" {
			fmt.Fprintf(&b, "\n    fix: %s", f.Fix)
		}
		for _, d := range f.Details {
			fmt.Fprintf(&b, "\n    %s", d)
		}
	}
	for _, c := range p.Checks {
		if c.Status == "blocked" {
			fmt.Fprintf(&b, "\n  not checked %s: %s", c.Name, c.Reason)
		}
	}
	return b.String()
}

func (p *Preflight) finding(code, severity, path, symbol, message, fix, confidence string, blocking bool, details ...string) {
	p.Findings = append(p.Findings, Finding{Code: code, Severity: severity, Path: path, Symbol: symbol, Message: message, Fix: fix, Confidence: confidence, blocking: blocking, Details: details})
	p.blocked = p.blocked || blocking
}

func (p *Preflight) check(name, status, reason string) {
	p.Checks = append(p.Checks, Check{Name: name, Status: status, Reason: reason})
}
func (p *Preflight) ioError(path string, err error) {
	p.finding("model.io_error", "error", path, "", fmt.Sprintf("cannot inspect %s: %v", path, err), "restore readable regular files and inspect again; generation will not overwrite unreadable inputs", "exact", true)
}

func (p *Preflight) finish() {
	// Check status describes its result, not merely that the code ran. Manual
	// findings may fail a check without making generated-file updates unsafe.
	codes := map[string][]string{
		"sources":         {"model.rules_missing", "model.invariants_missing"},
		"annotations":     {"model.marks_invalid"},
		"definitions":     {"model.definition_missing", "model.definition_conflict"},
		"imports":         {"model.import_missing", "model.import_cycle"},
		"model_review":    {"model.catalog_unused"},
		"generated_files": {"model.generated_stale", "model.generated_obsolete"},
	}
	for i := range p.Checks {
		if p.Checks[i].Status != "pass" {
			continue
		}
		for _, f := range p.Findings {
			if f.Severity == "error" && slices.Contains(codes[p.Checks[i].Name], f.Code) {
				p.Checks[i].Status = "fail"
				break
			}
		}
	}
	p.Complete = true
	for _, c := range p.Checks {
		if c.Status == "blocked" {
			p.Complete = false
		}
	}
	sort.SliceStable(p.Findings, func(i, j int) bool {
		a, b := p.Findings[i], p.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity == "error"
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Path < p.Changes[j].Path })
}

// Inspect collects independent catalog, generated-file and handwritten-source
// findings without writing anything. Structural errors block dependent plans;
// missing definitions do not hide mechanically stale generated artifacts.
func Inspect(app App, dir string) *Preflight {
	p := &Preflight{Format: formats.ModelPreflight.Name(), Directory: filepath.Clean(dir), Scope: "catalog_generation_source_inspection", Findings: []Finding{}, Checks: []Check{}, Changes: []Change{}, app: app}
	abs, err := filepath.Abs(dir)
	if err != nil {
		p.ioError(dir, err)
		p.check("sources", "blocked", "the inspection directory cannot be resolved")
		p.finish()
		return p
	}
	p.dir = abs
	c := app.Catalog()
	p.App = c.Name
	valid := true
	if err := app.Verify(); err != nil {
		p.finding("model.catalog_invalid", "error", "", "", err.Error(), "fix the application catalog before generating its model", "exact", true)
		valid = false
	}
	g, err := newGen(c)
	if err != nil {
		p.finding("model.catalog_invalid", "error", "", "", err.Error(), "fix the catalog names and model declarations before generating", "exact", true)
		valid = false
	}
	if !valid {
		p.check("catalog", "fail", "")
		p.check("generation", "blocked", "the application catalog is invalid")
		p.check("quint_typecheck", "not_checked", "preflight does not invoke Quint")
		p.finish()
		return p
	}
	p.check("catalog", "pass", "")
	before, snapshotErr := inputSnapshot(abs)
	if snapshotErr != nil {
		p.ioError(".", snapshotErr)
	}
	own, ownErr := readOwn(abs, modelFile(c.Name))
	rules, rulesErr := readOwn(abs, rulesFile(c.Name))
	for _, s := range []struct {
		path       string
		data       []byte
		err        error
		code, what string
	}{{modelFile(c.Name), own, ownErr, "model.invariants_missing", "the app's invariants"}, {rulesFile(c.Name), rules, rulesErr, "model.rules_missing", "the app's rules"}} {
		if s.err != nil {
			p.ioError(s.path, s.err)
		} else if s.data == nil {
			p.finding(s.code, "error", s.path, "", "there is no "+s.path+", "+s.what, fmt.Sprintf("`%s model -init` creates a starter only where handwritten files are absent; write the application's rules and invariants", c.Name), "exact", false)
		}
	}
	if ownErr != nil || rulesErr != nil {
		p.check("sources", "fail", "")
	} else {
		p.check("sources", "pass", "")
	}
	if ownErr == nil {
		if err := oldLayout(c.Name, g.aq, own); err != nil {
			p.finding("model.layout_invalid", "error", modelFile(c.Name), g.aq+"Rules", err.Error(), "move the existing Rules module to "+rulesFile(c.Name)+" and fix imports before regeneration", "source_scan", true)
			p.check("layout", "fail", "")
		} else {
			p.check("layout", "pass", "")
		}
	} else {
		p.check("layout", "blocked", "the handwritten model cannot be read")
	}
	g.defined = definedIn(rules, g.aq+"Rules")
	g.bodies = bodiesIn(own, g.aq)
	for n, b := range bodiesIn(rules, g.aq+"Rules") {
		g.bodies[n] = b
	}
	events, steps := g.eventsFile(), g.stepsFile()
	for _, clash := range g.clashes {
		p.finding("model.definition_conflict", "error", "", "", clash, "rename the colliding catalog declaration", "exact", true)
	}
	marks, markErr := parseMarks(own, g.aq)
	for _, s := range []struct {
		path    string
		src     []byte
		readErr error
	}{{modelFile(c.Name), own, ownErr}, {rulesFile(c.Name), rules, rulesErr}} {
		if s.readErr != nil {
			continue
		}
		if _, err := parseMarks(s.src, g.aq); err != nil {
			p.finding("model.marks_invalid", "error", s.path, "", fmt.Sprintf("grist model: %s: %v", s.path, err), "correct the grist annotation at the named definition", "source_scan", true)
		}
	}
	if ownErr != nil || rulesErr != nil {
		p.check("annotations", "blocked", "handwritten sources cannot be read")
	} else if markErr != nil {
		p.check("annotations", "fail", "")
	} else {
		p.check("annotations", "pass", "")
	}
	m, r := g.ownClashes(own)
	for _, s := range []struct {
		path  string
		names []string
	}{{modelFile(c.Name), m}, {rulesFile(c.Name), r}} {
		if len(s.names) > 0 {
			p.finding("model.definition_conflict", "error", s.path, strings.Join(s.names, ", "), fmt.Sprintf("grist model: %s defines %s, which the generated %s defines now (an obligation the catalog implies, judged by the bindings): delete the app's own, or rename it if it says more", s.path, strings.Join(s.names, ", "), stepsFile(c.Name)), "rename or remove the handwritten definition that collides with the generated definition", "source_scan", true)
		}
	}
	out := Generated{Files: map[string][]byte{"model/grist.qnt": Library, genFile(c.Name): events, stepsFile(c.Name): steps}, Holes: g.holes}
	if markErr == nil && ownErr == nil {
		out.Files[c.Name+".binding.yaml"] = g.binding("default", marks)
		out.Files[c.Name+".calls.binding.yaml"] = g.binding("calls", marks)
		for _, v := range marks.variants() {
			out.Files[c.Name+"."+v+".binding.yaml"] = g.binding(v, marks)
		}
	}
	if rulesErr == nil {
		for _, h := range g.holes {
			if !g.defined[h.Name] {
				out.Missing = append(out.Missing, h.Name)
				p.finding("model.definition_missing", "error", rulesFile(c.Name), h.Name, "the handwritten Rules module does not define "+h.Name+", which the generated skeleton calls", "define "+h.Name+" in module "+g.aq+"Rules; for instance: "+h.Stub, "source_scan", false, h.Doc)
			}
		}
		p.check("definitions", "pass", "")
	} else {
		p.check("definitions", "blocked", "the handwritten rules cannot be read")
	}
	if prev, err := readOwn(abs, genFile(c.Name)); err != nil {
		p.ioError(genFile(c.Name), err)
	} else if prev != nil {
		for _, w := range inputsChanged(c.Name, prev, events) {
			out.Warnings = append(out.Warnings, w)
			p.finding("model.inputs_changed", "warning", rulesFile(c.Name), "", w, "review and update the handwritten model for the changed catalog inputs", "exact", false)
		}
	}
	prevLock, lockErr := readOwn(abs, LockFile)
	if lockErr != nil {
		p.ioError(LockFile, lockErr)
		p.check("stored_schema", "blocked", "the stored schema lock cannot be read")
	} else {
		lock, errs, ws := lockOf(c, prevLock)
		for _, e := range errs {
			p.finding("model.lock_invalid", "error", LockFile, "", "grist model: "+c.Name+": "+e, "preserve stored event versions; add a new version and upcaster for incompatible shape changes", "exact", true)
		}
		if len(errs) > 0 {
			p.check("stored_schema", "fail", "")
		} else {
			p.check("stored_schema", "pass", "")
			out.Files[LockFile] = lock
			if !bytes.Equal(prevLock, lock) {
				out.Lock = lockChanges(prevLock, lock)
			}
		}
		for _, w := range ws {
			out.Warnings = append(out.Warnings, w)
			p.finding("model.lock_review", "warning", LockFile, "", w, "review the stored-schema change and its migration implications", "exact", false)
		}
	}
	if ownErr == nil && rulesErr == nil {
		scanErr := readGoSources(abs)
		if scanErr != nil {
			p.ioError("*.go", scanErr)
			p.check("model_review", "blocked", "Go sources used by heuristic inspection cannot be read")
		} else {
			ws, errs := forgotten(c, abs, own, rules)
			for _, e := range errs {
				p.finding("model.catalog_unused", "error", "", "", "grist model: "+c.Name+": "+e, "declare the block that appends the event, or remove the unused registration", "source_scan", true)
			}
			for _, w := range ws {
				out.Warnings = append(out.Warnings, w)
				p.finding("model.possible_omission", "warning", "", "", w, "review this source-scan observation; it is not proof that behavior is untested or irrelevant", "source_scan", false)
			}
			p.check("model_review", "pass", "")
		}
	} else {
		p.check("model_review", "blocked", "handwritten sources cannot be read")
	}
	if cycle, err := importCycle(abs, out.Files); err != nil {
		p.ioError("model", err)
		p.check("imports", "fail", "")
	} else if cycle != nil {
		p.finding("model.import_cycle", "error", modelFile(c.Name), "", fmt.Sprintf("grist model: %s import each other in a cycle (%s), which quint's REPL refuses (QNT101: Multiple modules conflict on the same name): each file imports only the ones below it in the model's layout, docs/grist.md §3.2 (the app's rules import grist and the events, never the steps or the invariants)", strings.Join(sortedUnique(cycle), ", "), strings.Join(cycle, " -> ")), "remove the backwards import so handwritten rules do not import generated steps or invariants", "source_scan", true)
		p.check("imports", "fail", "")
	} else {
		if ownErr == nil && own != nil {
			for _, module := range []string{g.aq, g.aq + "Calls"} {
				for _, want := range []string{g.aq + "Events", g.aq + "Rules"} {
					if !directImport(string(own), module, want) {
						p.finding("model.import_missing", "error", modelFile(c.Name), module+"/"+want, fmt.Sprintf("module %s does not import %s itself; the validator's evaluator cannot compile its model", module, want), fmt.Sprintf("add `import %s.*` inside module %s, from \"./%s_gen\" for events or \"./%s_rules\" for rules, beside the skeleton import", want, module, c.Name, c.Name), "source_scan", false)
					}
				}
			}
		}
		p.check("imports", "pass", "")
	}
	if u, ok := app.(interface{ CheckUpcasters() error }); ok {
		if err := u.CheckUpcasters(); err != nil {
			p.finding("model.upcaster_invalid", "error", LockFile, "", err.Error(), "fix the upcaster or explicitly declare the intended dropped fields before regenerating the schema lock", "exact", true)
			p.check("upcasters", "fail", "")
		} else {
			p.check("upcasters", "pass", "")
		}
	} else {
		p.check("upcasters", "not_checked", "the supplied catalog has no upcaster checker")
	}
	if !p.blocked {
		p.generated = &out
		p.inspectChanges()
	} else {
		p.check("generated_files", "blocked", "structural or unreadable-input errors prevent a safe generated-file plan")
	}
	if !p.blocked {
		if s, err := inputSnapshot(abs); err != nil {
			p.ioError(".", err)
			p.generated = nil
		} else if s != before {
			p.finding("model.inputs_unstable", "error", ".", "", "model inputs changed during inspection", "wait for edits to finish, then inspect the model again", "exact", true)
			p.generated = nil
		} else {
			p.inputs = s
		}
	}
	p.check("quint_typecheck", "not_checked", "run the normal validation/typecheck workflow after maintenance; no Quint process was started")
	p.finish()
	return p
}

func readGoSources(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := os.ReadFile(path); err != nil {
			return err
		}
	}
	return nil
}

func directImport(src, module, name string) bool {
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

func (p *Preflight) inspectChanges() {
	g := p.generated
	for _, path := range g.paths() {
		if err := safeTarget(p.dir, path); err != nil {
			p.ioError(path, err)
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.dir, path))
		operation := "update"
		if errors.Is(err, os.ErrNotExist) {
			operation = "create"
		} else if err != nil {
			p.ioError(path, err)
			continue
		} else if bytes.Equal(b, g.Files[path]) {
			continue
		}
		p.Changes = append(p.Changes, Change{Path: path, Operation: operation})
		f := Finding{Code: "model.generated_stale", Severity: "error", Path: path, Message: "generated file differs from what the application catalog generates", Fix: fmt.Sprintf("regenerate with `%s model` and commit the generated change", p.App), Confidence: "exact"}
		if path == LockFile {
			f.Details = slices.Clone(g.Lock)
		}
		p.Findings = append(p.Findings, f)
	}
	old, err := filepath.Glob(filepath.Join(p.dir, p.App+".*.binding.yaml"))
	if err != nil {
		p.ioError(p.App+".*.binding.yaml", err)
	} else {
		for _, path := range old {
			rel := filepath.Base(path)
			if g.Files[rel] != nil {
				continue
			}
			if err := safeTarget(p.dir, rel); err != nil {
				p.ioError(rel, err)
				continue
			}
			b, err := os.ReadFile(path)
			if err != nil {
				p.ioError(rel, err)
				continue
			}
			if !bytes.HasPrefix(b, []byte(bindingHeader+p.App+" App's catalog (")) {
				continue
			}
			p.Changes = append(p.Changes, Change{Path: rel, Operation: "remove"})
			p.finding("model.generated_obsolete", "error", rel, "", "this generated binding variant is no longer declared by the handwritten model", fmt.Sprintf("regenerate with `%s model` to remove the obsolete generated binding", p.App), "exact", false)
		}
	}
	for _, c := range p.Changes {
		if err := safeTarget(p.dir, c.Path); err != nil {
			p.ioError(c.Path, err)
		}
	}
	if p.blocked {
		p.check("generated_files", "fail", "")
	} else {
		p.check("generated_files", "pass", "")
	}
}

// Apply revalidates this plan, writes only generated files and removes only
// obsolete owned bindings, then inspects again. Advisory review findings from
// before regeneration survive: advancing generated baselines is not model review.
func (p *Preflight) Apply() (after *Preflight, err error) {
	defer func() {
		if after != nil {
			after.RetainWarnings(p)
		}
	}()
	if p == nil || p.app == nil {
		return p, errors.New("model preflight has no executable plan; inspect the application again")
	}
	current := Inspect(p.app, p.dir)
	if !p.CanGenerate() {
		return current, errors.New("model preflight cannot generate safely: fix structural, catalog and input errors first")
	}
	if !current.CanGenerate() || p.inputs != current.inputs || !sameGenerated(p.generated, current.generated) {
		return current, errors.New("model preflight plan is stale: inputs changed; inspect again before applying generated changes")
	}
	for _, c := range current.Changes {
		if err := safeTarget(p.dir, c.Path); err != nil {
			return current, err
		}
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return current, err
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return current, err
	}
	defer root.Close()
	for _, c := range current.Changes {
		if c.Operation == "remove" {
			if err := root.Remove(c.Path); err != nil {
				return Inspect(p.app, p.dir), err
			}
			continue
		}
		if err := root.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
			return Inspect(p.app, p.dir), err
		}
		if err := root.WriteFile(c.Path, current.generated.Files[c.Path], 0o644); err != nil {
			return Inspect(p.app, p.dir), err
		}
	}
	next := Inspect(p.app, p.dir)
	return next, nil
}

// RetainWarnings carries advisory review findings across mechanical baseline
// updates. Regenerating files is not an acknowledgment of model review.
func (p *Preflight) RetainWarnings(before *Preflight) {
	if p == nil || before == nil {
		return
	}
	for _, f := range before.Findings {
		if f.Severity != "warning" {
			continue
		}
		found := false
		for _, n := range p.Findings {
			if n.Code == f.Code && n.Path == f.Path && n.Symbol == f.Symbol && n.Message == f.Message {
				found = true
				break
			}
		}
		if !found {
			p.Findings = append(p.Findings, f)
		}
	}
	p.finish()
}

func sameGenerated(a, b *Generated) bool {
	if a == nil || b == nil || len(a.Files) != len(b.Files) {
		return false
	}
	for path, content := range a.Files {
		if !bytes.Equal(content, b.Files[path]) {
			return false
		}
	}
	return true
}

func safeTarget(dir, rel string) error {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("generated path %q leaves the application directory", rel)
	}
	for path := filepath.Join(dir, clean); ; path = filepath.Dir(path) {
		st, err := os.Lstat(path)
		if err == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("generated path %s traverses a symlink; replace it with a regular in-directory path", rel)
			}
			if path == filepath.Join(dir, clean) && !st.Mode().IsRegular() {
				return fmt.Errorf("generated target %s is not a regular file", rel)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if path == dir {
			break
		}
		if filepath.Dir(path) == path {
			return fmt.Errorf("generated path %s is outside the inspection directory", rel)
		}
	}
	// The inspection root may itself be nested under a symlink. Refuse it as
	// well: ownership of a relative generated path must not escape by ancestry.
	for path := filepath.Dir(dir); ; path = filepath.Dir(path) {
		st, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("inspection directory traverses symlink %s; use its real directory before generating", path)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return nil
}

// inputSnapshot covers the package source scans, lock, generated files and
// recursively reached Quint inputs. Paths and missing imports participate so
// adding a formerly absent input also invalidates a plan.
func inputSnapshot(dir string) (string, error) {
	paths := map[string]bool{}
	for _, pattern := range []string{"*.go", "*.binding.yaml", LockFile} {
		ps, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return "", err
		}
		for _, p := range ps {
			paths[p] = true
		}
	}
	model := filepath.Join(dir, "model")
	if err := filepath.WalkDir(model, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".qnt") {
			paths[path] = true
		}
		return nil
	}); err != nil {
		return "", err
	}
	var addImports func(string) error
	seen := map[string]bool{}
	addImports = func(path string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		paths[path] = true
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if m := importLine.FindStringSubmatch(line); m != nil {
				to := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(m[1])+".qnt"))
				if err := addImports(to); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for path := range paths {
		if strings.HasSuffix(path, ".qnt") {
			if err := addImports(path); err != nil {
				return "", err
			}
		}
	}
	h := sha256.New()
	for _, path := range sortedKeys(paths) {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(h, "%s\x00missing\x00", path)
			continue
		}
		if err != nil {
			return "", err
		}
		st, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		record, _ := json.Marshal(struct {
			Path, Mode string
			Data       []byte
		}{path, st.Mode().String(), b})
		h.Write(record)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
