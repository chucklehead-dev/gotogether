// Package syntax is the Go front end's parser (Q2's P1,
// docs/q2-front-end.md): Quint text into quint's parse IR (internal/qfront/ir)
// and its source map, as quint 0.33.1's parser makes them (Quint.g4 at
// the tag v0.33.1, and ToIrListener.ts, which turns ANTLR's tree into the
// IR).
//
// The parser is written by hand (§3.1): a lexer (lexer.go), recursive
// descent for modules, declarations and types, and precedence climbing for
// expressions, at the precedences ANTLR gives Quint.g4's left-recursive
// alternatives (an alternative's precedence is the number of
// alternatives after it, plus one: dotCall 31 binds tightest, pair 7
// loosest; a prefix alternative's last operand, as uminus's, asgn's, the
// else of ifElse and a let's body, is parsed at its own precedence).
//
// Nodes are numbered as quint numbers them: ToIrListener takes an id when
// ANTLR's walk leaves a rule, so a node's id comes after its children's,
// in the order the listener's exit methods ask for them; each id that
// listener puts in the source map is put there with the span quint gives
// it (its rule's first token to its last, the end column quint's
// arithmetic, ToIrListener.loc). Syntax errors that quint's parser
// reports while it parses (ANTLR's, and Quint.g4's own QNT006, QNT008 and
// QNT009) take their ids before any node, as quint's do.
//
// Errors: a syntax error stops the parse at the first token no program
// can continue with (where ANTLR, which recovers and goes on, reports its
// first error); the listener's own errors (QNT007, QNT012, QNT014, QNT015,
// QNT016) are reported with the node they name, and the parse goes on.
package syntax

import (
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qfront/ir"
)

// An Error is a parse error: its code, what to do about it, and where.
// ID is the id quint's parser gives the error (its reference).
type Error struct {
	Code string
	Msg  string
	Loc  ir.Loc
	ID   ir.ID
}

func (e Error) String() string {
	return fmt.Sprintf("%s:%d:%d: [%s] %s", e.Loc.Source, e.Loc.Start.Line+1, e.Loc.Start.Col+1, e.Code, e.Msg)
}

// A SourceEntry is a node's place in the source map.
type SourceEntry struct {
	ID  ir.ID
	Loc ir.Loc
}

// A Result is a file's parse.
type Result struct {
	Modules []*ir.Module
	// Errors are the syntax errors in the order quint reports them (by
	// position), then the listener's.
	Errors []Error
	// SourceMap is every id given a span, in the order given.
	SourceMap []SourceEntry
	// Fatal is a syntax error that stopped the parse: Modules are then
	// those finished before it.
	Fatal bool
}

// Parse parses src, the text of the file source names, numbering its
// nodes from gen. It never fails: what it cannot parse is in Errors.
func Parse(src, source string, gen *ir.IDGen) *Result {
	toks, lexErrs := Lex(src)
	base := gen.Last()
	forced := decide(toks, source, base)
	gen.Reset(base)
	r := parseOnce(toks, source, gen, forced)
	errs := syntaxErrors(r, toks, lexErrs, source)
	if len(errs) > 0 {
		// quint's parser numbers its syntax errors as it meets them,
		// before the listener numbers any node: parse again with ids
		// after theirs (the parse is deterministic, its errors the same)
		gen.Reset(base + ir.ID(len(errs)))
		r = parseOnce(toks, source, gen, forced)
		for i := range errs {
			errs[i].ID = base + ir.ID(i) + 1
		}
		var sm []SourceEntry
		for _, e := range errs {
			sm = append(sm, SourceEntry{e.ID, e.Loc})
		}
		r.sourceMap = append(sm, r.sourceMap...)
	}
	return &Result{Modules: r.modules, Errors: append(errs, r.lisErrs...), SourceMap: r.sourceMap, Fatal: r.fatal != nil}
}

// syntaxErrors are the lexer's and the parser's errors, ordered by
// position, as ANTLR's token stream meets them.
func syntaxErrors(r *parser, toks []Tok, lexErrs []LexError, source string) []Error {
	type at struct {
		index      int
		e          Error
		fatal, lex bool
	}
	var all []at
	for _, le := range lexErrs {
		// a lexer's error has no token: quint's listener gives it index 0
		p := ir.Pos{Line: le.Line - 1, Col: le.Col}
		all = append(all, at{le.Index, Error{Code: "QNT000", Msg: fmt.Sprintf("token recognition error at: '%s'", le.Text), Loc: ir.Loc{Source: source, Start: p, End: p}}, false, true})
	}
	for _, se := range r.synErrs {
		t := toks[se.tok]
		n := t.Stop - t.Start // -1 at EOF, as ANTLR's EOF token has it
		start := ir.Pos{Line: t.Line - 1, Col: t.Col, Index: t.Start}
		end := ir.Pos{Line: t.Line - 1, Col: t.Col + n, Index: t.Start + n}
		all = append(all, at{t.Start, Error{Code: se.code, Msg: se.msg, Loc: ir.Loc{Source: source, Start: start, End: end}}, se.fatal, false})
	}
	slices.SortStableFunc(all, func(a, b at) int { return a.index - b.index })
	if r.fatal == nil {
		var out []Error
		for _, a := range all {
			out = append(out, a.e)
		}
		return out
	}
	// ANTLR's lexer reports an error when the parser first reads past it:
	// a syntax error comes after every lexer error before the furthest
	// token the parser read ahead to find it, and the parse reads nothing
	// after it
	var out []Error
	var fatal Error
	boundary := toks[r.fatalFar].Start
	for _, a := range all {
		switch {
		case a.fatal:
			fatal = a.e
		case !a.lex || a.index < boundary:
			out = append(out, a.e)
		}
	}
	return append(out, fatal)
}

type synErr struct {
	tok       int
	code, msg string
	fatal     bool
}

// stop is a syntax error the parse cannot go on from.
type stop struct {
	tok int
	msg string
}

type parser struct {
	toks      []Tok
	p         int
	gen       *ir.IDGen
	source    string
	sourceMap []SourceEntry
	synErrs   []synErr
	lisErrs   []Error
	modules   []*ir.Module
	fatal     *stop
	// forced are the decisions (by their token) taken the other way, amb
	// the decisions where both ways parsed as far as continues read
	forced map[int]bool
	amb    []int
	far    int
	// fatalFar is far when the parse stopped
	fatalFar int
}

// span is a rule's tokens, first and last (inclusive).
type span struct{ a, b int }

// decide is the decisions ANTLR's full-context prediction takes against
// the parser's greedy choice. Where a construct can go on at a token that
// could also start the next expression (a let's body: `val d = p - q`
// then `-b <= d`), and both readings parse as far as the parser can see
// alone (continues), ANTLR reads on through the rest of the program and
// takes the exit if the construct's reading dies first: a parse that
// fails is retried with each such decision reversed, the latest first,
// and the reversal kept if that parse gets as far (further, for a
// decision after the failure) or parses.
func decide(toks []Tok, source string, base ir.ID) map[int]bool {
	forced := map[int]bool{}
	gen := &ir.IDGen{}
	gen.Reset(base)
	r := parseOnce(toks, source, gen, forced)
	for tries := 0; r.fatal != nil && tries < 64; tries++ {
		improved := false
		for i := len(r.amb) - 1; i >= 0; i-- {
			at := r.amb[i]
			if forced[at] {
				continue
			}
			f := maps.Clone(forced)
			f[at] = true
			gen.Reset(base)
			q := parseOnce(toks, source, gen, f)
			// a decision after the failure (one a speculation took, a
			// call's arguments' that the call's exit then hid) is kept
			// only where it takes the parse further
			if q.fatal == nil || q.fatal.tok > r.fatal.tok || (at < r.fatal.tok && q.fatal.tok == r.fatal.tok) {
				forced, r, improved = f, q, true
				break
			}
		}
		if !improved {
			break
		}
	}
	return forced
}

func parseOnce(toks []Tok, source string, gen *ir.IDGen, forced map[int]bool) (p *parser) {
	p = &parser{toks: toks, gen: gen, source: source, forced: forced}
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(stop)
			if !ok {
				panic(r)
			}
			p.fatal = &s
			p.fatalFar = p.far
			p.synErrs = append(p.synErrs, synErr{s.tok, "QNT000", s.msg, true})
		}
	}()
	p.modulesRule()
	return p
}

