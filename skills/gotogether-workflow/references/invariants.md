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
alone, and a directive naming another val of the model says the binding
does not check it.

A directive names a property as the main module does (`limitsHonoured`),
or qualified by any module of the spec that sees it, with `.` or `::`:
the instance (`exposure.limitsHonoured`), the module that declares it
(`limits.limitsHonoured`), or the main module. The table names each as
the main module does, in the model's order, with its meaning: the first
line of its `///` doc comment, else of the `//` comment above it.

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
A name that is no action of the model is an error at its line, so scan
only the code of the model the table is for.

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
has, and only their kinds of warning are shown (printed, both are) (a literate model's `.qnt` is written again when
it was current, as with `simulate -write-matrix`: the literate
reference). Links are relative to the document, so the table is the same
from any directory:

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
| `f.go:38: .Action("ince", ...) names ince, which is no action of model counter: did you mean inc? ...` (exit 1, every mode) | a driver entry, Switch key, directive or Record names no action | fix the name, or leave another model's code out of the patterns |
| `f.go:17: //quint:action on F names no action: give it action:NAME ...` | a directive without its action | add `action:NAME` |
| `warning: property p: nothing enforces it` / `no test checks it` (a `—` cell) | an unannotated property | annotate it, or say why in the document |
| `warning: action a: nothing records it` / `nothing drives it` (a `—` cell) | no code records the action's steps, or no connect driver runs it | record it where it takes effect, bind it, or say why |
| `FAIL property p: ...`, `FAIL action a: ...` (exit 1) | the same under `-require` | the same |
| `FAIL doc.md: stale: row p changed, row q added: run gotogether invariants ...` (exit 1) | the code moved, or the annotations changed | run the command it names, commit the document |
| `FAIL doc.md: missing: doc.md has neither <!-- gotogether:invariants --> ... nor <!-- gotogether:actions --> ...` | the document has no markers | add the two lines where each table goes |
| exit 2 | a bad invocation, a Go file that does not parse, a model quint refuses | the message says which |

Read Go source alone (go/parser): no types, no build, so it runs where
the code does not build. Matching is by name: `.Action` on a value that
is no `*connect.Binding` but shares a name with one in the file counts,
and a `Record` on a value of another type in a file that imports qobs
counts (another imported package's `Record`, qtest's, does not).
