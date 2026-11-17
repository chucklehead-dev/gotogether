# Invariant tables, generated

Read this when a design document (a walkthrough, a literate model) keeps
a table of the model's invariants: where the code enforces each and
which tests check it. Written by hand, such a table drifts with every
edit, and re-tracing its file:line references is a pass of its own.
Annotate the code instead, and generate the table; a second table, of
the model's actions, says which code records each step and which driver
runs it, from the code that already does so.

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
seen, nor a key set on a Switch after it is made (`sw["a"] = h`). The
rows are the model's actions as the main module sees them, `init` and
`step` left out as connect leaves them (a driver's `init` is no error).
A name that is no action of the model is an error at its line. Actions
cannot be qualified (a step names its action bare, as a trace does), so
a tree that records or drives two models' actions is scanned per model,
by its directories.

## The command

```sh
gotogether invariants -spec model/exposure.qnt -main exposure ./...       # print both tables
gotogether invariants -binding exposure.binding.yaml -write docs/walkthrough.md ./...
gotogether invariants -binding exposure.binding.yaml -check docs/walkthrough.md -require enforced,checked,recorded ./...
```

`-require` takes any of `enforced` (every property has a
`//quint:enforces`), `checked` (every property a `//quint:checks` in a
test), `recorded` (every action a directive or literal Record: trace
validation can see it) and `driven` (every action a connect driver's
entry: model-based tests run it), and fails (exit 1) where the table has
`—` instead of warning.

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
has, and only their kinds of warning are shown (printed, both are). A
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
`-links name` each site is its package and function, linked to its file
without a line: the table then changes only when a site is added,
removed or renamed, not when code above it moves (the default, `line`,
links each `file:line`):

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
| `warning: property p: nothing enforces it` / `no test checks it` (a `—` cell) | an unannotated property | annotate it, or say why in the document |
| `warning: action a: nothing records it` / `nothing drives it` (a `—` cell) | no code records the action's steps, or no connect driver runs it | record it where it takes effect, bind it, or say why |
| `FAIL property p: ...`, `FAIL action a: ...` (exit 1) | the same under `-require` | the same |
| `FAIL doc.md: stale: row p changed, row q added: run gotogether invariants ...` (exit 1) | the code moved, or the annotations changed | run the command it names, commit the document |
| `FAIL doc.md: missing: doc.md has no table of model M: add <!-- gotogether:invariants M --> ...` | the document has no markers of this model, nor unnamed ones | add the pair where the table goes |
| exit 2 | a bad invocation, a Go file that does not parse, a model quint refuses | the message says which |

Read Go source alone (go/parser): no types, no build, so it runs where
the code does not build. Matching is by name: `.Action` on a value that
is no `*connect.Binding` but shares a name with one in the file counts,
and a `Record` on a value of another type in a file that imports qobs
counts (another imported package's `Record`, qtest's, does not).
