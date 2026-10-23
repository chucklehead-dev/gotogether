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

Order: R4 → X (in its own order below) → Y → Z; R5 when the maintainer says.

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
    model; each exploration writes its own, in its own directory (they
    overwrote each other; Y2's review gave each exploration a directory). `gotogether evidence DIR` reports a suite:
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
  - `Process.ToVirtual`/`ToReal`: a library that reads the real clock
    through the seam, in a process, sees that process's clock (its skew,
    jumps and drift).
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
  - **Not needed: partitions for the store and the logs.** What a cut
    does to them is already a fault of theirs: streams' simlog evicts a
    consumer at any step (a session timeout: the group moves on, the
    partitions are read again from their commits, the old member's
    commits fenced) and loses produce and commit requests or their
    acknowledgements; the store loses commits' responses (its commit
    faults). A partition would be the same faults under another name.
  - **Left.** jobqueue's journal on a Disk (jobqueue is not on qenv).
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

## Fifth review: what X left (Y)

From the fifth architecture and design reviews (HISTORY, "Fifth review").
Most of X's machinery is sound; what it left is about trust: a check that
panics loses its run, a reproduce line that judges something else, claims
and options that pass for less than they say, and a clock model that is
not Go's. Decided with the maintainer: all of it, the breaking changes (Y4, Y5,
Y7) included. Order: Y1–Y9 before a tag, Y10–Y12 soon after. Each item
gets a review of its commit against its bullet here before the next
starts, and the review's findings are fixed first.

- **Y1. A run that panics is a failing run. Done** (`qtest.RunPanic`,
  `qenv.WithPanicStack`; its review's fixes: a corpus run's reproduce
  line, a stale task stack). `runOne` had no recover:
  a task's panic (the store's history check, the queue's contract check,
  Sim.Check: they all report by panicking) kills the test binary with no
  seed, schedule or corpus entry. Recover it into `Run.Err` with the
  stack, name the run, save it; Hegel's path keeps the re-raise.
- **Y2. Reproduce exactly what failed. Done** (`GOTOGETHER_RUN`, kept
  failure files, a cached verdict re-checked into its exploration's
  directory). `GOTOGETHER_SEED` and
  `GOTOGETHER_SCHEDULE` apply to every exploration of the test and judge
  claims over runs on one run (`Some(Conforms())` fails; `Complete()`
  passes). One variable scoped to the exploration that printed it
  (`GOTOGETHER_RUN='TestX#2:seed=17'`, also ending the SEED/SEEDS
  collision); a reproduction judges per-run claims only and says which it
  did not judge. A failing run's artifacts are kept where the report
  points (today under `t.TempDir()`, deleted when the test ends).
- **Y3. A truncated search fails. Done** (`Bound.Partial`; `Complete()`
  removed). `Exhaust` cut short by MaxRuns passed
  unless the test also claims `Complete()`. Truncation fails by default;
  `Bound.Partial` opts into a sample.
- **Y4. Go's clocks. Done** (`Sim.Clocks`, `qenv.Mono`/`Since`/`WaitFor`,
  jumps as nemeses; lease's lock and holders time on the monotonic clock,
  and its jump finding is now `WallInterval`'s). `qenv.Now` was a wall
  clock with skew and jumps,
  and `Now().Sub` is wall arithmetic, but Go's `time.Since` uses the
  monotonic reading an NTP step does not move: lease's "one jump breaks a
  lease timed on the holder's own clock" is the model's, not Go's. A
  monotonic reading (drift only: `qenv.Since`, `qenv.Until`), jumps as a
  per-process nemesis rather than a choice at every read (a log line
  shifted every later choice), deadlines as durations, and one
  `Sim.Clocks{Skew, Jump, Drift}` checked at the first process (today
  the knobs are no-ops without `ClockFaults`, and set late give some
  processes jumps but no skew). Say that skew and drift are per-process
  fault choices at their rates, not every process. lease's claims redone
  on the monotonic reading.
- **Y5. Run files that say what they are. Done** for the corpus: an
  entry records its format (`gotogether.run/1`) and the exploration that
  saved it, which alone replays it; an unknown format is refused with the
  fix, and an entry that no longer replays fails as stale with the seed
  that finds its run again. Not done, on purpose: a hash of the fault
  model (a new double's kinds would make every entry stale; replay
  already detects a divergence exactly) and the gosim version (replay
  detects that too). The defaults stay opt-in on their merits: a fault
  a test did not ask for changes what every existing test checks.
  The finding: a corpus entry recorded no
  format, byte rule, gosim version or fault model, so a refactor turns it
  into a red test reported as a failure, and every new fault kind has had
  to stay opt-in to keep old choices. One run file with a header (format,
  BytesVersion, gosim version, a hash of the declared kinds and Sim's
  settings), keyed by exploration, not by test; a stale entry is reported
  as stale, with the fix. Then choose defaults on their merits.
