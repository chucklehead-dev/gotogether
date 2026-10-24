# Changelog

Every published module (the core, qhegel, qweave, qanalysis, qgosim) is
versioned together; the examples are not published. A change to the
core's exported API updates `api/core.txt` and adds a line here.

## Unreleased

The first release. What a user meets, by module:

- `qtrace.LinearizeStates`: Linearize with a check that also gives each
  accepted order's model state; its complete search keeps one prefix of
  the same steps and state (Lowe's memo for Wing and Gong's search), so
  overlapping operations cost as many prefixes as states, not orders.
- qweave passed the go command its overlay in place of the first
  argument after the command: `qweave go test ./sim/` tested the current
  directory, `qweave go test -short ./x/` ran without -short, and
  `go test -c` ran the tests. Every argument now reaches go.
- `GOTOGETHER_ALL_FAULTS=1` turns every fault family on in every
  simulation: clock faults, stalls and a partition nemesis between every
  two processes, which are opt-in otherwise. `Network.PartitionNemesis`
  of a pair that is one already adds none.
- `ResolvePending` placed a pending call before the step after the last
  one recorded before a settle, which a step recorded after the settle
  could precede (a step at its instant on another recorder ordered
  later): a resolution the settle rules out could make a run conform.
  It now bounds the call by the first step recorded after the settle,
  and, as Reconstruct and Linearize do, takes two recorders' steps for
  concurrent within `RealTimeSlack`: a step just after a quiet settle on
  another machine bounds nothing.
- A claim some runs meet says, in its "as expected" line, when its
  seeds would miss it more than one time in a thousand at the rate it
  measured, and the seed count that would not.
- `GOTOGETHER_CHECK`'s store history checker classifies cycles as
  qhistory does (one classifier): it reports G-nonadjacent at snapshot
  isolation, which it took for write skew and let through.
- The toolkit's own mutation testing: `qweave -mutate -coverpkg` names a
  core package to mutate (never qmut, qrun, qcover or internal/hooks),
  `GOTOGETHER_MUTANT=<id>` turns one mutant on for a whole process
  (`=list` prints them), and `ci/mutate-core.sh <pkg>` runs its tests once
  a mutant and lists the survivors (nightly, for qtrace, qhistory,
  qsweep and internal/depgraph, each with its bar). Its first finding: Linearize's verdicts were unchecked by its
  unit tests, and a brute-force comparison then found a complete search
  cut short, with the guided search stopped, reported as a proof of no
  order: it is no verdict again (kvstore's stale read is proven on loads
  small enough for the default budget).
- An exploration's summary counts the runs that settled and those that
  left calls pending. A failing run's step log is what it recorded (the
  CLI's validate, with the same search, judges it as the test did), and
  a search's order is beside it (`.judged.steps.jsonl`);
  `GOTOGETHER_KEEP` keeps every run's step log (`runs/`), not only those
  a report describes.
- `gotogether scaffold -system` for a binding with eventual vals writes a
  Run that settles and claims `All(Settles())`. A client history (steps
  with invocation times) that fails in its recorded order says to check
  it linearized.
- A step log keeps an assumed step (`quint.assumed`, `Step.Ambiguous`):
  a resolution written out and validated again had its expect checked.
  `validate.InvariantResult.Eventually` marks an eventual val's result;
  `qtest.Run.Recorded` holds the steps as recorded beside `Steps`, the
  steps judged; `Chooser`'s doc gives the ChooseTask and ChooseNemesis
  protocols the scheduler looks for.
