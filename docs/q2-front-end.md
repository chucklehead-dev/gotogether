# Q2: a Go front end for Quint

Status: design and plan, 2026-11-25. Q3 (the oracle, §4) is built (§4.5); of the front end, P1's parser is built (§5.1), started before Q3's week of green nightlies by the maintainer's decision. It refines
ROADMAP's Q2 ("the front end (L)") and Q3 ("the oracle"), and ROADMAP says
that each is its own decision before it starts. It does not cover Q0's
remainder (the examples' compiled models) or Q1 (qeval's refusals and
`quint run`'s simulation in Go), which other work owns.

The goal is gotogether standalone. After Q0 and Q1 a consumer needs
quint only to write or change a model: to typecheck it and compile it to
the IR the Go evaluator reads. Q2 removes that last need, and quint stays
as the oracle that the Go front end is checked against.

Sources:
- the code in this repository at `origin/main` (paths below);
- quint 0.33.1 as installed by npm (`dist/src/**`, the compiled
  TypeScript);
- quint's repository at tag v0.33.1, a shallow clone. Paths given as
  `quint/...` are relative to it: `quint/src` (with
  `quint/src/generated/Quint.g4`), `quint/testFixture`, `quint/test`,
  `quint/integration-tests`, `examples` and the Rust `evaluator`.

Counts and timings were taken on this machine. Claims that remain
unverified are marked.

The maintainer's decisions of 2026-11-25 are in §6 under "Decided", and
the sections below follow them.

## 1. Scope: what gotogether asks of quint today

### 1.1 Every invocation

Every call goes through `internal/quintcmd.Command`. The table lists each
call site and says which item would replace it.

| site | subcommand | what gotogether reads | replaced by |
|---|---|---|---|
| binding/model.go:322 (`LoadModel`) | `typecheck --out` | the unflattened modules and the types table → `binding.Model` (actions, params and their types, boolean vals, vars, consts, typedefs, exports, doc comments) | **Q2** |
| validate/modelcache.go:85 | `typecheck --out`, cached by content (DESIGN §57) | as above, through `binding.ParseTypecheck` | **Q2** |
| validate/compiled.go:109, :148 (`Compile`) | `typecheck --out`, then `compile --target json --main --init --step` of the evaluation module | `CompiledModel.Typecheck` and `.IR` (`gotogether.model/1`) | **Q2** |
| validate/eval.go:234 | `compile --target json` of `binding.EvalModule`'s text | IR → `qeval.Load` | **Q2** |
| connect/require.go:93 | `compile --target json --invariant` | IR → `qeval.Load`; a missing invariant read from stderr by the QNT404 regex (`quintNotFound`) | **Q2** |
| lint/model.go:190 | `typecheck` (text) | the QNT000 effect-unification message, parsed by the regex `unification` (its wording) | **Q2** (P4) |
| internal/typegen/typegen.go:95 | `typecheck --out` | modules and types; `errors[].explanation` and `locs[].start` for the message | **Q2** |
| validate/model/simulate.go:604 | `typecheck` of the generated simulate module | pass or fail | **Q2** |
| connect/run.go:194; generateTest | `run --mbt`, `test --out-itf` | ITF traces | Q1 (simulation), the evaluator |
| validate/validate.go:521; validate/model/compare_replay.go:134 | `test` of internal/replay's modules | QNT508, QNT511, QNT513 and ITF | the evaluator in process (ROADMAP "What it removes"), not Q2 |
| validate/model/simulate.go:665; simulate_possible.go:126, :176, :251; simulate_graph.go:556 | `run` | traces, witness counts | Q1 |
| validate/model/native_verify.go:68 | `verify` (Apalache) | Apalache's result and trace | stays external |
| validate/manifest.go:915; cmd/gotogether/doctor.go:337 | `--version` | provenance and admission (`quintcmd.MinimumVersion` = 0.33.1) | records the language version the front end implements |

Q2 therefore replaces `typecheck` and `compile`. That is eight call
sites in six packages (binding, validate, validate/model, connect, lint,
internal/typegen). It does not replace `test`, `run` or `verify`. gotogether also writes
Quint text of its own, and it stays: internal/replay's modules (for
`quint test`, which becomes the oracle's path once replays run in the
evaluator) and `binding.EvalModule`'s text (expects, wrappers, `__gtg_`
names), which is what `compile` compiles. So the Go front end must
compile gotogether's generated Quint as well as the user's model.

**An option without Node.** The Rust evaluator has no front end. Its
own docs say it relies "on the Quint typescript tool for parsing, name
resolution and type/effect checking" (`evaluator/src/helpers.rs`).
quint 0.33.1 sends it JSON on stdin (`dist/src/rust/commandWrapper.js`;
`evaluator/src/main.rs`):
- `SimulateInput` carries `parsed`, which holds init, step, invariants,
  witnesses and the lookup table, plus the run's settings;
- `TestInput` carries a test's definition and the table.

What it reads is therefore expressions and the **table of whole
definitions** (`LookupDefinition`, `evaluator/src/ir.rs`). It reads no
`types` and no `effects`. Its development path compiles with
`--flatten false` (`helpers.rs`), so the Rust evaluator does not need
flattened modules either.

