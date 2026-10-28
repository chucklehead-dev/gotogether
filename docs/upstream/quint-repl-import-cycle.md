<!-- Drafted 2026-10-28 (ROADMAP H's Upstream; performance.md's D4, a REPL session for many replays, waited on it), not filed: the maintainer files it at github.com/informalsystems/quint. -->

### REPL: QNT101 "Multiple modules conflict on the same name" when the main file is imported back by a file it imports

**quint version:** 0.33.0 (both `--backend rust` and `--backend typescript`)

**Summary.** When file `a.qnt` imports a module from `b.qnt`, and `b.qnt` imports a module back `from "./a"`, the REPL (`quint -r a.qnt::main`, or `.load a.qnt main`) fails with QNT101 for every module of `a.qnt`. `quint typecheck a.qnt` and `quint run --main main a.qnt` accept the same files, so the files are valid; the REPL appears to load `a.qnt` twice (once as the file it was given, once through `b.qnt`'s `from "./a"`) instead of recognising it as the same file. An import in one direction only works in the REPL.

**Reproduction.**

`a.qnt`:
```
module rules {
  pure def double(x: int): int = 2 * x
}
module main {
  import rules.*
  import gen.* from "./b"
  export gen.*
}
module other {
  import rules.*
}
```

`b.qnt`:
```
module gen {
  import rules.* from "./a"
  export rules.*
  pure val four = double(2)
}
```

```
$ quint typecheck a.qnt; echo $?
0
$ quint run --main main --max-steps 1 --max-samples 1 --init true --step true a.qnt
[ok] No violation found (29ms at 34 traces/second).
$ printf 'four\n.exit\n' | quint -r a.qnt::main
syntax error:
 Error [QNT101]: Multiple modules conflict on the same name: other
  at .../a.qnt:8:1
 Error [QNT101]: Multiple modules conflict on the same name: main
 Error [QNT101]: Multiple modules conflict on the same name: rules
```

The same happens with an absolute path (`quint -r /abs/a.qnt::main`), with `.load a.qnt main` inside the REPL, and with either backend. With the `import rules.* from "./a"` line removed from `b.qnt` (and `rules` defined in `b.qnt` instead), the REPL loads the file and evaluates `four` to `4`.

**Expected.** The REPL resolves `from "./a"` in `b.qnt` to the file it already loaded, as `typecheck` and `run` do, and evaluates `four` to `4`.

**Why it matters.** Generated skeletons commonly split a model across files that import each other (a generated file importing the hand-written rules, the hand-written file importing the generated actions). Loading such a model once in a REPL session and evaluating many traces against it would avoid re-typechecking the model in a process per trace, which is most of the cost of checking traces against a large model.
