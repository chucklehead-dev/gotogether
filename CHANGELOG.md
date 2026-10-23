# Changelog

Every published module (the core, qhegel, qweave, qanalysis, qgosim) is
versioned together; the examples are not published. A change to the
core's exported API updates `api/core.txt` and adds a line here.

## Unreleased

The first release. What a user meets, by module:

- **The core** (`github.com/chucklehead-dev/gotogether`). The model drives
  Go (`connect`); trace validation (`qobs`, `qtrace`, `otelio`, `convert`,
  `binding`, `validate`, `input`); simulated environments with fault
  injection (`qenv`, `qtest.System`); metastability (`qload`, `qmeta`,
  `qrole`); coverage, mutants and sweeps (`qcover`, `qmut`, `qsweep`); the
  `gotogether` CLI and the agent skills. Its dependencies: OpenTelemetry
  and yaml.v3.
- **qhegel**: Hegel drives and the model judges and shrinks.
- **qweave**: weaving into a `go -overlay` (`//quint:action`, qenv's
  sync, coverage, mutants).
- **qanalysis**: go/analysis passes (`quintvet`, a golangci-lint plugin)
  and the static action map.
- **qgosim**: qenv and whole programs in gosim, on the fork
  `github.com/chucklehead-dev/gosim`.

Each published module's API is recorded in api/ (core.txt, qhegel.txt,
qweave.txt, qanalysis.txt, qgosim.txt with its `sim` side), fields
(with tags), methods, constants' and variables' values included;
`internal/apisurface` fails on any unrecorded change.

Added to the core since api/core.txt was first recorded:

- `qtest.Takes(action).InAtLeast(p)` and `qtest.Exercised(except...)`:
  claims that an exploration's runs took the model's actions, each option
  of the fault kinds they reached and each nemesis they enabled, by name
  (`Run.Nemeses`, `qenv.ChoiceLog.Nemeses`; a heal is not wanted);
  `qenv.FaultOptions` names a declared kind's options; `Evidence.Model` lists the
  actions never taken. `Expect` and `System.Mutate` judge a claim by one
  verdict, so NotVacuous kills mutants too.
- Evidence is a format (`Evidence.Format`, `qtest.EvidenceFormat`,
  `IsEvidence`) with a role (faithful, mutant, replay: `System.Mutant`,
  `ExploreConfig.Replay`); `GOTOGETHER_EVIDENCE=dir` writes it as
  dir/<test>/x<N>.json, cleared per test; `gotogether evidence` and
  `qcover.ReadSummary` read by format, and the suite's gaps count
  faithful explorations only.
- An option a workflow ignores fails it, saying why: `Global()` or
  `Linearize()` in a System's Opts, `SkipIfEmpty()` in Validate,
  `Linearize()` in qhegel's Opts (`qtest.Settings.Refuse`).