func (p *parser) cur() Tok { return p.look(p.p) }

func (p *parser) peek(k int) Tok { return p.look(p.p + k) }

// look is token i, read ahead: far is the furthest token read, which
// orders the lexer's errors before a syntax error (syntaxErrors).
func (p *parser) look(i int) Tok {
	i = min(i, len(p.toks)-1)
	p.far = max(p.far, i)
	return p.toks[i]
}

// is says whether the current token is the keyword or punctuation text.
func (p *parser) is(text string) bool { return isTok(p.cur(), text) }

func isTok(t Tok, text string) bool {
	return (t.Kind == TKeyword || t.Kind == TPunct) && t.Text == text
}

func (p *parser) advance() int {
	i := p.p
	if p.toks[i].Kind != TEOF {
		p.p++
	}
	return i
}

func describe(t Tok) string {
	if t.Kind == TEOF {
		return "the end of the file"
	}
	return fmt.Sprintf("'%s'", t.Text)
}

func (p *parser) failf(format string, args ...any) {
	panic(stop{p.p, fmt.Sprintf("unexpected %s: %s", describe(p.cur()), fmt.Sprintf(format, args...))})
}

// expect consumes the token text, or stops the parse there.
func (p *parser) expect(text, what string) int {
	if !p.is(text) {
		p.failf("expected '%s' %s", text, what)
	}
	return p.advance()
}

func (p *parser) last() int { return p.p - 1 }

func (p *parser) spanFrom(a int) span { return span{a, p.last()} }

func (p *parser) loc(s span) ir.Loc {
	st, sp := p.toks[s.a], p.toks[s.b]
	endCol := st.Col + (sp.Stop - st.Start)
	if sp.Stop == 0 {
		endCol = sp.Col
	}
	return ir.Loc{
		Source: p.source,
		Start:  ir.Pos{Line: st.Line - 1, Col: st.Col, Index: st.Start},
		End:    ir.Pos{Line: sp.Line - 1, Col: endCol, Index: sp.Stop},
	}
}

// id is ToIrListener.getId: the next id, its span in the source map.
func (p *parser) id(s span) ir.ID {
	id := p.gen.Next()
	p.sourceMap = append(p.sourceMap, SourceEntry{id, p.loc(s)})
	return id
}

// text is ANTLR's ctx.text: the span's tokens' texts, joined.
func (p *parser) text(s span) string {
	var b strings.Builder
	for i := s.a; i <= s.b; i++ {
		b.WriteString(p.toks[i].Text)
	}
	return b.String()
}

// notify is an error Quint.g4's own actions report (QNT006, QNT008,
// QNT009): at the token after the rule, and the parse goes on.
func (p *parser) notify(code, msg string) {
	p.synErrs = append(p.synErrs, synErr{p.p, code, msg, false})
}

// listenerErr is an error ToIrListener reports, naming the node id.
func (p *parser) listenerErr(code, msg string, id ir.ID) {
	var l ir.Loc
	for i := len(p.sourceMap) - 1; i >= 0; i-- {
		if p.sourceMap[i].ID == id {
			l = p.sourceMap[i].Loc
			break
		}
	}
	p.lisErrs = append(p.lisErrs, Error{Code: code, Msg: msg, Loc: l, ID: id})
}

// state is what a speculative parse takes back.
type state struct {
	p, sm, syn, lis int
	last            ir.ID
}

func (p *parser) save() state {
	return state{p.p, len(p.sourceMap), len(p.synErrs), len(p.lisErrs), p.gen.Last()}
}

func (p *parser) restore(s state) {
	p.p = s.p
	p.sourceMap = p.sourceMap[:s.sm]
	p.synErrs = p.synErrs[:s.syn]
	p.lisErrs = p.lisErrs[:s.lis]
	p.gen.Reset(s.last)
}

// tryStop runs f; if it stops the parse, everything it did is taken
// back and tryStop returns where it stopped; else nil, and f's work stays.
func (p *parser) tryStop(f func()) (st *stop) {
	s := p.save()
	defer func() {
		if r := recover(); r != nil {
			x, isStop := r.(stop)
			if !isStop {
				panic(r)
			}
			p.restore(s)
			st = &x
		}
	}()
	f()
	return nil
}

// continues runs cont, a construct's continuation at the current token T
// (an operator, a call's '('). Where cont stops the parse past T and T
// could start an expression, ANTLR's prediction, which follows the
// expression's exit into any context (a let's body), finds the exit path
// alive as long as cont's and takes it: continues then takes cont back
// and reports false (the construct ends before T), unless the expression
// from T stops first.
func (p *parser) continues(cont func()) bool {
	if !startsExpr(p.cur()) {
		cont()
		return true
	}
	at := p.p
	if p.forced[at] {
		return false
	}
	sc := p.tryStop(cont)
	if sc == nil {
		p.amb = append(p.amb, at)
		return true
	}
	sx := p.probe(func() { p.expr(0) })
	if sx == nil || sx.tok >= sc.tok {
		return false
	}
	panic(*sc)
}

// probe runs f and takes everything it did back, whether or not it stops
// the parse: where it stopped, or nil.
func (p *parser) probe(f func()) *stop {
	s := p.save()
	st := p.tryStop(f)
	p.restore(s)
	return st
}

// try runs f; if it stops the parse, everything it did is taken back and
// try reports false.
func (p *parser) try(f func()) (ok bool) {
	s := p.save()
	defer func() {
		if r := recover(); r != nil {
			if _, isStop := r.(stop); !isStop {
				panic(r)
			}
			p.restore(s)
			ok = false
		}
	}()
	f()
	return true
}

// ---- modules and declarations

// modules : HASHBANG_LINE? module+ EOF
func (p *parser) modulesRule() {
	if p.cur().Kind == THashbang {
		p.advance()
	}
	for {
		if p.cur().Kind != TDoc && !p.is("module") {
			if len(p.modules) == 0 || p.cur().Kind != TEOF {
				p.failf("expected a module")
			}
			return
		}
		p.module()
	}
}

func docText(toks []Tok) *string {
	var lines []string
	for _, t := range toks {
		rs := []rune(t.Text)
		if len(rs) > 4 {
			lines = append(lines, string(rs[4:len(rs)-1]))
		} else {
			lines = append(lines, "")
		}
	}
	s := strings.Join(lines, "\n")
	if s == "" {
		return nil
	}
	return &s
}

func (p *parser) docs() []Tok {
	var ds []Tok
	for p.cur().Kind == TDoc {
		ds = append(ds, p.cur())
		p.advance()
	}
	return ds
}

// module : DOCCOMMENT* 'module' qualId '{' documentedDeclaration* '}'
func (p *parser) module() {
	a := p.p
	doc := docText(p.docs())
	p.expect("module", "to start a module")
	name, _ := p.qualID()
	p.expect("{", "after the module's name")
	var decls []*ir.Decl
	for !p.is("}") {
		if p.cur().Kind != TDoc && !declStart(p.cur()) {
			p.failf("expected a declaration (const, var, assume, val, def, pure, action, run, temporal, nondet, type, import or export) or '}'")
		}
		decls = append(decls, p.documentedDeclaration()...)
	}
	p.advance()
	m := &ir.Module{Name: name, Doc: doc, Decls: decls}
	m.ID = p.id(p.spanFrom(a))
	p.modules = append(p.modules, m)
}

// qualifiers are the keywords that start an operator definition
// (read-only).
var qualifiers = []string{"val", "def", "pure", "action", "run", "temporal", "nondet"}

func declStart(t Tok) bool {
	if t.Kind != TKeyword {
		return false
	}
	return slices.Contains(qualifiers, t.Text) || slices.Contains([]string{"const", "var", "assume", "import", "export", "type"}, t.Text)
}

// documentedDeclaration : DOCCOMMENT* declaration. The doc goes to the
// last declaration it made (a sum type's last constructor), as the
// listener re-pushes the top of its stack.
func (p *parser) documentedDeclaration() []*ir.Decl {
	doc := docText(p.docs())
	ds := p.declaration()
	if doc != nil && len(ds) > 0 {
		ds[len(ds)-1].Doc = doc
	}
	return ds
}

