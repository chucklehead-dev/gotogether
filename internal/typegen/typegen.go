// Package typegen writes the Go types of a Quint model's types (gotogether
// types, ROADMAP CW5's static half): each type declaration a Go type that
// itf.Decode fills from the model's values, and State, a field per state
// variable, for a trace's states and connect.StateDriver's projections.
// The model is the source: generation goes from Quint to Go only, and a
// digest of each type in the file's header lets `gotogether types -check`
// name what changed since it was written.
//
// The mapping, by Quint type (Decode's rules, itf/decode.go, which
// connect's fit check mirrors):
//
//   - int, str, bool: int, string, bool;
//   - a record: a struct, a field per record field, exported, tagged
//     `itf:"<field>"` with the Quint name (Decode matches the tag exactly
//     before it ignores case and underscores);
//   - a tuple: a struct of fields F0, F1, ..., filled in order;
//   - Set[T] and List[T]: []T (a set in its elements' canonical order);
//   - K -> V: map[K]V when K's Go type is comparable (a pointer is not
//     counted so: its identity is not its value), else a slice of
//     struct{ Key K; Value V } pairs in the keys' order;
//   - a sum of Some(T) and None alone (Option): *T, nil for None;
//   - a sum whose variants carry nothing (an enum): a string type, a
//     constant per variant, and an Enum method (itf.Encode writes its
//     values as variants);
//   - any other sum: a struct with Tag (a string type of its own, with a
//     constant per variant, tagged `itf:",tag"`) and a field per variant
//     that carries a value, tagged `itf:"<Variant>"` (a variant without a
//     value is its tag alone);
//   - what Go cannot say here becomes itf.Value, with a comment saying
//     why: an uninterpreted type (type T, with no definition), a type
//     variable, a sum two of whose variants Decode cannot tell apart (they
//     differ in case or underscores alone), a type quint names that no
//     module declares.
//
// A Quint type declaration is an alias, so a declared type is a Go alias
// (type Amount = int) unless Go needs a type of its own: a struct (a
// record, a tuple, a sum with values) or an enum's string type. A
// polymorphic declaration (type Box[a] = ...) is no Go type: quint writes
// each application out (Box[int] as the record it is), and so does the
// generator, where it is used.
//
// Names are the Quint names, qualifiers stripped (lib::Item is Item),
// leading underscores dropped and the first letter upper-cased, the rest
// kept (byName is ByName, by_name By_name). An exported Go name is never
// a keyword. A name already taken in its scope (the package, or one
// struct's fields) takes a trailing underscore until it is free: in the
// package, the model's main module's types claim their names first, then
// those of the modules it imports, in the order it imports them (each
// module's by name), then State, then each type's tag type (<Type>Tag)
// and constants (<Type><Variant>); in a sum's struct, Tag comes first.
package typegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/internal/literate"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// Typecheck is quint typecheck's output for spec (its IR, every module of
// the file and of the files it imports, aliases kept by name). A refusal
// names quint's errors at their lines, a literate model's at its
// document's.
func Typecheck(ctx context.Context, quint, spec string) ([]byte, error) {
	if _, err := os.Stat(spec); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the spec %s does not exist: name the model's .qnt (or its literate .md)", spec)
	}
	abs, err := filepath.Abs(spec)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "gotogether-types-*.json")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := quintcmd.Command(ctx, quint, filepath.Dir(abs), "typecheck", filepath.Base(abs), "--out", tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("quint typecheck %s: %w", spec, ctx.Err())
		}
		// with --out, quint writes its errors there
		out = append(out, typecheckErrors(tmp.Name())...)
		text := literate.Rewrite(string(out), filepath.Dir(abs))
		return nil, fmt.Errorf("quint typecheck %s: %w\n%s(fix the model: its types are generated from what quint accepts)", literate.DocOf(spec), err, text)
	}
	return os.ReadFile(tmp.Name())
}

