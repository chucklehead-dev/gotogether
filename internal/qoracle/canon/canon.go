// Package canon is the canonical form of quint's JSON outputs (Q3,
// docs/q2-front-end.md §4.2): `quint parse --out` with its source map,
// `quint typecheck --out` and `quint compile --target json`. Two front
// ends that agree give equal canonical forms although their ids differ,
// and where they disagree the difference is local, at the declaration
// that differs.
//
// The form:
//   - Every node id becomes the path of the node that first carries it in
//     a walk of the modules in their order, each declaration's fields in
//     sorted order (`m/step/expr/args/1`): a declaration's nodes are named
//     from the declaration, never from a counter running across the file,
//     so ids shifted (another module first) change nothing, and a change
//     to one declaration renames nothing outside it. A node id that occurs
//     twice (an alias's type inlined at each use) keeps one path, so the
//     sharing is compared too. Every reference to an id is rewritten: the
//     keys and ids of `table`, the keys of `types` and `effects`, the ids
//     of params, opdefs and type nodes, and the source map's keys. An
//     entry of the table, types or effects keyed by an id no module holds
//     is dropped and counted (`$dropped`).
//   - The compile IR's names that embed an id (quint's unshadower:
//     a lambda parameter `p_<lambda id>`, a shadowing let `x_<let id>`,
//     binding.EvalModule's `__gtg_h0_<id>`) are rewritten: a suffix that
//     is the binding node's own id becomes `_^`; any other (an instance's
//     copy keeps the id of the original's lambda) becomes `_#k`, k
//     numbered by first appearance within its declaration.
//   - Type, row, effect and entity variables (`_t5`, `t_z_35`,
//     `tail__t6`, `_v13`) are renamed `$k` in order of first appearance
//     within each entry of `types` or `effects`, the unit in which every
//     reader (qeval's TypeOf, binding's types[id]) reads them.
//   - Errors become their code (read from `[QNTnnn]`), their source made
//     relative to Options.Roots, and their positions' line and column.
//   - Tier Read keeps the fields gotogether reads (§1.2): of the compile
//     IR what qeval.Load and its types read, of the typecheck output what
//     binding and typegen read, of a parse its modules, table, errors and
//     source-map spans. Tier All keeps every field.
//
// A non-self suffix on a let the user named `y_10` is canonicalized as
// if quint had renamed it: equal inputs stay equal, and two outputs that
// differ only by such a name and an instance's suffixes could be told
// apart where they mean the same (a false difference, never a false
// agreement).
package canon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kind is which of quint's outputs a document is.
type Kind int

const (
	// Parse is `quint parse --out` (with --source-map's map, if given).
	Parse Kind = iota
	// Typecheck is `quint typecheck --out`: the unflattened modules and
	// their types.
	Typecheck
	// Compile is `quint compile --target json`: one flattened module.
	Compile
)

// String names the kind as reports print it.
func (k Kind) String() string {
	switch k {
	case Parse:
		return "parse"
	case Typecheck:
		return "typecheck"
	case Compile:
		return "compile"
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// Tier is which fields the form keeps.
type Tier int

const (
	// Read keeps what gotogether reads of the output (§1.2's consumed
	// subset): the comparison Q2's front end is held to first.
	Read Tier = iota
	// All keeps every field, renumbered and renamed.
	All
)

// Options says how to canonicalize a document.
type Options struct {
	Kind Kind
	Tier Tier
	// Roots are directories an error's or the source map's paths are made
	// relative to (the first that contains a path), slash-separated, so
	// that two runs in different directories agree.
	Roots []string
}

// A Form is a canonical document: a tree of maps, slices, strings,
// json.Numbers, bools and nils.
type Form struct {
	Tree any
}

// JSON is the form's encoding: sorted keys, indented, the same bytes for
// equal forms.
func (f *Form) JSON() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(f.Tree); err != nil {
		panic(err) // the tree holds only what decoding made
	}
	return b.Bytes()
}

// Equal says whether two forms are the same.
func Equal(a, b *Form) bool { return bytes.Equal(a.JSON(), b.JSON()) }

// Of is data's canonical form. sourceMap is `--source-map`'s document for
// a Parse, or nil.
func Of(data, sourceMap []byte, o Options) (*Form, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("canon: the %s output: %w", o.Kind, err)
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("canon: the %s output is not a JSON object", o.Kind)
	}
	var sm map[string]any
	if len(sourceMap) > 0 {
		d, err := decode(sourceMap)
		if err != nil {
			return nil, fmt.Errorf("canon: the source map: %w", err)
		}
		if sm, ok = d.(map[string]any); !ok {
			return nil, fmt.Errorf("canon: the source map is not a JSON object")
		}
	}
	c := &canonicalizer{o: o, ids: map[string]string{}, binds: map[string]map[string]bool{}, bound: map[string]bool{}, sfx: map[string]map[string]int{}, declOf: map[string]string{}, dropped: map[string]int{}}
	return &Form{Tree: c.run(top, sm)}, nil
}

