package qoracle

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// FrontEndCodes are the QNT codes of quint's front end (§1.3's phases, and
// the parse-phase codes of the grammar and parseErrors.js): what the
// ill-typed generator must reach, quint judging (exit criterion 4).
// QNT000 is a syntax, type or effect error; the others one each. The
// codes 500 and up are the runtime's, out of the front end's scope. Read-only.
var FrontEndCodes = []string{
	"QNT000", "QNT006", "QNT007", "QNT008", "QNT009", "QNT012", "QNT013", "QNT014", "QNT015", "QNT016",
	"QNT098", "QNT099", "QNT101", "QNT102",
	"QNT200", "QNT201", "QNT202", "QNT203", "QNT204", "QNT205", "QNT206",
	"QNT404", "QNT405", "QNT406", "QNT407", "QNT408",
}

// A mutation makes a well-typed program ill: tag is the code it means to
// reach (a hint: quint's answer is the label).
type mutation struct {
	name, tag string
	apply     func(p *GenProgram, r *rand.Rand)
}

// mainDecl adds a declaration to the main module.
func mainDecl(p *GenProgram, head string) {
	m := p.Files[p.Main].Modules[0]
	m.Decls = append(m.Decls, &GenDecl{Head: head})
}

// edit replaces the first old in a file's text with new (the file then
// written raw).
func edit(p *GenProgram, file, old, new string) {
	f := p.Files[file]
	t := f.Text()
	if strings.Contains(t, old) {
		f.Raw = strings.Replace(t, old, new, 1)
	}
}

// mutations are the catalog: each front-end code, by the construct quint
// reports it for (its sources: parseErrors.js, the grammar's actions,
// quintParserFrontend.js, the collector, resolver and import errors, the
// effect, mode and nondet checkers). Read-only.
var mutations = []mutation{
	// syntax (ANTLR's errors are QNT000): a token class missing or extra
	{"missing-brace", "QNT000", func(p *GenProgram, r *rand.Rand) { edit(p, p.Main, "action step = any {", "action step = any ") }},
	{"extra-token", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val extra = 1 1") }},
	{"bad-operator", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val op = 1 +* 2") }},
	{"unclosed-string", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, `val s = "open`) }},
	// type errors: QNT000 at the node the solver blames
	{"operand-swapped", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val bad = x + y") }},
	{"wrong-arity", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val bad = swap((1, 2), 3)") }},
	{"bad-field", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val bad = upd({ f: 1, g: true }).h") }},
	{"match-case", "QNT000", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, "val bad = match SA(1) { | SA(i) => i | SB(b) => b | SC => 0 }")
	}},
	// effects: branches updating different variables (lint/model.go's case)
	{"effect-branches", "QNT000", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "action eb = if (y) x' = 1 else y' = false") }},
	// the grammar's actions
	{"assign-equals", "QNT006", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val eq = if (x = 1) true else false") }},
	{"lowercase-type", "QNT007", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "type lower = int") }},
	{"keyword-as-name", "QNT008", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val match = 1") }},
	{"paren-type-app", "QNT009", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "var l: List(int)") }},
	{"two-spreads", "QNT012", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, "val two = { ...{ f: 1, g: true }, ...{ f: 2, g: false }, f: 3 }")
	}},
	{"unbound-type-param", "QNT014", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "type T = List[a]") }},
	{"map-type", "QNT015", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "var mm: Map[int, int]") }},
	{"duplicate-field", "QNT016", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val dup = { f: 1, f: 2 }") }},
	// sources
	{"missing-file", "QNT013", func(p *GenProgram, r *rand.Rand) { mainDecl(p, `import nowhere.* from "./nowhere"`) }},
	{"import-cycle", "QNT098", func(p *GenProgram, r *rand.Rand) {
		p.Files["cyc.qnt"] = &GenFile{Raw: "module cyc {\n  import main.* from \"./main\"\n  val c = 1\n}\n"}
		mainDecl(p, `import cyc.* from "./cyc"`)
	}},
	{"case-clash", "QNT408", func(p *GenProgram, r *rand.Rand) {
		p.Files["Twin.qnt"] = &GenFile{Raw: "module Twin {\n  val t = 1\n}\n"}
		p.Files["twin.qnt"] = &GenFile{Raw: "module twin {\n  val t = 2\n}\n"}
		mainDecl(p, `import Twin.* from "./Twin"`)
		mainDecl(p, `import twin.* from "./twin"`)
	}},
	// names
	{"definition-cycle", "QNT099", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, "pure def cf(n: int): int = cg(n)")
		mainDecl(p, "pure def cg(n: int): int = cf(n)")
	}},
	{"builtin-redefined", "QNT101", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "pure def filter(n: int): int = n") }},
	{"duplicate-definition", "QNT101", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "var x: int") }},
	{"duplicate-module", "QNT102", func(p *GenProgram, r *rand.Rand) {
		f := p.Files[p.Main]
		f.Raw = f.Text() + "\nmodule main {\n  val again = 1\n}\n"
	}},
	{"unknown-name", "QNT404", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val bad = x + undefinedName") }},
	{"misspelled-name", "QNT404", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val bad = pikc(true, 1, 2)") }},
	{"unknown-module", "QNT405", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "import nomodule.*") }},
	{"missing-constant", "QNT406", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, `import lib(C = 1, D = 2) as L2 from "./lib"`)
	}},
	{"self-import", "QNT407", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "import main.*") }},
	// modes and effects
	{"assign-in-val", "QNT200", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "val av = x' = 1") }},
	{"impure-override", "QNT201", func(p *GenProgram, r *rand.Rand) { mainDecl(p, `import lib(C = x) as L3 from "./lib"`) }},
	{"double-update", "QNT202", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "action du = all { x' = 1, x' = 2, y' = true }") }},
	{"oneof-outside-nondet", "QNT203", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "action oo = x' = Set(1, 2).oneOf()") }},
	{"nondet-not-oneof", "QNT204", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, "action nn = { nondet k = 1 + 1\n    x' = k }")
	}},
	{"nondet-body-not-bool", "QNT205", func(p *GenProgram, r *rand.Rand) {
		mainDecl(p, "action nb = { nondet k = Set(1, 2).oneOf()\n    k }")
	}},
	{"nondet-top-level", "QNT206", func(p *GenProgram, r *rand.Rand) { mainDecl(p, "nondet top = Set(1, 2).oneOf()") }},
}

// Ill is the i-th ill program from seed: a generated program (Module)
// with mutation i % len(mutations) applied.
func Ill(seed uint64, i int) *GenProgram {
	p := Module(seed)
	m := mutations[i%len(mutations)]
	m.apply(p, rand.New(rand.NewPCG(seed, uint64(i))))
	p.Tag, p.Mutation = m.tag, m.name
	return p
}

// Mutations is the number of mutations in the catalog.
func Mutations() int { return len(mutations) }

// String names a generated program.
func (p *GenProgram) String() string {
	if p.Mutation != "" {
		return fmt.Sprintf("seed %d, %s (meant for %s)", p.Seed, p.Mutation, p.Tag)
	}
	return fmt.Sprintf("seed %d", p.Seed)
}
