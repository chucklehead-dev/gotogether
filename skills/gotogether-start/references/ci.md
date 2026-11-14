# gotogether in CI: install, tiers, validating without quint

Read this when setting up CI for a model-checked repository, or a
pipeline that validates recorded runs (a canary, a CronJob) where Node
and quint are not installed.

## Install in a workflow

The Action pins Go, Node, quint and its Rust evaluator, installs the
CLI, caches quint and gotogether's cache of quint's typechecks and
compiles, runs `gotogether doctor`, then your command; the job summary
gets each `RESULT:` line, and a failing one an error annotation:

```yaml
- uses: actions/checkout@v4
- uses: chucklehead-dev/gotogether/.github/actions/gotogether@<commit>
  with:
    working-directory: service
    doctor: strict
    run: |
      go test -short ./...
      gotogether validate -binding stock.binding.yaml runs/*.jsonl
```

Inputs: `run`, `working-directory`, `gotogether-version` (`action`: the
CLI of the ref the action is pinned at), `quint-version`, `node-version`,
`go-version`, `compiled`, `cache`, `doctor` (`true`, `strict`, `false`).
Output `result`: `PASS`, `FAIL`, `NO VERDICT`.

Without the Action: `QUINT_VERSION=0.33.1 ci/install-quint.sh` (the
evaluator from its release asset: quint's own fetch is rate-limited on
shared runners), and cache `~/.quint` and `~/.cache/gotogether/quint`
(`GOTOGETHER_CACHE`) keyed by quint's version and `hashFiles('**/*.qnt')`.
A container: `docker build -f ci/image/Dockerfile .` from a clone (not
published to a registry yet).

## Tiers

- Per push: `go test -short ./...`, `gotogether lint -binding B -src .`,
  `lint -sim .`, `validate` of recorded runs, `doctor -strict`. The Go
  evaluator decides passing runs; quint judges failures and a sample of
  passes (`GOTOGETHER_EVAL_SAMPLE`). A small service: 30-60 CPU s.
- Nightly: `go test ./...` (shrinking), fresh seeds
  (`GOTOGETHER_SEEDS`, `GOTOGETHER_SEED_OFFSET`, failures kept with
  `GOTOGETHER_CORPUS`), `GOTOGETHER_HEGEL_SEED=random`,
  `GOTOGETHER_EVAL=shadow`, mutants (`qweave -mutate`),
  `gotogether simulate -graph` and `-verify` (many `-binding`s in one job:
  `-timeout` the job's time left, so a slow proof is reported unfinished
  instead of leaving later bindings unvisited).
- Per release: the real tier's runs validated by the same binding.

## Validating without quint or Node

```sh
gotogether compile -binding stock.binding.yaml -o stock.model.json   # where quint is
gotogether validate -binding stock.binding.yaml -compiled stock.model.json runs/*.jsonl
```

The Go evaluator alone judges (each result says so; no pass sampled by
quint). Read the verdicts so:

| You see | Means | Next |
|---|---|---|
| `NO VERDICT (needs quint)` | the evaluator cannot decide the run (a fold over a set whose result depends on quint's order, a choice inside a bound action, hidden actions) | validate that run without `-compiled` |
| `compiled model ... is stale: ...` | the binding, a model source or the toolkit changed since it was compiled | `gotogether compile` again |
| `needs quint` refusing `-linearize`, an ITF input or `-manifest` | those are quint's to confirm or replay | drop `-compiled` for them |
| `... is not here: ... unchecked` | a model source is absent, so the artifact's hash is trusted | ship the sources to check them |

`gotogether doctor -compiled stock.model.json` says whether it is stale.

## Tests without quint

A binding's compiled model kept beside it (`stock.model.json` for
`stock.binding.yaml`, the default of `gotogether compile -binding
stock.binding.yaml`) judges the binding's qtest and qhegel runs where quint
is not installed: Record, Validate, Explore, Exhaust, Mutants, an invariant
the binding does not declare (`qtest.Invariants`) and the vacuity check
(`NotVacuous`), whose antecedent witnesses the compiled model carries. Use
`qtest.RequireModel(t, "stock.binding.yaml")` in those tests instead of
`RequireQuint`; tests of the model itself (`qtest.Model`, simulate, connect's
traces) still need quint.

```sh
gotogether compile -binding stock.binding.yaml -check   # CI, where quint is: fails when stale
GOTOGETHER_COMPILED=1 go test ./...                     # CI, without quint: every check compiled
```

`GOTOGETHER_COMPILED=1` fails a test whose binding has no compiled model, so a
quint left on PATH cannot hide a missing one; unset, the compiled model is used
only where quint is absent; `0` never uses it.
