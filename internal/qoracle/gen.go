package qoracle

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The generators (§4.1). qeval's (internal/qeval/gen_test.go) makes
// expressions and actions over one module for the evaluator's
// differential; it is a test file of another package, so the module
// generator here has an expression generator of its own, smaller, and
// grows what qeval's lacks: whole programs over several files, with
// imports, instances with constants substituted, exports, polymorphic
// operators and aliases, sum types and match, records with spread, lets
// and lambdas, a state machine and its invariant. Typing is all that
// matters here (nothing is evaluated), so its expressions are any
// well-typed ones. Ill mutates a generated program into one quint should
// refuse, tagged with the code it is meant to reach; quint's answer is
// the label, the tag a hint.

// A GenProgram is a generated program: its files by name, the main
// file's, and compile's main module (init and step are its own).
type GenProgram struct {
	Seed  uint64
	Files map[string]*GenFile
	Main  string // the main file's name
	// Tag is, for a mutated program, the code its mutation means to reach.
	Tag string
	// Mutation names the mutation.
	Mutation string
}

// A GenFile is one file's modules.
type GenFile struct {
	Modules []*GenModule
	// Raw, if set, is the file's text, written as is (a mutation's).
	Raw string
}

// A GenModule is a module: its declarations, each a tree.
type GenModule struct {
	Name  string
	Decls []*GenDecl
}

// A GenDecl is a declaration: a header (`pure def f(x: int): int =`) and
// a body expression, or a whole line (an import, a type).
type GenDecl struct {
	Head string
	Body *GenExpr // nil for a line
}

// A GenExpr is an expression of type T ("int" or "bool"): Text with $0,
// $1, ... standing for its kids.
type GenExpr struct {
	T    string
	Text string
	Kids []*GenExpr
}

func (e *GenExpr) String() string {
	s := e.Text
	// highest index first, so $1 is never read in $10
	for i := len(e.Kids) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, fmt.Sprintf("$%d", i), e.Kids[i].String())
	}
	return s
}

func (e *GenExpr) clone() *GenExpr {
	c := *e
	c.Kids = make([]*GenExpr, len(e.Kids))
	for i, k := range e.Kids {
		c.Kids[i] = k.clone()
	}
	return &c
}