func (p *parser) declaration() []*ir.Decl {
	a := p.p
	switch t := p.cur(); {
	case isTok(t, "const"), isTok(t, "var"):
		p.advance()
		name, _ := p.qualID()
		p.expect(":", "and the type after the name")
		ty, _ := p.typ(0)
		d := &ir.Decl{Kind: t.Text, Name: name, TypeAnnotation: ty}
		d.ID = p.id(p.spanFrom(a))
		return []*ir.Decl{d}
	case isTok(t, "assume"):
		p.advance()
		name := p.identOrHole()
		p.expect("=", "after the assumption's name")
		e, _ := p.expr(0)
		d := &ir.Decl{Kind: ir.KindAssume, Name: name, Assumption: e}
		d.ID = p.id(p.spanFrom(a))
		return []*ir.Decl{d}
	case isTok(t, "import"):
		return []*ir.Decl{p.importDecl()}
	case isTok(t, "export"):
		return []*ir.Decl{p.exportDecl()}
	case isTok(t, "type"):
		return p.typeDef()
	default:
		return p.operDef(false).decls
	}
}

// identOrHole : '_' | qualId
func (p *parser) identOrHole() string {
	if p.is("_") {
		p.advance()
		return "_"
	}
	n, _ := p.qualID()
	return n
}

// identifier : LOW_ID | CAP_ID | keywordAsID | reserved (QNT008)
func (p *parser) identifier() string {
	t := p.cur()
	switch {
	case t.Kind == TLowID || t.Kind == TCapID || isKeywordAsID(t):
		p.advance()
		return t.Text
	case isReserved(t):
		p.advance()
		p.notify("QNT008", fmt.Sprintf("Reserved keyword '%s' cannot be used as an identifier.", t.Text))
		return t.Text
	}
	p.failf("expected an identifier")
	return ""
}

func isIdentTok(t Tok) bool {
	return t.Kind == TLowID || t.Kind == TCapID || isKeywordAsID(t) || isReserved(t)
}

// qualId : identifier ('::' identifier)*
func (p *parser) qualID() (string, span) {
	a := p.p
	parts := []string{p.identifier()}
	for p.is("::") {
		p.advance()
		parts = append(parts, p.identifier())
	}
	return strings.Join(parts, "::"), p.spanFrom(a)
}

// simpleId : identifier | qualId (QNT008: a qualified name where a
// simple one is required)
func (p *parser) simpleID(context string) (string, span) {
	n, s := p.qualID()
	if strings.Contains(n, "::") {
		p.notify("QNT008", fmt.Sprintf("Identifiers in a %s cannot be qualified with '::'. Found %s.", context, n))
	}
	return n, s
}

func strp(s string) *string { return &s }

func (p *parser) fromSource() *string {
	if !p.is("from") {
		return nil
	}
	p.advance()
	t := p.cur()
	if t.Kind != TString {
		p.failf("expected the file's path in quotes after 'from'")
	}
	p.advance()
	return strp(t.Text[1 : len(t.Text)-1])
}

// identOrStar : '*' | qualId
func (p *parser) identOrStar() string {
	if p.is("*") {
		p.advance()
		return "*"
	}
	n, _ := p.qualID()
	return n
}

// importMod and instanceMod
func (p *parser) importDecl() *ir.Decl {
	a := p.advance()
	proto, _ := p.qualID()
	if p.is("(") {
		return p.instance(a, proto)
	}
	d := &ir.Decl{Kind: ir.KindImport, ProtoName: strp(proto)}
	if p.is(".") {
		p.advance()
		d.DefName = strp(p.identOrStar())
	} else if p.is("as") {
		p.advance()
		q, _ := p.qualID()
		d.QualifiedName = strp(q)
	}
	d.FromSource = p.fromSource()
	d.ID = p.id(p.spanFrom(a))
	return d
}

// instanceMod : 'import' moduleName '(' name '=' expr (',' name '=' expr)* ','? ')'
// ('.' '*' | AS qualifiedName) (FROM fromSource)?
func (p *parser) instance(a int, proto string) *ir.Decl {
	p.advance() // (
	type named struct {
		name string
		s    span
	}
	var names []named
	var exprs []*ir.Expr
	for {
		n, s := p.qualID()
		p.expect("=", "after the constant's name in an instance")
		e, _ := p.expr(0)
		names = append(names, named{n, s})
		exprs = append(exprs, e)
		if p.is(",") && !isTok(p.peek(1), ")") {
			p.advance()
			continue
		}
		if p.is(",") {
			p.advance()
		}
		break
	}
	p.expect(")", "to close the instance's constants")
	d := &ir.Decl{Kind: ir.KindInstance, ProtoName: strp(proto)}
	switch {
	case p.is("."):
		p.advance()
		p.expect("*", "after '.' in an instance (import M(...).*)")
		d.IdentityOverride = true
	case p.is("as"):
		p.advance()
		q, _ := p.qualID()
		d.QualifiedName = strp(q)
	default:
		p.failf("expected '.*' or 'as' after an instance's constants")
	}
	d.FromSource = p.fromSource()
	for i, n := range names {
		d.Overrides = append(d.Overrides, ir.Override{Param: &ir.Param{ID: p.id(n.s), Name: n.name}, Expr: exprs[i]})
	}
	d.ID = p.id(p.spanFrom(a))
	return d
}

// exportMod : EXPORT name '.' identOrStar | EXPORT name (AS name)?
func (p *parser) exportDecl() *ir.Decl {
	a := p.advance()
	proto, _ := p.qualID()
	d := &ir.Decl{Kind: ir.KindExport, ProtoName: strp(proto)}
	if p.is(".") {
		p.advance()
		d.DefName = strp(p.identOrStar())
	} else if p.is("as") {
		p.advance()
		q, _ := p.qualID()
		d.QualifiedName = strp(q)
	}
	d.ID = p.id(p.spanFrom(a))
	return d
}

// typeDef : 'type' qualId | 'type' typeDefHead '=' sumTypeDefinition | 'type' typeDefHead '=' type
func (p *parser) typeDef() []*ir.Decl {
	a := p.advance()
	name, _ := p.qualID()
	var params []string
	if p.is("[") {
		p.advance()
		for {
			t := p.cur()
			if t.Kind != TLowID {
				p.failf("expected a type parameter (a lowercase name)")
			}
			p.advance()
			params = append(params, t.Text)
			if !p.is(",") {
				break
			}
			p.advance()
		}
		p.expect("]", "to close the type's parameters")
	} else if !p.is("=") {
		d := &ir.Decl{Kind: ir.KindTypeDef, Name: name}
		d.ID = p.id(p.spanFrom(a))
		p.checkUppercase(d.ID, name)
		return []*ir.Decl{d}
	}
	p.expect("=", "after the type's name")
	// ANTLR prefers the sum type (its alternative comes first) wherever
	// both parse: `type T = A` is a sum of one variant, `A(int)` one
	// carrying an int, `Set(int)` one named Set; a type that goes on
	// (`A -> B`, `List[a]`) is an alias
	var decls []*ir.Decl
	if p.is("|") || p.try(func() {
		decls = p.sumType(a, name, params)
		if t := p.cur(); t.Kind != TDoc && t.Kind != TEOF && !p.is("}") && !declStart(t) {
			p.failf("a sum type's variants end its declaration")
		}
	}) {
		if decls == nil {
			decls = p.sumType(a, name, params)
		}
		return decls
	}
	ty, _ := p.typ(0)
	d := &ir.Decl{Kind: ir.KindTypeDef, Name: name, Type: ty, Params: params}
	d.ID = p.id(p.spanFrom(a))
	p.checkUppercase(d.ID, name)
	p.checkTypeVars(d)
	return []*ir.Decl{d}
}

// checkUppercase is the listener's QNT007.
func (p *parser) checkUppercase(id ir.ID, name string) {
	parts := strings.Split(name, "::")
	last := parts[len(parts)-1]
	if last != "" && isLow(rune(last[0])) {
		p.listenerErr("QNT007", "type names must start with an uppercase letter", id)
	}
}

// checkTypeVars is the listener's QNT014: a type variable the definition
// does not take.
func (p *parser) checkTypeVars(d *ir.Decl) {
	if d.Type == nil {
		return
	}
	vars, rows := typeNames(d.Type)
	var unbound []string
	for _, v := range append(vars, rows...) {
		if !slices.Contains(d.Params, v) {
			unbound = append(unbound, v)
		}
	}
	if len(unbound) > 0 {
		p.listenerErr("QNT014", fmt.Sprintf("the type variables %s are unbound: give them as the type's parameters (type T[a] = List[a])", strings.Join(unbound, ", ")), d.ID)
	}
}

