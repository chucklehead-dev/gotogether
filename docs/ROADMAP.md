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
  - **Left, then done with X8.** Hegel unseeded at night
    (`GOTOGETHER_HEGEL_SEED=random`) and a failure as a corpus entry
    (`GOTOGETHER_CORPUS`, uploaded). The first unseeded run failed
    hello's `ShrunkTo(1)` at 3 seeds of 10: Hegel's shrinking had kept a
    fault the failure did not need. Shrink now removes such faults
    itself, and 10 seeds of 10 end at one.

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
  - **Vacuity, done.** An invariant's antecedent (A of a top-level
    `A implies B`, read from the typechecked model, or the binding's
    `antecedents`) is looked for after an exploration: its negation is
    checked as an invariant on the runs where the invariant passed, in
    chunks of 8, stopping at the first witness. `Exploration.Vacuous`,
    the Evidence line and `qtest.NotVacuous()`. checkout's, streams',
    crossdb's and fleetrollout's faithful explorations claim it and pass:
    their runs settle.
  - **`quint verify`, done.** `gotogether simulate -verify N` checks each
    invariant over every trace of up to N steps (Apalache), classified as
    the binding says; the nightly runs it at 8 on every binding, capped by
    a binding's `simulate.verifySteps` (Apalache's cost climbs steeply
    with depth: broadcast takes 32 s at 6 steps and over 8 minutes at 8)
    or skipped with its `verifySkip` (grpcledger, not done in 10
    minutes; twophase, Choreo's recursion beyond Apalache). Broadcast is capped
    at 6 steps and kvstore at 7; the other twelve take 8, the whole pass
    about 10 minutes. Its finding: 6 steps were too few for
    edgepublish's duplicate payload, which sampling at 30 finds; 8 are
    enough.
  - **Readers of `evidence.json`, done.** Evidence names its test and
    model; a test's later explorations write `evidence-2.json` and on
    (each overwrote the last). `gotogether evidence DIR` reports a suite:
    each exploration's verdicts, then what no test tried (a model action,
    a declared fault point, a kind never faulted); `-strict` fails on a
    gap. `gotogether cover -sim` and actionmap read an evidence.json
    (`qcover.ReadSummary`). hello's and checkout's suites have no gap.
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
    steps take minutes. A `quint repl` session per instance was tried and
    was slower (DESIGN §9.7: quint's front end costs 10-40 ms per input);
    batches now use the TypeScript backend. Starting from a state needs
    an IR built in Go for the Rust evaluator, a project of its own. `//quint:action` could note the invocation on
    entry. grpcledger's spans carry their start already; its convert
    rules order by its own index, so nothing there needed it.
- **X5. The doubles' contracts (M).** Done for the store. Under
  `GOTOGETHER_CHECK=1` each `Store` keeps its committed history and
  checks every commit (`qenv/internal/history`, Adya's dependencies):
  G1a and G1c break every level, G-single snapshot isolation, any cycle
  serializability. A store with its validation off is caught (G-single
  for a lost update under SI, G2 for write skew under serializability);
  the working store passes qenv's tests and the store's examples, which
  CI now runs with the check.
  - The queue's contract too, after every operation: no message lost,
    none delivered after its acknowledgement, an Ordered queue's first
    deliveries in order. A queue forgetting a crashed consumer's messages
    is caught.
  - **Left.** The services' contracts; the history checker public, for a
    real database tier's recorded transactions.
- **X6. Clock faults (M).** Done. `Sim.ClockFaults` gives each process
  a clock of its own, read by `qenv.Now`: skewed from its creation
  ("clock.skew": ahead or behind by `ClockSkew`) and jumping at a read
  ("clock.jump": by `ClockJump`). True time (sleeps, timers, `Sim.Now`)
  is unchanged; off by default, so recorded runs keep their choices.
  examples/lease is the target the roadmap's candidates were not: none
  of streams, crossdb or jobqueue keeps its safety on a clock (streams
  fences by generation; crossdb's stamps go through the global
  ToVirtual; jobqueue's leases are the queue's one clock). Its findings:
  a lease timed on the holder's own clock is immune to skew and broken
  by one jump; comparing the lock's expiry with the holder's clock is
  broken by skew alone; fencing at the store survives both.
  - Drift: `Sim.ClockDrift` makes each clock run fast or slow by a
    fraction ("clock.drift"), and its process's sleeps with it. It breaks
    what skew cannot, a lease timed on the holder's own clock: lease's
    NoFencing conforms at 2% and fails at 25% (43% of runs), the margin
    left after a pause covering about 5%.
  - **Left.** `ToVirtual`/`ToReal` per process for libraries that read the
    real clock.
- **X7. Storage faults and partitions (M).** Done but the journal.
  - `qenv.Disk`: writes reach the OS at once and the disk on `Sync`; the
    crash of its process is a fault point ("disk.crash": nominal keeps
    every write, "lost" the synced ones, "torn" part of the last).
  - Against the toolkit's own `qobs.FileSink` it found what its doc
    claimed too broadly: one write per step survives a killed process,
    not a power loss, which loses the last steps and tears the line
    being written, and a torn last line made `ReadJSONL` reject the
    whole log. `ReadJSONL` now keeps the steps before it (a
    `*TornTail`, warned by validate); `SyncedFileSink` fsyncs each step.
  - Partitions between named processes for `Service` (`Sim.Partition`,
    `Heal`, and `Partitions`, a nemesis per link); `Settle` heals them.
  - Partitions for a `Queue` with a broker process (`Queue.Proc`): a
    publisher or consumer cut off waits for the heal, and a consumer's cut
    drops its connection, its unacknowledged messages redelivered and its
    acknowledgements lost (as a broker that times out a connection does).
    A nemesis's cut heals by itself after `Sim.PartitionLength`: checkout
    under partitions (`Config.Partitions`) was first stuck, its relay
    waiting on a cut no scheduling step would come to heal. It conforms,
    the cuts redelivering in about half its runs.
  - **Left.** jobqueue's journal on a Disk (jobqueue is not on qenv);
    partitions for the store and the logs.
- **X8. Smaller claims and corpora.**
  - **Done.** `ExploreConfig.Corpus`: saved runs (choices as JSON)
    replay before the seeds, and a failed claim saves the run it shows;
    `GOTOGETHER_CORPUS` for every exploration. Shrink's own pass over
    faults (above, X1).
  - **Done.** examples/hello's mutation test ends in `NoSurvivors` (CI
    weaves it): of 8 mutants, a client sending two requests of three
    survived (nothing said it finished), now killed by a `done` action
    and `Mutate`'s new claims (`Takes("done")`); three are marked
    equivalent with their reasons.
  - **Done.** checkout's faithful exploration at every level claims
    `Exercised()` and `NotVacuous()` (holding at fresh seed offsets).
    `FollowTraces` is in the README's and gotogether-start's tables, and
    has the test it lacked (qtest's flaky sender: a trace's losses as the
    network's drops).
  - qload's self-test `TestMoreCapacityNeverRecoversWorse`: of a
    spike's runs, the share that drains never falls as the server's rate
    grows (0 of 8 at 9/s, 3 at 10/s, 8 from 11/s).

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
