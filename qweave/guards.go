package qweave

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Lock invariants declared on a lock's field (ROADMAP LI2):
//
//	type Account struct {
//		mu   sync.Mutex //quint:guards conserved
//		a, b int
//	}
//
// names a method of the struct (func() bool; several may follow, checked
// in order) that must hold whenever mu is released. qweave weaves it where
// the release is, not where the struct is made: an Unlock of the field
// (x.mu.Unlock(), deferred or not, or the method value x.mu.Unlock) is
// woven, in place of qenv/weave's woven.MutexUnlock, into
// woven.MutexUnlockGuarded carrying the invariants bound to x, which qenv
// checks as qenv.Guard's are. Zero values, composite literals, new and
// every constructor are then covered, and no guard needs to find its
// simulation: the Unlock's own does.
//
// What a call site cannot see is an Unlock not through the field: its
// address taken (&x.mu handed to a function, a sync.Locker, sync.NewCond)
// or the field copied. Weave refuses those uses, naming qenv.Guard, which
// sees every simulated Unlock of the mutex, as the fix. An embedded lock
// (struct{ sync.Mutex }) is refused too: the struct itself is a Locker,
// and a conversion to one would escape unseen.
//
// The directive is qweave's alone: Orchestrion's aspect language has no
// join point for "the receiver is a field carrying a directive", so an
// Orchestrion build weaves the plain Unlock and checks nothing (gotogether
// lint -weave flags such fields). Under gosim, whose tool file names no
// qenv/weave (gosim simulates sync itself), nothing is woven and each such
// field is reported skipped.

// guardsDirective is the directive on a lock field.
const guardsDirective = "quint:guards"

// guardedUnlocks are qenv/weave's aspects of an exclusive Unlock, by the
// woven function a guarded field's Unlock calls instead (and its
// GuardedMutex type and field).
// Read-only.
var guardedUnlocks = map[string][3]string{
	"qenv-mutex-unlock-value":     {"MutexUnlockGuarded", "GuardedMutex", "Mutex"},
	"qenv-mutex-unlock-pointer":   {"MutexUnlockGuarded", "GuardedMutex", "Mutex"},
	"qenv-rwmutex-unlock-value":   {"RWUnlockGuarded", "GuardedRWMutex", "RWMutex"},
	"qenv-rwmutex-unlock-pointer": {"RWUnlockGuarded", "GuardedRWMutex", "RWMutex"},
}

// A guard is a lock field carrying //quint:guards.
type guard struct {
	pkg     string   // the declaring package's path
	typ     string   // the struct type's name
	field   string   // the lock field's name
	methods []string // the invariants' methods, in order
	site    string   // the field's declaration (file:line)
	pos     string   // the directive's position, for messages
}

// guardKey identifies a field across a package's variants (its test
// build re-checks it): the position of its name.
func guardKey(fset *token.FileSet, v *types.Var) string {
	return fset.Position(v.Pos()).String()
}

// weavesUnlocks reports whether the aspects include qenv/weave's Unlocks,
// which guarded fields' Unlocks replace.
func weavesUnlocks(aspects []*Aspect) bool {
	for _, a := range aspects {
		if _, ok := guardedUnlocks[a.ID]; ok {
			return true
		}
	}
	return false
}

// collectGuards finds the main module's //quint:guards fields, refusing a
// directive on anything but a named struct's sync.Mutex or sync.RWMutex
// field held by value, or naming no method of it of type func() bool.
func collectGuards(fset *token.FileSet, pkgs []*packages.Package) (map[string]*guard, error) {
	guards := map[string]*guard{}
	var err error
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if err != nil || p.Module == nil || !p.Module.Main || len(p.Errors) > 0 || p.TypesInfo == nil {
			return
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || err != nil {
					return err == nil
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, fd := range st.Fields.List {
					args, at, has := fieldDirective(fd)
					if !has {
						continue
					}
					if e := addGuard(fset, p, ts, fd, args, at, guards); e != nil {
						err = e
						return false
					}
				}
				return true
			})
		}
	})
	return guards, err
}

// fieldDirective returns the arguments of a field's //quint:guards (in its
// doc or its line comment) and where it is.
func fieldDirective(fd *ast.Field) (string, token.Pos, bool) {
	for _, cg := range []*ast.CommentGroup{fd.Doc, fd.Comment} {
		if cg == nil {
			continue
		}
		for _, c := range cg.List {
			rest, ok := strings.CutPrefix(c.Text, "//"+guardsDirective)
			if ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				return strings.TrimSpace(rest), c.Pos(), true
			}
		}
	}
	return "", token.NoPos, false
}

