package syntax

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qfront/ir"
)

// sexpr renders an expression with its opcodes and names, for asserting
// a parse's shape.
func sexpr(e *ir.Expr) string {
	switch e.Kind {
	case ir.ExName:
		return e.Name
	case ir.ExBool:
		return fmt.Sprint(e.Bool)
	case ir.ExInt:
		return e.Int.String()
	case ir.ExStr:
		return fmt.Sprintf("%q", e.Str)
	case ir.ExApp:
		parts := []string{e.Opcode}
		for _, a := range e.Args {
			parts = append(parts, sexpr(a))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case ir.ExLambda:
		var ps []string
		for _, p := range e.Params {
			ps = append(ps, p.Name)
		}
		return fmt.Sprintf("(%s => %s)", strings.Join(ps, " "), sexpr(e.Body))
	case ir.ExLet:
		return fmt.Sprintf("(let %s %s %s %s)", e.Opdef.Qualifier, e.Opdef.Name, sexpr(e.Opdef.Expr), sexpr(e.Body))
	}
	return "?"
}

func parseModule(t *testing.T, body string) (*ir.Module, *Result) {
	t.Helper()
	r := Parse("module M {\n"+body+"\n}\n", "m.qnt", &ir.IDGen{})
	if r.Fatal {
		t.Fatalf("parse of %q: %v", body, r.Errors)
	}
	if len(r.Modules) != 1 {
		t.Fatalf("parse of %q: %d modules", body, len(r.Modules))
	}
	return r.Modules[0], r
}

func parseExpr(t *testing.T, src string) string {
	t.Helper()
	m, r := parseModule(t, "val e = "+src)
	if len(r.Errors) > 0 {
		t.Fatalf("%q: errors %v", src, r.Errors)
	}
	return sexpr(m.Decls[0].Expr)
}

// The expression grammar's precedences and associativities are ANTLR's
// for Quint.g4's alternatives in their order.
func TestPrecedence(t *testing.T) {
	cases := []struct{ src, want string }{
		{"a + b * c", "(iadd a (imul b c))"},
		{"a - b - c", "(isub (isub a b) c)"},
		{"a ^ b ^ c", "(ipow a (ipow b c))"},
		{"-a ^ b", "(iuminus (ipow a b))"},
		{"-a * b", "(imul (iuminus a) b)"},
		{"-a.f(b)", "(iuminus (f a b))"},
		{"a < b + 1", "(ilt a (iadd b 1))"},
		{"a and b or c", "(or (and a b) c)"},
		{"a or b implies c iff d", "(implies (or a b) (iff c d))"},
		{"p leadsTo q implies r", "(implies (leadsTo p q) r)"},
		{"a -> b + 1", "(Tup a (iadd b 1))"},
		{"a == b -> c", "(Tup (eq a b) c)"},
		{"x' = a + 1 and y' = b", "(and (assign x (iadd a 1)) (assign y b))"},
		{"x' = a -> b", "(Tup (assign x a) b)"},
		{"if (c) a else b + 1", "(ite c a (iadd b 1))"},
		{"if (c) a else b -> d", "(ite c a (Tup b d))"},
		{"s.map(x => x + 1).size()", "(size (map s (x => (iadd x 1))))"},
		{"r.f.g", "(field (field r \"f\") \"g\")"},
		{"t._2", "(item t 2)"},
		{"l[i][j]", "(nth (nth l i) j)"},
		{"f(a, b,)", "(f a b)"},
		{"Set(1, 2)", "(Set 1 2)"},
		{"and(a, b)", "(and a b)"},
		{"and { a, b, }", "(and a b)"},
		{"all { a, b }", "(actionAll a b)"},
		{"any { a }", "(actionAny a)"},
		{"(1, 2, 3)", "(Tup 1 2 3)"},
		{"()", "(Tup)"},
		{"(a)", "a"},
		{"{ a }", "a"},
		{"[1, 2]", "(List 1 2)"},
		{"[]", "(List)"},
		{"{ a: 1, b: 2 }", `(Rec "a" 1 "b" 2)`},
		{"{ ...r, a: 1, b: 2 }", `(with (with r "a" 1) "b" 2)`},
		{"0x1F + 1_000", "(iadd 31 1000)"},
		{`"s"`, `"s"`},
		{"val y = 1 y + 1", "(let val y 1 (iadd y 1))"},
		{"val y = 1; y", "(let val y 1 y)"},
		{"pure def f(x) = x f(1)", "(let puredef f (x => x) (f 1))"},
		{"nondet v = S.oneOf() v", "(let nondet v (oneOf S) v)"},
		{"(x, y) => x", "(x y => x)"},
		{"_ => 1", "(_ => 1)"},
		{"q::debug(x)", `(q::debug "q::debug(x)" x)`},
		{"A::b", "A::b"},
		{"match e { A(x) => x | B => 0 | _ => 1 }", `(matchVariant e "A" (x => x) "B" (_ => 0) "_" (_ => 1))`},
	}
	for _, c := range cases {
		if got := parseExpr(t, c.src); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// Nodes are numbered as quint's listener numbers them: children first,
// left to right, each id as its exit method asks for it.
func TestIDOrder(t *testing.T) {
	m, r := parseModule(t, "val x = 1 + 2")
	d := m.Decls[0]
	ids := []ir.ID{d.Expr.Args[0].ID, d.Expr.Args[1].ID, d.Expr.ID, d.ID, m.ID}
	for i, id := range ids {
		if id != ir.ID(i+1) {
			t.Errorf("ids %v, want 1..5", ids)
			break
		}
	}
	if len(r.SourceMap) != 5 {
		t.Errorf("source map %v", r.SourceMap)
	}
	// a def with parameters: params, body, then the lambda, then the def
	// (its id at its name)
	m, r = parseModule(t, "def f(a, b) = a")
	d = m.Decls[0]
	lam := d.Expr
	got := []ir.ID{lam.Params[0].ID, lam.Params[1].ID, lam.Body.ID, lam.ID, d.ID}
	if fmt.Sprint(got) != "[1 2 3 4 5]" {
		t.Errorf("def ids %v", got)
	}
	nameLoc := r.SourceMap[4].Loc
	if nameLoc.Start.Col != 4 || nameLoc.End.Col != 4 {
		t.Errorf("a def's id is at its name: %+v", nameLoc)
	}
}

// A span ends where quint's arithmetic ends it: the start column plus
// the span's length in characters, whatever line the span ends on.
func TestSpans(t *testing.T) {
	r := Parse("module M {\n  val x =\n    1\n}", "m.qnt", &ir.IDGen{})
	var mod ir.Loc
	for _, e := range r.SourceMap {
		if e.ID == r.Modules[0].ID {
			mod = e.Loc
		}
	}
	// "module" at 0:0, "}" at 3:0, index 27: the end column 0+27
	if mod.Start != (ir.Pos{Line: 0, Col: 0, Index: 0}) || mod.End != (ir.Pos{Line: 3, Col: 27, Index: 27}) {
		t.Errorf("module span %+v", mod)
	}
}

// Declarations: each kind with the fields quint gives it.
func TestDeclarations(t *testing.T) {
	m, r := parseModule(t, `
  /// the constant
  const N: Set[int]
  var x: int -> str
  assume _ = N.size() > 0
  import A.* from "./a"
  import B as BB
  import C(N = 1, K = 2) as CC from "c"
  import D(N = N).*
  export A.f
  type T
  type U[a] = List[a]
  /// the sum
  type S = | P(int) | Q
  type Single = Only
  val (a, _, c) = (1, 2, 3)
  pure val { f, g } = r
  def h(p: int, q: str): bool = true
  action step = x' = x`)
	if len(r.Errors) > 0 {
		t.Fatalf("errors %v", r.Errors)
	}
	var got []string
	for _, d := range m.Decls {
		s := d.Kind + " " + d.Name
		if d.Doc != nil {
			s += " doc=" + *d.Doc
		}
		switch d.Kind {
		case ir.KindImport, ir.KindExport, ir.KindInstance:
			s = d.Kind + " " + *d.ProtoName
			for _, p := range []*string{d.DefName, d.QualifiedName, d.FromSource} {
				if p != nil {
					s += " " + *p
				}
			}
			if d.Kind == ir.KindInstance {
				s += fmt.Sprintf(" %d overrides identity=%v", len(d.Overrides), d.IdentityOverride)
			}
		case ir.KindDef:
			s += " " + d.Qualifier + " " + sexpr(d.Expr)
		case ir.KindTypeDef:
			if d.Type != nil {
				s += " " + d.Type.Kind
			}
			if d.Params != nil {
				s += fmt.Sprint(d.Params)
			}
		}
		got = append(got, s)
	}
	want := []string{
		"const N doc=the constant",
		"var x",
		"assume _",
		"import A * ./a",
		"import B BB",
		"instance C CC c 2 overrides identity=false",
		"instance D 1 overrides identity=true",
		"export A f",
		"typedef T",
		"typedef U list[a]",
		"typedef S sum",
		`def P def (__PParam => (variant "P" __PParam))`,
		`def Q doc=the sum val (variant "Q" (Tup))`,
		"typedef Single const",
		"def quintDestructTemp" + "", // checked below
		`def a val (item quintDestructTemp@ 1)`,
		`def c val (item quintDestructTemp@ 3)`,
		"", // pure val temp
		`def f pureval (field quintDestructTemp@ "f")`,
		`def g pureval (field quintDestructTemp@ "g")`,
		"def h puredef? ",
		"def step action (assign x x)",
	}
	if len(got) != len(want) {
		t.Fatalf("declarations:\n%s", strings.Join(got, "\n"))
	}
	temp := regexpTemp.ReplaceAllString
	for i, w := range want {
		g := temp(got[i], "quintDestructTemp@")
		switch {
		case w == "" || strings.HasPrefix(w, "def quintDestructTemp"):
			if !strings.HasPrefix(g, "def quintDestructTemp@") {
				t.Errorf("declaration %d: %s, want a destructuring's temporary", i, got[i])
			}
		case strings.HasPrefix(w, "def h"):
			if g != `def h def ((p q) => true)` && !strings.HasPrefix(g, "def h def (p q => true)") {
				t.Errorf("declaration %d: %s", i, got[i])
			}
		case g != w:
			t.Errorf("declaration %d:\n got %s\nwant %s", i, g, w)
		}
	}
	// the destructuring's temporary is named by its own id
	tmp := m.Decls[14]
	if tmp.Name != fmt.Sprintf("quintDestructTemp%d", tmp.ID) {
		t.Errorf("temporary %s, id %d", tmp.Name, tmp.ID)
	}
	// an annotated def's annotation is an operator type without an id,
	// its parameters' types shared
	h := m.Decls[20]
	if h.TypeAnnotation == nil || !h.TypeAnnotation.NoID || h.TypeAnnotation.Args[0] != h.Expr.Params[0].TypeAnnotation {
		t.Errorf("h's annotation %+v", h.TypeAnnotation)
	}
}

// A tuple lambda's parameter is named by its id; its fields are lets,
// the last parameter's outermost.
func TestTupleLambda(t *testing.T) {
	m, _ := parseModule(t, "val f = ((a, b)) => a")
	lam := m.Decls[0].Expr
	p := lam.Params[0]
	if p.Name != fmt.Sprintf("quintTupledLambdaParam%d", p.ID) {
		t.Errorf("param %s, id %d", p.Name, p.ID)
	}
	want := fmt.Sprintf("(%[1]s => (let pureval b (item %[1]s 2) (let pureval a (item %[1]s 1) a)))", p.Name)
	if got := sexpr(lam); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

// A destructuring in a let binds its fields inside the let.
func TestLetDestructuring(t *testing.T) {
	got := regexpTemp.ReplaceAllString(parseExpr(t, "val (a, b) = p a + b"), "T")
	if want := "(let val T p (let val b (item T 2) (let val a (item T 1) (iadd a b))))"; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

// The errors Quint.g4's actions and the listener report, at quint's
// positions (0-based line and column).
func TestErrors(t *testing.T) {
	cases := []struct {
		body, want string
	}{
		{"val match = 1", "QNT008 1:10"},
		{"val x = { a::b: 1 }", "QNT008 1:14"},
		{"val x = 1 = 2", "QNT006 2:0"},
		{"var x: T(int)", "QNT009 2:0"},
		{"type t = int", "QNT007 1:0"},
		{"type T = List[a]", "QNT014 1:0"},
		{"const x: Map[int, str]", "QNT015 1:9"},
		{"val r = { a: 1, a: 2 }", "QNT016 1:8"},
		{"val r = { ...a, ...b }", "QNT012 1:8"},
		{"val x = 1 +", "QNT000 2:0"},
		{"var x int", "QNT000 1:6"},
		{"val x = if (a) b", "QNT000 2:0"},
		{"def f(x: int) = x", "QNT000 1:14"},
		{"val x = @", "QNT000 1:8"},
	}
	for _, c := range cases {
		r := Parse("module M {\n"+c.body+"\n}", "m.qnt", &ir.IDGen{})
		if len(r.Errors) == 0 {
			t.Errorf("%s: no error, want %s", c.body, c.want)
			continue
		}
		e := r.Errors[0]
		if got := fmt.Sprintf("%s %d:%d", e.Code, e.Loc.Start.Line, e.Loc.Start.Col); got != c.want {
			t.Errorf("%s: first error %s (%s), want %s", c.body, got, e.Msg, c.want)
		}
	}
}

// Quint.g4's own errors take their ids before any node's, as quint's
// parser numbers them while it parses and the listener after.
func TestErrorIDsFirst(t *testing.T) {
	r := Parse("module M { val x = 1 = 2 }", "m.qnt", &ir.IDGen{})
	if len(r.Errors) != 1 || r.Errors[0].ID != 1 {
		t.Fatalf("errors %v", r.Errors)
	}
	if v := r.Modules[0].Decls[0].Expr.Args[0]; v.ID != 2 {
		t.Errorf("first node's id %d, want 2", v.ID)
	}
	if r.SourceMap[0].ID != 1 {
		t.Errorf("the error's id is first in the source map: %v", r.SourceMap[0])
	}
}

var regexpTemp = regexp.MustCompile(`quintDestructTemp[0-9]+`)

// Where a let's value could go on at a token that also starts its body,
// the parse goes on unless that fails later, as ANTLR's full-context
// prediction decides (decide): `val d = p - q` then `-b <= d` is the
// let's body, not `p - q - b <= d` (which leaves the let none).
func TestLetValueEndsBeforeBody(t *testing.T) {
	cases := []struct{ src, want string }{
		{"{ val d = p - q\n -b <= d }", "(let val d (isub p q) (ilte (iuminus b) d))"},
		{"{ val d = p - q - b\n d }", "(let val d (isub (isub p q) b) d)"},
		{"{ val m = l\n [1, 2] }", "(let val m l (List 1 2))"},
		{"{ val t = f\n (1, 2) }", "(let val t f (Tup 1 2))"},
		{"{ val t = f(1, 2)\n t }", "(let val t (f 1 2) t)"},
		{"a\n - b", "(isub a b)"},
	}
	for _, c := range cases {
		if got := parseExpr(t, c.src); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// A syntax error is where ANTLR reports its first: where every reading
// stops, or at the token where the construct could have ended, when the
// reading that ends it there gets as far (a call's '(', a def's '(').
func TestErrorAtExit(t *testing.T) {
	cases := []struct{ body, want string }{
		{"val x = f(1 +)", "1:9"},
		{"val x = 1 -", "1:10"},
		{"val x = Map[int, str]", "1:11"},
		{"def f(x) = x +", "1:5"},
		{"def f(x, y: int): int = x", "1:5"},
		{"def f(x: int) = x", "1:14"},
		{"def f(x) = x\nval y = 1 +", "3:0"},
		{"val x = a and", "1:10"},
		{"val x = [1, }", "1:12"},
	}
	for _, c := range cases {
		r := Parse("module M {\n"+c.body+"\n}", "m.qnt", &ir.IDGen{})
		if !r.Fatal {
			t.Errorf("%s: parsed", c.body)
			continue
		}
		e := r.Errors[0]
		if got := fmt.Sprintf("%d:%d", e.Loc.Start.Line, e.Loc.Start.Col); got != c.want {
			t.Errorf("%s: first error at %s (%s), want %s", c.body, got, e.Msg, c.want)
		}
	}
}