// typeNames are the type variables and the row variables of t, each once,
// in the order quint's collector meets them.
func typeNames(t *ir.Type) (vars, rows []string) {
	add := func(s *[]string, n string) {
		if !slices.Contains(*s, n) {
			*s = append(*s, n)
		}
	}
	var walk func(t *ir.Type)
	var row func(r *ir.Row)
	row = func(r *ir.Row) {
		if r == nil {
			return
		}
		switch r.Kind {
		case ir.RowRow:
			for _, f := range r.Fields {
				walk(f.FieldType)
			}
			row(r.Other)
		case ir.RowVar:
			add(&rows, r.Name)
		}
	}
	walk = func(t *ir.Type) {
		if t == nil {
			return
		}
		if t.Kind == ir.TyVar {
			add(&vars, t.Name)
		}
		for _, x := range []*ir.Type{t.Elem, t.Arg, t.Ctor} {
			walk(x)
		}
		for _, x := range t.Args {
			walk(x)
		}
		walk(t.Res)
		row(t.Fields)
	}
	walk(t)
	return vars, rows
}

type variant struct {
	label     string
	labelSpan span
	ty        *ir.Type
	tySpan    span
	hasType   bool
	s         span
}

// sumType is typeSumDef after the '=' (ToIrListener.exitTypeSumDef).
func (p *parser) sumType(a int, name string, params []string) []*ir.Decl {
	sepd := false
	if p.is("|") {
		p.advance()
		sepd = true
	}
	var vs []variant
	for {
		va := p.p
		label, ls := p.simpleID("variant label")
		v := variant{label: label, labelSpan: ls}
		if p.is("(") {
			p.advance()
			ta := p.p
			v.ty, _ = p.typ(0)
			v.tySpan = p.spanFrom(ta)
			v.hasType = true
			p.expect(")", "to close the variant's type")
		}
		v.s = p.spanFrom(va)
		if !v.hasType {
			v.ty = unitType(p.id(v.s))
		}
		vs = append(vs, v)
		if !p.is("|") {
			break
		}
		p.advance()
	}
	s := p.spanFrom(a)
	id := p.id(s)
	fields := make([]ir.RowField, len(vs))
	for i, v := range vs {
		fields[i] = ir.RowField{FieldName: v.label, FieldType: v.ty}
	}
	def := &ir.Decl{ID: id, Kind: ir.KindTypeDef, Name: name, Params: params,
		Type: &ir.Type{ID: id, Kind: ir.TySum, Fields: &ir.Row{Kind: ir.RowRow, Fields: fields, Other: &ir.Row{Kind: ir.RowEmpty}}}}
	p.checkTypeVars(def)
	typeConst := &ir.Type{ID: p.id(s), Kind: ir.TyConst, Name: name}
	ret := typeConst
	if params != nil {
		args := make([]*ir.Type, len(params))
		for i, n := range params {
			args[i] = &ir.Type{ID: p.id(s), Kind: ir.TyVar, Name: n}
		}
		ret = &ir.Type{ID: id, Kind: ir.TyApp, Ctor: typeConst, Args: args}
	}
	if len(vs) == 1 && isUnitType(vs[0].ty) && !sepd {
		// `type T = A`: an alias of A, unless written `type T = | A`
		def.Type = &ir.Type{ID: p.id(vs[0].s), Kind: ir.TyConst, Name: p.text(vs[0].s)}
		return []*ir.Decl{def}
	}
	decls := []*ir.Decl{def}
	for _, v := range vs {
		label := &ir.Expr{ID: p.id(v.s), Kind: ir.ExStr, Str: v.label}
		c := &ir.Decl{Kind: ir.KindDef, Name: v.label}
		if isUnitType(v.ty) {
			unit := &ir.Expr{ID: p.id(v.labelSpan), Kind: ir.ExApp, Opcode: "Tup"}
			c.Qualifier = "val"
			c.TypeAnnotation = ret
			c.Expr = &ir.Expr{ID: p.id(v.s), Kind: ir.ExApp, Opcode: "variant", Args: []*ir.Expr{label, unit}}
		} else {
			param := "__" + v.label + "Param"
			ps := []*ir.Param{{ID: p.id(v.tySpan), Name: param}}
			wrapped := &ir.Expr{ID: p.id(v.labelSpan), Kind: ir.ExName, Name: param}
			app := &ir.Expr{ID: p.id(v.s), Kind: ir.ExApp, Opcode: "variant", Args: []*ir.Expr{label, wrapped}}
			c.Qualifier = "def"
			c.TypeAnnotation = &ir.Type{NoID: true, Kind: ir.TyOper, Args: []*ir.Type{v.ty}, Res: ret}
			c.Expr = &ir.Expr{ID: p.id(v.s), Kind: ir.ExLambda, Params: ps, Qualifier: "def", Body: app}
		}
		c.ID = p.id(v.s)
		decls = append(decls, c)
	}
	return decls
}

// exMissing is an expression the listener never pushed (a record with
// two spreads).
const exMissing = "<missing>"

// undefinedExpr is ToIrListener.undefinedExpr: a let of
// __undefinedExprGenerated, filling a hole in the listener's stack.
func (p *parser) undefinedExpr(s span) *ir.Expr {
	const name = "__undefinedExprGenerated"
	letID := p.id(s)
	defID := p.id(s)
	boolID := p.id(s)
	nameID := p.id(s)
	return &ir.Expr{ID: letID, Kind: ir.ExLet,
		Opdef: &ir.Decl{ID: defID, Kind: ir.KindDef, Qualifier: "val", Name: name, Expr: &ir.Expr{ID: boolID, Kind: ir.ExBool, Bool: true}},
		Body:  &ir.Expr{ID: nameID, Kind: ir.ExName, Name: name}}
}

func unitType(id ir.ID) *ir.Type {
	return &ir.Type{ID: id, Kind: ir.TyTup, Fields: &ir.Row{Kind: ir.RowRow, Other: &ir.Row{Kind: ir.RowEmpty}}}
}

func isUnitType(t *ir.Type) bool {
	return t.Kind == ir.TyTup && t.Fields != nil && t.Fields.Kind == ir.RowRow && len(t.Fields.Fields) == 0 && t.Fields.Other != nil && t.Fields.Other.Kind == ir.RowEmpty
}

// An operDef's result: its declarations (a destructuring's temporary and
// its fields), and for a destructuring in a let, what the let binds.
type operDefResult struct {
	decls []*ir.Decl
	// destructured: the pattern's kind (tuple or record) and names, for
	// exitLetIn, which binds them inside the let
	pattern string
	names   []string
}

