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

Added to the core since api/core.txt was first recorded:

- `qtest.Takes(action).InAtLeast(p)` and `qtest.Exercised(except...)`:
  claims that an exploration's runs took the model's actions and faulted
  what they reached; `Evidence.Model` lists the actions never taken.
- `System.Exhaust(t, qtest.Bound{Delays, Faults})`: every run within the
  bound, fewest deviations first, as an Exploration; `qtest.Complete()`,
  `Evidence.Search`, `Run.Schedule` and `GOTOGETHER_SCHEDULE`.
- Real-time order: `qtrace.Step.Invoked` (`qobs.Record`'s `invoked`
  key, a converted span's start); Reconstruct reports an order its keys
  force that real time contradicts; `qtest.Linearize()` and
  `qtrace.Linearize` search the orders operations' intervals allow.
- `ExploreConfig.Corpus` (and `GOTOGETHER_CORPUS`): saved runs replayed
  before the seeds, a failed claim's run saved; `Run.Corpus`.
- qhegel: `Shrink` removes the faults the shrunk failure does not need
  (Hegel's shrinking stopped at a local minimum); `GOTOGETHER_HEGEL_SEED`
  overrides a search's seed.
- Vacuity: `binding.Model.Antecedents` (A of each `A implies B`
  invariant), a binding's `antecedents`, `Exploration.Vacuous`,
  `Evidence.Vacuous` and the claim `qtest.NotVacuous()`.
- `qenv.Disk` (writes, fsync, and a crash's "disk.crash" fault: lost or
  torn unsynced writes) and partitions between processes for
  `qenv.Service` (`Sim.Partition`, `Heal`, `Partitions` as nemeses).
- `qobs.SyncedFileSink`, `qobs.NewSyncedJSONLSink`: an fsync per step.
  `qtrace.ReadJSONL` keeps the steps before a torn last line and returns
  a `*qtrace.TornTail`; `input.File.Torn`.
- Clock faults: `Sim.ClockFaults` gives each process a clock that starts
  skewed and may jump ("clock.skew", "clock.jump"), read by `qenv.Now`;
  `Process.ClockOffset`. examples/lease.
- Under `GOTOGETHER_CHECK=1` every `qenv.Store` checks each commit's
  dependency cycles against its isolation level (G1a, G1c, G-single, G2).
- CheckBatch replays a chunk of several traces with quint's TypeScript
  backend unless one was chosen ($QUINT_BACKEND, Options.Backend): Rust
  starts an evaluator per test.
- `gotogether simulate -binding`: the model's simulation against each
  invariant the binding's new `simulate` section (`binding.Simulation`)
  says it guarantees (`holds`) or lets the code break (`allows`).

Before this, the project was quint-connect-go
(github.com/casselc/quint-connect-go); docs/HISTORY.md has the path here.