- The searches are validate's: `Checker.Search(steps, SearchOptions)` and
  `Search.Mark`, which qtest's Linearize, LinearizeByKey and
  ResolvePending call, and so does the CLI: `gotogether validate
  -linearize`, `-linearize-key key`, `-linearize-budget n`,
  `-resolve-pending`. `validate.DefaultLinearizeBudget` and
  `DefaultResolveBudget` (qtest's are the same).
- Linearize memoizes its candidates by prefix: an order that fails at a
  step accepts its prefixes before it and rejects the one through it, so
  the complete search starts from what the guided one learned and never
  replays an extension of a rejected prefix (quorumkv's Repick 209 s to
  138 s, BlindRepair 268 s to 116 s).
- `validate.Options.VerdictsOnly`: verdicts and where each failed, no ITF
  and no model state, the failing runs replayed on the batch's backend.
  Linearize's and ResolvePending's candidates are checked so (they read
  only where each failed; the detail phase had forced ITF and Rust back
  on), and an exploration's runs are searched side by side: quorumkv's
  Repick from 297 s to 209 s on four cores.
- Linearize prefers a conforming order in which the invariants hold: the
  first conforming order was taken, and a run could violate an
  invariant by the order picked (a mutant "caught" for nothing).
- qhistory: a transaction's own appends read back out of order are
  `internal` (the count alone was compared), and a value no transaction
  appended is garbage in any read, not "G1b of transaction 0" in a
  shorter one.
- Linearize's complete search is cut by its budget alone: a beam of 64
  prefixes made five concurrent operations no order explains no verdict
  whatever the budget, while the report said to raise it.
- ResolvePending places a pending call's effect before the first settle
  after its invocation: one placed after a probe that saw it missing made
  an eventual val hold and the run conform.
- A settle settles a trace only if no other recorder recorded a step
  later in time (a warning names it): a harness's settle while a server
  was still at work settled the run, and `Settles()` held.
- `Sim.ResumeFaults`: faults stay off after `Settle` (a probe runs on the
  settled system), which made every phase after a run's first settle
  fault-free without a word; it starts a faulty phase again, and Settle
  says so.
- Eventual vals no run judged are said so: a trace whose last record is
  no settle carries an issue naming the binding's eventual vals, an
  exploration in which none settled logs it, and a claim on an eventual
  val no run judged fails with the fix (a binding with only eventual
  vals let `None(Violates(...))` hold for nothing).
- A linearization (or resolution) cut by its budget is no verdict for
  the invariants and eventual vals of the order shown either: one that
  conformed step by step but failed an eventual val was reported as the
  run's violation.
- Linearize keeps the real-time order a recorder knows: a completion
  recorded by `Call.Done` takes its invocation record's time (no
  `"invoked"` needed; it overrides one), and steps of one recorder are
  ordered by seq, exactly. In a simulation calls take no virtual time
  unless something sleeps, and within `RealTimeSlack` every call was
  concurrent: a client's read that missed its own write, or a stale read
  after a signal, passed linearized. An actor of which no step has an
  invocation time is warned of (sequential consistency, not
  linearizability).
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

- Workflow shapes settled before tags: the environment's fault rates are
  the System's (`System.Rates`, `WithRates`) for Explore, Sweep, Mutate,
  Fuzz and qhegel.Shrink, where they were `ExploreConfig.Rates` and
  positional arguments of Fuzz and Shrink; `ShrunkTo` is a `RunClaim`
  (Run.Expect and a Violation's take it, an exploration's does not
  compile with it); `MutationReport.Expect` takes several claims;
  `Signal.Wait` takes a duration (0: no limit), as `WaitFor` does; a
  run's identity is `Run.ID()` ("seed=17", "schedule=17:1",
  "corpus=<file>"), its seed, schedule and corpus fields unexported.
  `System.FaultRates()` is the rates its workflows draw at (its Rates, or
  DefaultRates when zero): encode a shrunk failure for the fuzz corpus at
  them. `System.Judge` fails on steps recorded outside the run and on a
  Rates key that covers nothing, as an exploration does, and no longer
  skips an empty run (`CaseCheck.SkipIfEmpty` says whether the caller
  should). A negative `Signal.Wait` duration times out at once. A
  `Synced` step log on a writer without `Sync`, and a step recorded
  after `Close`, are the sink's error, not silently less durable or lost.