// typecheckErrors are the errors quint typecheck --out wrote to path, one
// per line with where each is (file:line:col, 1-based).
func typecheckErrors(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out struct {
		Errors []struct {
			Explanation string `json:"explanation"`
			Locs        []struct {
				Source string `json:"source"`
				Start  struct{ Line, Col int }
			} `json:"locs"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	var b []byte
	for _, e := range out.Errors {
		if len(e.Locs) > 0 {
			l := e.Locs[0]
			b = fmt.Appendf(b, "%s:%d:%d: ", l.Source, l.Start.Line+1, l.Start.Col+1)
		}
		b = fmt.Appendf(b, "%s\n", e.Explanation)
	}
	return b
}

// A Model is a module's types and state variables, as typecheck output
// has them: the module's own and those of the modules it imports,
// instantiates or exports, transitively (their variables are its state).
type Model struct {
	// Module is the main module
	Module string
	types  []*typeDecl
	vars   []*varDecl
}

// typeDecl is one of the model's type declarations.
type typeDecl struct {
	module string
	name   string // as declared
	key    string // in the digests: name, module::name outside the main module
	params []string
	body   *qeval.Type // nil: uninterpreted (type T, with no definition)
	scope  map[string]*typeDecl
	goName string
}

// varDecl is a state variable.
type varDecl struct {
	name  string // short: qualifiers stripped, as itf.Parse keys a state
	typ   *qeval.Type
	scope map[string]*typeDecl
}

type tcDecl struct {
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	Type           json.RawMessage `json:"type"`
	TypeAnnotation json.RawMessage `json:"typeAnnotation"`
	Params         json.RawMessage `json:"params"`
	ProtoName      string          `json:"protoName"`
	DefName        string          `json:"defName"`
	QualifiedName  string          `json:"qualifiedName"`
}

// Read is module's types and variables in typecheck output tc.
func Read(tc []byte, module string) (*Model, error) {
	var out struct {
		Modules []struct {
			Name         string   `json:"name"`
			Declarations []tcDecl `json:"declarations"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(tc, &out); err != nil {
		return nil, fmt.Errorf("reading quint typecheck's output: %w", err)
	}
	decls := map[string][]tcDecl{}
	var names []string
	for _, m := range out.Modules {
		decls[m.Name] = m.Declarations
		names = append(names, m.Name)
	}
	if _, ok := decls[module]; !ok {
		return nil, fmt.Errorf("the spec has no module %s: name its main module with -main (its modules: %s)", module, strings.Join(names, ", "))
	}
	// the modules whose types and variables are the model's: the main
	// module first, then those it reaches, in the order it reaches them
	closure := []string{module}
	for i := 0; i < len(closure); i++ {
		for _, d := range decls[closure[i]] {
			switch d.Kind {
			case "import", "instance", "export":
				if _, ok := decls[d.ProtoName]; ok && !slices.Contains(closure, d.ProtoName) {
					closure = append(closure, d.ProtoName)
				}
			}
		}
	}
	m := &Model{Module: module}
	own := map[string]map[string]*typeDecl{}
	for _, mod := range names {
		own[mod] = map[string]*typeDecl{}
		for _, d := range decls[mod] {
			if d.Kind != "typedef" {
				continue
			}
			td := &typeDecl{module: mod, name: d.Name, key: d.Name}
			if mod != module {
				td.key = mod + "::" + d.Name
			}
			if len(d.Params) > 0 {
				_ = json.Unmarshal(d.Params, &td.params)
			}
			if len(d.Type) > 0 && string(d.Type) != "null" {
				t, err := qeval.ParseType(d.Type)
				if err != nil {
					return nil, fmt.Errorf("module %s, type %s: %w", mod, d.Name, err)
				}
				td.body = t
			}
			own[mod][d.Name] = td
		}
	}
	scopes := map[string]map[string]*typeDecl{}
	var scopeOf func(mod string, seen map[string]bool) map[string]*typeDecl
	scopeOf = func(mod string, seen map[string]bool) map[string]*typeDecl {
		if s, ok := scopes[mod]; ok {
			return s
		}
		s := map[string]*typeDecl{}
		if seen[mod] {
			return s
		}
		seen[mod] = true
		for _, d := range decls[mod] {
			switch d.Kind {
			case "import", "instance", "export":
			default:
				continue
			}
			if _, ok := decls[d.ProtoName]; !ok {
				continue
			}
			for name, td := range scopeOf(d.ProtoName, seen) {
				as := ""
				switch {
				case d.DefName == "*", d.Kind != "import" && d.QualifiedName == "":
					as = name
				case d.DefName != "":
					if d.DefName == name {
						as = name
					}
				case d.QualifiedName != "":
					as = d.QualifiedName + "::" + name
				default:
					as = d.ProtoName + "::" + name
				}
				if as != "" {
					s[as] = td
				}
			}
		}
		for name, td := range own[mod] { // a module's own names win
			s[name] = td
		}
		scopes[mod] = s
		return s
	}
	seenVar := map[string]bool{}
	for _, mod := range closure {
		sc := scopeOf(mod, map[string]bool{})
		for _, n := range slices.Sorted(maps.Keys(own[mod])) {
			td := own[mod][n]
			td.scope = sc
			m.types = append(m.types, td)
		}
		for _, d := range decls[mod] {
			if d.Kind != "var" {
				continue
			}
			short := d.Name[strings.LastIndex(d.Name, ":")+1:]
			if seenVar[short] {
				continue // itf.Parse keys a state's variables by this name: one field
			}
			seenVar[short] = true
			t, err := qeval.ParseType(d.TypeAnnotation)
			if err != nil {
				return nil, fmt.Errorf("module %s, var %s: %w", mod, d.Name, err)
			}
			m.vars = append(m.vars, &varDecl{name: short, typ: t, scope: sc})
		}
	}
	slices.SortFunc(m.vars, func(a, b *varDecl) int { return strings.Compare(a.name, b.name) })
	return m, nil
}

// Types is how many type declarations the model has.
func (m *Model) Types() int { return len(m.types) }

// Vars is how many state variables the model has (State's fields).
func (m *Model) Vars() int { return len(m.vars) }

// An Entry is one of the model's types or variables in a file's header:
// Kind "type" or "var", its Quint name and its definition's digest.
type Entry struct{ Kind, Name, Digest string }

func (e Entry) String() string { return e.Kind + " " + e.Name + " " + e.Digest }

// Digests are the model's entries, types then variables, each by name: a
// digest of the Quint type, which changes when the declaration does (a
// referenced type's change is that type's).
func (m *Model) Digests() []Entry {
	var out []Entry
	for _, td := range m.types {
		text := "uninterpreted"
		if td.body != nil {
			text = td.body.String()
		}
		if len(td.params) > 0 {
			text = "[" + strings.Join(td.params, ", ") + "] " + text
		}
		out = append(out, Entry{"type", td.key, digest(text)})
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	for _, v := range m.vars {
		out = append(out, Entry{"var", v.name, digest(v.typ.String())})
	}
	return out
}

func digest(s string) string {
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:6])
}

// whole is the digest of all of entries.
func whole(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.String() + "\n")
	}
	return digest(b.String())
}