func decode(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

type canonicalizer struct {
	o Options
	// ids maps an id, as its JSON text (a number, or a form's path), to
	// its canonical path
	ids map[string]string
	// binds is the names each lambda and let binds, by its id (name);
	// sfx numbers, per declaration, the suffixes that are no id
	binds map[string]map[string]bool
	// bound is every name some lambda or let binds, less its suffix
	bound map[string]bool
	sfx   map[string]map[string]int
	// declOf is the declaration path each id first occurs in, and scope
	// the declaration being written
	declOf map[string]string
	scope  string
	// layer is each declaration's toposort layer (order.go); kept the
	// ids the written modules hold (Read: an entry of another's is
	// outside the tier)
	layer    map[string]int
	kept     map[string]bool
	modOrder []int
	modLayer map[string]int
	dropped  map[string]int
}

// idText is v as an id's key: a number's text or a form's string.
func idText(v any) (string, bool) {
	switch x := v.(type) {
	case json.Number:
		return x.String(), true
	case string:
		return x, true
	}
	return "", false
}

func (c *canonicalizer) run(top, sm map[string]any) any {
	mods, _ := top["modules"].([]any)
	modKeys := keysOf(mods, func(_ int, m map[string]any) string {
		s, _ := m["name"].(string)
		return s
	}, "module")
	// the walk follows the canonical order, never quint's within a layer:
	// an id two declarations share takes the path of the first in it
	c.layer = layers(top)
	ml := moduleLayers(mods)
	c.modOrder = canonicalOrder(len(mods), func(i int) int {
		mm, _ := mods[i].(map[string]any)
		name, _ := mm["name"].(string)
		return ml[name]
	}, func(i int) string { return modKeys[i] })
	c.modLayer = ml
	// pass 1: every id the modules hold, and the names that embed one
	for _, i := range c.modOrder {
		mm, ok := mods[i].(map[string]any)
		if !ok {
			continue
		}
		c.scope = ""
		c.assign(mm["id"], modKeys[i])
		decls, _ := mm["declarations"].([]any)
		dkeys := declKeys(decls)
		for _, j := range c.declOrder(decls, dkeys) {
			d := decls[j]
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			path := modKeys[i] + "/" + dkeys[j]
			c.scope = path
			c.walkIDs(dm, path)
			if c.o.Kind == Compile {
				c.collectBinders(dm)
			}
		}
	}

	out := map[string]any{}
	if old, ok := top["$dropped"].(map[string]any); ok {
		for k, v := range old {
			if n, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
				c.dropped[k] += n
			}
		}
	}
	var outMods []any
	for _, i := range c.modOrder {
		mm, ok := mods[i].(map[string]any)
		if !ok {
			continue
		}
		if c.o.Kind == Compile && c.o.Tier == Read && top["main"] != nil && mm["name"] != top["main"] {
			continue // qeval reads the main module alone
		}
		mo := c.module(mm, modKeys[i]).(map[string]any)
		name, _ := mm["name"].(string)
		mo["$layer"] = json.Number(strconv.Itoa(c.modLayer[name]))
		outMods = append(outMods, mo)
	}
	c.scope = ""
	out["modules"] = orEmpty(outMods)
	c.kept = map[string]bool{}
	collectIDs(out["modules"], c.kept)
	if c.o.Kind == Compile || c.o.Kind == Parse || c.o.Tier == All {
		if t, ok := top["table"].(map[string]any); ok {
			out["table"] = c.table(t)
		}
	}
	if c.o.Kind != Parse {
		if t, ok := top["types"].(map[string]any); ok {
			out["types"] = c.entries("types", t, c.typeEntry)
		}
	}
	if c.o.Tier == All {
		if t, ok := top["effects"].(map[string]any); ok {
			out["effects"] = c.entries("effects", t, c.effectEntry)
		}
		for _, k := range []string{"stage", "warnings"} {
			if v, ok := top[k]; ok {
				out[k] = c.generic(v, "")
			}
		}
	}
	if m, ok := top["main"]; ok {
		out["main"] = m
	}
	out["errors"] = c.errors(top["errors"])
	if sm != nil || top["sourceMap"] != nil {
		if sm == nil {
			sm, _ = top["sourceMap"].(map[string]any)
		}
		out["sourceMap"] = c.sourceMap(sm)
	}
	if len(c.dropped) > 0 {
		d := map[string]any{}
		for k, n := range c.dropped {
			d[k] = json.Number(strconv.Itoa(n))
		}
		out["$dropped"] = d
	}
	return out
}