func addGuard(fset *token.FileSet, p *packages.Package, ts *ast.TypeSpec, fd *ast.Field, args string, at token.Pos, guards map[string]*guard) error {
	pos := fset.Position(at)
	where := fmt.Sprintf("qweave: %s: //%s", pos, guardsDirective)
	if len(fd.Names) == 0 {
		return fmt.Errorf("%s on an embedded lock: the struct itself is then a sync.Locker, whose Unlocks a call site cannot see; name the field (mu sync.Mutex //%s ...) and lock it through the name", where, guardsDirective)
	}
	tn, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
	if !ok {
		return fmt.Errorf("%s: no type for %s", where, ts.Name.Name)
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return fmt.Errorf("%s on a field of %s, an alias: put it on the struct type's own declaration", where, ts.Name.Name)
	}
	ft := p.TypesInfo.TypeOf(fd.Type)
	lock := ""
	if n, ok := ft.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "sync" && (n.Obj().Name() == "Mutex" || n.Obj().Name() == "RWMutex") {
		lock = n.Obj().Name()
	}
	if lock == "" {
		return fmt.Errorf("%s on a field of type %s: it guards a sync.Mutex or sync.RWMutex field held by value (a pointer may be shared, and its Unlocks reached elsewhere); guard a pointer's mutex with qenv.Guard", where, types.TypeString(ft, types.RelativeTo(p.Types)))
	}
	var methods []string
	for _, a := range splitArgs(args) {
		if a.Value != "" || !token.IsIdentifier(a.Key) {
			return fmt.Errorf("%s %s: each argument is the name of a method of %s (func() bool), not %q", where, args, ts.Name.Name, a.Key+":"+a.Value)
		}
		methods = append(methods, a.Key)
	}
	if len(methods) == 0 {
		return fmt.Errorf("%s names no invariant: name a method of %s returning whether it holds (//%s conserved, func (x *%s) conserved() bool)", where, ts.Name.Name, guardsDirective, ts.Name.Name)
	}
	ms := types.NewMethodSet(types.NewPointer(named))
	for _, m := range methods {
		sel := ms.Lookup(p.Types, m)
		if sel == nil {
			return fmt.Errorf("%s %s: %s has no method %s; declare func (x *%s) %s() bool, true when the state the lock guards is consistent", where, args, ts.Name.Name, m, ts.Name.Name, m)
		}
		sig := sel.Type().(*types.Signature)
		if sig.Params().Len() != 0 || sig.Results().Len() != 1 || !types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) {
			return fmt.Errorf("%s %s: %s.%s is %s, want func() bool", where, args, ts.Name.Name, m, types.TypeString(sig, types.RelativeTo(p.Types)))
		}
	}
	for _, name := range fd.Names {
		v, ok := p.TypesInfo.Defs[name].(*types.Var)
		if !ok {
			continue
		}
		decl := fset.Position(name.Pos())
		guards[guardKey(fset, v)] = &guard{pkg: p.PkgPath, typ: ts.Name.Name, field: name.Name, methods: methods,
			site: fmt.Sprintf("%s:%d", filepath.Base(decl.Filename), decl.Line), pos: pos.String()}
	}
	return nil
}

// guardedField returns the guard of the field a sync method's receiver
// expression selects (x.mu of x.mu.Unlock()), with the expression of the
// struct holding it (x, through any embedded structs) and whether that is
// a pointer.
func (w *fileWeaver) guardedField(recv ast.Expr) (g *guard, fx *ast.SelectorExpr, implicit []string, pointer bool) {
	if len(w.p.guards) == 0 {
		return nil, nil, nil, false
	}
	fx, ok := ast.Unparen(recv).(*ast.SelectorExpr)
	if !ok {
		return nil, nil, nil, false
	}
	s := w.p.info.Selections[fx]
	if s == nil || s.Kind() != types.FieldVal {
		return nil, nil, nil, false
	}
	v, _ := s.Obj().(*types.Var)
	if v == nil || w.p.guards[guardKey(w.p.fset, v)] == nil {
		return nil, nil, nil, false
	}
	g = w.p.guards[guardKey(w.p.fset, v)]
	t := w.p.info.TypeOf(fx.X)
	idx := s.Index()
	for _, i := range idx[:len(idx)-1] {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return nil, nil, nil, false
		}
		f := st.Field(i)
		implicit = append(implicit, f.Name())
		t = f.Type()
	}
	_, pointer = t.Underlying().(*types.Pointer)
	return g, fx, implicit, pointer
}

// woven is a guarded field's Unlock, rendered: the struct bound once to
// __qw_g (where and when the original receiver was evaluated), and the
// woven unlock, its function and its argument's type and value (the
// field's mutex and its invariants, bound to __qw_g).
type wovenUnlock struct{ bind, fn, typ, arg string }

// guardAccess refuses a guarded field's Unlock in another package than
// its struct's if an invariant is unexported there: it could not be
// called.
func (w *fileWeaver) guardAccess(g *guard, fx *ast.SelectorExpr) error {
	if g.pkg != w.p.path {
		for _, m := range g.methods {
			if !token.IsExported(m) {
				return positioned{fmt.Errorf("%s: %s.%s's Unlock outside its package %s: the invariant %s, which //%s names at %s, is unexported, so this package cannot check it; unlock it through a method of %s, or export the invariant",
					w.p.fset.Position(fx.Pos()), g.typ, g.field, g.pkg, m, guardsDirective, g.site, g.typ)}
			}
		}
	}
	return nil
}