const (
	headerPrefix = "// Code generated by gotogether types from "
	entryPrefix  = "// gotogether:"
)

// FileEntries are the entries in a generated file's header, and whether
// it has the header gotogether types writes.
func FileEntries(src []byte) ([]Entry, bool) {
	lines := strings.Split(string(src), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], headerPrefix) {
		return nil, false
	}
	var out []Entry
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "//") {
			break
		}
		rest, ok := strings.CutPrefix(l, entryPrefix)
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 3 && (f[0] == "type" || f[0] == "var") {
			out = append(out, Entry{f[0], f[1], f[2]})
		}
	}
	return out, true
}

// Changes are the differences from a file's entries to the model's, one
// each, sorted: "type Item changed", "var stock added", "type Old
// removed".
func Changes(model, file []Entry) []string {
	key := func(e Entry) string { return e.Kind + " " + e.Name }
	have := map[string]string{}
	for _, e := range file {
		have[key(e)] = e.Digest
	}
	want := map[string]bool{}
	var out []string
	for _, e := range model {
		want[key(e)] = true
		switch d, ok := have[key(e)]; {
		case !ok:
			out = append(out, key(e)+" added")
		case d != e.Digest:
			out = append(out, key(e)+" changed")
		}
	}
	for _, e := range file {
		if !want[key(e)] {
			out = append(out, key(e)+" removed")
		}
	}
	slices.Sort(out)
	return out
}

