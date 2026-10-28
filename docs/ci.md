# gotogether in CI

What to run when, for a repository of yours checked against a Quint
model, and how to install it there. The numbers are this repository's
(ci/test-times.txt: CPU seconds per package, the test binary's and its
quint processes', on CI's 4-core runners).

## Install: the Action, the image, or by hand

**The Action** installs Go, Node, quint and its Rust evaluator at pinned
versions, the gotogether CLI, keeps quint's install and gotogether's
cache of quint's typechecks and compiles between runs, runs `gotogether
doctor`, then your command, with its result in the job summary (an
error annotation for each failing `RESULT:` line):

```yaml
jobs:
  model:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: chucklehead-dev/gotogether/.github/actions/gotogether@<commit>
        with:
          working-directory: service        # its go.mod gives Go's version
          doctor: strict                    # fail on a version skew
          run: |
            go test -short ./...
            gotogether validate -binding stock.binding.yaml runs/*.jsonl
```

Its inputs: `run` (bash, gotogether on PATH), `working-directory`,
`gotogether-version` (a tag or commit; `action`, the default, builds the
CLI from the action's own source, so the CLI is the ref you pinned),
`quint-version` (0.33.0), `node-version` (22), `go-version` (default:
the go.mod's), `compiled` (`'true'`: neither Node nor quint, for
`validate -compiled`), `cache`, `doctor` (`true`, `strict`, `false`).
Its output `result` is the last `RESULT:` line's verdict (`PASS`, `FAIL`,
`NO VERDICT`). Pin it at a commit until the toolkit is tagged.

**The image** (`ci/image/Dockerfile`) holds Go, Node, quint and the
evaluator pinned by build arguments, and the CLI built from the context:

```sh
docker build -f ci/image/Dockerfile -t gotogether .
docker run --rm -v "$PWD:/w" -w /w gotogether gotogether validate -binding b.yaml runs/*.jsonl
```

This repository's CI builds it and runs `gotogether doctor -strict`
inside, and pushes it nowhere: which registry publishes it, under what
name, is the maintainer's call. Build it yourself until then.

**By hand**: `QUINT_VERSION=0.33.0 ci/install-quint.sh` installs quint
from npm and its evaluator from the release asset (quint's own fetch goes
through GitHub's API, rate-limited for shared runners), then `go install
github.com/chucklehead-dev/gotogether/cmd/gotogether@<version>`;
`gotogether doctor` says what is installed and what disagrees.

## Caching quint's compile

Every check typechecks its model and compiles the binding's evaluation
module with quint, about a second each; gotogether keeps both by content
(quint's version, the model and its imports, the module's text) in
`GOTOGETHER_CACHE` (default `~/.cache/gotogether/quint`; `off` turns it
off). Keep it between runs, keyed by the models: the Action does this.

```yaml
      - uses: actions/cache@v4
        with:
          path: ~/.cache/gotogether/quint
          key: gotogether-models-${{ runner.os }}-0.33.0-${{ hashFiles('**/*.qnt') }}
          restore-keys: gotogether-models-${{ runner.os }}-0.33.0-
```

A stale entry is never read (its key is its content), so restoring an
older cache only saves what still matches. Cache `~/.quint` too (the
evaluator, keyed by quint's version): install-quint.sh skips it when
present. `gotogether doctor` reports the cache's size.

## Tiers

| tier | what | catches | cost here |
|---|---|---|---|
| per push | `go test -short ./...` with your model-checked tests; `gotogether lint -binding b.yaml -src .`, `lint -sim .`; `validate` of recorded runs; `doctor -strict` | a step the model refuses, an invariant broken, a binding that no longer fits, a `go` statement in simulated code, a skew | hello 39 CPU s, bank 27, twophase 55, jobqueue 33; the core's validate package 960 |
| nightly | `go test ./...` (shrinking against the model), explorations at fresh seeds (`GOTOGETHER_SEEDS=500`, `GOTOGETHER_SEED_OFFSET`), Hegel at a fresh seed (`GOTOGETHER_HEGEL_SEED=random`), `GOTOGETHER_EVAL=shadow`, mutants (`qweave -mutate`), `simulate -graph` and `-verify` | rarer schedules and faults, an evaluator disagreement with quint, a check that cannot fail (a surviving mutant), an invariant the design does not guarantee | this repository's nightly: a job per example at 500 seeds, up to five hours each |
| per release | the real tier (the system against real dependencies, its runs validated), `doctor -strict` against the released modules, the corpus replayed | what a simulation's doubles are kinder about than the real thing | the examples' real tiers: minutes each |

**Per push.** Under a CPU budget: `-short` explorations run tens of
seeds, and the Go evaluator decides the runs that pass (DESIGN §56), so
a passing suite starts few quint processes; quint judges every failure,
and a sample of the evaluator's passes (`GOTOGETHER_EVAL_SAMPLE`, one in
fifty by default; this repository's CI sets `1/5`). A small
model-checked service's suite costs 30 to 60 CPU seconds here; a
repository's budget is the sum. Keep slow tests behind
`testing.Short()`.

**Nightly.** Everything quint can check: the full tests, fresh seeds
(a failing run is kept as a corpus entry: `GOTOGETHER_CORPUS=dir`, then
committed under a test's `ExploreConfig.Corpus` so per-push replays it),
Hegel's searches, `GOTOGETHER_EVAL=shadow` (quint judges everything the
evaluator does, a disagreement fails: the guard on the per-push tier's
trust), mutation testing (a survivor is a check that cannot fail),
`gotogether simulate -graph` (every reachable state of a small model) and
`-verify` (Apalache, bounded). Expect hours; run them in parallel jobs.

**Per release.** The real environment's runs, validated by the same
binding (`gotogether validate`, or `-compiled` where quint is not
installed), and `gotogether doctor -strict -C <module>` so the CLI, the
module's core, qweave and gotogether-nav agree.

## Validating where there is no quint

A canary, a CronJob or a laptop during an incident may have neither
Node nor quint. Compile the model where quint is (per push), ship the
artifact with the binding, and validate there with the Go evaluator
alone:

```sh
gotogether compile -binding stock.binding.yaml -o stock.model.json      # needs quint
gotogether validate -binding stock.binding.yaml -compiled stock.model.json runs/*.jsonl   # needs neither
```

With the Action: `compiled: 'true'`. What it claims (DESIGN §64): a pass
and a failure are the evaluator's, each result saying so, with no pass
sampled by quint; a run the evaluator cannot decide is `NO VERDICT
(needs quint)`, never a guess; searches (`-linearize`), ITF inputs and
`-manifest` are refused; a stale artifact (another binding, a changed
source, an evaluation module this gotogether writes otherwise, a later
format, quint before 0.32) is refused with the fix, `gotogether
compile`. `gotogether doctor -compiled stock.model.json` checks it ahead.
A source of the model absent where it runs is not checked, and the
report says so.
