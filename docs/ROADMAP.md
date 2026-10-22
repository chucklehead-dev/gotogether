# Roadmap

What is open. What is done (the A–D, U, V and W tracks, the move to
gotogether) and the reviews that shaped it are in docs/HISTORY.md; why the
code is the way it is, in DESIGN.md.

Standing decisions, with the maintainer:
- Nothing is released yet. Tags wait for the maintainer (R5).
- Crashes are considered at every scheduling step (`NemesisEvery` is
  opt-in); `qenv.Service`'s crash window is on by default.
- The global recorder is opt-in (`qtest.Global()`).
- qhegel becomes a module of its own before any tag, so the core needs
  only OTel and yaml.v3.
- MIT license.

Order: R4 → X (in its own order below); R5 when the maintainer says.

## W8. First hour and docs

Done (W8a–g):
- a stale-docs gate over README, AGENTS and the examples' READMEs;
- the hello snippets checked against their source (`TestSnippetsMatchSource`);
- `qtest.ShrunkTo` and `Violates(...).At`, which replaced the examples'
  hand-written checks;
- the scaffold's `-out`, its typed `Mutation`, and empty explorations
  that fail;
- `validate` without ITF by default;
- the quickstart, passing from a fresh clone;
- the start skill's first hour and glossary;
- an unchecked invariant explained in reports.

The plan as written:
- **The quickstart.**
  - A 15-line quickstart first in README: install, `cd examples/hello &&
    go test .`, then the hello test.
  - One copy-paste install block.
  - gotogether-start in README's skill list, with a glossary and a
    first-hour path.
  - qweave's install.
- **The hello snippets.** The ones in README and gotogether-start are
  copied from hello_test.go, and a test checks that they match.
  `ShrunkTo(n)` replaces hello_test's hand count of faults.
- **README.**
  - The Packages table, regenerated from `go doc -short`.
  - One environment-variable table.
  - The ASCII diagram fixed.
  - The status line reworded for gotogether.
- **The scaffold.**
  - It writes its files rather than printing them.
  - A typed `Mutation`.
  - An empty run is a named Explore error, not a hand-written guard.
- **Wording.** Reports reworded. The examples' own hand-written
  assertions become claims.
- **Validate.** Done: `gotogether validate` writes no ITF by default;
  `-itf` asks for it, and `-noitf` fails saying so.
- **Stale strings.** `Sweep.Expect` and the free `qtest.Explore`, gone
  from docs and skills.
- **A stale-docs gate.** The deprecated-identifier test also reads
  README.md, AGENTS.md and the examples' READMEs.

Done when a fresh clone's quickstart passes as written.

## Release readiness (R)

- **R1. Modules that can be consumed.** Done (R1a–f):
  - every example is a module, and go.work ties them for development;
  - qhegel is a module, and the core needs no Hegel: a qobs-only program
    has neither hegel nor purego in its go.sum;
  - the published modules require pushed commits, with no `replace`;
  - CI tests each module with `GOWORK=off`, and installs the commands at
    the pushed commit;
  - `load` is `input`.

  Not checked here: golangci-lint's custom build of the plugin (no
  golangci-lint in this environment). The plugin's doc names the
  version to use.

  The plan as written:
  - A go.work for development, and no `replace` in published modules:
    `go install …@version` refuses them, and a dependency's replace is
    ignored.
  - qhegel in a module of its own (it brings hegel and purego); AGENTS'
    core-dependency rule becomes "OTel and yaml.v3".
  - A `GOWORK=off` CI job per published module.
  - Skills say `go install …@version`, and golangci's custom-gcl builds
    the plugin.
  - The supported Go range is documented (gosim translates one release's
    standard library).
  - Examples stay untagged, with their `replace`.