// Options are what a generated file says beyond the model.
type Options struct {
	// Package is the file's package
	Package string
	// From names the model in the header (the spec as given)
	From string
}

// CheckPackage refuses a package name Go would not compile.
func CheckPackage(name string) error {
	if token.IsKeyword(name) {
		return fmt.Errorf("-package %q is a Go keyword: name it otherwise (%stypes, say)", name, name)
	}
	if !token.IsIdentifier(name) || name == "_" {
		return fmt.Errorf("-package %q is no Go package name: name it with a Go identifier (lower case by convention: modeltypes)", name)
	}
	return nil
}

// Generate is the Go file of m's types, gofmt'd.
func Generate(m *Model, o Options) ([]byte, error) {
	if err := CheckPackage(o.Package); err != nil {
		return nil, err
	}
	g := &gen{m: m, taken: map[string]bool{}, consts: map[*typeDecl][]string{}, byID: map[int64][]*typeDecl{}}
	for _, td := range m.types {
		if id := idOf(td.body); id != 0 && len(td.params) == 0 {
			g.byID[id] = append(g.byID[id], td)
		}
	}
	g.name()
	var body bytes.Buffer
	g.state(&body)
	var poly []string
	for _, td := range g.sorted() {
		if len(td.params) > 0 {
			poly = append(poly, fmt.Sprintf("%s[%s]", td.key, strings.Join(td.params, ", ")))
			continue
		}
		g.decl(&body, td)
	}
	var b bytes.Buffer
	entries := m.Digests()
	fmt.Fprintf(&b, "%s%s (module %s); DO NOT EDIT.\n//\n", headerPrefix, filepath.ToSlash(o.From), m.Module)
	fmt.Fprintf(&b, "%stypes %s (the model's types; gotogether types -check compares each)\n", entryPrefix, whole(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "%s%s\n", entryPrefix, e)
	}
	fmt.Fprintf(&b, "\npackage %s\n\n", o.Package)
	if g.usesITF {
		b.WriteString("import \"github.com/chucklehead-dev/gotogether/itf\"\n\n")
	}
	if len(poly) > 0 {
		fmt.Fprintf(&b, "// Polymorphic in the model, so no Go type here: %s. Each use is written out where it occurs (an Option is a pointer).\n\n", strings.Join(poly, ", "))
	}
	b.Write(body.Bytes())
	src, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("typegen: the generated Go does not parse (a bug: report it with the model): %w\n%s", err, b.Bytes())
	}
	return src, nil
}

type gen struct {
	m      *Model
	taken  map[string]bool
	state0 string
	tags   map[*typeDecl]string
	consts map[*typeDecl][]string
	// byID are the monomorphic declarations by their body's node id (a
	// body that applies a polymorphic alias keeps that alias's: several)
	byID    map[int64][]*typeDecl
	usesITF bool
}

// exported is a Quint name as a Go one: the qualifier stripped, leading
// underscores dropped, the first letter upper-cased.
func exported(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	name = strings.TrimLeft(name, "_")
	r, n := utf8.DecodeRuneInString(name)
	if name == "" || !unicode.IsLetter(r) {
		return "X" + name
	}
	return string(unicode.ToUpper(r)) + name[n:]
}

// claim is name, or name with trailing underscores, whichever is free
// first in taken; it is then taken.
func claim(taken map[string]bool, name string) string {
	for taken[name] {
		name += "_"
	}
	taken[name] = true
	return name
}

