<!-- Drafted 2026-11-25 (internal/qoracle, Q3; docs/q2-front-end.md §4.5's rule 3), not filed: the maintainer files it at github.com/informalsystems/quint. -->

### A module written before another changes the typecheck's verdict: name and export resolution follow the order of node ids

**quint version:** 0.33.1

**Summary.** Writing an unrelated module first in a file (one that only
consumes ids: `module __shift { pure val s1 = 1 pure val s2 = Set(1, 2) }`)
changes whether `quint typecheck` accepts the file's other modules. Two
cases from quint's own repository; both files typecheck as given and are
refused once the module is prepended on the main file's first line.

**Reproduction 1** (examples/language-features, at the v0.33.1 tag): copy
`importFrom.qnt` and `imports.qnt` into a directory, then

```sh
quint typecheck importFrom.qnt     # accepted
{ printf 'module __shift { pure val s1 = 1 pure val s2 = Set(1, 2) } '; cat importFrom.qnt; } > shifted.qnt
quint typecheck shifted.qnt
#  Error [QNT405]: Module 'basics' not found
#  Error [QNT404]: Name 'MyF::ExportedBasics::double' not found
```

`imports.qnt`'s `export basics as ExportedBasics` resolves or not
depending on the modules' order within a toposort layer.

**Reproduction 2** (quint/test/names/collector.test.ts's
`assume _ = val foo = 1 { foo }`):

```sh
printf 'module m {\n  assume _ = val foo = 1 { foo }\n}\n' > a.qnt
quint typecheck a.qnt              # accepted
printf 'module __shift { pure val s1 = 1 pure val s2 = Set(1, 2) } module m {\n  assume _ = val foo = 1 { foo }\n}\n' > b.qnt
quint typecheck b.qnt
#  Error [QNT000]: Couldn't unify int and bool
```

**Where the ids enter.** quint/src/static/toposort.ts sorts each layer
with `newLayer.sort()`, which compares bigint ids as strings ("105" before
"98"), and returns cycles as `Set(edges.keys())`, an immutable Map's order
(by its keys' hashes once over 8 entries). Declarations and modules of a
layer are then visited in that order by the phases after it; a resolution
that depends on visiting order (an export seen before or after its
importer, an assumption named `_`) depends on the ids. Two programs that
differ only in a module the others never use should get one verdict.

**Suggested fix.** Order a layer by source position (or by name) rather
than by id, in `toposort`, and make the cycle report order-free (sort the
cycle's ids by position). A regression test: typecheck each example with
and without a leading id-consuming module and compare verdicts.

**Downstream.** gotogether's Go front end (Q2) implements the unshifted
verdict and records both programs as `verdict-flip` in its oracle's
internal/qoracle/testdata/shift-known.txt.
