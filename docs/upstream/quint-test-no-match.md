# quint test passes when no test matched

Observed with quint 0.33.1 (Rust evaluator v0.7.1) on Linux/amd64. This
note drafts an upstream issue; none has been sent. From golem's first use
(#144): its bug stories, written as `run`s, were silently not run.

## Reproduction

```quint
module m {
  var x: int
  action init = x' = 0
  action inc = x' = x + 1
  run bugStory = init.then(inc).expect(x == 2)
  run incTest = init.then(inc).expect(x == 1)
}
```

- `quint test m.qnt` runs only `incTest` (the default `--match` is names
  ending in `Test`), prints `1 passing` and exits 0. `bugStory`, which
  fails, is not mentioned.
- `quint test --match nosuch m.qnt` prints the module's name and nothing
  else, and exits 0: no test ran, and the result is a pass.
- `quint test --match bugStory m.qnt` runs it: `Error [QNT508]: Expect
  condition does not hold true`, exit 1.

## Proposal

1. Exit nonzero, or at least warn, when `--match` (default or given)
   selects no test in the main module: a CI job whose filter matches
   nothing passes forever.
2. When the default filter skips `run` definitions, say how many and how
   to include them (`3 runs not matching --match "Test$" were skipped; use
   --match '.*'`).

## On gotogether's side

ROADMAP L4: a `quint test` that gotogether runs and that matches nothing
is an error, not a pass. A model's own `quint test` calls are quint's, as
above.