- Nemeses are chosen per family (`fault.nemesis.crash`, `.partition`,
  `.jump`, a double's own), each at its rate: Rates keys
  `nemesis.crash`, `nemesis.partition`, `nemesis.jump` (`nemesis` covers
  them). A crash-only run draws the same values as before, but its
  choices' kind is renamed (`fault.nemesis` to `fault.nemesis.crash`), as
  is its coverage point (`Coverage.Point("nemesis.crash")`): a corpus
  entry or a recorded run with crashes no longer replays and fails as
  stale, with the seed that finds its run again. One with partitions or
  jumps takes other choices than before.
- `System.Exhaust(t, qtest.Bound{Delays, Faults})`: every run within the
  bound, fewest deviations first, as an Exploration; a search `MaxRuns`
  cuts short fails unless `Bound.Partial`; `Evidence.Search` and
  `Run.Schedule`.
- `GOTOGETHER_RUN='TestX#2:seed=17'` (or `:schedule=`, `:corpus=`) runs
  one run of one exploration alone, as a failing run's report prints it,
  judged by the claims about every run only; it replaces
  `GOTOGETHER_SEED` and `GOTOGETHER_SCHEDULE`, which every exploration of
  the test read. A failure's step log and module are copied out of the
  test's temporary directory, and a cached verdict is checked again so
  its module is there.
- Real-time order: `qtrace.Step.Invoked` (`qobs.Record`'s `invoked`
  key, a converted span's start); Reconstruct reports an order its keys
  force that real time contradicts; `qtest.Linearize()` and
  `qtrace.Linearize` search the orders operations' intervals allow.
- `System.Mutate(t, cfg, claims...)`: claims the baseline meets and a
  mutant is killed by failing; a mutant whose runs record nothing is
  killed. examples/hello's mutation test ends in `NoSurvivors`.
- `ExploreConfig.Corpus` (and `GOTOGETHER_CORPUS`): saved runs replayed
  before the seeds, a failed claim's run saved; `Run.Corpus`. An entry
  (format `gotogether.run/1`) names the exploration that saved it, which
  alone replays it; one that no longer replays fails as stale, with the
  seed that finds its run again.
- qhegel: `Shrink` removes the faults the shrunk failure does not need
  (Hegel's shrinking stopped at a local minimum); `GOTOGETHER_HEGEL_SEED`
  overrides a search's seed.
- Vacuity: `binding.Model.Antecedents` (A of each `A implies B`
  invariant), a binding's `antecedents`, `Exploration.Vacuous`,
  `Evidence.Vacuous` and the claim `qtest.NotVacuous()`, which fails
  when it cannot judge (no antecedent, or one quint cannot evaluate).
- `qtest.LinearizeBudget(n)` and `DefaultLinearizeBudget`: a search that
  runs out of its budget is no verdict (conformance n/a), not a
  rejection. `qtrace.Linearize` returns whether the budget cut it short.
- `qenv.Disk` (writes, fsync, and a crash's "disk.<name>.crash" fault:
  `powerLoss` or `tornWrite` of unsynced writes) and partitions between
  processes, on the simulation's `Network` (`Sim.Net()`: `Partition`,
  `Heal`, `Partitioned`, `OnCut`, `PartitionNemesis`, `CutLength`), for
  `qenv.Service` and for a `qenv.Queue` whose broker runs in a process
  (`NewQueue(s, name, p)`: a broker given at construction, not a field
  to forget); a nemesis's cut heals by itself after `CutLength`.
- Fault kinds per instance: `queue.<name>.ack`, `store.<name>.commit`,
  `disk.<name>.crash`, as `rpc.<name>.*` (`"queue"` covers every queue's;
  `"queue.ack"` matches none, and an exploration whose Rates key, or a
  sweep whose FaultKind, covers no kind declared fails, naming it:
  `qenv.KnownFault`). `Process.Spawn` starts a task in a
  process from outside it. The new fault models run on the native
  runtime (`TestNativeFaultModels`).
- `qobs.SyncedFileSink`, `qobs.NewSyncedJSONLSink`: an fsync per step.
  `qtrace.ReadJSONL` keeps the steps before a torn last line and returns
  a `*qtrace.TornTail`; `input.File.Torn`.
- Clock faults: `Sim.Clocks{Skew, Jump, Drift}`. A process has a wall
  clock (`qenv.Now`: skewed, jumping by a nemesis) and a monotonic one
  (`qenv.Mono`, `qenv.Since`: what `time.Since` measures; a drift moves
  both); `qenv.WaitFor`, `Process.ClockOffset`, `Process.ToVirtual`/`ToReal`.
  examples/lease.
- A run that panics fails as a run (`qtest.RunPanic`, with the task's
  stack through `qenv.WithPanicStack`), named, kept in the corpus and
  with its reproduce line; it used to end the test binary.
- Under `GOTOGETHER_CHECK=1` every `qenv.Store` checks each commit's
  dependency cycles against its isolation level (G1a, G1c, G-single, G2),
  and every `qenv.Queue` its at-least-once contract after each operation.
- CheckBatch replays a chunk of several traces with quint's TypeScript
  backend unless one was chosen ($QUINT_BACKEND, Options.Backend): Rust
  starts an evaluator per test.
- `gotogether simulate -binding`: the model's simulation against each
  invariant the binding's new `simulate` section (`binding.Simulation`)
  says it guarantees (`holds`) or lets the code break (`allows`);
  `-verify N` checks every trace of up to N steps with quint verify
  (Apalache) instead of sampling.
- `gotogether evidence DIR`: a suite's explorations, from the evidence.json
  each writes under GOTOGETHER_KEEP (one per exploration, in its own
  directory, named by its TestX#N), and what no test tried; `-strict` fails on a gap. Evidence
  names its `Test` and `Spec`; `qtest.ReadEvidence`, `qcover.ReadSummary`
  (a Summary or an evidence.json, read by `gotogether cover -sim`).

Before this, the project was quint-connect-go
(github.com/casselc/quint-connect-go); docs/HISTORY.md has the path here.