// name gives the package's names, in the order the package doc says.
func (g *gen) name() {
	for _, td := range g.m.types {
		if len(td.params) == 0 {
			td.goName = claim(g.taken, exported(td.name))
		}
	}
	g.state0 = claim(g.taken, "State")
	g.tags = map[*typeDecl]string{}
	for _, td := range g.m.types {
		if len(td.params) > 0 || td.body == nil || td.body.Kind != "sum" {
			continue
		}
		if option(td.body) || collides(td.body) != "" {
			continue
		}
		if !enum(td.body) {
			g.tags[td] = claim(g.taken, td.goName+"Tag")
		}
		for _, v := range td.body.Fields {
			g.consts[td] = append(g.consts[td], claim(g.taken, td.goName+exported(v.Name)))
		}
	}
}

// sorted are the model's types by Go name (a polymorphic one by its
// Quint name).
func (g *gen) sorted() []*typeDecl {
	out := slices.Clone(g.m.types)
	n := func(td *typeDecl) string {
		if td.goName != "" {
			return td.goName
		}
		return exported(td.name)
	}
	slices.SortStableFunc(out, func(a, b *typeDecl) int { return strings.Compare(n(a), n(b)) })
	return out
}

func enum(t *qeval.Type) bool {
	for _, v := range t.Fields {
		if !v.Type.Nullary() {
			return false
		}
	}
	return true
}

// collides is why itf.Decode cannot tell two of a sum's variants apart
// (it matches a variant's field ignoring case and underscores, the first
// field that matches winning), or "". An enum's tags are compared whole.
func collides(t *qeval.Type) string {
	if enum(t) {
		return ""
	}
	seen := map[string]string{}
	for _, v := range t.Fields {
		k := strings.ToLower(strings.ReplaceAll(v.Name, "_", ""))
		if o, ok := seen[k]; ok {
			return fmt.Sprintf("its variants %s and %s are one name to itf.Decode, which ignores case and underscores", o, v.Name)
		}
		seen[k] = v.Name
	}
	return ""
}

func (g *gen) state(w *bytes.Buffer) {
	fmt.Fprintf(w, "// %s is the model's state, a field per state variable, tagged with its name:\n// itf.Decode fills it from a trace's state (its Vars as a record), and it\n// is connect.StateDriver's projection of the whole state.\ntype %s struct {\n", g.state0, g.state0)
	taken := map[string]bool{}
	for _, v := range g.m.vars {
		ty, note := g.goType(v.typ, v.scope)
		fmt.Fprintf(w, "\t%s %s `itf:%q`%s\n", claim(taken, exported(v.name)), ty, v.name, comment(note))
	}
	w.WriteString("}\n\n")
}

func comment(note string) string {
	if note == "" {
		return ""
	}
	return " // itf.Value: " + note
}

// decl writes td's Go declaration.
func (g *gen) decl(w *bytes.Buffer, td *typeDecl) {
	name := td.goName
	if td.body == nil {
		g.usesITF = true
		fmt.Fprintf(w, "// %s is the model's type %s, uninterpreted (declared with no\n// definition): its values decode as itf.Value.\ntype %s = itf.Value\n\n", name, td.key, name)
		return
	}
	doc := fmt.Sprintf("// %s is the model's type %s: %s.\n", name, td.key, td.body)
	t := td.body
	switch t.Kind {
	case "rec", "tup":
		ty, _ := g.shape(t, td.scope)
		fmt.Fprintf(w, "%stype %s %s\n\n", doc, name, ty)
		return
	case "sum":
		if option(t) {
			break
		}
		if why := collides(t); why != "" {
			g.usesITF = true
			fmt.Fprintf(w, "%s// It is kept as itf.Value: %s.\ntype %s = itf.Value\n\n", doc, why, name)
			return
		}
		if enum(t) {
			fmt.Fprintf(w, "%s// An enum: its values are its variants' names.\ntype %s string\n\n", doc, name)
			fmt.Fprintf(w, "// The variants of %s.\nconst (\n", name)
			for i, v := range t.Fields {
				fmt.Fprintf(w, "\t%s %s = %q\n", g.consts[td][i], name, v.Name)
			}
			fmt.Fprintf(w, ")\n\n// Enum marks %s as a Quint enum: itf.Encode writes its values as variants.\nfunc (%s) Enum() {}\n\n", name, name)
			return
		}
		tag := g.tags[td]
		fmt.Fprintf(w, "%s// Tag is the variant; the field of that name holds its value (a variant\n// without one is its tag alone).\ntype %s %s\n\n", doc, name, g.sum(t, td.scope, tag))
		fmt.Fprintf(w, "// %s is a variant of %s.\ntype %s string\n\n// The variants of %s.\nconst (\n", tag, name, tag, name)
		for i, v := range t.Fields {
			fmt.Fprintf(w, "\t%s %s = %q\n", g.consts[td][i], tag, v.Name)
		}
		w.WriteString(")\n\n")
		return
	}
	ty, note := g.shape(t, td.scope)
	if note != "" {
		doc += "// It is kept as itf.Value: " + note + ".\n"
	}
	fmt.Fprintf(w, "%stype %s = %s\n\n", doc, name, ty)
}