A Go front end could therefore drive the Rust evaluator with no Node at
all. The protocol is internal to quint (its main.rs documents it as "the
integration with the `quint` typescript tool") and is not a published
contract. It is an option, not a dependency.

### 1.2 The two outputs, and what is read of them

The Go front end must produce two different JSON documents. They are
not one document at two stages.

**`quint typecheck --out`** is the unflattened modules. Its declarations
come in the order of phase 4 of quint's parser (`parsePhase4toposort`),
with aliases kept by name. It also holds `table`, `types` and `effects`.
These are the fields gotogether reads:
- Modules: `name` and `declarations`.
- Each declaration (`binding/model.go` `tcDecl`; `internal/typegen`
  `tcDecl`): `id`, `kind`, `name`, `qualifier`, `doc` (for `///
  gotogether: diagnostic`, binding/diagnostic.go), `expr` (whole, as raw
  JSON, walked for assignments, calls, reads, choices and antecedents),
  `typeAnnotation`, `type` and `params` of a typedef, and `protoName`,
  `defName` and `qualifiedName` of an import or export.
- The lambda params' `name` and `type`.
- `types[id].type`. binding reads it by the declaration's id for vals'
  bool and inferred operator types (`oper` args).
- `errors[]`: `explanation`, and `locs[].source` and `locs[].start` with
  0-based line and col (typegen).

These fields are not read: `effects` (21.6 KB of hello's 43 KB
typecheck), `table` (binding resolves names itself), `types[].typeVariables`
and `rowVariables`, `warnings`, and `stage`.

**`quint compile --target json`** is one flattened main module (DESIGN §45). It
includes the `q::init`, `q::step` and `q::inv` definitions that
`compile` adds. Aliases are inlined (`aliasInliner` runs in
`fullFlattener`). Lambda parameters and let-names are renamed `name_<id>`
by `unshadower`, and an imported definition appears only if something
references it (`flattener`). `qeval.Load` (internal/qeval/ir.go) reads:
- `modules[main].declarations` of kind var, const and def (types,
  imports and assumptions are skipped).
- `Decl`: `id`, `kind`, `name`, `qualifier`, `expr`.
- `Expr`: `id`, `kind`, `name`, `opcode`, `args`, `value`, `params`
  (`id`, `name`), `qualifier`, `expr`, `opdef`.
- `table[id]`: `id`, `kind`, `name` only. The table entry's own copy of
  the definition, with `depth` and `hidden`, is ignored.
- `types` read lazily by `TypeOf`/`Params`/`Nondets` (internal/qeval/types.go):
  `id`, `kind`, `name`, `elem`, `arg`, `res`, `args`, and
  `fields` as rows (`row`/`empty`/a row variable).
- `main` and `errors`. A non-empty `errors` refuses the IR.

`effects` is not read. It is 59 KB of hello's 120 KB IR
(examples/hello/hello.model.json), and no Go package reads an `effects`
key. The table's definition copies, `depth`, `hidden`, the types' variable
lists and `warnings` are not read by qeval either. Q2 aims first at this
**consumed subset**.

The Rust evaluator does not read `effects` either (§1.1), so under the
maintainer's decision 5 the Go front end never writes them. It does read
the table's definition copies, so those are written only if the Rust
stdin path is taken up.

Two consequences:
- **Ids leak into names.** The compile IR's names carry ids (`p_<id>`,
  `__gtg_h0_10`), and qeval's printer undoes them (internal/qeval/print.go,
  `unsuffixed`). Type variables carry generated names (`_t5`). A
  comparison by canonical renumbering must therefore rewrite names as
  well as ids (§4.2).
- **`CompiledModel.Differs` compares bytes, not meaning.** It compares
  `Typecheck` and `IR` with `sameJSON`, that is, compacted bytes
  (validate/compiled.go:230). ROADMAP's "by meaning" holds for the
  versions that compiled the model, which are provenance, and does not
  hold for the IR. A model compiled by Go and the same model compiled by
  quint would differ in every id. Before a Go-compiled artifact and a
  quint-compiled one can stand for each other (`compile -check` in CI
  where quint is installed), `Differs` must compare canonical forms.
  `LoadCompiled` also refuses an artifact whose `Quint` is not a
  supported quint version, so a Go-compiled artifact needs a recorded
  provenance (§3.2).

### 1.3 What quint's `compile` does that a port must reproduce

From `dist/src/cliCommands.js` (`compile`, `typecheck`) and
`quintAnalyzer.js`, quint runs these phases:
1. **parse** (ANTLR, `ToIrListener`).
2. **source resolution** (imported files; QNT013, QNT408, QNT098 cyclic
   imports).
3. **import and name resolution**: collector QNT101 and QNT102, resolver
   QNT404, import errors QNT405 to QNT407.
4. **toposort**: QNT099, cyclic definitions.
5. **analysis**: type-application resolution, type inference
   (constraint generation and solving), effect inference, the
   multiple-updates checker (QNT202), the nondet checker (QNT203 to
   QNT206) and the mode checker (QNT200, QNT201). Every type, effect and
   type-application error is **QNT000**, with an error tree and a
   reference id.
6. **compile**: the `q::*` definitions are parsed and analysed
   incrementally (`analyzeInc`), names are resolved again, then
   `flattenModules`: unshadow, inline aliases, flatten exports and
   imports, copy instances with fresh ids (`instanceFlattener`, which
   also copies the analysis for them), and resolve again.

quint's front end is about 6,800 non-comment lines of compiled JS:
parsing 1,488 (with `ToIrListener`), names 539, flattening 240, types
1,353, effects 1,717 (with an `Effect.g4` grammar for the builtins'
effect signatures) and ir 1,441 (visitors and printing). The generated
ANTLR parser adds another 8,466. A Go port is plausibly 8 to 12 thousand
lines before tests. That is an estimate, not a measurement.

## 2. The language to parse and check

### 2.1 Grammar

`Quint.g4` at v0.33.1 has 380 lines. It is LL with ANTLR's left-recursion
and alternative order setting precedence, and its notable parts are:
- **Modules.** `module qualId { documentedDeclaration* }` with `///`
  doc comments. On main the body is a `moduleItem*` and doc comments
  attach in `ToIrListener`. The grammar has already changed after the
  version we pin.
- **Declarations.** `const`, `var`, `assume`, instance, operator
  definition, typedef, import and export.
