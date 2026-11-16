# Invariant tables, generated

Read this when a design document (a walkthrough, a literate model) keeps
a table of the model's invariants: where the code enforces each and
which tests check it. Written by hand, such a table drifts with every
edit, and re-tracing its file:line references is a pass of its own.
Annotate the code instead, and generate the table.

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

## The command

```sh
gotogether invariants -spec model/exposure.qnt -main exposure ./...       # print it
gotogether invariants -binding exposure.binding.yaml -write docs/walkthrough.md ./...
gotogether invariants -binding exposure.binding.yaml -check docs/walkthrough.md -require enforced,checked ./...
```

The arguments are directories, or `dir/...` (default `./...`: vendor,
testdata, hidden and `_` directories left out, and other modules, as the
go command does); import paths are not resolved. `-write` replaces what
lies between the document's two lines

```markdown
<!-- gotogether:invariants -->
<!-- /gotogether:invariants -->
```

and nothing outside them (a literate model's `.qnt` is written again when
it was current, as with `simulate -write-matrix`: the literate
reference). Links are relative to the document, so the table is the same
from any directory:

```markdown
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `withinCap` | n never exceeds the cap. | `Counter.Inc` [../counter/counter.go:18](../counter/counter.go#L18) | `TestInc` [../counter/counter_test.go:8](../counter/counter_test.go#L8) |
| `atCap` | | — | — |
```

## What it reports

| Output | Means | Next |
|---|---|---|
| `f.go:17: //quint:enforces names withinCapp: model counter has no property withinCapp: did you mean withinCap? name one of ...` (exit 1, every mode) | the directive names no property | fix the name, or the model |
| `f.go:9: //quint:checks is in helper, which is no test: ...` | a checks directive outside a test | move it to the test that calls the helper |
| `f.go:15: //quint:enforces in F annotates no statement: ...` | nothing on the next line | put it above the statement, or on F's doc |
| `warning: property p: nothing enforces it` / `no test checks it` (a `—` cell) | an unannotated property | annotate it, or say why in the document |
| `FAIL property p: ...` (exit 1) | the same under `-require enforced,checked` | the same |
| `FAIL doc.md: stale: row p changed, row q added: run gotogether invariants ...` (exit 1) | the code moved, or the annotations changed | run the command it names, commit the document |
| `FAIL doc.md: missing: ... has no <!-- gotogether:invariants --> ...` | the document has no markers | add the two lines where the table goes |
| exit 2 | a bad invocation, a Go file that does not parse, a model quint refuses | the message says which |

Read Go source alone (go/parser): no types, no build, so it runs where
the code does not build. It does not find an action without an
implementation; that is the alignment lint's (ROADMAP CW5), still open.