// sum is a struct for sum t: Tag (of type tag), then a field per variant
// that carries a value.
func (g *gen) sum(t *qeval.Type, scope map[string]*typeDecl, tag string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "struct {\n\tTag %s `itf:\",tag\"`\n", tag)
	taken := map[string]bool{"Tag": true}
	for _, v := range t.Fields {
		if v.Type.Nullary() {
			continue
		}
		ty, note := g.goType(v.Type, scope)
		fmt.Fprintf(&b, "\t%s %s `itf:%q`%s\n", claim(taken, exported(v.Name)), ty, v.Name, comment(note))
	}
	b.WriteString("}")
	return b.String()
}

// goType is t as a Go type expression in scope, and why a part of it is
// itf.Value ("" when none is).
func (g *gen) goType(t *qeval.Type, scope map[string]*typeDecl) (string, string) {
	// a declared type quint wrote out (in a polymorphic alias's
	// application) is that type, by the id its body's node keeps, if it
	// is that type: a declaration applying a polymorphic alias (type
	// MaybeItem = Option[Item]) keeps the alias's id, which every other
	// application of it shares
	if tds := g.byID[idOf(t)]; len(tds) > 0 {
		have := g.expand(t, scope, 0).String()
		for _, td := range tds {
			if g.expand(td.body, td.scope, 0).String() == have {
				return td.goName, ""
			}
		}
	}
	return g.shape(t, scope)
}

// expand is t with every alias it names replaced by its definition, for
// comparing types as Quint does, structurally.
func (g *gen) expand(t *qeval.Type, scope map[string]*typeDecl, depth int) *qeval.Type {
	if t == nil || depth > 32 {
		return t
	}
	if t.Kind == "const" {
		if td := resolve(t.Name, scope, g.m); td != nil && td.body != nil && len(td.params) == 0 {
			return g.expand(td.body, td.scope, depth+1)
		}
		return t
	}
	out := *t
	out.Elem, out.Arg, out.Res = g.expand(t.Elem, scope, depth+1), g.expand(t.Arg, scope, depth+1), g.expand(t.Res, scope, depth+1)
	out.Args = nil
	for _, a := range t.Args {
		out.Args = append(out.Args, g.expand(a, scope, depth+1))
	}
	out.Fields = nil
	for _, f := range t.Fields {
		out.Fields = append(out.Fields, qeval.Field{Name: f.Name, Type: g.expand(f.Type, scope, depth+1)})
	}
	return &out
}

// idOf is t's node id where an alias written out keeps it (a structured
// type: a scalar's node is no alias's identity).
func idOf(t *qeval.Type) int64 {
	if t == nil {
		return 0
	}
	switch t.Kind {
	case "rec", "tup", "sum", "set", "list", "fun":
		return t.ID
	}
	return 0
}