- One seam between qtest and a package that makes runs its own way
  (qhegel): `qtest.CheckCases(t, workflow, binding, opts...)` and
  `System.CheckCases` give a `CaseCheck` (the checker, how each case
  records), refusing what such a check ignores; `System.Judge(t,
  chooser)` runs and checks one run outside any exploration (Shrink's
  replays and its nominal run, sharing explorations' verdicts); and
  `Expectation.Holds(*Run)` judges a claim by its type. They replace
  `qtest.Settings` and `Resolve`, `ExploreConfig.Replay`, and qhegel's
  fake test and its string match on a claim's description, which
  misjudged `AnyOf(Conforms(), ...)` when Hegel found nothing.
  `qhegel.CheckAll` is unexported.
- `qenv.KnownFault` is internal: a process-wide query of what is
  declared would freeze that scope in the API. The workflows still fail
  a Rates key, a sweep's FaultKind or an exception that covers nothing.
- connect is model-based testing only: its fault scripts move next to
  FollowTraces, as `qtest.FaultMap`, `qtest.Fault` and
  `qtest.FaultScript`, and connect no longer links the simulator.
- `sktrace` leaves the core: SimKube's trace format is not the core's to
  freeze. It is examples/sktrace, an unpublished module fleetrollout and
  retrystorm use (`github.com/chucklehead-dev/gotogether/examples/sktrace`).
- Plumbing out of the public API, into the core's internal packages:
  `qobs.Strict`, `Escaped`, `SetDiscard`; `qrun.SetResolver`,
  `GoroutineID`, `Task`; `qenv.Current`, `TaskPanic`, `WithPanicStack`
  and the eight `LockKey`-style functions woven sync calls (qenv/weave
  reaches them through qenv/internal/keyed). Gone, with no caller:
  `qenv.FaultOptions`, `qcover.Default`, `qcover.From`; unexported:
  `qobs.Normalize`, `qobs.Attributes`, `connect.Diff`,
  `validate.Validate` (use a Checker), `qenv.WaitUntil` (use `WaitFor`);
  `qtrace.Fields` moved into the CLI. The five step-log sink
  constructors are two, by durability: `qobs.FileSink(path, d)` and
  `NewJSONLSink(w, d)` with `PerStep` (the old FileSink), `Synced` and
  `Buffered`. `qenv.Swarm` stays, now tested.
- A more faithful environment: a `qenv.Service`'s request may arrive
  late (`rpc.<name>.delay`: `late`, just after its caller's timeout;
  `veryLate`, a timeout after it, after a retry that backed off;
  `lateDuplicate`, a copy that late; `slow`, late but in time), so a
  late original can land after its retry; a second double of one kind and name in a
  simulation panics (they shared their fault kinds); `Sim.Stalls`
  pauses a process (`nemesis.pause`) or keeps it alive but slow
  (`nemesis.slow`); a `Disk` fills (`ErrNoSpace`), fails an fsync
  (`ErrIO`, whose writes a power loss then drops, as Linux does) and
  returns corrupt reads; an exploration of
  seeds runs its first seed twice and fails on the first choice or step
  that differs.
- Liveness: `Sim.Settle` returns a `*qenv.Unsettled` (`ErrUnsettled`)
  when its time runs out before the system is quiet, a task that is no
  daemon still at work or waiting; an exploration judges such a run
  rather than fail it (`Run.Unsettled`, `validate.Report.Unsettled`): it
  does not conform, and `qtest.Settles()` states it alone.
- examples/quorumkv: a replicated store (ABD over three replicas, hinted
  handoff) whose mutants each need one of SettleCore, Completes,
  `eventually`, PCT or LinearizeByKey; its first version re-picked a
  version on retry, which a linearized history showed.
- `qtest.Pending(action)`: a run left a call of the action invoked and
  never completed (`Some(Pending("put"))`).
- Linearize finds an order its guided search misses: when that search
  finds none, every order real time allows is searched, prefix by prefix
  (rejected prefixes pruned), and finding none there, within budget, is a
  proof. A candidate carries the run's settle, so an order whose eventual
  val fails (the probe saw another last write) is not taken.
- ResolvePending: an assumed step is in the process of the step it
  follows (no inferred restarts), re-sequenced steps drop their order
  keys and call ids (a completed call's id named another record), a call
  takes effect only after every step recorded before its invocation, and
  a resolution the binding cannot replay is skipped. Linearize drops call
  ids on re-sequencing too.
- Steer refuses a Chooser factory (its choosers pick tasks and nemeses
  the prefix cannot replay) and System.Mutate (each mutant would steer
  its own sample); it replays entropy chunks (gosim); a steered run's file
  is under its package's directory and replays only in its exploration.
  The determinism self-check of a Chooser factory's runs replays the
  first run's choices, whatever the factory hands out.
- qhistory: G-nonadjacent (two or more rw edges, never adjacent:
  forbidden under snapshot isolation, which allows write skew),
  duplicate elements in a read, and a transaction id 0 refused.
- A step recorded in a simulation carries the simulation's virtual time,
  not the wall clock's (its tasks' and its creator's: `internal/hooks`
  Now), so a client history recorded there has its intervals; and
  explorations run Linearize, LinearizeByKey and ResolvePending, each
  run's search on its own (they were refused).
- `qtest.ResolvePending()`: Record and Validate search what each pending
  call may have done (no effect, or an effect after a step recorded
  since its invocation, unobserved: `qtrace.Step.Ambiguous`, no expect)
  and accept the run if a resolution conforms, saying which.
- `ExploreConfig.Steer`: runs steered toward what the seeds rarely
  reach, each replaying a run that found a new step, observed state,
  fault or code block up to a random choice, then on a fresh seed; a
  steered run's choices are saved, and `GOTOGETHER_RUN=...:choices=<file>`
  runs it alone.
- `qhistory`: a database's isolation level checked from its clients'
  history: Elle's list-append workload (`Workload.Generate`) and
  `Check(level, txns)`, which infers each key's version order from the
  reads and reports Adya's anomalies (G0, G1a, G1b, G1c, G-single, G2,
  incompatible orders, garbage, internal), forbidden or allowed by the
  level. Checked against qenv's Store at each of its levels.