// operDef : qualifier normalCallName ... (annotatedOperDef, deprecatedOperDef)
// | 'val' destructuringPattern '=' expr ';'? | 'pure' 'val' destructuringPattern '=' expr ';'?
func (p *parser) operDef(inLet bool) operDefResult {
	a := p.p
	qual := p.qualifier()
	if (qual == "val" || qual == "pureval") && (p.is("(") || p.is("{")) {
		return p.destructuring(a, qual, inLet)
	}
	ns := p.p
	name := p.normalCallName()
	nameSpan := p.spanFrom(ns)
	var params []*ir.Param
	var body *ir.Expr
	annotated := false
	var retType *ir.Type
	// the def's rest: (':' type)? ('=' expr)? ';'?, the result type only
	// where the parameters allow it
	rest := func(withRet bool) {
		if withRet && p.is(":") {
			p.advance()
			retType, _ = p.typ(0)
		}
		if p.is("=") {
			p.advance()
			body, _ = p.expr(0)
		}
		if p.is(";") {
			p.advance()
		}
	}
	if p.is("(") {
		// ANTLR's decision between annotatedOperDef (every parameter
		// annotated, at least one, and the result) and deprecatedOperDef
		// (none annotated, the parentheses optional) reads ahead as far
		// as it must. Where both fail, it takes the alternative that can
		// leave the rule (the deprecated one, without its parentheses,
		// the '(' read as the next expression, a let's body): the error
		// is then at '(', unless the parameters' path reads further
		// than that expression can.
		annot := func() {
			params, retType, body = nil, nil, nil
			p.advance()
			for {
				params = append(params, p.annotatedParameter())
				if !p.is(",") {
					break
				}
				p.advance()
				if p.is(")") {
					break
				}
			}
			p.expect(")", "to close the parameters")
			if !p.is(":") {
				p.failf("an operator with annotated parameters needs its result's type (def f(x: int): int = ...): annotate every parameter and the result, or none of them")
			}
			rest(true)
		}
		depr := func() {
			params, retType, body = nil, nil, nil
			p.advance()
			for !p.is(")") {
				params = append(params, p.parameter())
				if !p.is(",") {
					break
				}
				p.advance()
			}
			if p.is(":") {
				p.failf("annotate every parameter and the result (def f(x: int): int = ...), or none of them")
			}
			p.expect(")", "to close the parameters")
			rest(true)
		}
		at := p.p
		var sa *stop
		if !p.forced[at] {
			sa = p.tryStop(annot)
		}
		switch {
		case p.forced[at]:
			params = nil
			rest(false)
		case sa == nil:
			annotated = true
		case p.tryStop(depr) == nil:
			p.amb = append(p.amb, at)
		default:
			sd := p.tryStop(depr)
			sx := p.probe(func() { p.expr(0) })
			switch {
			case sa.tok > sd.tok && (sx == nil || sa.tok > sx.tok):
				panic(*sa)
			case sx != nil && sd.tok > sx.tok:
				panic(*sd)
			}
			// the def ends at its name
			params = nil
			rest(false)
		}
	} else {
		rest(true)
	}
	s := p.spanFrom(a)
	switch {
	case body == nil:
		// a header without a body: the listener's default body
		body = &ir.Expr{ID: p.id(s), Kind: ir.ExBool, Bool: true}
	case body.Kind == exMissing:
		body = p.undefinedExpr(s)
	}
	if len(params) > 0 {
		body = &ir.Expr{ID: p.id(s), Kind: ir.ExLambda, Params: params, Qualifier: qual, Body: body}
	}
	d := &ir.Decl{Kind: ir.KindDef, Name: name, Qualifier: qual, Expr: body}
	d.ID = p.id(nameSpan)
	if annotated {
		args := make([]*ir.Type, len(params))
		for i, prm := range params {
			args[i] = prm.TypeAnnotation
		}
		d.TypeAnnotation = &ir.Type{NoID: true, Kind: ir.TyOper, Args: args, Res: retType}
	} else if retType != nil {
		d.TypeAnnotation = retType
	}
	return operDefResult{decls: []*ir.Decl{d}}
}

// qualifier : 'val' | 'def' | 'pure' 'val' | 'pure' 'def' | 'action' | 'run' | 'temporal' | 'nondet'
func (p *parser) qualifier() string {
	t := p.cur()
	if t.Kind == TKeyword {
		switch t.Text {
		case "val", "def", "action", "run", "temporal", "nondet":
			p.advance()
			return t.Text
		case "pure":
			p.advance()
			if p.is("val") || p.is("def") {
				return "pure" + p.toks[p.advance()].Text
			}
			p.failf("expected 'val' or 'def' after 'pure'")
		}
	}
	p.failf("expected a definition (val, def, pure val, pure def, action, run, temporal or nondet)")
	return ""
}

// normalCallName : op=(AND | OR | IFF | IMPLIES | LEADS_TO | SET | LIST) | qualId
func (p *parser) normalCallName() string {
	t := p.cur()
	if t.Kind == TKeyword && slices.Contains([]string{"and", "or", "iff", "implies", "leadsTo", "Set", "List"}, t.Text) {
		p.advance()
		return t.Text
	}
	n, _ := p.qualID()
	return n
}

// destructuring: 'val' destructuringPattern '=' expr ';'?
func (p *parser) destructuring(a int, qual string, inLet bool) operDefResult {
	var names []string
	pattern := "tuple"
	if p.is("(") {
		p.advance()
		names = append(names, p.identOrHole())
		if !p.is(",") {
			p.failf("expected ',': a tuple pattern names two fields or more")
		}
		for p.is(",") {
			p.advance()
			names = append(names, p.identOrHole())
		}
		p.expect(")", "to close the tuple pattern")
	} else {
		pattern = "record"
		p.advance()
		n, _ := p.simpleID("record field")
		names = append(names, n)
		for p.is(",") {
			p.advance()
			n, _ := p.simpleID("record field")
			names = append(names, n)
		}
		p.expect("}", "to close the record pattern")
	}
	p.expect("=", "after the pattern")
	e, _ := p.expr(0)
	if p.is(";") {
		p.advance()
	}
	tempID := p.id(p.spanFrom(a))
	temp := &ir.Decl{ID: tempID, Kind: ir.KindDef, Name: fmt.Sprintf("quintDestructTemp%d", tempID), Qualifier: qual, Expr: e}
	r := operDefResult{decls: []*ir.Decl{temp}, pattern: pattern, names: names}
	if !inLet {
		for i, n := range names {
			if n != "_" {
				r.decls = append(r.decls, p.destructuredField(pattern, temp.Name, i, n, qual))
			}
		}
	}
	return r
}

// destructuredField is ToIrListener.defForDestructuredField: four ids
// with no place in the source map.
func (p *parser) destructuredField(pattern, temp string, i int, name, qual string) *ir.Decl {
	defID, appID, nameID, accID := p.gen.Next(), p.gen.Next(), p.gen.Next(), p.gen.Next()
	acc := &ir.Expr{ID: accID, Kind: ir.ExInt, Int: big.NewInt(int64(i + 1))}
	op := "item"
	if pattern == "record" {
		acc = &ir.Expr{ID: accID, Kind: ir.ExStr, Str: name}
		op = "field"
	}
	return &ir.Decl{ID: defID, Kind: ir.KindDef, Name: name, Qualifier: qual,
		Expr: &ir.Expr{ID: appID, Kind: ir.ExApp, Opcode: op, Args: []*ir.Expr{{ID: nameID, Kind: ir.ExName, Name: temp}, acc}}}
}

// ---- types

// typ is Quint.g4's type at precedence min: typeFun (16, right
// associative) and typeOper (15, right associative) over the primaries.
func (p *parser) typ(min int) (*ir.Type, span) {
	a := p.p
	t, s := p.typePrimary()
	for {
		switch {
		case p.is("->") && 16 >= min:
			p.advance()
			res, _ := p.typ(16)
			t = &ir.Type{Kind: ir.TyFun, Arg: t, Res: res}
			s = p.spanFrom(a)
			t.ID = p.id(s)
		case p.is("=>") && 15 >= min:
			p.advance()
			res, _ := p.typ(15)
			t = &ir.Type{Kind: ir.TyOper, Args: []*ir.Type{t}, Res: res}
			s = p.spanFrom(a)
			t.ID = p.id(s)
		default:
			return t, s
		}
	}
}

