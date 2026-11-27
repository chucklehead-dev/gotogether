# Invariant tables, generated

Read this when a design document (a walkthrough, a literate model) keeps
a table of the model's invariants: where the code enforces each and
which tests check it. Written by hand, such a table drifts with every
edit, and re-tracing its file:line references is a pass of its own.
Annotate the code instead, and generate the table; a second table, of
the model's actions, says which code records each step and which driver
runs it, from the code that already does so; a third lists what the
model says it leaves out (its assumptions) and the code aimed at each.
The same command lints a design document's claims: each sentence its
author marks `Claim:` must cite the property or assumption it rests on
(below, "Claims a document makes").

## The annotations

```go
// Admit lets a request in while the limit allows.
//
//quint:enforces limitsHonoured
func (l *Limiter) Admit(r Request) error {
	/* ... */
	//quint:enforces exposure.noLostIncrements
	l.count.Add(1)
	/* ... */
}

//quint:checks limitsHonoured, noLostIncrements
func TestAdmit(t *testing.T) { /* ... */ }
```

- `//quint:enforces p` on a declaration's doc comment (a function, a
  method, a type) annotates the declaration; inside a function, on the
  line above a statement (or trailing it), it annotates that statement,
  named by its function.
- `//quint:checks p` is on a Test or Fuzz function of a `_test.go` file,
  or on a statement inside one (a subtest's closure too): the test it
  names is that function. Anywhere else it is an error.
- One directive names one or more properties, separated by spaces or
  commas.

## Properties and their names

The properties are the model's boolean vals (and temporals) that its
main module sees unqualified: its own, and those of a module it imports
or instantiates flattened (`module exposure { import limits(N = 3).* }`).
A qualified import's (`import choreo(...) as choreo`) are not. With
`-binding` they are the binding's `invariants` and `eventually` vals
alone. Three flags choose them instead, a model's scenario targets and
mutant witnesses being boolean vals nothing enforces by design:

- `-props a,b,c`: exactly these, in this order;
- `-invariant safe`: the conjuncts of `val safe = and { a, b }` (or
  `a and b`, `all { a, b }`), expanded through conjuncts that are
  themselves conjunctions of vals, depth first; a val that is no
  conjunction is itself, and one with a conjunct that is no val's name
  (`x != 5`) stays a row too, before its named conjuncts, since no other
  row stands for that conjunct;
- `-exclude a,b`: these left out, after the others.

`-props` and `-invariant` replace the binding's set (give one of the
two); `-exclude` removes from whichever set is chosen. A name that is
no boolean val is refused (exit 2) with the nearest. A directive naming
a val the table leaves out says so.

A directive names a property as the main module does (`limitsHonoured`),
or qualified by any module of the spec that sees it, with `.` or `::`:
the instance (`exposure.limitsHonoured`), the module that declares it
(`limits.limitsHonoured`), or the main module. The table names each as
the main module does, in the model's order (or `-props`'), with its
meaning: the first sentence of its `///` doc comment's first paragraph,
else of the `//` comment above it, its wrapped lines joined (up to a
period followed by a space or the end).