- **Y6. Claims that say what they prove. Done:** one verdict
  (`Exploration.verdict`) for `Expect` and `Mutate`'s `meets`, so
  NotVacuous kills mutants too; `Exercised` by option name (among those a
  choice offered) and by nemesis name (`Run.Nemeses`); nemeses chosen per
  family, each at its rate; NotVacuous fails when it cannot judge;
  Linearize out of budget is n/a (`LinearizeBudget`). Claims stay a
  closed set: a user's own claim is a test of its own over `x.Runs`.
  The finding: claims are a closed switch,
  judged twice (`Expect`, and `Mutate`'s `meets`, which already differ).
  One judgment for both. `Exercised` counts each option and each nemesis
  by name (one crash of one process now exercises every nemesis, drop
  covers duplicate); Rates gets `nemesis.crash`, `nemesis.partition` and
  `nemesis.jump` (partitions and clock jumps now dilute crashes unseen). `NotVacuous` fails when an
  antecedent cannot be evaluated and says when no invariant has one.
  Linearize out of budget is inconclusive, which `Rejects` does not match.
- **Y7. Sim's shape, before names freeze. Done:** the network is
  `Sim.Net()`, a `Network` (Partition, Heal, Partitioned, OnCut,
  PartitionNemesis, CutLength); `Process.Spawn`; `NewQueue(s, name, p)`;
  per-instance kinds (`queue.<name>.ack`, `store.<name>.commit`,
  `disk.<name>.crash` with `powerLoss`, `tornWrite`); the new fault
  models on the native runtime (`TestNativeFaultModels`). Not done, on
  purpose: a process for `NewStore` (no network reaches a store in any
  example; a store across a cut is a Service in front of it, as crossdb's
  cloud is), and a panic for a Rates key that matches no kind (kinds are
  declared as doubles are made, after the rates are given).
  The finding: Sim's eleven config fields
  and the network state: `Sim.Net` (Partition, Heal, Partitioned, the
  nemesis renamed `PartitionNemesis`, an exported `OnCut`, the cut
  length), the clocks as Y4's value, `Process.Spawn` exported. Every
  double takes its process at construction (`NewQueue(s, name, p)`; a
  forgotten `q.Proc` silently disables partitions). Fault kinds per
  instance (`queue.<name>.ack`, `store.<name>.commit`, `disk.<name>.crash`,
  as `rpc.<name>` is; prefixes keep `queue` matching all); disk's options
  `powerLoss`, `tornWrite`. The new fault models tested on the native
  runtime, or refused there.
- **Y8. The API record sees methods and fields. Done:** api/<module>.txt
  for the core, qhegel, qweave, qanalysis and qgosim (and qgosim's
  `sim` side), read with go/ast: type-checking a sub-module from the
  core's test would need its dependencies (Hegel, x/tools), which are not
  in the core's module graph, and promoted members show under the
  embedded type's own record. Fields with their tags, methods, interface
  methods (unexported too), constants' values and places, variables'
  values, an internal type's API through its alias; the test fails on any
  byte of difference. The finding: api/core.txt was `go doc
  -short`, which lists no method or field: none of X's API reached the
  diff. Record the published sub-modules too.
- **Y9. An option a workflow ignores fails. Done:** each Option records
  its name; explorations refuse Global and Linearize, Validate refuses
  Global, RecorderOptions and SkipIfEmpty, qhegel's cases refuse
  Linearize and SkipIfEmpty (`Settings.Refuse`), each naming why. The
  finding: `Linearize()` and `Global()`
  in a System's or qhegel's Opts do nothing in Explore, Exhaust or
  Shrink, silently.
- **Y10. Evidence as a format. Done:** `Evidence.Format`
  (`gotogether.evidence/2`) refused when other, and read by format
  (qcover, `gotogether evidence`) rather than by a key or a file name;
  `GOTOGETHER_EVIDENCE` (dir/<package>/<test>/x<N>.json), cleared per test, and
  KEEP's x<N>/evidence.json cleared likewise; `Evidence.Role` (faithful,
  mutant: Mutate's or `System.Mutant`, replay: `ExploreConfig.Replay`,
  qhegel's), gaps counting faithful only; `evidence -strict` on hello
  and checkout in CI. The finding: a format field and one reader (cover and
  actionmap guess by a key today); its own `GOTOGETHER_EVIDENCE` dir,
  cleared per test (a rerun appends duplicates); explorations numbered
  and labelled faithful or mutant (mutants now count toward "taken");
  `evidence -strict` on hello and checkout in CI.
- **Y11. The core's Hegel properties against the current core. Done:**
  qhegel's qenv_test.go is `internal/hegelprops`, an unpublished module
  replacing the core and qhegel with the checkout, in CI with
  GOTOGETHER_CHECK. qgosim's own tests stay on its pinned core;
  examples/gosim (replace => ../..) runs qgosim on the current one. The
  finding: qhegel
  and qgosim pin the core before X, and CI runs them GOWORK=off: none of
  X's qenv changes reached qhegel's core-property tests. Move those to an
  unpublished module with `replace`, or add a workspace job.
- **Y12. Docs. Done:** DESIGN §42 (the X track) and §43 (this one),
  §10.7 brought up to date; `qenv.Faults` gone from AGENTS and the
  faults skill (the gate matches it in backquotes now); README's tables
  (clocks, the network, Disk, SyncedFileSink, nemesis families,
  `evidence`, `-verify`, CHECK's checks); `scaffold -system` claims
  `Exercised()` (and `NotVacuous()` where an invariant has an
  antecedent) and names its mutant; `simulate` says "none of N sampled
  traces" and, when the binding caps `-verify`, says to raise the cap;
  a passing Expect logs a one-line summary. The finding: a DESIGN section for X (exhaust, linearize, clocks,
  disk, partitions, the history and contract checks, vacuity, corpus,
  verify), and §10.7's "partitions not yet". Stale `qenv.Faults` in
  AGENTS and the faults skill (the docs gate matches only `Faults(`).
  README's tables (clocks, partitions, Disk, SyncedFileSink, `evidence`,
  CHECK's new checks). `scaffold -system` writes `Exercised()` and
  `NotVacuous()`. `simulate` says "none of 1000 sampled traces", and does
  not advise raising a capped `-verify`. A passing Expect logs a one-line
  evidence summary.

Consider: the nightly's confidence per claim ((1−p̂)ⁿ for Some, the
binomial tail for InAtLeast), and fleetrollout, edgepublish and gosim in
it; simulate's and evidence's logic as library calls
(`validate.Simulate`, `qtest.Suite`); a `Machine` grouping processes for
disks, clocks and partitions; bare expectations' arity (`Conforms` every
run, the rest some) and contradictory pairs; "run 3" not "schedule 3";
`Reaches` by prefix as Rates are.