func (p *parser) typePrimary() (*ir.Type, span) {
	a := p.p
	t := p.cur()
	switch {
	case isTok(t, "("):
		p.advance()
		var args []*ir.Type
		trailing := false
		for !p.is(")") {
			x, _ := p.typ(0)
			args = append(args, x)
			trailing = false
			if !p.is(",") {
				break
			}
			p.advance()
			trailing = true
		}
		p.expect(")", "to close the type")
		if p.is("=>") {
			// '(' (type (',' type)*)? ','? ')' '=>' type
			p.advance()
			res, _ := p.typ(14)
			s := p.spanFrom(a)
			return &ir.Type{ID: p.id(s), Kind: ir.TyOper, Args: orNoTypes(args), Res: res}, s
		}
		s := p.spanFrom(a)
		switch {
		case len(args) == 0:
			return &ir.Type{ID: p.id(s), Kind: ir.TyTup, Fields: &ir.Row{Kind: ir.RowEmpty}}, s
		case len(args) == 1 && !trailing:
			return args[0], s
		case len(args) == 1:
			p.p = s.b
			p.failf("a tuple type has two fields or more")
		}
		fields := make([]ir.RowField, len(args))
		for i, x := range args {
			fields[i] = ir.RowField{FieldName: fmt.Sprint(i), FieldType: x}
		}
		return &ir.Type{ID: p.id(s), Kind: ir.TyTup, Fields: &ir.Row{Kind: ir.RowRow, Fields: fields, Other: &ir.Row{Kind: ir.RowEmpty}}}, s
	case (isTok(t, "Set") || isTok(t, "List")) && isTok(p.peek(1), "["):
		p.advance()
		p.advance()
		elem, _ := p.typ(0)
		p.expect("]", "to close the type")
		s := p.spanFrom(a)
		kind := ir.TySet
		if t.Text == "List" {
			kind = ir.TyList
		}
		return &ir.Type{ID: p.id(s), Kind: kind, Elem: elem}, s
	case isTok(t, "{"):
		p.advance()
		row := &ir.Row{Kind: ir.RowEmpty}
		if !p.is("}") {
			row = p.row()
		}
		p.expect("}", "to close the record type")
		s := p.spanFrom(a)
		return &ir.Type{ID: p.id(s), Kind: ir.TyRec, Fields: row}, s
	case isTok(t, "int"), isTok(t, "str"), isTok(t, "bool"):
		p.advance()
		s := p.spanFrom(a)
		return &ir.Type{ID: p.id(s), Kind: t.Text}, s
	case t.Kind == TLowID && !isTok(p.peek(1), "::") && !isTok(p.peek(1), "[") && !isTok(p.peek(1), "("):
		p.advance()
		s := p.spanFrom(a)
		return &ir.Type{ID: p.id(s), Kind: ir.TyVar, Name: t.Text}, s
	case isIdentTok(t):
		name, _ := p.qualID()
		if p.is("[") || p.is("(") {
			wrong := p.is("(")
			p.advance()
			var args []*ir.Type
			for {
				x, _ := p.typ(0)
				args = append(args, x)
				if !p.is(",") {
					break
				}
				p.advance()
			}
			argSpan := span{a + 2, p.last()}
			if wrong {
				p.expect(")", "to close the type's arguments")
				p.notify("QNT009", fmt.Sprintf("Use square brackets instead of parenthesis for type application: %s[%s]", name, strings.ReplaceAll(p.text(argSpan), ",", ", ")))
			} else {
				p.expect("]", "to close the type's arguments")
			}
			s := p.spanFrom(a)
			id := p.id(s)
			ctor := &ir.Type{ID: p.id(s), Kind: ir.TyConst, Name: name}
			if name == "Map" && len(args) == 2 {
				p.listenerErr("QNT015", fmt.Sprintf("Use '%s -> %s' instead of 'Map[%s, %s]' for map types", typeString(args[0]), typeString(args[1]), typeString(args[0]), typeString(args[1])), id)
				return &ir.Type{ID: id, Kind: ir.TyFun, Arg: args[0], Res: args[1]}, s
			}
			return &ir.Type{ID: id, Kind: ir.TyApp, Ctor: ctor, Args: args}, s
		}
		s := p.spanFrom(a)
		return &ir.Type{ID: p.id(s), Kind: ir.TyConst, Name: name}, s
	}
	p.failf("expected a type")
	return nil, span{}
}

func orNoTypes(ts []*ir.Type) []*ir.Type {
	if ts == nil {
		return []*ir.Type{}
	}
	return ts
}

// row : (rowLabel ':' type) (',' rowLabel ':' type)* (',' | '|' rowVar)? | '|' rowVar
func (p *parser) row() *ir.Row {
	r := &ir.Row{Kind: ir.RowRow, Other: &ir.Row{Kind: ir.RowEmpty}}
	if !p.is("|") {
		for {
			n, _ := p.simpleID("record")
			p.expect(":", "after the field's name")
			t, _ := p.typ(0)
			r.Fields = append(r.Fields, ir.RowField{FieldName: n, FieldType: t})
			if !p.is(",") {
				break
			}
			p.advance()
			if p.is("}") || p.is("|") {
				break
			}
		}
	}
	if p.is("|") {
		p.advance()
		r.Other = &ir.Row{Kind: ir.RowVar, Name: p.identifier()}
	}
	return r
}

// typeString is quint's typeToString, for QNT015's message.
func typeString(t *ir.Type) string {
	switch t.Kind {
	case ir.TyInt, ir.TyStr, ir.TyBool:
		return t.Kind
	case ir.TyConst, ir.TyVar:
		return t.Name
	case ir.TySet:
		return "Set[" + typeString(t.Elem) + "]"
	case ir.TyList:
		return "List[" + typeString(t.Elem) + "]"
	case ir.TyFun:
		return "(" + typeString(t.Arg) + " -> " + typeString(t.Res) + ")"
	}
	return t.Kind
}

// ---- expressions

// The precedences ANTLR gives expr's left-recursive alternatives (31
// alternatives; the k-th has 32-k).
const (
	precDot     = 31
	precListApp = 28
	precPow     = 27
	precUminus  = 26
	precMul     = 25
	precAdd     = 24
	precRel     = 23
	precAsgn    = 22
	precErrorEq = 21
	precAnd     = 19
	precOr      = 17
	precIff     = 16
	precLeadsTo = 15
	precImplies = 14
	precPair    = 7
	precIfElse  = 4
	precLetIn   = 3
)

// binops are the binary alternatives: precedence, associativity and the
// opcode the listener gives them (read-only).
var binops = map[string]struct {
	prec   int
	right  bool
	opcode string
}{
	"^": {precPow, true, "ipow"}, "*": {precMul, false, "imul"}, "/": {precMul, false, "idiv"}, "%": {precMul, false, "imod"},
	"+": {precAdd, false, "iadd"}, "-": {precAdd, false, "isub"},
	">": {precRel, false, "igt"}, "<": {precRel, false, "ilt"}, ">=": {precRel, false, "igte"}, "<=": {precRel, false, "ilte"},
	"!=": {precRel, false, "neq"}, "==": {precRel, false, "eq"},
	"=": {precErrorEq, false, "eq"}, "and": {precAnd, false, "and"}, "or": {precOr, false, "or"}, "iff": {precIff, false, "iff"},
	"leadsTo": {precLeadsTo, false, "leadsTo"}, "implies": {precImplies, false, "implies"}, "->": {precPair, false, "Tup"},
}

// app is ToIrListener.pushApplication: an application with the span's
// id, and q::debug's text as its first argument.
func (p *parser) app(s span, name string, args []*ir.Expr) *ir.Expr {
	id := p.id(s)
	if name == "q::debug" && len(args) == 1 {
		str := &ir.Expr{ID: p.gen.Next(), Kind: ir.ExStr, Str: p.text(s)}
		return &ir.Expr{ID: id, Kind: ir.ExApp, Opcode: name, Args: []*ir.Expr{str, args[0]}}
	}
	return &ir.Expr{ID: id, Kind: ir.ExApp, Opcode: name, Args: args}
}

// expr is Quint.g4's expr at precedence min: a primary, then the
// suffix and binary alternatives whose precedence is min or more.
func (p *parser) expr(min int) (*ir.Expr, span) {
	a := p.p
	e, s := p.primary()
	for {
		t := p.cur()
		switch {
		case isTok(t, ".") && precDot >= min:
			p.advance()
			var name string
			if n := p.cur(); n.Kind == TKeyword && slices.Contains([]string{"and", "or", "iff", "implies", "leadsTo"}, n.Text) {
				p.advance()
				name = n.Text
			} else {
				name, _ = p.qualID()
			}
			if p.is("(") && p.continues(func() {
				p.advance()
				args := []*ir.Expr{e}
				if !p.is(")") {
					args = append(args, p.argList()...)
				}
				p.expect(")", "to close the arguments")
				s = p.spanFrom(a)
				e = p.app(s, name, args)
			}) {
				continue
			}
			s = p.spanFrom(a)
			id := p.id(s)
			if m := tupleIndex.FindStringSubmatch(name); m != nil {
				idx, _ := new(big.Int).SetString(m[1], 10)
				e = p.app(s, "item", []*ir.Expr{e, {ID: id, Kind: ir.ExInt, Int: idx}})
			} else {
				e = p.app(s, "field", []*ir.Expr{e, {ID: id, Kind: ir.ExStr, Str: name}})
			}
		case isTok(t, "[") && precListApp >= min:
			if !p.continues(func() {
				p.advance()
				i, _ := p.expr(0)
				p.expect("]", "to close the index")
				s = p.spanFrom(a)
				e = p.app(s, "nth", []*ir.Expr{e, i})
			}) {
				return e, s
			}
		case (t.Kind == TPunct || t.Kind == TKeyword) && binops[t.Text].opcode != "" && binops[t.Text].prec >= min:
			op := binops[t.Text]
			if !p.continues(func() {
				p.advance()
				next := op.prec + 1
				if op.right {
					next = op.prec
				}
				rhs, _ := p.expr(next)
				s = p.spanFrom(a)
				if t.Text == "=" {
					p.notify("QNT006", "unexpected '=', did you mean '=='?")
				}
				e = p.app(s, op.opcode, []*ir.Expr{e, rhs})
			}) {
				return e, s
			}
		default:
			return e, s
		}
	}
}