// guardedUnlock renders a guarded field's Unlock.
func (w *fileWeaver) guardedUnlock(a *Aspect, g *guard, fx *ast.SelectorExpr, implicit []string, pointer bool, inner func(ast.Node) string) wovenUnlock {
	alias := wovenAlias
	if name := nameOf(w.imports, wovenPath); name != "" && name != "_" && name != "." {
		alias = name
	} else {
		w.need[alias] = wovenPath
	}
	x := inner(fx.X)
	for _, f := range implicit {
		x = "(" + x + ")." + f
	}
	u := wovenUnlock{bind: "__qw_g := &(" + x + ")"}
	if pointer {
		u.bind = "__qw_g := " + x
	}
	var invs []string
	for _, m := range g.methods {
		invs = append(invs, fmt.Sprintf("{Name: %q, Site: %q, Holds: func() bool { return __qw_g.%s() }}", g.typ+"."+m, g.site, m))
	}
	names := guardedUnlocks[a.ID]
	u.fn, u.typ = alias+"."+names[0], alias+"."+names[1]
	u.arg = fmt.Sprintf("%s{%s: &__qw_g.%s, Invariants: []%s.Invariant{%s}}", u.typ, names[2], g.field, alias, strings.Join(invs, ", "))
	return u
}

// positioned is a refusal whose message starts with its position (file,
// line and column), which Weave does not prefix with the file again.
type positioned struct{ error }

func (p positioned) Unwrap() error { return p.error }

// guardEscapes refuses a use of a guarded field other than as the
// receiver of its own methods (x.mu.Lock(), x.mu.Unlock, ...): an Unlock
// through its address, or of a copy, is not woven with its invariants.
func (w *fileWeaver) guardEscapes(n ast.Node, parent ast.Node) error {
	fx, ok := n.(*ast.SelectorExpr)
	if !ok || len(w.p.guards) == 0 {
		return nil
	}
	s := w.p.info.Selections[fx]
	if s == nil || s.Kind() != types.FieldVal {
		return nil
	}
	v, _ := s.Obj().(*types.Var)
	if v == nil {
		return nil
	}
	g := w.p.guards[guardKey(w.p.fset, v)]
	if g == nil {
		return nil
	}
	if ps, ok := parent.(*ast.SelectorExpr); ok && ps.X == fx {
		if m := w.p.info.Selections[ps]; m != nil && m.Kind() == types.MethodVal {
			return nil // x.mu.Lock(), x.mu.Unlock, ...
		}
	}
	return positioned{fmt.Errorf("%s: %s.%s used other than as its methods' receiver (its address taken, or the lock copied): an Unlock through it is not woven with the invariants //%s names at %s, so it would go unchecked; lock and unlock it through the field (x.%s.Unlock()), or drop the directive and guard it with qenv.Guard(ctx, &x.%s, name, holds), which sees every simulated Unlock",
		w.p.fset.Position(fx.Pos()), g.typ, g.field, guardsDirective, g.site, g.field, g.field)}
}

// skippedGuards are the guards a build without qenv/weave's Unlocks does
// not check (gosim's tool file, or none of qenv/weave), one note each.
func skippedGuards(guards map[string]*guard) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(guards)) {
		g := guards[k]
		out = append(out, fmt.Sprintf("%s: //%s on %s.%s not checked: the tool file's aspects weave no sync Unlock (qenv/weave is not named; gosim simulates sync itself)", g.pos, guardsDirective, g.typ, g.field))
	}
	return out
}

// checkGuardABI refuses to weave a guarded Unlock into a module whose
// core's package woven lacks MutexUnlockGuarded (a core older than the
// directive): an added function of the ABI, which qweave checks for before
// writing a call of it.
func (c Config) checkGuardABI() error {
	pkgs, err := packages.Load(c.packagesConfig(packages.NeedName|packages.NeedTypes|packages.NeedModule, false), wovenPath)
	if err != nil {
		return fmt.Errorf("qweave: loading %s: %w", wovenPath, err)
	}
	if len(pkgs) == 1 && len(pkgs[0].Errors) == 0 && pkgs[0].Types != nil {
		sc := pkgs[0].Types.Scope()
		if sc.Lookup("MutexUnlockGuarded") != nil && sc.Lookup("RWUnlockGuarded") != nil {
			return nil
		}
	}
	version := "an unknown version"
	if len(pkgs) == 1 && pkgs[0].Module != nil {
		m := pkgs[0].Module
		version = m.Path + " " + m.Version
		if m.Replace != nil {
			version += " => " + m.Replace.Path
		} else if m.Version == "" {
			version = m.Path + " at " + m.Dir
		}
	}
	return fmt.Errorf("qweave: //%s fields' Unlocks call %s.MutexUnlockGuarded, which the module's core (%s) lacks: move the module to a core with it (go get %s@latest), or remove the directives and guard the locks with qenv.Guard", guardsDirective, wovenPath, version, toolkit)
}