// shape is t's Go type written out: goType's, without looking t itself up
// among the declared types (a declaration's body is its own).
func (g *gen) shape(t *qeval.Type, scope map[string]*typeDecl) (string, string) {
	value := func(why string) (string, string) {
		g.usesITF = true
		return "itf.Value", why
	}
	if t == nil {
		return value("quint gave no type")
	}
	var notes []string
	sub := func(u *qeval.Type) string {
		ty, n := g.goType(u, scope)
		if n != "" && !slices.Contains(notes, n) {
			notes = append(notes, n)
		}
		return ty
	}
	joined := func(ty string) (string, string) { return ty, strings.Join(notes, "; ") }
	switch t.Kind {
	case "int":
		return "int", ""
	case "str":
		return "string", ""
	case "bool":
		return "bool", ""
	case "const":
		td := resolve(t.Name, scope, g.m)
		switch {
		case td == nil:
			return value(fmt.Sprintf("the model's type %s is declared in no module quint read", t.Name))
		case len(td.params) > 0:
			return value(fmt.Sprintf("%s is polymorphic, and quint named it without its arguments", t.Name))
		}
		return td.goName, ""
	case "var":
		return value(fmt.Sprintf("%s is a type variable: any value", t.Name))
	case "set", "list":
		return joined("[]" + sub(t.Elem))
	case "fun":
		k, v := sub(t.Arg), sub(t.Res)
		if g.comparable(t.Arg, scope, 0) {
			return joined("map[" + k + "]" + v)
		}
		return joined("[]struct {\n\tKey " + k + "\n\tValue " + v + "\n}")
	case "tup", "rec":
		// each field's itf.Value says why in a comment of its own
		if len(t.Fields) == 0 {
			return "struct{}", ""
		}
		var b strings.Builder
		b.WriteString("struct {\n")
		taken := map[string]bool{}
		for i, f := range t.Fields {
			ty, note := g.goType(f.Type, scope)
			if t.Kind == "tup" {
				fmt.Fprintf(&b, "\tF%d %s%s\n", i, ty, comment(note))
				continue
			}
			fmt.Fprintf(&b, "\t%s %s `itf:%q`%s\n", claim(taken, exported(f.Name)), ty, f.Name, comment(note))
		}
		b.WriteString("}")
		return b.String(), ""
	case "sum":
		if some, ok := t.Option(); ok && some != nil {
			return joined("*" + sub(some))
		}
		if why := collides(t); why != "" {
			return value(why)
		}
		if enum(t) {
			return "string", ""
		}
		ty := g.sum(t, scope, "string")
		// the variants' notes are their fields' comments
		return ty, ""
	}
	return value(fmt.Sprintf("quint's type kind %s has no Go counterpart here", t.Kind))
}

// resolve is the declaration name refers to in scope; failing that (a
// scope the generator did not follow), the one declaration of the model
// whose name, unqualified, is name's.
func resolve(name string, scope map[string]*typeDecl, m *Model) *typeDecl {
	if td, ok := scope[name]; ok {
		return td
	}
	short := name[strings.LastIndex(name, ":")+1:]
	var found *typeDecl
	for _, td := range m.types {
		if td.name == short {
			if found != nil {
				return nil
			}
			found = td
		}
	}
	return found
}

// comparable reports whether t's Go type is comparable and compares
// values as Quint does (a pointer compares its address: not so).
func (g *gen) comparable(t *qeval.Type, scope map[string]*typeDecl, depth int) bool {
	if t == nil || depth > 32 {
		return false
	}
	switch t.Kind {
	case "int", "str", "bool":
		return true
	case "const":
		td := resolve(t.Name, scope, g.m)
		if td == nil || td.body == nil || len(td.params) > 0 {
			return false
		}
		return g.comparable(td.body, td.scope, depth+1)
	case "tup", "rec":
		for _, f := range t.Fields {
			if !g.comparable(f.Type, scope, depth+1) {
				return false
			}
		}
		return true
	case "sum":
		if option(t) || collides(t) != "" {
			return false
		}
		for _, v := range t.Fields {
			if !v.Type.Nullary() && !g.comparable(v.Type, scope, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// option reports whether t is Quint's Option (Some(T) | None, which is a
// pointer); a sum of None alone is an enum.
func option(t *qeval.Type) bool {
	some, ok := t.Option()
	return ok && some != nil
}
