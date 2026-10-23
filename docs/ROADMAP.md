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
    and still failing if the faults stop. A sweep of every example at
    seeds 1001 on found two more: checkout's sweep met every pair of
    fault kinds only at its seeds (its rates are now 3x), and
    fleetrollout's in-memory-progress mutant went over budget through a
    lost response alone in 6.5% of runs, which 20 seeds miss a quarter
    of the time (now 40 seeds at a higher API fault rate).
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
- **X2. Model coverage and anti-vacuity (S, then M).** Stage 1 done:
  - **Done.**
    - Evidence's `model` line: the model's actions each run took (up to
      its failure) and those no run took.
    - `Takes(action).InAtLeast(p)`, the twin of `Reaches` for a model
      action; `Exercised(except...)`, every model action taken and every
      reached fault kind faulted in some run (hello's faithful test).
    - `gotogether simulate -binding`, on every binding in CI.
  - **Its finding.** A binding's invariants are of two kinds. Some the
    model guarantees (`holds`: no simulated trace violates them). Others
    are requirements on the code that the model lets a bad
    implementation break (checkout's `chargedAtMostOnce`, streams' four,
    edgepublish's known F1/F3): `allows`, where a simulation that never
    violates one means checking the code against it is vacuous. The
    binding's `simulate` section says which; replay-only models (no
    `step`: gcounter, kafka, fleetrollout, the uniqueids scheme) say
    `skip` and why.
  - **Left.** `quint verify` (Apalache) nightly on the small models.
    Vacuity: an invariant's antecedent (from a top-level `implies`, or
    declared in the binding) gets a witness def in the rendered module,
    and the report says which held vacuously in every run. Readers of
    `evidence.json`: `gotogether cover -sim` and actionmap (V13's later
    work).
- **X3. Bounded exhaustive schedules (M).** Done. `System.Exhaust(t,
  qtest.Bound{Delays, Faults, Without, MaxRuns})` runs every run within
  the bound breadth first by deviations, replaying each from the start
  (stateless; no entropy, so gosim asks per choice), and returns an
  Exploration: the same claims and Evidence, a `search` line, and
  `qtest.Complete()`. A run is named by its schedule (`17:1,40:2`, the
  choices made otherwise), which `GOTOGETHER_SCHEDULE` runs alone. Every
  option of a choice counts alike; no DPOR.
  - **Its findings.** The verdict cache makes it cheap: hello's 921 runs
    within 2 delays and 2 faults are 21 quint checks. One fault is the
    useful bound for the larger examples: checkout's 2828 one-fault runs
    conform, and DualWrite's window, which seeds find only at ten times
    the crash rate, shows with no rate to tune; streams' 435 catch
    `no-dedupe` and the reorder. Streams at one delay and one fault is
    over 40,000 runs: delays need DPOR, or a bound on which tasks may be
    delayed, before they scale past toy systems.
- **X4. Real-time order (S–M).** Done.
  - Steps carry when their operation was invoked (`Step.Invoked`:
    `qobs.Record`'s `invoked`, a converted span's start). One whose
    invocation is unknown has no real-time bound (it took effect some
    time before it was recorded). Reconstruct reports an order its
    thread or order keys force that real time contradicts.
  - `qtest.Linearize()` (on Validate and Record; `qtrace.Linearize`
    underneath) checks a client's history in an order the intervals
    allow: a guided search of single moves around the failing step, kept
    when they get further, checked in batches with conformance only. It
    is not exhaustive: finding none is evidence, not proof.
  - kvstore records a client history (`Client.Record`,
    `kv.LoadRecorded`): in recording order the faithful service is
    rejected in every load tried; Linearize finds an order (about 20
    orders, 10–20 s for 32 steps). `StaleRead` (a lagging snapshot) is
    caught as a read no order explains.
  - **Left.** Every order tried replays from the start, so a few hundred
    steps take minutes: starting a replay from the model's state at the
    window (the failure's state is in the report) would make each try
    the window's length. `//quint:action` could note the invocation on
    entry. grpcledger's spans carry their start already; its convert
    rules order by its own index, so nothing there needed it.
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