func orEmpty(s []any) []any {
	if s == nil {
		return []any{}
	}
	return s
}

// keysOf names each element of xs by name, or by kind and position when it
// has none; a repeated name takes ~2, ~3.
func keysOf(xs []any, name func(int, map[string]any) string, kind string) []string {
	keys := make([]string, len(xs))
	seen := map[string]int{}
	unnamed := 0
	for i, x := range xs {
		m, _ := x.(map[string]any)
		k := ""
		if m != nil {
			k = name(i, m)
		}
		if k == "" {
			k = fmt.Sprintf("%s@%d", kind, unnamed)
			unnamed++
		}
		seen[k]++
		if n := seen[k]; n > 1 {
			k = fmt.Sprintf("%s~%d", k, n)
		}
		keys[i] = k
	}
	return keys
}

func declKeys(decls []any) []string {
	return keysOf(decls, func(_ int, d map[string]any) string {
		if s, ok := d["name"].(string); ok && s != "" {
			// a name quint made from an id (a destructuring's temporary)
			// is keyed without it: its repeats are told apart by ~n
			for _, p := range generatedPrefixes {
				if last := lastSegment(s); strings.HasPrefix(last, p) {
					return strings.TrimSuffix(s, last) + p + "@"
				}
			}
			return s
		}
		// imports, exports and instances: what they name
		var parts []string
		for _, f := range []string{"protoName", "defName", "qualifiedName", "fromSource"} {
			if s, ok := d[f].(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		k, _ := d["kind"].(string)
		return k + ":" + strings.Join(parts, ",")
	}, "decl")
}

func (c *canonicalizer) assign(v any, path string) {
	k, ok := idText(v)
	if !ok {
		return
	}
	if _, seen := c.ids[k]; !seen {
		c.ids[k] = path
		c.declOf[k] = c.scope
	}
}

// walkIDs gives every id under n its path, in sorted field order.
func (c *canonicalizer) walkIDs(n any, path string) {
	switch x := n.(type) {
	case map[string]any:
		c.assign(x["id"], path)
		for _, k := range sortedKeys(x) {
			if k == "id" {
				continue
			}
			c.walkIDs(x[k], path+"/"+k)
		}
	case []any:
		for i, e := range x {
			c.walkIDs(e, path+"/"+strconv.Itoa(i))
		}
	}
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

var suffixRE = regexp.MustCompile(`^(.*)_([0-9]+)$`)

// lastSegment is a name without its namespace (`m::L::n` is n).
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

// collectBinders records, for each lambda and let under n, the names it
// binds as the source wrote them: a parameter or a let's name less its
// namespace and less the suffix of its binder's own id.
func (c *canonicalizer) collectBinders(n any) {
	switch x := n.(type) {
	case map[string]any:
		own, _ := idText(x["id"])
		bind := func(v any) {
			s, ok := v.(string)
			if !ok || own == "" {
				return
			}
			s = lastSegment(s)
			if m := suffixRE.FindStringSubmatch(s); m != nil && m[2] == own {
				s = m[1]
			}
			if c.binds[own] == nil {
				c.binds[own] = map[string]bool{}
			}
			c.binds[own][s] = true
			c.bound[s] = true
			if m := suffixRE.FindStringSubmatch(s); m != nil {
				c.bound[m[1]] = true // an instance's copy, suffixed by its original's
			}
		}
		switch x["kind"] {
		case "lambda":
			ps, _ := x["params"].([]any)
			for _, p := range ps {
				if pm, ok := p.(map[string]any); ok {
					bind(pm["name"])
				}
			}
		case "let":
			if od, ok := x["opdef"].(map[string]any); ok {
				bind(od["name"])
			}
		}
		for _, k := range sortedKeys(x) {
			c.collectBinders(x[k])
		}
	case []any:
		for _, e := range x {
			c.collectBinders(e)
		}
	}
}

// id is an id's canonical path; one no module holds is named where it
// was found (an entry's nested node). A form's path stays itself.
func (c *canonicalizer) id(v any, where string) any {
	k, ok := idText(v)
	if !ok {
		return v
	}
	if p, ok := c.ids[k]; ok {
		return p
	}
	if _, isPath := v.(string); isPath {
		return v
	}
	p := where + "!"
	c.ids[k] = p
	return p
}

// name is a name with the ids it embeds made canonical. The compile IR's
// // unshadower's `x_<binder id>`, the binder a lambda or a let that binds
// x (an instance's copy names the original's, which compile's output
// keeps in the original's module), becomes `x_@<binder's path>`. A
// suffix that is no id of the output becomes `x_#k`, k numbered by first
// appearance in its declaration; one that is an id of a node binding no
// x is the user's and stays.
//
// Every output's names that quint's parser makes from an id
// (`quintTupledLambdaParam<id>` for `((a, b)) => e`, `quintDestructTemp<id>`
// for destructuring, `__undefinedParam<id>` and `_undefinedDeclaration<id>`
// where a parse failed) take the id's path the same way (`@path`), or
// `#k` where the output holds no such id.
func (c *canonicalizer) name(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	base, suffix := s, ""
	if c.o.Kind == Compile {
		if m := suffixRE.FindStringSubmatch(s); m != nil {
			base, suffix = c.unshadowed(s, m)
		}
	}
	return c.generated(base) + suffix
}

// unshadowed splits a compile IR's name x_<digits> into x and its
// canonical suffix, or keeps it whole (the user's).
func (c *canonicalizer) unshadowed(s string, m []string) (string, string) {
	if _, seen := c.declOf[m[2]]; seen {
		if c.binds[m[2]][lastSegment(m[1])] {
			return m[1], "_@" + c.ids[m[2]]
		}
		return s, ""
	}
	if !c.bound[lastSegment(m[1])] {
		return s, "" // no local name: the user's (`__gtg_inv_1`)
	}
	return m[1], fmt.Sprintf("_#%d", c.unseen("u"+m[2]))
}

// unseen numbers, within the declaration written, an id no module holds.
func (c *canonicalizer) unseen(key string) int {
	sx := c.sfx[c.scope]
	if sx == nil {
		sx = map[string]int{}
		c.sfx[c.scope] = sx
	}
	k, ok := sx[key]
	if !ok {
		k = len(sx) + 1
		sx[key] = k
	}
	return k
}

// generatedPrefixes start the names quint's parser makes from an id
// (read-only).
var generatedPrefixes = []string{"quintTupledLambdaParam", "quintDestructTemp", "__undefinedParam", "_undefinedDeclaration"}

var generatedRE = regexp.MustCompile(`^(quintTupledLambdaParam|quintDestructTemp|__undefinedParam|_undefinedDeclaration)([0-9]+)$`)

// generated is a name with the id quint's parser put in it made canonical.
func (c *canonicalizer) generated(s string) string {
	ns, last := "", s
	if i := strings.LastIndex(s, "::"); i >= 0 {
		ns, last = s[:i+2], s[i+2:]
	}
	m := generatedRE.FindStringSubmatch(last)
	if m == nil {
		return s
	}
	if _, seen := c.declOf[m[2]]; seen {
		return ns + m[1] + "@" + c.ids[m[2]]
	}
	return fmt.Sprintf("%s%s#%d", ns, m[1], c.unseen("g"+m[2]))
}

// generic is n with its ids and names rewritten and every field kept.
func (c *canonicalizer) generic(n any, where string) any {
	switch x := n.(type) {
	case map[string]any:
		out := map[string]any{}
		for _, k := range sortedKeys(x) {
			switch k {
			case "id", "reference":
				out[k] = c.id(x[k], where+"/"+k)
			case "name", "opcode":
				out[k] = c.name(x[k])
			default:
				out[k] = c.generic(x[k], where+"/"+k)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = c.generic(e, where+"/"+strconv.Itoa(i))
		}
		return out
	}
	return n
}

// pick is n with only fields kept, each by f.
func pick(n map[string]any, fields []string, f func(k string, v any) any) map[string]any {
	out := map[string]any{}
	for _, k := range fields {
		if v, ok := n[k]; ok {
			out[k] = f(k, v)
		}
	}
	return out
}

// The fields each tier keeps (read-only).
var (
	compileDeclFields = []string{"id", "kind", "name", "qualifier", "expr"}
	compileExprFields = []string{"id", "kind", "name", "opcode", "args", "value", "params", "qualifier", "expr", "opdef"}
	typeNodeFields    = []string{"id", "kind", "name", "elem", "arg", "res", "args", "fields"}
	rowFields         = []string{"kind", "fields", "other", "name"}
	typecheckDecl     = []string{"id", "kind", "name", "qualifier", "doc", "expr", "typeAnnotation", "type", "params", "protoName", "defName", "qualifiedName"}
)

func (c *canonicalizer) module(m map[string]any, key string) any {
	decls, _ := m["declarations"].([]any)
	dkeys := declKeys(decls)
	type ordered struct {
		layer int
		key   string
		v     map[string]any
	}
	var ds []ordered
	for j, d := range decls {
		c.scope = key + "/" + dkeys[j]
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if c.o.Tier == Read && c.o.Kind == Compile {
			switch dm["kind"] {
			case "var", "const", "def":
			default:
				continue // qeval skips types, imports and assumptions
			}
		}
		id, _ := idText(dm["id"])
		l := c.layer[id]
		v := c.decl(dm).(map[string]any)
		v["$layer"] = json.Number(strconv.Itoa(l))
		ds = append(ds, ordered{l, dkeys[j], v})
	}
	c.scope = ""
	// by layer, and within one by key: quint's order there is its ids' (order.go)
	slices.SortStableFunc(ds, func(a, b ordered) int {
		if a.layer != b.layer {
			return a.layer - b.layer
		}
		return strings.Compare(a.key, b.key)
	})
	var out []any
	for _, o := range ds {
		out = append(out, o.v)
	}
	if c.o.Tier == All || c.o.Kind == Parse {
		mo := c.generic(withoutKey(m, "declarations"), "").(map[string]any)
		mo["declarations"] = orEmpty(out)
		return mo
	}
	return map[string]any{"name": m["name"], "declarations": orEmpty(out)}
}

func withoutKey(m map[string]any, k string) map[string]any {
	o := make(map[string]any, len(m))
	for kk, v := range m {
		if kk != k {
			o[kk] = v
		}
	}
	return o
}

func (c *canonicalizer) decl(d map[string]any) any {
	if c.o.Tier == All || c.o.Kind == Parse {
		return c.generic(d, "")
	}
	if c.o.Kind == Typecheck {
		return pick(d, typecheckDecl, func(k string, v any) any {
			switch k {
			case "id":
				return c.id(v, "")
			case "name":
				return c.name(v)
			}
			return c.generic(v, "")
		})
	}
	return c.compileDecl(d)
}

func (c *canonicalizer) compileDecl(d map[string]any) map[string]any {
	return pick(d, compileDeclFields, func(k string, v any) any {
		switch k {
		case "id":
			return c.id(v, "")
		case "name":
			return c.name(v)
		case "expr":
			return c.compileExpr(v)
		}
		return v
	})
}

func (c *canonicalizer) compileExpr(v any) any {
	x, ok := v.(map[string]any)
	if !ok {
		return v
	}
	return pick(x, compileExprFields, func(k string, v any) any {
		switch k {
		case "id":
			return c.id(v, "")
		case "name", "opcode":
			return c.name(v)
		case "args":
			as, _ := v.([]any)
			out := make([]any, len(as))
			for i, a := range as {
				out[i] = c.compileExpr(a)
			}
			return out
		case "params":
			ps, _ := v.([]any)
			out := make([]any, len(ps))
			for i, p := range ps {
				pm, _ := p.(map[string]any)
				out[i] = pick(pm, []string{"id", "name"}, func(k string, v any) any {
					if k == "id" {
						return c.id(v, "")
					}
					return c.name(v)
				})
			}
			return out
		case "expr":
			return c.compileExpr(v)
		case "opdef":
			if od, ok := v.(map[string]any); ok {
				return c.compileDecl(od)
			}
		}
		return v
	})
}

// table is the lookup table, keyed by canonical ids.
func (c *canonicalizer) table(t map[string]any) any {
	return c.entries("table", t, func(key string, e any) any {
		em, ok := e.(map[string]any)
		if !ok {
			return e
		}
		if c.o.Tier == Read && c.o.Kind == Compile {
			return pick(em, []string{"id", "kind", "name"}, func(k string, v any) any {
				if k == "id" {
					return c.id(v, key)
				}
				return c.name(v)
			})
		}
		return c.generic(em, key)
	})
}

// entries is a table keyed by ids, its keys canonical; an entry keyed by
// an id no module holds is dropped and counted.
func (c *canonicalizer) entries(what string, t map[string]any, f func(key string, e any) any) any {
	type kv struct {
		key, scope string
		v          any
	}
	var kept []kv
	for _, k := range sortedKeys(t) {
		p, ok := c.ids[k]
		if _, seen := c.declOf[k]; !seen || !ok {
			c.dropped[what]++
			continue
		}
		if c.o.Tier == Read && !c.kept[p] {
			continue
		}
		kept = append(kept, kv{p, c.declOf[k], t[k]})
	}
	// canonical keys decide the order nested unseen ids are named in
	slices.SortFunc(kept, func(a, b kv) int { return strings.Compare(a.key, b.key) })
	out := map[string]any{}
	for _, e := range kept {
		if _, dup := out[e.key]; dup {
			// two ids with one path: cannot happen, one path holds one id
			continue
		}
		c.scope = e.scope
		out[e.key] = f(e.key, e.v)
	}
	c.scope = ""
	return out
}

// vars renames variables by first appearance within one entry.
type vars struct {
	n  int
	of map[string]string
}

func (v *vars) get(name string) string {
	if v.of == nil {
		v.of = map[string]string{}
	}
	r, ok := v.of[name]
	if !ok {
		v.n++
		r = "$" + strconv.Itoa(v.n)
		v.of[name] = r
	}
	return r
}

func (c *canonicalizer) typeEntry(key string, e any) any {
	em, ok := e.(map[string]any)
	if !ok {
		return e
	}
	vs := &vars{}
	out := map[string]any{"type": c.typeNode(em["type"], key+"/type", vs)}
	if c.o.Tier == All {
		for _, f := range []string{"typeVariables", "rowVariables"} {
			if l, ok := em[f].([]any); ok {
				r := make([]any, len(l))
				for i, n := range l {
					if s, ok := n.(string); ok {
						r[i] = vs.get(s)
					}
				}
				slices.SortFunc(r, func(a, b any) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
				out[f] = r
			}
		}
	}
	return out
}

// typeNode is a type (or a row) with its ids canonical and its variables
// renamed.
func (c *canonicalizer) typeNode(v any, where string, vs *vars) any {
	switch x := v.(type) {
	case map[string]any:
		// a row of no fields over another is that other: quint writes a
		// unit's fields as {row, [], empty} or as {empty}, by the order its
		// solver substituted in (ids again: binding/testdata/typed.qnt shifted)
		if fs, ok := x["fields"].([]any); ok && x["kind"] == "row" && len(fs) == 0 && x["other"] != nil {
			return c.typeNode(x["other"], where, vs)
		}
		out := map[string]any{}
		for _, k := range sortedKeys(x) {
			if c.o.Tier == Read && !slices.Contains(typeNodeFields, k) && !slices.Contains(rowFields, k) && k != "fieldName" && k != "fieldType" {
				continue
			}
			switch k {
			case "id":
				// dropped: a type node's id in `types` is from one of two id
				// spaces, the modules' (an annotation's) or a builtin
				// signature's, parsed with ids of its own (types/parser.js):
				// no form can tell them apart, and no reader uses it
				// (qeval.Type.ID is decoded, never read)
				continue
			case "name":
				if x["kind"] == "var" {
					if s, ok := x[k].(string); ok {
						out[k] = vs.get(s)
						continue
					}
				}
				out[k] = x[k]
			default:
				out[k] = c.typeNode(x[k], where+"/"+k, vs)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = c.typeNode(e, where+"/"+strconv.Itoa(i), vs)
		}
		return out
	}
	return v
}

func (c *canonicalizer) effectEntry(key string, e any) any {
	vs := &vars{}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for _, k := range sortedKeys(x) {
				if k == "reference" || k == "id" {
					out[k] = c.id(x[k], key+"/effect")
					continue
				}
				if k == "name" && (x["kind"] == "variable" || x["kind"] == "quantified") {
					if s, ok := x[k].(string); ok {
						out[k] = vs.get(s)
						continue
					}
				}
				out[k] = walk(x[k])
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				if s, ok := e.(string); ok {
					out[i] = vs.get(s) // effectVariables, entityVariables
				} else {
					out[i] = walk(e)
				}
			}
			return out
		}
		return v
	}
	return walk(e)
}

var codeRE = regexp.MustCompile(`\[(QNT[0-9]{3})\]`)

// Code is the QNT code of an error's explanation ("" if it names none).
func Code(explanation string) string {
	if m := codeRE.FindStringSubmatch(explanation); m != nil {
		return m[1]
	}
	return ""
}

func (c *canonicalizer) rel(p string) string {
	p = filepath.ToSlash(p)
	for _, r := range c.o.Roots {
		r = strings.TrimSuffix(filepath.ToSlash(r), "/")
		if r == "" {
			continue
		}
		if p == r {
			return "."
		}
		if strings.HasPrefix(p, r+"/") {
			return p[len(r)+1:]
		}
	}
	return p
}

func (c *canonicalizer) pos(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	fields := []string{"line", "col"}
	if c.o.Tier == All {
		fields = append(fields, "index")
	}
	return pick(m, fields, func(_ string, v any) any { return v })
}

func (c *canonicalizer) errors(v any) any {
	es, _ := v.([]any)
	out := []any{}
	for _, e := range es {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		o := map[string]any{}
		code, _ := em["code"].(string)
		if expl, ok := em["explanation"].(string); ok {
			if code == "" {
				code = Code(expl)
			}
			if c.o.Tier == All {
				o["explanation"] = expl
			}
		}
		o["code"] = code
		locs, _ := em["locs"].([]any)
		ol := []any{}
		for _, l := range locs {
			lm, ok := l.(map[string]any)
			if !ok {
				continue
			}
			x := map[string]any{"start": c.pos(lm["start"]), "end": c.pos(lm["end"])}
			if s, ok := lm["source"].(string); ok {
				x["source"] = c.rel(s)
			}
			ol = append(ol, x)
		}
		o["locs"] = ol
		if r, ok := em["reference"]; ok {
			o["reference"] = c.id(r, "errors/"+strconv.Itoa(len(out)))
		}
		out = append(out, o)
	}
	if c.o.Tier == Read && len(out) > 1 {
		// the first error, then the set of the others: quint reports a
		// type error once per module analysed before it, so a module
		// written first doubles it (TestHarnessPushTier's first-line)
		first := out[0]
		rest := map[string]any{}
		fk := fullJSON(first)
		for _, e := range out[1:] {
			if k := fullJSON(e); k != fk {
				rest[k] = e
			}
		}
		out = []any{first}
		for _, k := range sortedKeys(rest) {
			out = append(out, rest[k])
		}
	}
	return out
}

// sourceMap is the source map keyed by canonical ids, each span with its
// file named, not numbered.
func (c *canonicalizer) sourceMap(sm map[string]any) any {
	files := map[string]string{}
	if idx, ok := sm["sourceIndex"].(map[string]any); ok {
		for k, v := range idx {
			if s, ok := v.(string); ok {
				files[k] = c.rel(s)
			}
		}
	}
	m, _ := sm["map"].(map[string]any)
	if m == nil {
		// a form's own: already keyed by path, its spans named
		m, _ = sm["spans"].(map[string]any)
	}
	spans := c.entries("sourceMap", m, func(_ string, e any) any {
		l, ok := e.([]any)
		if !ok || len(l) != 3 {
			return e
		}
		f := fmt.Sprint(l[0])
		if n, ok := files[f]; ok {
			f = n
		}
		return []any{f, c.pos(l[1]), c.pos(l[2])}
	})
	return map[string]any{"spans": spans}
}

// fullJSON is v's encoding, whole.
func fullJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