- **R4. Readiness.** Done:
  - LICENSE (MIT, The gotogether Authors);
  - CHANGELOG.md;
  - api/core.txt, the core's exported surface, kept by a test until tags
    give apidiff a baseline;
  - the tag order below, in AGENTS.

  The plan as written:
  - A root LICENSE (MIT; gosim's MIT and attribution beside it).
  - A CHANGELOG.
  - An API review per package (`go doc`) and an apidiff baseline.
  - The tag order: gosim, root, qweave, qhegel, qanalysis, qgosim.
- **R5. Tags**, when the maintainer says. Then the pseudo-versions give
  way to them.

## What a green suite proves (X)

From a review of the testing regimes by the architect and design agents,
after W. A green suite today proves less than it looks:
- **It is a fixed sample.** CI re-checks the same seeds and Hegel seed
  every run; nothing bounds the search or widens it.
- **It can be vacuous.** No report says which model actions the runs
  took, or which invariants held only because their antecedent never
  did. `Reaches` is used in one test, `NoSurvivors` in none.
- **The models are not run on their own.** No CI step runs `quint run
  --invariant` on the examples' models.
- **The doubles' contracts are trusted.** `TestIsolationAnomalies` shows
  each Store level allows the anomalies it should, not that it allows
  only those (DESIGN §10.7).
- **The real tiers record their own order.** The model checks the order
  the implementation claims (kvstore records under its lock); nothing
  compares it with the real time clients saw, and the reconstruction
  orders concurrent steps by recording order, sound only when they
  commute.
- **No `-race`.** The recorder and sinks, the native runtime and the
  real servers run without it.

The track, in order:

- **X1. Hygiene (S).** Done, but two parts:
  - **Done.**
    - `-race` in CI on the recorder, the validator, the native runtime
      and the real tiers (none found).
    - qenv's tests fail on goroutines left behind (`leak_test.go`).
    - A nightly workflow: 500 fresh seeds a night per example
      (`GOTOGETHER_SEEDS`, `GOTOGETHER_SEED_OFFSET` by date), and 30
      minutes of fuzzing whose finds are uploaded.
  - **Its first run's finding.** hello's anti-vacuity bar wanted request
    faults in half the runs, against a measured 56%. It held for seeds
    1–30 and failed for 1001–1060. It is now 30%: well under the rate,
    and still failing if the faults stop.
  - **Left.** Hegel unseeded at night (the tests pin `WithSeed`), and
    turning a failure into a corpus entry: both with X8's corpus.

  The plan as written: `go test -race -short` on the packages that are
  not simulated (qobs, qtrace, validate, qrun, qenv's native tests,
  kvstore, grpcledger, streams' real tiers). A leak check in qenv's
  TestMain from `runtime.Stack` (no goleak: the core takes no new
  dependency). A nightly workflow with fresh seeds (`Seeds: 2000`,
  `-fuzztime 30m`, Hegel unseeded) whose failures become corpus entries
  (`EncodeChoices`, recording the gosim module version next to
  `BytesVersion`: translated runs depend on it).
- **X2. Model coverage and anti-vacuity (S, then M).** Exploration counts
  each run's actions and outcomes and names the model's actions no run
  took: a `model` line in Evidence; `Takes(action)` (the twin of
  `Reaches` for a model action, with `InAtLeast`); `Exercised()`, every
  model action taken and every declared fault kind faulted in some run,
  exceptions named with a reason. A CI step runs `quint run --invariant`
  on every example model; `quint verify` (Apalache) nightly on the small
  ones, outside `go test`. Then vacuity: an invariant's antecedent (from
  a top-level `implies`, or declared in the binding) gets a witness def
  in the rendered module, and the report says which held vacuously in
  every run. Readers of `evidence.json`, which the model coverage writes:
  `gotogether cover -sim` and actionmap (V13's later work).
- **X3. Bounded exhaustive schedules (M; after W7).** A stateless,
  delay-bounded DFS chooser (`qenv/internal/choice`): option 0 is the
  oldest task or nominal, so a non-zero schedule choice is a delay and a
  non-zero fault choice a fault, and "≤ k delays, ≤ f faults" falls out
  of the protocol. `System.Exhaust(t, qtest.Bound{Delays, Faults,
  MaxRuns})` returns an Exploration (the same claims and Evidence);
  `Complete()` fails a search MaxRuns cut short, and Evidence says
  "complete: N schedules" or "truncated". The verdict cache makes
  schedules with the same steps cost a Go run, not a quint check. Wide
  value choices (`sched.wait`'s durations) count against the bound or
  stay nominal: document which. No DPOR (it needs every seam's
  footprint). Targets: checkout's `DualWrite` at f=1, hello's dedupe and
  streams' reorder at k≤2.
- **X4. Real-time order (S–M).** Clients record invoke and complete
  times; if A completed before B was invoked, A's step must precede B's
  (`qtrace`, an issue with a fix, no quint). Then, for overlapping
  steps, `Validate(..., qtest.Order(qtest.Linearize))` searches the
  orders their intervals allow, the model as the sequential spec. Targets:
  kvstore's real tier (a read path outside the lock), grpcledger's.
- **X5. The doubles' contracts (M).** Under `GOTOGETHER_CHECK=1`, Store emits
  read and write sets with commit versions and a checker
  (`qenv/internal/history`, Elle-style) fails on cycles the level forbids
  (G0, G1c, G-single, G2): soundness next to TestIsolationAnomalies'
  completeness. Reusable on a real database tier.
- **X6. Clock faults (M).** Per-process skew and jumps (`clock.skew`,
  `clock.jump`; option 0 none), applied in `qenv.Now` by the task's
  process; profiles and `ToVirtual`/`ToReal` stay global. Targets:
  streams' fencing, crossdb's grace period, jobqueue's leases (moved onto
  qenv).
- **X7. Storage faults and partitions (M).** A `qenv.Disk` double (lost
  writes not synced, a torn last record, rollback on crash), first
  against the toolkit's own `qobs.FileSink`, then jobqueue's journal;
  partitions between named processes in `Service`.
- **X8. Smaller claims and corpora.** An exploration corpus
  (`ExploreConfig.Corpus`: saved runs replay first, a failing one is
  written with its reproduce line); one example's mutation test ending in
  `NoSurvivors`, and `Reaches`/`Exercised` in checkout's conforming
  tests; `FollowTraces` in the README and gotogether-start's table; qload
  monotonicity (more capacity never recovers worse) as self-tests.

Deferred: online streaming verification (resuming quint from a checkpoint
is unsound for nondeterministic models; `gotogether validate -follow` over
rolling windows from `init` if wanted), differential runs (the model is
already the common oracle), DPOR.

Order: X1 → X2 (stage 1 and the models' `quint run`) → X3 → X4 → X5 → X6
→ X2 (vacuity) → X7; X8 alongside.

## Dropped on purpose

- REVIEW S2's embeddable `qenv.Layer`: `qenv.As` and Wrapper cover the
  chain.
- REVIEW P2's `Together` and `qhegel.Want`: `Violation.Expect`
  superseded them.
- Renaming the CLI's subcommands.
- Rewriting history to drop a5cc809's 15 MB test binary: .git is 26 MB,
  and the docs cite commit hashes.