// tupleIndex is a tuple field's name, _1 to _99 (read-only).
var tupleIndex = regexp.MustCompile(`^_([1-9][0-9]?)$`)

// argList : expr (',' expr)*
func (p *parser) argList() []*ir.Expr {
	var args []*ir.Expr
	for {
		e, _ := p.expr(0)
		args = append(args, e)
		if !p.is(",") || isTok(p.peek(1), ")") {
			return args
		}
		p.advance()
	}
}

// exprList is expr (',' expr)* ','? up to close.
func (p *parser) exprList(close string) []*ir.Expr {
	var es []*ir.Expr
	for {
		e, _ := p.expr(0)
		es = append(es, e)
		if !p.is(",") {
			return es
		}
		p.advance()
		if p.is(close) {
			return es
		}
	}
}

// identOrHoleAt is the end of an identOrHole starting at token i, or -1.
func (p *parser) identOrHoleAt(i int) int {
	if i >= len(p.toks) {
		return -1
	}
	if isTok(p.look(i), "_") {
		return i + 1
	}
	if !isIdentTok(p.look(i)) {
		return -1
	}
	i++
	for i+1 < len(p.toks) && isTok(p.look(i), "::") && isIdentTok(p.look(i+1)) {
		i += 2
	}
	return i
}

// lambdaAhead says which lambda starts at the cursor: 1 for
// lambdaUnsugared, 2 for lambdaTupleSugar, 0 for none.
func (p *parser) lambdaAhead() int {
	i := p.p
	if j := p.identOrHoleAt(i); j > 0 {
		if isTok(p.look(j), "=>") {
			return 1
		}
		return 0
	}
	if !isTok(p.look(i), "(") {
		return 0
	}
	params := func(i int) (int, int) { // past the list, and its length
		n := 0
		for {
			j := p.identOrHoleAt(i)
			if j < 0 {
				return -1, 0
			}
			n++
			if !isTok(p.look(j), ",") {
				return j, n
			}
			i = j + 1
		}
	}
	if isTok(p.look(i+1), "(") {
		if j, n := params(i + 2); j > 0 && n >= 2 && isTok(p.look(j), ")") && isTok(p.look(j+1), ")") && isTok(p.look(j+2), "=>") {
			return 2
		}
	}
	if j, _ := params(i + 1); j > 0 && isTok(p.look(j), ")") && isTok(p.look(j+1), "=>") {
		return 1
	}
	return 0
}

// annotatedParameter : identOrHole ':' type, its id at its span.
func (p *parser) annotatedParameter() *ir.Param {
	a := p.p
	n := p.identOrHole()
	p.expect(":", "and the parameter's type (every parameter annotated, or none)")
	t, _ := p.typ(0)
	return &ir.Param{ID: p.id(p.spanFrom(a)), Name: n, TypeAnnotation: t}
}

// parameter : identOrHole, its id at its span.
func (p *parser) parameter() *ir.Param {
	a := p.p
	n := p.identOrHole()
	return &ir.Param{ID: p.id(p.spanFrom(a)), Name: n}
}

func (p *parser) lambda(kind int) (*ir.Expr, span) {
	a := p.p
	var params []*ir.Param
	switch {
	case kind == 2:
		p.advance()
		p.advance()
		for {
			params = append(params, p.parameter())
			if !p.is(",") {
				break
			}
			p.advance()
		}
		p.expect(")", "")
		p.expect(")", "")
	case p.is("("):
		p.advance()
		for {
			params = append(params, p.parameter())
			if !p.is(",") {
				break
			}
			p.advance()
		}
		p.expect(")", "")
	default:
		params = append(params, p.parameter())
	}
	p.expect("=>", "")
	body, _ := p.expr(0)
	s := p.spanFrom(a)
	if kind == 1 {
		return &ir.Expr{ID: p.id(s), Kind: ir.ExLambda, Params: params, Qualifier: "def", Body: body}, s
	}
	// ((a, b)) => e: one tuple parameter, its fields bound by lets
	id := p.id(s)
	fresh := &ir.Param{ID: id, Name: fmt.Sprintf("quintTupledLambdaParam%d", id)}
	for i, prm := range params {
		letID := p.id(s)
		appID := p.id(s)
		nameID := p.id(s)
		intID := p.id(s)
		body = &ir.Expr{ID: letID, Kind: ir.ExLet, Body: body, Opdef: &ir.Decl{ID: prm.ID, Kind: ir.KindDef, Name: prm.Name, Qualifier: "pureval",
			Expr: &ir.Expr{ID: appID, Kind: ir.ExApp, Opcode: "item", Args: []*ir.Expr{{ID: nameID, Kind: ir.ExName, Name: fresh.Name}, {ID: intID, Kind: ir.ExInt, Int: big.NewInt(int64(i + 1))}}}}}
	}
	return &ir.Expr{ID: p.id(s), Kind: ir.ExLambda, Params: []*ir.Param{fresh}, Qualifier: "def", Body: body}, s
}

// startsExpr says whether t can begin an expression.
func startsExpr(t Tok) bool {
	switch t.Kind {
	case TLowID, TCapID, TInt, TString, TBool:
		return true
	case TKeyword:
		return !slices.Contains([]string{"else", "const", "var", "assume", "type", "module", "int", "str", "bool"}, t.Text)
	case TPunct:
		return slices.Contains([]string{"(", "{", "[", "-", "_"}, t.Text)
	}
	return false
}