- **Operator definitions.** The annotated form, and the "deprecated"
  form with unannotated params (quint issue #923). The qualifiers are
  `val`, `def`, `pure val`, `pure def`, `action`, `run`, `temporal` and
  `nondet`.
- **Destructuring.** `val (a, b) = e` and `val { f } = e`.
- **Imports.** `import M.x|* (from "f")`, `import M (as N) (from "f")`,
  instances `import M(c = e, ...).*` and `import M(c = e) as I`.
  Exports: `export M.x|*`, `export M (as N)`.
- **Types.** Functions `->`, operators `=>`, `Set[...]`, `List[...]`,
  unit, tuples, records with row variables `{ f: T | r }`, int, str,
  bool, type variables, constants, type applications `T[a, b]`, and sum
  type definitions `| A(T) | B`.
- **Expressions.** In priority order: dot calls, lambdas (including the
  tuple sugar `((a, b)) => e`), applications, list index `e[i]`, `^`
  (right-associative), unary minus, `* / %`, `+ -`, relations,
  `x' = e`, `and {}`/`and`, `or {}`/`or`, `iff`, `leadsTo`, `implies`,
  `match`, `all {}`, `any {}`, literals and names (with `0x` and `_`
  separators), tuples, unit, `->` pairs, records with `...` spread,
  lists, `if/else`, let (`operDef expr`), parentheses and braces.
- **Semantic actions in JavaScript** that report errors, which any port
  must carry over: QNT006 (`=` for `==`), QNT008 (a qualified name where
  a simple one is required, or a reserved word as an identifier) and
  QNT009 (parentheses for a type application). `parseErrors.js` adds
  QNT007, QNT012, QNT014, QNT015 and QNT016.
- **Keywords as identifiers.** `from`, `as`, `Set` and `List` may be
  identifiers. `and`, `or`, `iff`, `implies`, `leadsTo`, `match`,
  `import` and `export` are reserved.

`internal/quintnames` already holds the reserved names. Its `Builtins`
and `Keywords` are tested against quint's own `names/base.js` and
`QuintLexer.js`, so the Go lexer takes its keyword table from there.

The repository itself uses less than all of this. Its 180 `.qnt` files
(about 15,100 lines; 45 files import from another file) use imports,
instances (15 files), `export` (28 files), polymorphic type aliases (7),
sum types, `match`, record spread, `run`s with `.then`, and
`always`/`eventually` in vals. None of them uses `temporal`
declarations, `assume`, destructuring, `apalache::` or hex literals
(checked with grep). That is the first subset.

By the maintainer's decision 4 (revised 2026-11-25), destructuring,
`temporal` and `assume` are in the front end from P1/P2, and only
`apalache::` is refused at first, as `qeval.Unsupported` is ("needs
quint"), rather than guessed. quint's own corpus uses them far more than
this repository does. Counted by grep over the 77 examples with comments
stripped, destructuring matching `val (a, b) = e` and `((a, b)) => e`:
33 use at least one; destructuring 18 (13 use nothing else of the four),
`temporal` 13 (9), `assume` 8 (3), `apalache::` 1 (1). Destructuring alone
releases 13 examples, with `temporal` 24, with `assume` too 32; only the
one `apalache::` example is left. 6 of the 73 fixtures use at least one.

- **Destructuring** is sugar over tuples and records: resolved and
  flattened into field reads (as quint's IR writes them), so the type
  checker and qeval gain nothing new.
- **`temporal`** definitions are parsed, typed and effect-checked, and
  passed through. Judging one is a liveness question (Apalache, or the
  graph's lassos), not a replay: a check that would evaluate a temporal
  operator refuses there, naming it, while the rest of the model is used.
- **`assume`** is a named boolean over constants: typed, and evaluated
  once when an instance's constants are bound; a false one is reported
  with its name and position.

`apalache::` files enter Q3 as expected refusals, not as accepted
programs.

### 2.2 Types and effects

The types are those listed in §2.1, with these rules:
- polymorphic operators, with let-polymorphism over type variables;
- rows for records and sums, which may be open (`Type.Open` in qeval);
- aliases with parameters, resolved by `TypeApplicationResolver`;
- builtin signatures from `types/builtinSignatures.js` (177 lines),
  written as type strings that `types/parser.js` parses.

Effects come from `effects/`: read, update and temporal components over
entity variables, with builtin effect signatures in `Effect.g4` form
(`effects/builtinSignatures.js`, 344 lines). Modes (pure, val, def,
action, temporal, run) are checked against the inferred effects by
`modeChecker.js`. That checker reports QNT200 and QNT201 and suggests
the right qualifier.

### 2.3 The error codes gotogether relies on

These are the codes this repository reads or names:
- **QNT101**: a builtin redefined, or modules conflicting.
  `internal/quintnames`, lint, grist's generator and the skills all name
  it.
- **QNT008**: a keyword used as a name.
- **QNT404**: name not found (connect/require.go regex).
- **QNT000**: effect unification "Expected [a] and [a,b] to be the
  same" (lint/model.go regex), and every type error.
- **QNT200**: lint names it for `x' = a or b`.
- **QNT500**: an uninitialized constant (the skills' table).

QNT508, QNT510, QNT511, QNT513 and QNT600 are the runtime's or the Rust
evaluator's, not the front end's, and are out of Q2's scope.

**What "parity" can mean here.** quint's type and effect errors all
carry QNT000. Their codes therefore distinguish little, and their
position is the source-map span of the node the solver blames (the
`reference` id). For example, `v + 1 == true` reports `[QNT000]
Couldn't unify int and bool` at 2:13 to 2:25, the whole equality,
0-based. So the bar is the code, the file and the start and end
positions, and not the wording. The two regexes that read wording today
(lint's unification and connect's QNT404) become structured fields of
the Go front end's errors, so nothing depends on wording afterwards.

## 3. Options and recommendation

### 3.1 The parser: by hand

**Generate from the ANTLR grammar** (antlr4-go/antlr/v4's Go target)
or **write it by hand** (a lexer, recursive descent for declarations
and types, and precedence climbing for expressions ordered as Quint.g4
orders its alternatives).

**Recommendation: by hand.** The reasons:
- The core may not take heavy dependencies (AGENTS.md: OTel, yaml.v3,
  x/sys, x/mod), and the ANTLR runtime would be a new one in every
  consumer's module graph.
- The grammar's semantic actions are JavaScript (QNT006, QNT008 and
  QNT009) and need porting either way.
- `ToIrListener` (1,404 lines) is the larger part of the parsing work
  and is hand-written in either case.
- A hand-written parser controls its own error recovery and positions,
  which the source map and lint need.

The cost is that ANTLR's generic syntax errors ("mismatched input ...
expecting ...") will not match word for word. They are QNT000 in quint,
and Q3 compares them by position of the offending token only.
Ambiguities that ANTLR settles by alternative order need explicit
lookahead in a hand-written parser: `{ e }` braces against a record,
lambda against paren or tuple, let as `operDef expr`, and
`qualId ' = e`. Each is a fixture in Q3's parse corpus. The grammar
stays the specification: the parser's doc comment cites Quint.g4's tag,
and a test can check that every labelled alternative (`# dotCall`,
`# letIn`, and the rest) has a corpus entry that exercises it.

### 3.2 Structure: quint's phases, ported one for one

**Recommendation: port quint's phases as they are**, as separate
packages, so that the oracle can compare each phase on its own:

```text
internal/qfront/syntax     lexer, parser, AST with positions, source map
internal/qfront/names      source resolution, collector, resolver, toposort, unshadower
internal/qfront/types      type-application resolution, constraint generation, solver
internal/qfront/effects    effect inference, mode, nondet and multiple-updates checks
internal/qfront/flatten    exports, imports, instances, aliases inlined
internal/qfront            Typecheck(spec) and Compile(spec, main, init, step), writing quint's JSON
```

These sit in the "values" layer, below binding (ARCHITECTURE §2), and
import nothing of the module but internal/quintnames and
internal/formats. They stay internal for now (decision 6): published
API is a promise (api/core.txt).

Quint offers three observable points, and porting phase for phase gives
one checkpoint at each:
- `quint parse --out --source-map` writes the parsed modules, the table
  and the source map (sourceIndex, then id → [file, start, end]);
- `quint typecheck --out`;
- `quint compile --target json`.

Folding phases together (typecheck the flattened module, say) would be
simpler. It would differ where quint is subtle: an instance's copies
take the analysis of the original, and the blame position comes from
the solver's order.

**Output.** Write quint's JSON, byte-shaped as quint writes it, and pass
it through the existing readers (`binding.ParseTypecheck`, `qeval.Load`)
first. Every consumer, the cache (DESIGN §57) and the compiled artifact then
stay unchanged, and the oracle compares JSON. An in-memory handoff (an
IR struct straight to qeval and binding) is a later optimization. Keep
it out of the first pass.

**Provenance.** A Go-compiled `gotogether.model/1` keeps `Quint` as the
quint version whose language the front end implements and was certified
against by Q3 (0.33.1), so older readers' admission check
(`QuintSupported`) still holds. It adds an optional `compiler: "go"` (or
`"quint"`) field, which needs no new major version under the formats
rules (DESIGN §63). A `gotogether.model/2` is acceptable instead if it
is cleaner (decision 8).

### 3.3 How close the IR must be

There are three possible bars:
1. **Byte-identical.** This needs quint's id generator order and every
   incidental field. It is the costliest and buys nothing over (2).
2. **Equal after canonical renumbering**, over the consumed subset
   first (§1.2) and over everything written later. Ids are renumbered
   in a canonical walk, and so are names derived from ids and type
   variable names (§4.2).
3. **Equal by meaning.** qeval gives the same values, successors and
   types on the corpus.

**Recommendation: (2) is the gate, and (3) is the backstop.** (2) is
mechanical and leaves a localised diff to shrink when it fails, so it
catches a wrong resolution even when it evaluates the same on the
corpus. (3) already exists: the `qeval-differential` and
`evaluator-shadow` nightlies and `TestAgreesWithQuint`. Pointed at
Go-compiled IR, it catches what (2) would accept and the evaluator would
see. Declaration order is part of (2): binding's `Vars` follow the order
of the typecheck output, so a different toposort would show up in
scaffold output.

### 3.4 Error parity

- Every error carries a code and a position.
- On the ill-typed corpus, the first error's code, file and start and
  end positions must equal quint's.
- The set of all errors must equal quint's as a set of (code, position)
  pairs, if Q3 shows that to be reachable. Otherwise the set must
  contain the first error, and the document then records why.
- The wording is our own. Messages follow AGENTS.md's rule (say what to
  do next) and are free to read better than quint's error trees.
- A program quint accepts must be accepted, and one quint refuses must
  be refused. Of the two directions, **accepting what quint refuses is
  the dangerous one**: it is a silent wrong answer (a model with a type
  quint would not infer, whose values the binding then converts
  otherwise). The ill-typed generator exists for that direction.

### 3.5 Performance targets

Measured on this machine with quint 0.33.1 (wall time of `quint
typecheck`):

| model | size | time |
|---|---|---|
| examples/hello | 1 file | 0.48 s, mostly Node starting and loading quint |
| examples/inventory-grist/model/checkout.qnt | 6 files, 1,101 lines | 2.2 s |
| examples/library | 1,110 lines | 2.4 s |

ROADMAP Q9 reports 7 to 40 s for grist's generator in a quint process.
That figure has not been re-measured here.

Targets:
- parse and check of the repository's largest model under 50 ms in
  process (more than 40 times faster);
- time linear in the model's size on the corpus, with no quadratic
  table scans (quint's `Flattener.enterExport` filters the whole lookup
  table per export);
- a typecheck cheap enough that `binding.LoadModel` needs no
  cross-process cache, so the cache (DESIGN §57) becomes a quint-only path.

A benchmark over the corpus sits beside qeval's `bench_test.go`.

### 3.6 Upstream: not contacted

By the maintainer's decision 1, gotogether proceeds on its own. Neither
Q3 nor Q2 waits for Informal Systems, and no issue is drafted.

The license allows this: quint is Apache 2.0 (`LICENSE`, package.json).
Fixtures are fetched at the pinned tag, not vendored (decision 3), so
nothing of quint's is redistributed.

The Rust evaluator has no parser, so "the Rust evaluator grows a front
end" would be a new Rust component. It would not serve `go install`
users unless it shipped as a binary we fetch, which is what Q11
removes. A Go library serves gotogether whatever upstream does.

The questions below stay **open and are not being asked**. Each is
answered for now by its default: quint's behaviour at the pinned tag is
the contract, measured by Q3. They are kept here for the maintainer to
reconsider.
1. Is a front end outside Node planned, in Rust or otherwise? If so,
   when, and would its IR be the same JSON? The evaluator's README lists
   "Requirements before deprecating the Typescript Evaluator" but says
   nothing of the front end.
2. Is the JSON IR of `parse --out`, `typecheck --out` and `compile
   --target json` a contract? Is there a schema or a version field?
   Which fields are incidental, such as `depth`, `hidden` and the
   table's copies?
3. Could `quint/testFixture` be published as a versioned conformance
   suite of inputs, expected IR, and expected errors with positions?
   Today it holds parse outputs only (§4.1).
4. Is the node a type or effect error blames (the `reference`) stable
   across releases, or an artifact of the solver's order? Would distinct
   codes for type errors, instead of QNT000, be welcome?
5. Is Quint.g4 the normative syntax? What changes are planned, such as
   removing `deprecatedOperDef` (#923) and doc comments as module items
   (on main since v0.33.1)?
6. Is the Rust evaluator's stdin protocol (`simulate-from-stdin`,
   `test-from-stdin`) supported for a third-party front end?
7. Would a Go front end be of use upstream, for example as a second
   implementation that checks quint's own?

## 4. Q3 first: the oracle

### 4.1 The corpus

These are the corpus sources, each pinned and listed in a manifest with
its source and sha256:
- **The repository's models.** All 180 `.qnt` files. Each one is
  compiled as a model, typechecked, and compiled with the init and step
  its binding names.
- **The Quint gotogether generates**: every binding's
  `binding.EvalModule` text, the simulate modules of validate/model,
  connect's `--invariant` compiles, and grist's generated models (the
  `*_gen.qnt`, `*_steps.qnt` and `*_rules.qnt` files of the grist
  examples and the generator's goldens). These are what the front end
  will compile most often.
- **quint's repository at the tag `QUINT_VERSION` pins** (0.33.1, in
  ci.yml and nightly.yml), fetched by a script beside
  ci/install-quint.sh and never vendored (decision 3). The tag's sources
  are:
  - **`examples/`**: 77 `.qnt` files, 13,827 lines. All 77 parse and
    typecheck at this tag: `examples/README.md`'s table says so, and a
    rerun of `quint typecheck` on each passed 77 of 77. They are the
    accepted corpus, less the 29 that use constructs refused at first
    (§2.1). 28 use instances and 4 use `export`.
  - **`quint/testFixture/`**: 73 `.qnt` files, 1,629 lines. 52 are at
    the top level, 11 under `apalache/`, 7 under `simulator/` and 3
    under `typechecking/`. Their **expected outputs are `quint parse`'s
    only**: 32 `--out` JSON files and 24 `--source-map` files, written
    by `quint/scripts/update-fixtures.sh` with paths rewritten to
    `mocked_path/testFixture`. They hold no typecheck or compile
    expectations.
    - 16 of the parse outputs expect errors: QNT099, QNT405, QNT404 (a
      name, a type alias), QNT101, QNT406, QNT013, QNT098 (three
      cycles), QNT008, QNT015, and ANTLR syntax errors as QNT000
      ("mismatched input '}' expecting ...", "extraneous input").
    - So the fixtures check tier 1 (parse) and the parse-phase errors
      directly, and tiers 2 to 4 only by running quint on them.
  - **`quint/integration-tests/lang/io.md`**: 17 txm tests, among them
    the typecheck errors with their positions:
    - `TrivialTypeError.qnt`: QNT000 at 2:7;
    - `typechecking/OverrideErrors.qnt`: QNT000 at 8:16 and QNT201 at
      9:16.

    These positions are 1-based as quint prints them; `locs` in JSON
    are 0-based. They are the only expected type-error positions
    upstream checks in.
  - **The inline programs of the unit tests**: 51 `*.test.ts` files with
    about 680 `it(` cases. A program there is a list of declaration
    strings, which `quint/test/builders` wraps as `module wrapper { ...
    }` (`buildModuleWithDecls`, 55 call sites; `buildDef`, 17;
    `parseMockedModule`, 10). They can be extracted by rendering the
    same wrapper. Their expected types and effects are TypeScript
    assertions, which Q3 does not need to port, since quint itself
    judges each program in the harness.
  - **`evaluator/fixtures/`**: a few `quint compile --flatten false`
    outputs (ewd426, ewd840, jmt, simple, tictactoe), for a later Rust
    stdin path.
- **Generated well-typed programs.** qeval's generator
  (internal/qeval/gen_test.go) makes well-typed vals and actions over
  the builtins it supports, and quint already judges it (an entry quint
  refuses is a generator bug, differential_test.go's `ill`). It grows
  to whole modules: imports across files, instances with constant
  substitution, exports, polymorphic operators and aliases, sum types
  and `match`, and records with spread.
- **Generated ill-typed programs.** These are mutations of well-typed
  ones, each tagged with the phase it expects to fail in. The tag is a
  hint; quint's answer is the label. They cover:
  - an operand swapped for one of another type, a wrong arity, and an
    unknown or misspelled name (QNT404);
  - a reserved name as a parameter (quintnames, QNT101 and QNT008);
  - a duplicate definition, conflicting modules, an import cycle and a
    definition cycle (QNT098 and QNT099);
  - an instance naming a missing constant (QNT406) and an impure
    override (QNT201);
  - an assignment in a val and a temporal operator in an action
    (QNT200), a nondet outside an action (QNT203 to QNT206), and a
    double update (QNT202);
  - branches assigning different variables (QNT000 effect unification,
    the case lint/model.go reads today);
  - syntax errors at every token class.

The corpus is considered complete for Q3 when every QNT code of §1.3's
front-end phases, and every labelled alternative of Quint.g4, has at
least one entry that quint itself reports or exercises.

### 4.2 Canonical renumbering

A canonical form `canon(json)` is computed for each of the three outputs
(parse with its source map, typecheck, compile):
- Walk the modules and declarations in their order, and each expression
  depth-first in field order. Give every node id the next integer.
- Rewrite every reference to an id: the keys and `id` values of
  `table`, the keys of `types` and `effects`, the ids of params, opdefs
  and type nodes, and the source map's keys.
- Rewrite names that embed an id: unshadower's `name_<id>` (qeval's
  `unsuffixed` rule) and `__gtg_*_<id>`.
- Rename type, row, effect and entity variables in order of first
  appearance.
- Drop the fields outside the comparison tier: the consumed subset
  first (§1.2), everything later.

The canonical form is itself tested before anything is compared with it:
- `canon` of quint's output is idempotent;
- it is invariant when ids are shifted, for example the same model
  compiled after a dummy module that consumes ids;
- two models that differ by one renamed definition differ in exactly
  that place.

### 4.3 The comparison and its jobs

Each corpus entry is compiled by quint and by the Go front end. The
comparison tiers, in order:
1. **parse:** canonical modules and source-map spans equal.
2. **typecheck:** canonical modules and types equal; effects in a later
   tier.
3. **compile:** canonical IR equal.
4. **errors:** first error's code, file, start and end equal; then
   error sets.
5. **meaning:** qeval on Go's IR gives what it gives on quint's
   (`TestAgreesWithQuint`'s successors, the differential's values).

How the jobs run:
- **The nightly.** A `qfront-differential` job beside `qeval-differential`
  (nightly.yml) runs with the same seed-by-date scheme and the same
  rerun variable pattern (`GOTOGETHER_QFRONT_SEED=n`). Its push tier is
  a small fixed batch, and its nightly batch is large.
- **Shadow mode.** `evaluator-shadow` gains `GOTOGETHER_FRONTEND=shadow`.
  Every typecheck and compile that an example's tests make runs both
  ways, and a difference fails the test.
- **Mutation testing.** `ci/mutate-core.sh` runs on internal/qfront. A
  surviving mutant is a program the corpus lacks (ROADMAP Q3).

**Shrinking a disagreement.** ROADMAP names qshrink, but `qshrink.Shrink`
reruns a driver's recorded actions and is shaped for traces. What Q3
reuses is its search, `internal/ddmin` (three-valued ddmin over index
sets), applied first to declarations and then to an entry's
subexpressions, beside the tree shrink the differential test already
has (differential_test.go, `shrink`). The test of a candidate is "still
disagrees in the same tier and at the same canonical path". Each shrunk
disagreement is printed as a `.qnt` with both outputs' canonical diff
and kept as a regression fixture.

### 4.4 Exit criteria before Q2's front end starts

1. The corpus manifest exists, with the coverage of §4.1.
2. `canon` passes its own tests (§4.2).
3. The harness runs quint against itself and finds no difference.
4. The ill-typed generator's entries reach every front-end QNT code
   with quint as the judge.
5. The nightly job runs green for a week under the red-nightly rule,
   with a stub "front end" that shells out to quint, so that the
   plumbing is proven before any Go front end exists.

Q3's tiers 2 to 5 then wait for P2 to P4.

### 4.5 Q3 as built (2026-11-25)

internal/qoracle; its mechanism is DESIGN §111. Against §4.4:

1. **The manifest** (internal/qoracle/testdata/corpus.json, one entry a
   line): 862 entries: the repository's 182 tracked .qnt files (listed by git, so modules tests write into the tree, like validate's literate counter.qnt, are not pinned), 148 evaluation modules and 101 --invariant compiles from its bindings (6 bindings skipped: k8s YAMLs with a `spec:` key, and grist's generator goldens' shop.qnt, which quint refuses), quint's 77 examples and 73 test fixtures, and 281 inline programs extracted from its 51 unit-test files; 476 are compiled too. quint accepts 648, refuses 215 (their codes recorded), and one uses `apalache::` ("needs-quint"). One of quint's own examples crashes its compile (below). Each entry has its text's sha256, its
   constructs (a lexical scan: instances, exports, aliases, sum types,
   `match`, spread, `temporal`, `assume`, destructuring, `apalache::`,
   hex, ...), compile's main, init, step and invariants, and quint's
   verdict (its error codes for typecheck and compile). `apalache::`
   entries expect "needs-quint". quint's files are fetched by
   `ci/fetch-quint-src.sh` at the tag, its commit checked against
   `ci/quint-src.commit`. Per push, TestManifestCurrent fails on a
   repository .qnt file the manifest does not pin; the nightly checks
   quint's files and verdicts too. Not yet in the corpus: validate/model's
   simulate modules (their text is built inside `Simulate`), the
   Rust evaluator's fixtures, and a count of Quint.g4's labelled
   alternatives against the corpus (§4.1's completeness for the
   grammar; for the codes, see 4).
2. **canon** passes its tests: idempotent on quint's outputs, a shifted
   model's form equal to the model's, a renamed or changed definition
   differing there alone, and equality checked against brute force over
   every bijection on small IRs. Each was ablated (the id rewriting, the
   suffix rule, the variable renaming, the layer order): a test fails.
   Two departures from §4.2: ids become paths, not counters (local
   diffs), and declarations are compared by toposort layer, since
   quint orders a layer by its ids as strings.
3. **The harness against itself** (2026-11-25, this machine, the nightly's settings with a sample of 5): quint against quint with every id shifted on all 863 entries then pinned (one more since, Q1's sample.qnt), parse, typecheck and compile: 861 agree, and the two that differ are quint's own id-dependence (below, testdata/shift-known.txt); every pinned verdict held; the 25 generated programs (5 well-typed, 20 ill) agree. 57 minutes wall, 104 CPU minutes on 4 cores, two quint processes at a time (quintcmd's admission). The plain stub (`GOTOGETHER_QFRONT_STUB=quint`) is the same comparison without the shift. Getting there took five changes to the form, each a quint behaviour the shift exposed (the order, the doubled errors, the end columns, the parser's id-made names, the empty rows), which is what the shifted stub is for.
4. **The ill-typed generator** reaches every front-end code, quint
   judging (TestIllReachesEveryCode): QNT000 (syntax, type, effect),
   QNT006 to QNT009, QNT012 to QNT016, QNT098, QNT099, QNT101, QNT102,
   QNT200 to QNT206 and QNT404 to QNT408, from 37 mutations of the
   module generator's programs (four files: aliases, a sum type,
   polymorphic and record operators, an export, instances with constants,
   a state machine). quint accepts every generated well-typed program
   sampled.
5. **The nightly** `qfront-differential` runs the corpus and a dated
   sample; a week of green nights is still to come.

Disagreements shrink by ddmin over declarations (positions kept: a
removed declaration becomes blanks) and, for generated programs, by
their tree; both are checked (against brute force over every subset,
and for 1-minimality over the reductions).

**What it found in quint 0.33.1**, each a constraint on P1 to P5:
- The toposort sorts a layer with JavaScript's default sort, comparing
  ids as strings: shifting ids reorders independent declarations, and
  modules. Bar (2)'s "declaration order is part of it" holds only up to
  the layer. *For the maintainer:* binding's `Vars` follow the
  typecheck's order; either binding orders them itself, or P1 must
  number nodes exactly as quint does.
- **quint's verdict itself depends on its ids** on two corpus entries:
  examples/language-features/importFrom.qnt is refused (QNT405 'basics',
  QNT404 through an export) once a module is written before it, and
  `assume _ = val foo = 1 { foo }` (a unit test's) gains a QNT000. A Go
  front end that numbers nodes otherwise will meet both; they are listed
  in internal/qoracle/testdata/shift-known.txt, which the nightly logs
  rather than fails on, and fails on once one agrees.
- A type error is reported once per module analysed before it: errors
  compare as the first, then the set (§3.4's set).
- A node starting on a file's first line keeps that line's column shift
  in its end column, wherever it ends (a module's closing brace): the
  source map's end columns are quint's arithmetic, not positions.
- `compile --out` writes the modules before flattening; the IR is
  stdout's (a file, not a pipe: node exits before a pipe drains), which
  a failed compile may fill with text (`[DEBUG] generating undefined
  expr`). The compile IR keeps the non-main modules unshadowed while its
  table's names for them are unshadowed.
- A type node's id inside `types` comes from the modules' id space or
  from a builtin signature's own parse: no form can tell them apart, and
  qeval decodes `Type.ID` without reading it. P5 can drop the field.
- `val n2 = n; val r = { val n2 = n2 + 1; n2 }` typechecks and fails
  compile with QNT099.
- The parser makes names from ids (`quintTupledLambdaParam<id>` for
  `((a, b)) => e`, `quintDestructTemp<id>` for `val (a, b) = e`): P1 must
  write them, and the form names them by the id's path.
- A unit's fields are written `{row, [], empty}` or `{empty}`, by the
  solver's order (binding/testdata/typed.qnt shifted).
- `quint compile` of quint's own
  examples/classic/distributed/TwoPhaseCommit/two_phase_commit_modules.qnt
  (main two_phase_commit) fails with "Internal error while flattening"
  and writes nothing: its manifest verdict is `tool-failure`.

**For P5, not built here:** `CompiledModel.Differs` comparing forms
(canon.Of and canon.Equal are ready), and `GOTOGETHER_FRONTEND=shadow`
in `evaluator-shadow`: both need P5's seam at the eight call sites.
Mutation testing of internal/qfront waits for it to exist.

## 5. The plan

Sizes use the roadmap's S, M and L. The roadmap's "L" for Q2 is
optimistic, since P2 and P3 are each about an L. The honest total for Q2
after Q3 is two L's, three M's and an S. Each phase ships behind
`GOTOGETHER_FRONTEND=quint|go|shadow`. The default stays `quint` through
P5. It becomes `go` only after a period in shadow on the nightly
(decision 7), and quint then remains the nightly shadow.

| phase | size | delivers | exit criteria | risk |
|---|---|---|---|---|
| **Q3** (prerequisite) | M | §4: corpus, generators, `canon`, harness, nightly job | §4.4 | the corpus misses what users write (mitigated by mutation runs on the port, and by every adopter's model joining the corpus) |
| **P1** parser and source map (built, §5.1) | M | internal/qfront/syntax: lexer (from quintnames' tables), parser, AST, source map, parse-phase QNT codes (QNT006 to QNT016) | the whole corpus parses; tier 1 equal (canonical modules, the source map's spans); syntax-error corpus: code and start position equal | the ANTLR ambiguities of §3.1; the grammar drifting upstream |
| **P2** names, imports, instances, flattening | L | source resolution, collector, resolver, toposort, unshadower, flattener, instance flattener (constants substituted, fresh ids), export, alias inliner | `quint parse --out`'s table equal canonically; typecheck's modules equal without types; compile's modules and table equal without types; QNT013, QNT098, QNT099, QNT101, QNT102 and QNT404 to QNT408 at quint's positions | instance flattening's interaction with analysis copies (§1.3, step 6); declaration order |
| **P3** type checker | L | type-application resolution, constraint generation and solving with rows, polymorphism, builtin signatures ported (or parsed from quint's strings at build time), QNT000 type errors with reference spans | tier 2 types equal canonically; ill-typed corpus: first type error's position equal; N consecutive green nightlies with no disagreement | **the blamed node depends on the solver's order** (§6, risk 1); silent acceptance |
| **P4** effects and modes | M | effect inference, the mode, nondet and multiple-updates checkers, the inferred effects are used for checking and not written (decision 5) | QNT200 to QNT206 and effect QNT000 at quint's positions; lint's unification regex replaced by structured errors | effects are large and unread, so the temptation is to skip the check; it must not be skipped, since an ill-moded model accepted is risk 2 |
| **P5** wiring | M | the eight call sites of §1.1 behind one seam; `Differs` by `canon`; compiled artifact provenance (§3.2); doctor's line; `compile -check` without quint; RequireQuint skips that guarded only typecheck or compile removed | every example's suite green with `GOTOGETHER_FRONTEND=go` and with `shadow`; `gotogether compile` and `binding.LoadModel` with no quint on PATH | a behaviour change hidden in a reader that assumed quint's order or fields |
| **P6** quint off consumer paths | S | with Q1's simulation and the in-process replays: no consumer path starts quint; quint is the oracle only (Q3 jobs, `shadow`, the confirmation of a failure where installed); Q11's one binary | a consumer's `go test` and `gotogether validate` with no Node on the machine, on every example | depends on Q1 and the replay work, not on Q2 alone |

### 5.1 P1 as built (2026-11-25)

internal/qfront: `ir` (quint's IR, written as quint writes it), `syntax`
(the lexer, its keywords from internal/quintnames, and the parser, by
hand: recursive descent, precedence climbing at ANTLR's precedences for
Quint.g4's left-recursive alternatives) and `qfront.Parse` (the main file
and the files it imports `from`, as quint's phases 1 and 2 load them),
which writes `quint parse --out`'s modules and errors and `--source-map`'s
map. Destructuring, `temporal` and `assume` parse from the start;
`apalache::` names are ordinary qualified names to a parser, so its
refusal is P2's (the resolver's).

**In.** Every alternative of Quint.g4 at v0.33.1 but the REPL's
`declarationOrExpr`; the listener's desugarings (sum types' constructors,
`((a, b)) => e`, destructuring at module level and in lets, spread,
`match`, `q::debug`'s text, the single-variant alias); the parse-phase
errors: ANTLR's syntax errors (QNT000), Quint.g4's QNT006, QNT008 and
QNT009, the listener's QNT007, QNT012, QNT014, QNT015 and QNT016, source
resolution's QNT013 and QNT408; and the `depth` quint's name collector
writes into the parse's declarations.

**Numbering.** Nodes are numbered as ToIrListener numbers them (an id when
its rule is left, children first, in each exit method's order; syntax
errors first, as ANTLR reports them while parsing), with quint's spans
(its end-column arithmetic). On the corpus the Go parse's source map
equals quint's id for id on every program it parses alike: ids, files
and spans, not only the canonical form. So the id-dependent behaviours
of §4.5 (layer order, `quintTupledLambdaParam<id>`, the temporaries'
names) come out as quint's wherever the parse is all the ids there are;
the typecheck-order effects are P2's and P3's, and binding will order its
variables itself (the maintainer's decision).

**ANTLR's prediction.** Where a construct could go on at a token that also
starts an expression (a binary `-`, `[`, a call's `(`, a reserved word
read as a name, a def's `(`), ANTLR's SLL prediction follows the
expression's exit into any context (a let's body) and, where both
readings fail, takes the exit: its error is then at that token, not where
the construct's reading stops (`f(1 +)` at `(`, `def f(x) = x +` at the
def's `(`). Where both readings parse that far it reads on with the real
context (full LL), and a let's value ends before its body
(`val d = p - q` then `-b <= d`): two of quint's own programs need that.
`syntax.continues` and `syntax.decide` reproduce these; ANTLR's lexer is
followed too (an unclosed string skips the rest of the file; a lexer
error is reported when the parser first reads past it, which may be after
a syntax error before it).

**Against quint** (P1's tier, `qoracle.P1`: the parse without its lookup
table, the errors of P1's codes; after a syntax error the first error
alone, by code and start, since P1 stops where ANTLR recovers), the whole
corpus with quint 0.33.1, 2026-11-25:

| entries | agree | of them, quint's ids | first error only |
|---|---|---|---|
| 863 | 861 | 833 | 28 |

(833 is every entry compared with its modules: none agrees canonically
with other ids.)

By construct (an entry counts under each it uses): all agree on
instances (153), sum types (68), destructuring (65), exports (61), `run`
(57), doc comments (41), `match` (40), spread (37), temporal operators
(28), `temporal` (27), polymorphic aliases (20), `assume` (20) and
`apalache::` (1). The two that differ are one class, P2's: quint's
toposort drops definitions in a cycle (QNT099, testFixture's
_0100cyclicDefs and _0101noRecursion), which a parse alone keeps. The 28
compared by their first error are every corpus entry with a syntax error
(quint's fixtures and unit tests). Generated programs
(`GOTOGETHER_QFRONT_SAMPLE=50`: 50 modules and three ill-typed mutations
of each): all 50 well-typed agree, 141 of the 150 mutations; the 9 others
are P2's mutations, in three classes: a definition cycle (QNT099, 4: the
toposort's drop again), a duplicate module (QNT102, 4: canon keys two
modules of one name by their content in quint's toposort order) and a
duplicate definition (QNT101, 1: two equal declarations told apart by the
same order). P1's fixtures (internal/qfront/testdata, 31 files) joined
the manifest (894 entries). The harness's stub is
`GOTOGETHER_QFRONT_STUB=go` (quint's parse cut by `qoracle.ParseTier`
against `qoracle.GoParse`); internal/qfront's own push-tier test compares
fixtures exercising every alternative and the parse-phase errors, by
canonical form and by raw ids.

**Out, left for P1.** Recovery after a syntax error (ANTLR's partial
modules and later errors are not reproduced: P1 stops at the first);
quint's listener's stacks after a record with two spreads, except as a
definition's body; a count of Quint.g4's labelled alternatives against
the corpus (the fixture covers them by hand); a nightly step running the
corpus with the `go` stub (its two QNT099 entries would need a known list
like shift-known.txt); and the after-P1 uses below (lint's token scanner,
validate's import scan). Everything after the parse is P2 on.

What can ship early, and on what:
- **After P1.** lint's token scanner and validate's import scan become
  the parser:
  - lint/model.go's `quintTokens` and the reserved-name and
    `x' = a or b` checks gain real positions;
  - `importPaths` (validate/manifest_model.go), which "reads tokens, as
    quint's lexer does" to avoid a 0.3 s process, gets an exact import
    graph for cache keys and manifests.

  Annotated parameter types become readable without quint. Most models
  annotate actions (`action apply(r: int): bool`), but inferred types
  need P3.

  Q4 (bindings typed at load) does not need Q2. `binding.Model` already
  has parameter types from the typecheck, and a compiled model carries
  it, so Q4 can start now.
- **After P2.** The positions for `Explain` arrive. DESIGN §58 prints
  source text from the IR because "quint's JSON IR has ids but no
  positions" and the flattened module's ids are not the parse's. A Go
  flattener can keep each copy's origin span, so `Failure.Why` can name
  `file:line:col`.

  connect's "invariant not found" check (QNT404) can also run without a
  process.
- **After P3.** Inferred types are available in process: the binding's
  plan, typegen (`gotogether types`), and Q5's generators drawn from
  Quint types. These run in shadow, or as an addition beside quint's
  typecheck, not as the judge of acceptance.
- **After P4.** Acceptance itself moves to Go, which is P5.

## 6. Risks, ranked, and questions for the maintainer

**Risks.**
1. **Error-position parity rests on the solver's order.** QNT000's
   reference is whichever node the constraint solver blames, so exact
   position parity may force a near-literal port of
   `constraintGenerator` and `constraintSolver`, and may break whenever
   quint changes them. Mitigation: Q3 measures how often a looser bar
   (an enclosing node's span) would be needed before P3 commits, and
   open question 4 (not being asked, §3.6).
2. **Silent acceptance.** Go accepts, or types differently, a model
   quint refuses. A wrong inferred type changes what the binding
   converts, and every later verdict rests on it. Mitigations:
   - the ill-typed generator;
   - shadow mode in every example nightly;
   - refusing unimplemented constructs as "needs quint" (§2.1);
   - `go` the default only after a nightly shadow period (decision 7).
3. **Upstream drift.** The grammar changed between v0.33.1 and main, and
   the IR is not a documented contract. One Go front end therefore
   implements exactly one quint language version, recorded as `Quint` in
   its artifacts and checked by `quintcmd`'s admission. Every
   `QUINT_VERSION` bump re-runs Q3 in full. A disagreement is either
   quint's change, which is ported, or our regression, which is fixed.
   The bump lands only green, so a bump becomes an M of work whenever
   quint changes its front end.
4. **Two front ends to maintain**, ours and quint's. If Informal Systems
   builds a non-Node front end of its own, ours is a second
   implementation. That has value as an oracle for both, but the cost
   persists.
5. **Scope creep toward full parity**: effects written, `documentation`,
   the TLA+ target, evaluating temporal operators. Keep to the consumed
   subset and refuse the rest (decisions 4 and 5).

**Decided** (the maintainer, 2026-11-25):
1. **No contact with Informal Systems for now.** gotogether proceeds on
   its own, and the maintainer may reconsider. Q3 is not gated on
   upstream. The §3.6 questions are open and not being asked, and no
   docs/upstream note is written.
2. **The bar is yes.** That is canonical equality over the read subset,
   with qeval's agreement as the backstop (§3.3), plus first-error
   position parity (§3.4).
3. **quint's fixtures and examples are fetched at the pinned tag**
   (`QUINT_VERSION`), not vendored.
4. **Destructuring, `temporal` and `assume` are accepted from the start;
   only `apalache::` is refused as "needs quint" at first** (revised the
   same day from refusing all four: the three release 32 of quint's 77
   examples, §2.1). `temporal` definitions are checked and passed
   through; a check that would evaluate one refuses there.
5. **`effects` is written only if the Rust evaluator's stdin path needs
   it.** At v0.33.1 it does not (§1.1), so the Go front end writes no
   `effects`.
6. **The front end stays internal for now** (internal/qfront).
7. **`go` becomes the default only after a period in shadow on the
   nightly, not at P5's exit.** How long that period is remains open
   (below).
8. **An optional `compiler` field in `gotogether.model/1` is fine.** A
   new major version is equally acceptable if cleaner: backward
   compatibility does not drive the design. P5 picks whichever is
   cleaner when it builds it.

**Still open:**
- How long the shadow period of decision 7 lasts, and what ends it: a
  count of green nightlies, or a QUINT_VERSION bump survived.
- Whether the Rust stdin path is taken up at all (P6 or later). It would
  need the table's definition copies written (§1.2).

**Keeping quint as the oracle.** After P6, quint and Node appear only:
- in Q3's jobs, the push tier and the nightly;
- in `GOTOGETHER_FRONTEND=shadow`;
- in the existing confirmations of a failure where quint is installed.

A `QUINT_VERSION` bump in ci.yml and nightly.yml, and
`quintcmd.MinimumVersion`, is the trigger for a full Q3 run. Its
disagreements are the porting work list for that version. doctor
reports quint as the oracle, not as a requirement (Q11).