**Qualify annotations when a tree holds several models.** A name
qualified by a module the spec does not have (`publication.noExpiredServe`
in the attempts model's run) is another model's: it is left out and
counted once, `note: 2 annotation names qualified by modules this spec
does not have (other models'), left out of the table of model attempts:
publication (2)`. An unqualified name that is no property stays an
error, and so does one qualified by a module of the spec that lacks the
val. So one package can carry two models' annotations, each run scanning
`./...`.

## Actions and the code that implements them

No annotation is added for actions: the code that implements one names
it already, and the actions table reads it with go/parser (no types, by
selector name, as `lint -binding -src` does). An action is

- **recorded** by a `//quint:action action:a ...` directive on a function
  (binding's own parsing, the rule qweave shares), or a `Record(ctx,
  "a", ...)` or `Invoke(ctx, "a", ...)` call on qobs or on a recorder, in
  a file that imports qobs;
- **driven** by a connect driver: a literal `.Action("a", ...)`,
  `.Handle("a", ...)` or `.Skip("a", ...)` on a `connect.Bind(...)`
  chain (or on a variable, parameter, field or function result of type
  `*connect.Binding`, or a variable assigned from such a chain), or a key
  of a `connect.Switch{"a": ...}` literal.

qobs and connect are recognised under any name the file imports them as
(`cn "github.com/chucklehead-dev/gotogether/connect"`). Only string
literals count: an action named through a constant or a variable is not
seen, nor a key set on a Switch after it is made (`sw["a"] = h`). A
`.Skip` counts as driven (the driver accounts for the action), shown
`skipped in TestX`.

The rows are the actions `step` applies itself (through `any`, `all`,
`if`, `nondet`): not `init` and `step`, not frame actions applied only
inside other actions (`keepN`), and not what `init` applies (`initWith`:
a trace names that step `init`). A directive or driver naming any of
these is no error, and no row. `-exclude` takes actions out of the rows
too (one flag: Quint names are unique in a module). A name that is no
action of the model is an error at its line.

The actions table is read only when it is in play: printed (no
document), or the document has an actions pair of this model's name
(or an unnamed one). Otherwise no code implementing actions is read, no
action is an error or a note, and `-require recorded` or `driven` is a
bad invocation (exit 2). Actions cannot be qualified (a step names its
action bare, as a trace does), so with a table in play:

- a connect driver in a file whose `connect.Config{Spec: "..."}` names
  another spec (resolved from its package's directory, or by base name)
  is left out, noted once: `note: 3 connect driver entries in files
  whose connect.Config names another spec, left out of the actions of
  model publication: ../model/attempts.qnt (3)`;
- an unknown action recorded (directive or Record) in a file whose
  property annotations all name another model's module is that model's,
  counted in the other-model note;
- any other unknown action stays an error.

## Assumptions: what the model leaves out

A model is right about what it says; the bugs are often in what it
leaves out (an unbounded queue where the real one caps, one signing key
where keys rotate, a retention it never counts). Name each such
simplification on a line of its own in the model, a plain `//` comment,
so quint reads the model unchanged:

```quint
module queue {
  var q: List[int]
  // @assume unboundedQueue = underCap: the queue never fills; the real one caps at 10k
  // @assume oneSigningKey: one key signs every document; production rotates keys
  val underCap = q.length() < 10000
  /* ... */
}
```

- `// @assume NAME: statement` is one line, reviewed like a property;
  NAME is its own and may not be a name the main module sees (a
  directive naming it could otherwise mean a property). A literate
  model's quint blocks carry the lines as any comment; problems are
  reported at the document's line.
- `= val` names a boolean val that states it (a state inside the
  assumption satisfies it), for a trace to be checked against later; the
  val then leaves the default properties (no "nothing enforces it" row),
  unless `-props` or a binding selects it. Prose alone is allowed.
- Not `///`: quint joins a doc comment to the next definition's (and
  refuses one at a module's end). An `@assume` after code, a line with
  no name or colon, a repeated name, a clash, or a `= val` that is no
  boolean val is an error at its line (exit 1, no table written).

Code aimed at an assumption says so, placed as `//quint:enforces` is (a
declaration's doc comment, or the line above a statement), in tests or
not:

```go
// TestQueueFull fills the real queue past the model's assumption.
//
//quint:exercises unboundedQueue
func TestQueueFull(t *testing.T) { /* ... */ }

//quint:monitors unboundedQueue
func (q *Queue) reportDepth() { /* ... */ }
```

`//quint:exercises` is a test, a double's fault, a sweep row or a
generator that goes past the assumption on purpose; `//quint:monitors`
is the production check that watches it. Names resolve as properties
do (bare, or qualified by the declaring module, the main module or one
that sees it; another model's module is noted and left out). A
directive naming no assumption, `//quint:exercises` naming a property,
and `//quint:enforces` naming an assumption or the val that states one,
are each an error at its line with the fix.

The table goes between `<!-- gotogether:assumptions -->` markers (named
for one of several models as the others are), a row per assumption in
the source's order:

```markdown
| assumption | statement | stated by | exercised by | monitored by |
|---|---|---|---|---|
| `noOverflow` | n + 1 never overflows; Go's int wraps past math.MaxInt | `belowMaxInt` | `TestIncPastMaxInt` [../counter/counter_test.go:46](../counter/counter_test.go#L46) | — |
| `capFixed` | the cap never changes; the real Cap is a field anyone may set | — | — | — |
```

The "stated by" column is there when an assumption names a val. Printed
(no document), the table follows the others when the model lists any.

## Claims a document makes

A design document's prose states guarantees no property states (golem's
ADR had one, and it was false). Mark each guarantee as a claim and cite
what makes it true:

```markdown
Claim: a transfer never spends a balance twice (prop: noDoubleSpend),
as long as one caller holds the account (assume: oneCaller).

- **Claim:** a refund lands exactly once (prop: exactlyOnce).
```

A paragraph, list item or quoted line whose text begins `Claim:` (or
`**Claim:**`) is a claim through the end of its paragraph or item, so
its citation may be on a wrapped line. `(prop: a, b)` cites boolean vals
(or temporals) the main module sees, bare or `module.name`, whatever the
table's selection; `(assume: name)` cites an `@assume` line. Fenced code,
`Claim:` mid-line and unmarked prose are never read: nothing guesses what
a sentence asserts, so mark the sentences you mean.

```sh
gotogether invariants -spec model/bank.qnt -claims docs/design.md -require cited ./...
gotogether invariants -binding bank.binding.yaml -check docs/walkthrough.md -claims docs/design.md,docs/adr/0003.md ./...
```

A claim citing nothing, and a citation naming nothing the model has (or
the other kind: a property in `assume:`, an assumption or the val that
states one in `prop:`), fail at the claim's line. A cited property
nothing enforces or no test checks, or a cited assumption nothing
targets, is a note; `-require cited` makes it a `FAIL`. Alone, `-claims`
prints no table and `-require` takes `cited` only; beside `-check` it
lints as well, one CI line for both.

## The command

```sh
gotogether invariants -spec model/exposure.qnt -main exposure ./...       # print both tables
gotogether invariants -binding exposure.binding.yaml -write docs/walkthrough.md ./...
gotogether invariants -binding exposure.binding.yaml -check docs/walkthrough.md -require enforced,checked,recorded ./...
```

`-require` takes any of `enforced` (every property has a
`//quint:enforces`), `checked` (every property a `//quint:checks` in a
test), `recorded` (every action a directive or literal Record: trace
validation can see it), `driven` (every action a connect driver's
entry: model-based tests run it), `exercised`, `monitored` and
`targeted` (every assumption a `//quint:exercises`, a
`//quint:monitors`, or either), and fails (exit 1) where the table has
`—` instead of warning. An assumption kind with no assumptions table in
the document is a bad invocation (exit 2).

The arguments are directories, or `dir/...` (default `./...`: vendor,
testdata, hidden and `_` directories left out, and other modules, as the
go command does); import paths are not resolved. `-write` replaces what
lies between each pair of lines the document has, the properties' and
the actions':

```markdown
<!-- gotogether:invariants -->
<!-- /gotogether:invariants -->

<!-- gotogether:actions -->
<!-- /gotogether:actions -->
```

and nothing outside them; `-check` compares the tables the document
has. A row with nothing of a kind `-require` names is a `FAIL` line of
its own; the bare rows of a kind not required are one note a kind,
`note: 5 actions nothing records (recorded is not required): a, b, ...`
(of the tables in play). A
literate model's `.qnt` is written again when it was current, as with
`simulate -write-matrix` (the literate reference); `-literate=false`
treats the document as plain Markdown and refuses a `.md` spec (name the
`.qnt` your own tangler, lmt for one, writes).

One document can hold several models' tables, each pair named for its
model: `<!-- gotogether:invariants attempts -->` ...
`<!-- /gotogether:invariants attempts -->`, and the same for
`actions`. A run writes and checks only the pair of its name (`-name`,
default the main module, or the module the binding names), else the
unnamed pair; the others are left as they are. A document of one model
keeps unnamed markers.

Links are relative to the document, so the table is the same from any
directory. A site is its function (`Type.Method`), qualified by its
package (`state.Restore`, `telemetry.Call.AttemptID`) when the tables
span packages, and a `checks` directive in a `t.Run` closure with a
literal name names the subtest (`TestReset/from_the_cap`). With
`-links name` each site is its function (qualified as above), linked to
its file by the file's base name, without a line: the table then
changes only when a site is added, removed or renamed, not when code
above it moves (the default, `line`, links each `file:line`):

```markdown
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `withinCap` | n never exceeds the cap. | `Counter.Inc` [../counter/counter.go:18](../counter/counter.go#L18) | `TestInc` [../counter/counter_test.go:8](../counter/counter_test.go#L8) |
| `atCap` | | — | — |
```

```markdown
| action | recorded by | driven by |
|---|---|---|
| `inc` | `Counter.Inc` [../counter/counter.go:25](../counter/counter.go#L25) | `bound` [../counter/counter_test.go:38](../counter/counter_test.go#L38) |
| `reset` | `Counter.Reset` [../counter/counter.go:38](../counter/counter.go#L38) | `TestModel` [../counter/mbt_test.go:16](../counter/mbt_test.go#L16) |
| `idle` | — | — |
```

## What it reports

| Output | Means | Next |
|---|---|---|
| `f.go:17: //quint:enforces names withinCapp: model counter has no property withinCapp: did you mean withinCap? name one of ...` (exit 1, every mode) | the directive names no property | fix the name, or the model |
| `f.go:9: //quint:checks is in helper, which is no test: ...` | a checks directive outside a test | move it to the test that calls the helper |
| `f.go:15: //quint:enforces in F annotates no statement: ...` | nothing on the next line | put it above the statement, or on F's doc |
| `note: N annotation names qualified by modules this spec does not have (other models') ...` | other models' qualified annotations in the tree | nothing: their own run tables them |
| `f.go:12: //quint:checks names p: p is a val of model M that this table leaves out ...` | the binding, `-props`, `-invariant` or `-exclude` left p out | add it to the selection, or name one the table has |
| `invariants: -props names x, which is no boolean val of model M: did you mean ...` (exit 2) | a selection flag names no boolean val | the name it suggests |
| `doc.md has no table of model M: add <!-- gotogether:invariants M --> ...; its tables are named a, b` | no pair of this model's name, nor unnamed | add the pair, or `-name` |
| `f.go:38: .Action("ince", ...) names ince, which is no action of model counter: did you mean inc? ...` (exit 1, every mode) | a driver entry, Switch key, directive or Record names no action | fix the name, or leave another model's code out of the patterns |
| `f.go:17: //quint:action on F names no action: give it action:NAME ...` | a directive without its action | add `action:NAME` |
| `note: 1 property nothing enforces (enforced is not required): p` (also `no test checks`, `nothing records`, `nothing drives`; a `—` cell) | rows without that kind, which `-require` does not name | annotate them, or say why in the document |
| `FAIL property p: nothing enforces it: put //quint:enforces p ...`, `FAIL action a: nothing records it: ...` (exit 1) | a bare row of a kind `-require` names | the directive or driver it names |
| `note: 3 connect driver entries in files whose connect.Config names another spec ...` | another model's drivers in the tree | nothing: their own run tables them |
| `invariants: -require driven is of the actions table, and doc.md has none of model M ...` (exit 2) | an action kind required with no actions table in play | add the pair, or require enforced and checked alone |
| `model.qnt:12: @assume withinCap has the name of model M's definition ...`, `/// @assume is a doc comment ...` (exit 1, no table written) | an `@assume` line declaring no assumption the table can show | the fix it names: rename, write it with `//`, give `name: statement` |
| `f.go:9: //quint:exercises names noOverflw: model M has no assumption noOverflw: did you mean noOverflow? ...` | a directive naming no assumption | fix the name, or declare it in the model |
| `f.go:9: //quint:enforces names oneCaller: oneCaller is an assumption of model M (what it leaves out), not a property ...` | a property directive naming an assumption (or `... states assumption a`: its val) | `//quint:exercises` or `//quint:monitors` |
| `FAIL assumption a: nothing targets it: put //quint:exercises a ...` (exit 1) | `-require targeted`, and nothing aimed at it | a test or double past it, or a production check |
| `doc.md:12: claim "..." cites no property or assumption: cite the property that makes it true, (prop: name), ...` (exit 1) | a marked claim that cites nothing | cite what makes it true, add it to the model, or drop `Claim:` |
| `doc.md:12: claim "..." cites property withinCapp: model M has no property withinCapp: did you mean withinCap? ...` (also `cites assumption`, `... not a property: cite it as (assume: x)`) | a citation the model does not have, or of the other kind | the name it suggests, the kind it names, or the model |
| `FAIL doc.md:12: claim "..." cites property p, which nothing enforces: put //quint:enforces p ...` (a `note:` without `-require cited`) | the claim rests on a property, or an assumption, the code does not back | the directive it names |
| `ok   doc.md: 3 claims, citing 2 properties and 1 assumption of model M`, `FAIL doc.md: N problems in its claims, ...` | each document's verdict | fix each line above it |
| `FAIL doc.md: stale: row p changed, row q added: run gotogether invariants ...` (exit 1) | the code moved, or the annotations changed | run the command it names, commit the document |
| `FAIL doc.md: missing: doc.md has no table of model M: add <!-- gotogether:invariants M --> ...` | the document has no markers of this model, nor unnamed ones | add the pair where the table goes |
| exit 2 | a bad invocation, a Go file that does not parse, a model quint refuses | the message says which |

Read Go source alone (go/parser): no types, no build, so it runs where
the code does not build. Matching is by name: `.Action` on a value that
is no `*connect.Binding` but shares a name with one in the file counts,
and a `Record` on a value of another type in a file that imports qobs
counts (another imported package's `Record`, qtest's, does not).