func (p *parser) primary() (*ir.Expr, span) {
	a := p.p
	t := p.cur()
	if k := p.lambdaAhead(); k > 0 {
		return p.lambda(k)
	}
	switch {
	case t.Kind == TKeyword && slices.Contains(qualifiers, t.Text):
		// letIn : operDef expr
		def := p.operDef(true)
		body, _ := p.expr(precLetIn)
		s := p.spanFrom(a)
		temp := def.decls[0]
		if def.pattern == "" {
			return &ir.Expr{ID: p.id(s), Kind: ir.ExLet, Opdef: temp, Body: body}, s
		}
		for i, n := range def.names {
			if n == "_" {
				continue
			}
			f := p.destructuredField(def.pattern, temp.Name, i, n, temp.Qualifier)
			body = &ir.Expr{ID: p.id(s), Kind: ir.ExLet, Opdef: f, Body: body}
		}
		return &ir.Expr{ID: p.id(s), Kind: ir.ExLet, Opdef: temp, Body: body}, s
	case isTok(t, "("):
		p.advance()
		if p.is(")") {
			p.advance()
			s := p.spanFrom(a)
			return &ir.Expr{ID: p.id(s), Kind: ir.ExApp, Opcode: "Tup"}, s
		}
		first, _ := p.expr(0)
		if p.is(")") {
			p.advance()
			return first, p.spanFrom(a)
		}
		if !p.is(",") {
			p.failf("expected ',' or ')'")
		}
		p.advance()
		es := append([]*ir.Expr{first}, p.exprList(")")...)
		p.expect(")", "to close the tuple")
		s := p.spanFrom(a)
		return p.app(s, "Tup", es), s
	case isTok(t, "{"):
		if p.recordAhead() {
			return p.record()
		}
		p.advance()
		e, _ := p.expr(0)
		p.expect("}", "to close the braces")
		return e, p.spanFrom(a)
	case isTok(t, "["):
		p.advance()
		var es []*ir.Expr
		if !p.is("]") && !p.is(",") {
			es = p.exprList("]")
		} else if p.is(",") {
			p.advance()
		}
		p.expect("]", "to close the list")
		s := p.spanFrom(a)
		return p.app(s, "List", es), s
	case isTok(t, "if"):
		p.advance()
		p.expect("(", "after 'if'")
		c, _ := p.expr(0)
		p.expect(")", "to close the condition")
		th, _ := p.expr(0)
		p.expect("else", "after the 'if' branch")
		el, _ := p.expr(precIfElse)
		s := p.spanFrom(a)
		return p.app(s, "ite", []*ir.Expr{c, th, el}), s
	case isTok(t, "match") && startsExpr(p.peek(1)):
		return p.match()
	case isTok(t, "all") || isTok(t, "any") || ((isTok(t, "and") || isTok(t, "or")) && isTok(p.peek(1), "{")):
		// all and any start nothing else: a missing '{' is the error
		p.advance()
		p.expect("{", "after '"+t.Text+"'")
		es := p.exprList("}")
		p.expect("}", "to close the list")
		s := p.spanFrom(a)
		op := map[string]string{"all": "actionAll", "any": "actionAny", "and": "and", "or": "or"}[t.Text]
		return p.app(s, op, es), s
	case isTok(t, "-"):
		p.advance()
		x, _ := p.expr(precUminus)
		s := p.spanFrom(a)
		return p.app(s, "iuminus", []*ir.Expr{x}), s
	case t.Kind == TInt:
		p.advance()
		v, ok := new(big.Int).SetString(strings.ReplaceAll(t.Text, "_", ""), 0)
		if !ok {
			p.p = a
			p.failf("not an integer")
		}
		s := p.spanFrom(a)
		return &ir.Expr{ID: p.id(s), Kind: ir.ExInt, Int: v}, s
	case t.Kind == TBool:
		p.advance()
		s := p.spanFrom(a)
		return &ir.Expr{ID: p.id(s), Kind: ir.ExBool, Bool: t.Text == "true"}, s
	case t.Kind == TString:
		p.advance()
		s := p.spanFrom(a)
		return &ir.Expr{ID: p.id(s), Kind: ir.ExStr, Str: t.Text[1 : len(t.Text)-1]}, s
	case t.Kind == TKeyword && slices.Contains([]string{"and", "or", "iff", "implies", "leadsTo", "Set", "List"}, t.Text) && isTok(p.peek(1), "("):
		var e *ir.Expr
		var s span
		if p.tryStop(func() { p.advance(); e, s = p.operApp(a, t.Text) }) == nil {
			return e, s
		}
		// a call that fails: ANTLR may read the name alone (below)
		fallthrough
	case isIdentTok(t):
		name, _ := p.qualID()
		if p.is("(") {
			var e *ir.Expr
			var s span
			if p.continues(func() { e, s = p.operApp(a, name) }) {
				return e, s
			}
		}
		switch {
		case p.is("'"):
			// asgn : qualId '\'' ASGN expr
			p.advance()
			p.expect("=", "after x' (x' = e)")
			rhs, _ := p.expr(precAsgn)
			s := p.spanFrom(a)
			lhs := &ir.Expr{ID: p.id(s), Kind: ir.ExName, Name: name}
			return p.app(s, "assign", []*ir.Expr{lhs, rhs}), s
		}
		s := p.spanFrom(a)
		return &ir.Expr{ID: p.id(s), Kind: ir.ExName, Name: name}, s
	}
	p.failf("expected an expression")
	return nil, span{}
}

// operApp : normalCallName '(' argList? ','? ')'
func (p *parser) operApp(a int, name string) (*ir.Expr, span) {
	p.advance() // (
	var args []*ir.Expr
	if !p.is(")") {
		args = p.argList()
		if p.is(",") {
			p.advance()
		}
	}
	p.expect(")", "to close the arguments")
	s := p.spanFrom(a)
	return p.app(s, name, args), s
}

// recordAhead says whether the '{' at the cursor starts a record: a
// spread, or a field's name (perhaps qualified) and ':'.
func (p *parser) recordAhead() bool {
	if isTok(p.peek(1), "...") {
		return true
	}
	j := p.identOrHoleAt(p.p + 1)
	return j > 0 && !isTok(p.look(p.p+1), "_") && isTok(p.look(j), ":")
}

// record : '{' recElem (',' recElem)* ','? '}'
func (p *parser) record() (*ir.Expr, span) {
	a := p.advance()
	type elem struct {
		name   *ir.Expr
		e      *ir.Expr
		spread bool
	}
	var elems []elem
	for {
		ea := p.p
		if p.is("...") {
			p.advance()
			e, _ := p.expr(0)
			elems = append(elems, elem{e: e, spread: true})
		} else {
			n, _ := p.simpleID("record")
			p.expect(":", "after the field's name")
			e, _ := p.expr(0)
			elems = append(elems, elem{name: &ir.Expr{ID: p.id(p.spanFrom(ea)), Kind: ir.ExStr, Str: n}, e: e})
		}
		if !p.is(",") {
			break
		}
		p.advance()
		if p.is("}") {
			break
		}
	}
	p.expect("}", "to close the record")
	s := p.spanFrom(a)
	var spreads []*ir.Expr
	var pairs [][2]*ir.Expr
	seen := map[string]bool{}
	var dups []string
	for _, el := range elems {
		if el.spread {
			spreads = append(spreads, el.e)
			continue
		}
		pairs = append(pairs, [2]*ir.Expr{el.name, el.e})
		if seen[el.name.Str] && !slices.Contains(dups, el.name.Str) {
			dups = append(dups, el.name.Str)
		}
		seen[el.name.Str] = true
	}
	if len(dups) > 0 {
		id := p.id(s)
		plural := "Field"
		if len(dups) > 1 {
			plural = "Fields"
		}
		p.listenerErr("QNT016", fmt.Sprintf("%s %s cannot be defined more than once", plural, strings.Join(dups, ", ")), id)
	}
	switch {
	case len(spreads) == 0:
		var args []*ir.Expr
		for _, pr := range pairs {
			args = append(args, pr[0], pr[1])
		}
		return p.app(s, "Rec", args), s
	case len(spreads) > 1:
		id := p.id(s)
		p.listenerErr("QNT012", "... may be used once in { ...record, <fields> }", id)
		// quint's listener pushes no expression here: the definition it
		// is the body of fills the hole (undefinedExpr); elsewhere the
		// listener's stacks go astray, which P1 does not follow
		return &ir.Expr{ID: id, Kind: exMissing}, s
	}
	rec := spreads[0]
	for _, pr := range pairs {
		rec = &ir.Expr{ID: p.id(s), Kind: ir.ExApp, Opcode: "with", Args: []*ir.Expr{rec, pr[0], pr[1]}}
	}
	return rec, s
}

// matchSumExpr : MATCH expr '{' '|'? matchSumCase ('|' matchSumCase)* '}'
func (p *parser) match() (*ir.Expr, span) {
	a := p.advance()
	e, _ := p.expr(0)
	p.expect("{", "after the matched expression")
	if p.is("|") {
		p.advance()
	}
	type mcase struct {
		label, param string
		body         *ir.Expr
		s            span
	}
	var cases []mcase
	for {
		ca := p.p
		c := mcase{label: "_", param: "_"}
		if p.is("_") {
			p.advance()
		} else {
			c.label, _ = p.simpleID("variant label")
			if p.is("(") {
				p.advance()
				if p.is("_") {
					p.advance()
				} else {
					c.param, _ = p.simpleID("match case parameter")
				}
				p.expect(")", "to close the case's parameter")
			}
		}
		p.expect("=>", "after the case's pattern")
		c.body, _ = p.expr(0)
		c.s = p.spanFrom(ca)
		cases = append(cases, c)
		if !p.is("|") {
			break
		}
		p.advance()
	}
	p.expect("}", "to close the match")
	s := p.spanFrom(a)
	id := p.id(s)
	args := []*ir.Expr{e}
	for _, c := range cases {
		label := &ir.Expr{ID: p.id(c.s), Kind: ir.ExStr, Str: c.label}
		elimID := p.id(c.s)
		prm := &ir.Param{ID: p.id(c.s), Name: c.param}
		args = append(args, label, &ir.Expr{ID: elimID, Kind: ir.ExLambda, Qualifier: "def", Body: c.body, Params: []*ir.Param{prm}})
	}
	return &ir.Expr{ID: id, Kind: ir.ExApp, Opcode: "matchVariant", Args: args}, s
}