## Sixth review: whole product (Z)

From four reviews of the whole product after Y (HISTORY, "Sixth
review"): architecture and maintainability, correctness, a newcomer's
usability and docs, missing features and lessons learned. Decided with
the maintainer: all of Z1–Z8, breaking the API freely before tags. In
order, bugs first; each item reviewed against its bullet before the
next.

- **Z1. Silent wrong answers. Done:** a crashed `Service`'s workers are
  freed (a generation per incarnation); a `Store` commit's lock wait is
  one wait for every key, seen by deadlock detection; `Linearize` keeps
  the recorded steps' gaps and cycles; connect's state check refuses an
  unexported or misspelled field (`itf.DecodeProjection`); an
  incarnation acting after its successor is an error; `EncodeChoices`
  refuses a nominal choice no byte encodes. Of the suspicions: the
  verdict cache key carries seq, error and intervals; `Bound.Without`
  kinds are checked; a crashed queue broker is unreachable and drops its
  consumers' connections; a duplicate is not processed once its first
  copy was refused (ErrOverloaded); connect clears a kept Dir's traces.
  Left for Z7: `ErrStuck` only when no timer is set (a periodic timer
  hides a stall until the deadline: the Settle verdict). Read, no
  counterexample: the history checker's scans over the transaction's own
  writes, and SI's G-SIa unchecked (such reads form a G-single cycle or
  are serializable).