// Text is a module's source.
func (m *GenModule) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s {\n", m.Name)
	for _, d := range m.Decls {
		b.WriteString("  " + d.Head)
		if d.Body != nil {
			b.WriteString(" " + d.Body.String())
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// Text is a file's source.
func (f *GenFile) Text() string {
	if f.Raw != "" {
		return f.Raw
	}
	var parts []string
	for _, m := range f.Modules {
		parts = append(parts, m.Text())
	}
	return strings.Join(parts, "\n")
}

// MainModule is the main file's last module, compile's main.
func (p *GenProgram) MainModule() string {
	f := p.Files[p.Main]
	if f == nil || len(f.Modules) == 0 {
		return ""
	}
	return f.Modules[len(f.Modules)-1].Name
}

// Write writes the program's files into dir and returns it as a
// Program (compile's main its main module, if it has init and step).
func (p *GenProgram) Write(dir, id string) (Program, error) {
	for name, f := range p.Files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(f.Text()), 0o644); err != nil {
			return Program{}, err
		}
	}
	prog := Program{ID: id, File: filepath.Join(dir, p.Main)}
	if strings.Contains(p.Files[p.Main].Text(), "action step") {
		prog.Main = p.MainModule()
	}
	return prog, nil
}

func (p *GenProgram) clone() *GenProgram {
	c := *p
	c.Files = map[string]*GenFile{}
	for n, f := range p.Files {
		nf := &GenFile{Raw: f.Raw}
		for _, m := range f.Modules {
			nm := &GenModule{Name: m.Name}
			for _, d := range m.Decls {
				nd := *d
				if d.Body != nil {
					nd.Body = d.Body.clone()
				}
				nm.Decls = append(nm.Decls, &nd)
			}
			nf.Modules = append(nf.Modules, nm)
		}
		c.Files[n] = nf
	}
	return &c
}

// ---- generation ----------------------------------------------------------

type mgen struct {
	r *rand.Rand
	// fns are the callable operators in scope of the main module: name,
	// argument types, result type
	fns   []genFn
	vars  map[string]string // the main module's state variables' types
	fresh int
}

type genFn struct {
	name string
	args []string
	res  string
}

func (g *mgen) chance(p float64) bool { return g.r.Float64() < p }

func (g *mgen) name(prefix string) string {
	g.fresh++
	return fmt.Sprintf("%s%d", prefix, g.fresh)
}

func lit(t, s string) *GenExpr { return &GenExpr{T: t, Text: s} }

func app(t, text string, kids ...*GenExpr) *GenExpr { return &GenExpr{T: t, Text: text, Kids: kids} }

// leaf is a literal or a name of type t in env.
func (g *mgen) leaf(t string, env map[string]string) *GenExpr {
	var names []string
	for n, nt := range env {
		if nt == t {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	if len(names) > 0 && g.chance(0.6) {
		return lit(t, names[g.r.IntN(len(names))])
	}
	if t == "bool" {
		return lit(t, []string{"true", "false"}[g.r.IntN(2)])
	}
	return lit(t, []string{"0", "1", "2", "7", "(-3)", "100"}[g.r.IntN(6)])
}

// expr is an expression of type t ("int" or "bool") of depth at most d
// over env's names and g's operators.
func (g *mgen) expr(t string, d int, env map[string]string) *GenExpr {
	if d <= 0 || g.chance(0.2) {
		return g.leaf(t, env)
	}
	e := func(t string) *GenExpr { return g.expr(t, d-1, env) }
	with := func(n, nt string) map[string]string {
		m := map[string]string{}
		for k, v := range env {
			m[k] = v
		}
		m[n] = nt
		return m
	}
	var opts []func() *GenExpr
	add := func(w int, f func() *GenExpr) {
		for range w {
			opts = append(opts, f)
		}
	}
	add(2, func() *GenExpr { return app(t, "(if ($0) $1 else $2)", e("bool"), e(t), e(t)) })
	add(1, func() *GenExpr {
		n := g.name("t")
		u := []string{"int", "bool"}[g.r.IntN(2)]
		return app(t, "{ val "+n+" = $0\n    $1 }", e(u), g.expr(t, d-1, with(n, u)))
	})
	add(1, func() *GenExpr { return app(t, "pick($0, $1, $2)", e("bool"), e(t), e(t)) }) // polymorphic
	for _, f := range g.fns {
		if f.res != t {
			continue
		}
		add(2, func() *GenExpr {
			var kids []*GenExpr
			var holes []string
			for i, a := range f.args {
				kids = append(kids, e(a))
				holes = append(holes, fmt.Sprintf("$%d", i))
			}
			return app(t, f.name+"("+strings.Join(holes, ", ")+")", kids...)
		})
	}
	switch t {
	case "int":
		for _, op := range []string{"+", "-", "*"} {
			add(2, func() *GenExpr { return app(t, "($0 "+op+" $1)", e("int"), e("int")) })
		}
		add(1, func() *GenExpr { return app(t, "swap(($0, $1))._1", e("int"), e("int")) })
		add(1, func() *GenExpr { return app(t, "upd({ f: $0, g: $1 }).f", e("int"), e("bool")) })
		add(1, func() *GenExpr { return app(t, "{ ...{ f: $0, g: $1 }, f: $2 }.f", e("int"), e("bool"), e("int")) })
		add(1, func() *GenExpr {
			return app(t, "ms("+[]string{"SA($0)", "SB($1)", "SC"}[g.r.IntN(3)]+")", e("int"), e("bool"))
		})
		add(1, func() *GenExpr {
			acc, v := g.name("acc"), g.name("v")
			return app(t, "Set($0, $1).fold($2, ("+acc+", "+v+") => $3)", e("int"), e("int"), e("int"),
				g.expr("int", d-1, with(acc, "int")))
		})
		add(1, func() *GenExpr {
			return app(t, "(match $0 { | SA(i) => i | SB(b) => $1 | SC => $2 })",
				app("S", []string{"SA($0)", "SB($1)", "SC"}[g.r.IntN(3)], e("int"), e("bool")), e("int"), e("int"))
		})
	case "bool":
		for _, op := range []string{"and", "or", "implies"} {
			add(1, func() *GenExpr { return app(t, "($0 "+op+" $1)", e("bool"), e("bool")) })
		}
		for _, op := range []string{"<", "<=", "==", "!="} {
			add(2, func() *GenExpr { return app(t, "($0 "+op+" $1)", e("int"), e("int")) })
		}
		add(1, func() *GenExpr { return app(t, "not($0)", e("bool")) })
		add(1, func() *GenExpr {
			v := g.name("v")
			return app(t, "Set($0, $1).exists("+v+" => $2)", e("int"), e("int"), g.expr("bool", d-1, with(v, "int")))
		})
	}
	return opts[g.r.IntN(len(opts))]()
}

// Module generates a well-typed program from seed: base.qnt (aliases, a
// sum type, polymorphic and record operators), lib.qnt (a constant, used
// through instances), mid.qnt (base re-exported) and main.qnt (the
// state machine, through mid's export and lib's instances).
func Module(seed uint64) *GenProgram {
	g := &mgen{r: rand.New(rand.NewPCG(seed, 0x51ab)), vars: map[string]string{}}
	p := &GenProgram{Seed: seed, Files: map[string]*GenFile{}, Main: "main.qnt"}
	line := func(s string) *GenDecl { return &GenDecl{Head: s} }

	base := &GenModule{Name: "base"}
	base.Decls = append(base.Decls,
		line("type S = SA(int) | SB(bool) | SC"),
		line("type P[a] = (a, a)"),
		line("type R = { f: int, g: bool }"),
		line("pure def pick(c, a, b) = if (c) a else b"),
		line("pure def swap(p: P[int]): P[int] = (p._2, p._1)"),
		line("pure def upd(r: R): R = { ...r, f: r.f + 1 }"),
		line("pure def ms(s: S): int = match s { | SA(i) => i | SB(b) => if (b) 1 else 0 | SC => 0 }"),
	)
	// a few operators of base's own, over what is declared before them
	for range 1 + g.r.IntN(3) {
		n, res := g.name("b"), []string{"int", "bool"}[g.r.IntN(2)]
		body := g.expr(res, 2, map[string]string{"x": "int"})
		base.Decls = append(base.Decls, &GenDecl{Head: fmt.Sprintf("pure def %s(x: int): %s =", n, res), Body: body})
		g.fns = append(g.fns, genFn{n, []string{"int"}, res})
	}
	p.Files["base.qnt"] = &GenFile{Modules: []*GenModule{base}}

	lib := &GenModule{Name: "lib"}
	lib.Decls = append(lib.Decls, line("const C: int"), line("pure def lc(x: int): int = x + C"), line("pure val lv = C * 2 > 0"))
	p.Files["lib.qnt"] = &GenFile{Modules: []*GenModule{lib}}

	mid := &GenModule{Name: "mid"}
	mid.Decls = append(mid.Decls, line(`import base.* from "./base"`), line("export base.*"))
	n := g.name("m")
	mid.Decls = append(mid.Decls, &GenDecl{Head: fmt.Sprintf("pure def %s(x: int): int =", n), Body: g.expr("int", 2, map[string]string{"x": "int"})})
	g.fns = append(g.fns, genFn{n, []string{"int"}, "int"})
	p.Files["mid.qnt"] = &GenFile{Modules: []*GenModule{mid}}

	main := &GenModule{Name: "main"}
	main.Decls = append(main.Decls, line(`import mid.* from "./mid"`))
	k1, k2 := 1+g.r.IntN(5), 1+g.r.IntN(5)
	main.Decls = append(main.Decls, line(fmt.Sprintf(`import lib(C = %d) as L from "./lib"`, k1)))
	g.fns = append(g.fns, genFn{"L::lc", []string{"int"}, "int"})
	if g.chance(0.5) {
		main.Decls = append(main.Decls, line(fmt.Sprintf(`import lib(C = %d).* from "./lib"`, k2)))
		g.fns = append(g.fns, genFn{"lc", []string{"int"}, "int"})
	}
	main.Decls = append(main.Decls, line("var x: int"), line("var y: bool"))
	g.vars = map[string]string{"x": "int", "y": "bool"}
	main.Decls = append(main.Decls, &GenDecl{Head: "action init = all { x' = 0, y' =", Body: app("bool", "$0 }", g.expr("bool", 1, nil))})
	var acts []string
	for range 1 + g.r.IntN(3) {
		a := g.name("a")
		acts = append(acts, a)
		if g.chance(0.5) {
			// a nondet choice the action's assignments read
			v := g.name("k")
			env := map[string]string{"x": "int", "y": "bool", v: "int"}
			main.Decls = append(main.Decls, &GenDecl{Head: fmt.Sprintf("action %s = {", a),
				Body: app("bool", "nondet "+v+" = Set($0, $1).oneOf()\n    all { x' = $2, y' = $3 } }", g.expr("int", 1, g.vars), g.expr("int", 1, g.vars), g.expr("int", 2, env), g.expr("bool", 2, env))})
		} else {
			main.Decls = append(main.Decls, &GenDecl{Head: fmt.Sprintf("action %s = all {", a),
				Body: app("bool", "$0, x' = $1, y' = $2 }", g.expr("bool", 2, g.vars), g.expr("int", 2, g.vars), g.expr("bool", 2, g.vars))})
		}
	}
	main.Decls = append(main.Decls, line("action step = any { "+strings.Join(acts, ", ")+" }"))
	main.Decls = append(main.Decls, &GenDecl{Head: "val inv =", Body: g.expr("bool", 3, g.vars)})
	p.Files["main.qnt"] = &GenFile{Modules: []*GenModule{main}}
	return p
}

// ---- the tree shrink -----------------------------------------------------

// reductions are the programs one step smaller than p: a declaration's
// body node replaced by one of its kids of its type, or by a literal of
// its type; a body-less declaration of base or main dropped.
func reductions(p *GenProgram) []*GenProgram {
	var out []*GenProgram
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range p.Files {
			if !yield(n) {
				return
			}
		}
	})
	for _, fn := range names {
		for mi, m := range p.Files[fn].Modules {
			for di, d := range m.Decls {
				if d.Body == nil {
					c := p.clone()
					cm := c.Files[fn].Modules[mi]
					cm.Decls = slices.Delete(cm.Decls, di, di+1)
					out = append(out, c)
					continue
				}
				var paths [][]int
				var walk func(e *GenExpr, path []int)
				walk = func(e *GenExpr, path []int) {
					paths = append(paths, slices.Clone(path))
					for i, k := range e.Kids {
						walk(k, append(path, i))
					}
				}
				walk(d.Body, nil)
				for _, path := range paths {
					at := func(c *GenProgram) **GenExpr {
						e := &c.Files[fn].Modules[mi].Decls[di].Body
						for _, i := range path {
							e = &(*e).Kids[i]
						}
						return e
					}
					node := *at(p)
					for ki, k := range node.Kids {
						if k.T == node.T && len(path) > 0 {
							c := p.clone()
							*at(c) = (*at(c)).Kids[ki]
							out = append(out, c)
						}
					}
					if len(node.Kids) > 0 && len(path) > 0 && (node.T == "int" || node.T == "bool") {
						c := p.clone()
						l := "0"
						if node.T == "bool" {
							l = "true"
						}
						*at(c) = lit(node.T, l)
						out = append(out, c)
					}
				}
			}
		}
	}
	return out
}

// ShrinkModule is p reduced, one reduction at a time, while still holds:
// a greedy tree shrink (qeval's differential's), after ShrinkDecls has
// taken out whole declarations. It returns the program and the
// candidates tried.
func ShrinkModule(p *GenProgram, still func(*GenProgram) bool, budget int) (*GenProgram, int) {
	tried := 0
	for {
		progressed := false
		for _, c := range reductions(p) {
			if tried >= budget {
				return p, tried
			}
			tried++
			if still(c) {
				p, progressed = c, true
				break
			}
		}
		if !progressed {
			return p, tried
		}
	}
}

// Size is the number of expression nodes and declarations in p.
func (p *GenProgram) Size() int {
	n := 0
	var count func(e *GenExpr)
	count = func(e *GenExpr) {
		n++
		for _, k := range e.Kids {
			count(k)
		}
	}
	for _, f := range p.Files {
		for _, m := range f.Modules {
			for _, d := range m.Decls {
				n++
				if d.Body != nil {
					count(d.Body)
				}
			}
		}
	}
	return n
}