- `qtest.LinearizeByKey(arg)` partitions each actor's history by an
  argument's value and linearizes each partition alone, as its own actor
  (Porcupine's partitioning), for models whose keys are independent.
- `qenv.NewPCT(base, seed, depth, steps)` schedules by Probabilistic
  Concurrency Testing (random task priorities, depth-1 change points):
  bugs that need one task to run far ahead of another, which a uniform
  schedule practically never makes. A chooser that implements
  `ChooseTask(runnable []int)` picks the task by id; its picks are
  recorded as `sched.task` choices, so its runs replay without it.
- `qobs.Invoke(ctx, action, kv...)` records an operation when it is
  invoked and `Call.Done(err, kv...)` when it completes: a trace keeps
  an invocation never completed (`qtrace.Trace.Pending`,
  `validate.Result.Pending`, "invoked, never completed" in a report),
  and `qtest.Completes()` states that every call completed. Steps carry
  `Invocation` and `Call` (`quint.invocation`, `quint.call`).
- `Sim.Settle` takes runnable tasks in turn (a task spinning until
  another acts no longer starves it), and `Sim.SettleCore(d, core...)`
  keeps faulting every process outside a core while the core must finish.
- A settle is in the trace: `Sim.Settle` records one (a step with
  `Settled`, `quint.settled`) when it finds the system quiet, and
  `qobs.Settled(ctx)` records one where a deployment knows. A trace whose
  recorder recorded nothing after it is `qtrace.Trace.Settled`
  (`validate.Result.Settled`); only its eventual properties are judged,
  by every check, and `qtest.Settles()` requires it. A Settle that
  finds the system stuck returns an `*Unsettled` too (`Unsettled.Stuck`).
- qhegel: `Shrink` judges a run whose system does not settle as a
  violation (an exploration's verdict) instead of failing the case as one
  that "did not run"; an eventual property's failure rejects no prefix in
  the case checker's cache; a case's notes and `Violation.Last` skip
  invocation and settle records.
- A binding's `eventually` names vals that must hold when a run ends
  (liveness obligations: no work pending), checked in its last state;
  a result names each `validate.EventuallyName(val)`, and an exploration
  judges them only on a run whose system settled.
- `qtest.NoSurvivors` marks an equivalent mutant by function, change and
  code (`"(Counter).Add: drop call: qobs.Record(...)"`), as a survivor's
  report now prints it: a line added above no longer moves the mark. A
  mark of that form naming two mutants (two `if err != nil` in one
  function) fails, asking for the position.
- `qenv.Script.Misnamed` says which scripted faults cannot happen as
  written (a kind no double declared, matched exactly: no prefix, no
  nemesis family; an option the kind lacks; the nominal option; a Follows
  of no other fault), and an exploration fails on each; it never fired,
  and the claim it was for failed as if the code survived it. Script no
  longer panics in the run on them. A well-named scripted fault a run
  never made (`Run.Unscripted`) fails the exploration too, whatever its
  claims: a crash "after step 300" of a shorter run passed without
  crashing.
- `gotogether validate` ends `RESULT: PASS (3 actors conform to the
  model and satisfy its 2 invariants)` or `RESULT: FAIL (2 of 3 actors: 1
  mismatch after deposit, 1 rejects withdraw)`, in the claims' words
  (`validate.Result.Reason`), where it said `VIOLATION` for any failure;
  it names its one artifacts directory, a step log's step names its file
  and line (`Step.Source` `runs/a.jsonl:5`, `input.Read`), and an input of
  no known shape is refused with its keys and a step log's
  (`input.StepLogExample`).
- A state mismatch's report prints the binding's expect as written
  (`binding.Call.ExpectTemplate`, `validate.Failure.ExpectTemplate`)
  under the rendered one, and the model's state after the step in a
  batch's report too (a chunk replayed on TypeScript has its failures
  replayed on Rust, which writes their ITF).
- Two incarnations of a process overlapping (an old one acting after its
  successor started) is an `error overlap` issue, not a warning: a run
  that passed with it fails. Fence the old incarnation, or order the
  steps with a domain clock.
- A Queue's broker process that is down is unreachable, and its crash
  drops its consumers' connections; held messages come back oldest first.
- `connect.Config.Dir` is one run's: its earlier traces are removed.
- `itf.DecodeProjection`, which connect's state check decodes with: a
  StateDriver's state type with an unexported field, or a field the
  model's record lacks (a misspelling), fails the check instead of
  comparing nothing.
- `qtest.Takes(action).InAtLeast(p)` and `qtest.Exercised(except...)`:
  claims that an exploration's runs took the model's actions, each option
  of the fault kinds they reached and each nemesis they enabled, by name
  (`Run.Nemeses`, `qenv.ChoiceLog.Nemeses`; a heal is not wanted);
  `Evidence.Model` lists the
  actions never taken. `Expect` and `System.Mutate` judge a claim by one
  verdict, so NotVacuous kills mutants too.
- Evidence is a format (`Evidence.Format`, `qtest.EvidenceFormat`
  `gotogether.evidence/2`, whose qenv.Coverage keys are lower case,
  `IsEvidence`) with a role (faithful, mutant, replay: `System.Mutant`,
  `ExploreConfig.Replay`); `GOTOGETHER_EVIDENCE=dir` writes it as
  dir/<package>/<test>/x<N>.json, cleared per test; `gotogether evidence` and
  `qcover.ReadSummary` read by format, and the suite's gaps count
  faithful explorations only.
- An option a workflow ignores fails it, saying why: `Global()` or
  `Linearize()` in a System's Opts, `SkipIfEmpty()` in Validate,
  `Linearize()` in qhegel's Opts.
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
- `gotogether evidence DIR`: a suite's explorations, from the evidence
  each writes (under GOTOGETHER_EVIDENCE, as below), and what no faithful
  exploration tried; `-strict` fails on a gap. Evidence names its `Test`
  (TestX#N), `Package` and `Spec`; `qtest.ReadEvidence`,
  `qcover.ReadSummary` (a Summary or evidence, read by `gotogether cover
  -sim`).

Before this, the project was quint-connect-go
(github.com/casselc/quint-connect-go); docs/HISTORY.md has the path here.