- **Z2. Reports a user can act on. Done:** a mismatch prints the
  binding's expect as written under the rendered one, and the model's
  state in a batch's report too (a chunk's failures replay on Rust, which
  writes their ITF); a passing claim's example is headed `as expected,
  <claim>:`, indented, without paths its test removes; counts are said in
  the claims' words ("21 of 30 runs reject apply"), in the CLI too
  (`RESULT: FAIL (2 of 3 traces: 1 mismatch after deposit, 1 rejects
  withdraw)`, `validate.Result.Reason`); a misnamed scripted fault fails,
  and the strict-order advice is gone; validate names its artifacts
  directory and each step's file, and refuses an unknown shape with its
  keys and the shapes wanted; the skills say `.Expect(t, claims...)`, not
  a `qtest.Expect` that does not exist.
- **Z3. The newcomer's path. Done:** a glossary after Quickstart, whose
  second paragraph says what ran; the environment variables at the end;
  "a module of your own" in Install, and the installed skills say where
  examples/ lives. A scaffold that checks what the model says (lint
  refuses an empty constant; every boolean val listed and classified for
  simulate; an unclassified invariant that held holds). The faults
  skill's "this fault, at this point" (a scripted crash) and why a fault
  never fired. CLI help that says where to start, each command's purpose
  and example, `help` exit 0, `version`. godoc for the choosers' aliases,
  qtest's package doc opening with System, qobs.Record's keys. Every
  fenced go block parses and names only exported APIs
  (`internal/docsnippets`); the deny-list stays for prose. hello's
  equivalent mutants marked by function and code.
- **Z4. The API before tags (S–M).** One primitive between qtest and
  qhegel (`System.Judge(t, chooser) *Run` or similar) in place of
  `Settings`, `Resolve`, `ExploreConfig.Replay` and Shrink's fake TB; a
  claim recognised by type, not its description. About 35 identifiers
  that are plumbing (qobs Strict/Escaped/SetDiscard, qrun's hooks, qenv's
  LockKey functions for weave) or have no caller (Swarm, FaultOptions,
  qcover.Default/From, itf's RecordOf/TupleOf/SetOf, the untested sinks)
  move to an internal hook package or go. sktrace leaves the core (two
  examples use it); connect's fault scripts move next to FollowTraces.
  KnownFault unexported, its check scoped to the run. Workflow shapes:
  Rates in one place, `Sweep` on testing.TB, `SweepResult.Expect`,
  `Run`'s identity in one field, `Signal.Wait` on a duration. qweave's
  and qanalysis's x/tools aligned (or merged, now or never). Considered:
  typed options, making an ignored option a compile error rather than
  Y9's refusal.
- **Z5. Maintainability (S–M).** Strict/Escaped scoped to the run's
  recorder, so qtest's tests run in parallel (1m23s → ~30s estimated); a
  CI matrix for the serial job; a workspace-mode CI job so sub-modules
  meet the current core. ROADMAP's done items to HISTORY, AGENTS'
  histories to HISTORY (5.1k words loaded every session), the README's
  package table checked against api/*.txt, Go doc comments under the docs
  gate. Later: one exploration engine fed by run sources (Explore and
  Exhaust repeat their setup, Exploration has seven mode flags), one
  verdict method per claim, one memoizing checker for qtest and qhegel,
  qload's two recovery worlds made one.
- **Z6. A more faithful environment (S each).** Message delay in
  `Service`: a request is never late and a duplicate arrives at once, so
  "a late original overwrites the retry's result" cannot happen. Pause
  and gray-failure (slow, alive) nemeses. Disk errors on fsync, read
  corruption, ENOSPC. A duplicate double name panics. A determinism
  self-check (a seed run twice compares choices and steps) and lint for
  map ranges, math/rand and multi-case select in simulated code. A
  double's contract checked the other way too: it can do what its
  contract allows (X5 checks only that it does no more).
- **Z7. Liveness (S, then M).** Settle reports whether it settled (it
  returns nil when its time runs out with work pending) and a
  `Settles()` claim; a run ending at its deadline with non-daemon tasks
  waiting and no deadline of their own says so (a periodic timer once
  hid such a stall, Z1). Then obligations: a binding names a model def of
  pending work and `Eventually(...)` is judged in virtual time across
  the workflows; a mode where faults continue outside a chosen quorum
  (VOPR's); a fair scheduler for settle and liveness windows; steps
  recorded at invocation so a trace says "invoked, never completed".
- **Z8. Search and scale (M–L).** PCT as a chooser; a footprint-based
  reduction for Exhaust (the doubles know what each seam touches);
  Linearize per key (Porcupine's partitioning); exploration steered by
  coverage or model state; the history checker public, for real
  databases (an Elle-style workload); trace validation with unobserved
  or ambiguous steps (Jepsen's `:info`, hidden actions: the validator
  searches their resolutions). Online validation stays deferred.

## Dropped on purpose

- REVIEW S2's embeddable `qenv.Layer`: `qenv.As` and Wrapper cover the
  chain.
- REVIEW P2's `Together` and `qhegel.Want`: `Violation.Expect`
  superseded them.
- Renaming the CLI's subcommands.
- Rewriting history to drop a5cc809's 15 MB test binary: .git is 26 MB,
  and the docs cite commit hashes.
