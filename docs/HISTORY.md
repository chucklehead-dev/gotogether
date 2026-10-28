# History

The plans that are done, in the order they were made, and the reviews that
led to them. What is open is in ROADMAP.md; why the code is the way it
is, in DESIGN.md.

## The first roadmap (A–D, U), the second (V, R) and the third review (W)

What comes after DESIGN §15–16: connecting code coverage to the model,
Go's profiling and tooling, and simkube's lessons. Each item says what it
answers and where it goes. Tracks run in parallel; within a track, items
are in order. The unification track (U) comes from an architecture and
API review, and its items land before the features that would otherwise
add a second way to do the same thing.

## A. Code coverage against the model

The fault coverage of §15.11 says which faults runs reached. These items
say which code they ran, and what the model says about it.

- **A1. Per-step code coverage. Done (DESIGN §17).** qweave instruments
  blocks (`qweave -cover`) for qcover; each block finds its run and task
  through qrun, and the blocks a task runs between its steps are the later
  step's. Go's own `-cover` counters are global to the process and their
  file format is internal, so a run in parallel with others could not be
  told apart.
  - Gives: which blocks each run ran; for each action and outcome, the
    blocks its steps ran; code run by no step of its task; outcomes of an
    action that ran the same code (the model tells them apart, the code
    does not).
  - Next: an uncovered block after a fault point or double call whose kind
    was never faulted (an error path the exploration did not try; needs
    S3's declared kinds), and a static pass in qweave for blocks no
    function reaches (dead, as `deadcode` finds).
- **A2. Mutation testing, woven by qweave. Done (DESIGN §26).** A mutant per covered block
  (a flipped condition, a dropped statement), each explored. A survivor
  in covered code is code the model does not constrain: the sharper
  answer to "what is this code for?".
- **A3. Go fuzzing over the Chooser's entropy.** A fuzz input is a run:
  `f.Fuzz(func(t, data []byte))` with a chooser fed by data, the model as
  oracle, the corpus as regressions. Coverage-guided exploration, which
  seeds and Hegel are not.

## B. Profiling with the simulation and the CTMC

- **B1. pprof labels by role and action.** qrole's annotations (and
  `//quint:action`) also set `pprof.Do` labels: a CPU profile by model
  action and server.
- **B2. Measured service times.** A benchmark's per-action latencies
  become `qenv.Capacity.Time` (an empirical distribution) and the chain's
  μ, with a check of the exponential assumption (coefficient of
  variation).
- **B3. Flight recorder on alert.** `runtime/trace.FlightRecorder`
  snapshots the last seconds when the rule from `Region.Rules` fires
  (qrole has the gauges): the storm arrives with its execution trace.
- **B4. A virtual-time profile.** Task stacks weighted by virtual time
  spent in each seam, in pprof's format: where a storm's latency goes. In
  its own module (pprof's protobuf).

## C. Go tooling

- **C1. Analyzers.** `gotogether lint -sim` and `-weave` as
  `golang.org/x/tools/go/analysis` passes (gopls, golangci-lint), and a
  new one checking `//quint:action` arguments against the binding.
- **C2. `testing/synctest` as a runtime.** Real goroutines and channels
  with fake time, in-process: qenv's native runtime in a bubble.
  Deterministic time, not schedules: for timing behaviour.
- **C3. Coverage of real environments.** Binaries built with `-cover`
  (Maelstrom, kwok, kind runs), merged with `go tool covdata`, paired
  with the same runs' validated steps: A1's report from a real
  environment.
- **C4. `log/slog` as a step source.** A handler recording steps from
  records with a given attribute, like `convert` for spans.
- **C5. Static action → code.** `go/ssa` call graphs of annotated actions,
  compared with A1's dynamic map.

## D. simkube

simkube (MIT, Rust) captures and replays Kubernetes clusters, with no
determinism, faults or oracle: a source of workloads, not a replacement.

- **D1. Trace reader** (msgpack, v3) in an example module: applied and
  deleted objects become timed tasks against kubesim (fleetrollout,
  crossdb), so explorations search faults around a real workload.
- **D2. Trace → `qload.Shape`.** Event and pod-creation rates per tick: a
  measured arrival process for `qmeta.Calibrate`, and production spikes
  as candidate triggers for `FindTrigger`, scalable beyond replay speed.
- **D3. Pod lifetimes.** KWOK Stage configurations for the kwok demos, and
  kubesim status lag drawn from a trace's lifetime index (a chooser kind,
  option 0 nominal).
- **D4. Sanitizing and selecting step logs** before validation, for
  production OTLP (a `gotogether` subcommand or `convert` rules).
- **D5. Scenario lint**: a scenario's dependencies exist before a run.

## U. Unification

From the architecture and API review (the reviews, below): where the library
has grown two ways of doing one thing, and the lessons of §15–16 turned
into structure. In order, each before the features that need it:

1. **S1 qrun** (done with A1): one lookup of a run's state.
2. **P2 quantified expectations** and `Expect` on Violation and Recovery
   (before A2: "killed" needs `Some`/`None`).
3. **S4 one run record** with choices aligned to steps (before A3, and
   completes A1's alignment).
4. **P1 `qtest.System`** with an error-returning `Run` and a chooser
   option; P3 Check/Validate folded into it.
5. **S2 chooser `Unwrap`** and S3 declared fault kinds with named options
   (before D3).
6. **P5 run-declared constants**, P6 messages, P4 one recovery API.
7. **S5 one hook entry** (before B1), S6 small duplicates (before C1),
   S7 the orbit's definition.
8. **P7 skills restructured**, P8 one evidence report (as A2 and A3 land).

## Order

A1 first, with S1: it joins the fault coverage we have with code coverage,
and A2 and C3 build on its report. Then U2–U4, D1–D2 (they change what
simulations are fed), B1–B2 (they feed the CTMC measured values), A3, and
the rest, each after the U items it depends on.

## Status

- A1: per-step code coverage, done (DESIGN §17). Its report's next
  classifications (an uncovered block after an unfaulted fault point,
  `deadcode`) wait for S3's declared kinds and the static pass in qweave.
- S1: done (qrun).
- U2 (P2): done (DESIGN §18): `Some`, `All`, `None`; `Violation.Expect`.
  `Expect` on `qmeta.Recovery` goes with P4's one recovery API.
- U3 (S4): choices aligned to steps (DESIGN §19): `Choice.Step`, Script
  waiting on the run's recorder, `Recorder.Env`, `Violation.Choices`
  replayable.
- U4 (P1): `qtest.System` (DESIGN §20): Explore, Sweep, FollowTraces,
  `qhegel.Shrink`; `Run` returns an error; `ExploreConfig.Chooser`.
  Next in it: one `Run` type across explorations, Violations and qgosim;
  P3 (Check vs Validate); `gotogether scaffold` printing a System.
- D1–D2: done (DESIGN §21): `sktrace`, `qload.Piecewise`, retrystorm under
  a production scale-up, fleetrollout under a replayed workload (and the
  model's `scale` for other writers).
- B1–B2: done (DESIGN §22): pprof labels by role; `qmeta.Measure`, with a
  verdict that changes with the service-time shape.
- A3: done (DESIGN §23): `qenv.Bytes`/`FromBytes`/`EncodeChoices`,
  `System.Fuzz`, `VerdictCache`; CI fuzzes checkout for 60 s.
- U5: chooser `Unwrap` and `qenv.As` (DESIGN §24); fault options by name,
  additive (DESIGN §25: `Faults`, `FaultOf`, `Only`, `Scripted.Name`). The
  `fault.` prefix in rates stays until a breaking release.
- A2: done (DESIGN §26): `qweave -mutate`, `qmut`, `System.Mutate`; on
  checkout it found that no step carried the stock after a cancel (fixed
  in the binding), and that refused orders record no step (a model change,
  open).
- U6: done (DESIGN §27): trap messages (constants at step 1, unscripted
  faults, `GOTOGETHER_SEED`, unhealthy baselines, λ ≥ capacity); factors
  naming their model constants; one `FindTrigger` over `qmeta.System`
  with `Expect`.
- U7: done (DESIGN §28): the chain's v is the hindsight orbit, qrole's the
  waiting part (`Sample.Waiting`, `TestTwoOrbits`); the lint out of qenv
  (`internal/lint`); S5 superseded by B1.
- U8 (P8): one evidence report (DESIGN §29): `Exploration.Evidence`,
  `Swept.Evidence`. Coverage gaps as expectations and a JSON form wait;
  P7 (the skills restructure) is next.
- Choreo (DESIGN §30): examples/twophase, a Choreo model with named
  transitions; no toolkit change needed.
- C1, C5: done (DESIGN §31): qlint, qanalysis (quintvet, golangci plugin,
  quintaction), actionmap comparing static reach with A1's coverage.
- U8 (P7): done (DESIGN §32): `gotogether-start`, references on demand, one
  verdicts reference.
- B3, B4: done (DESIGN §33): `qmeta.Watch`; `qenv.Profile` (pprof,
  hand-encoded, no dependency).
- C2: done (DESIGN §34): `NewNative(c, nil)` in a synctest bubble, as is.
- C3: done (DESIGN §34): `qcover.ReadGoCover`, `Compare`, `gotogether cover`.
- C4: done (DESIGN §35): `qobs.SlogHandler`.
- D4: done (DESIGN §36): `qtrace.Filter`, `gotogether steps -out`.
- D5, D3: done (DESIGN §37): `sktrace.Lint`; `Lifetimes`, `Quantiles`,
  kubesim `PodTimes`. KWOK Stages from a trace: deferred (no kwok here).
- A1's classifications: done (DESIGN §37): `actionmap` `Dead`,
  `UntriedErrorPaths`.
- Examples beyond the roadmap: examples/grpcledger (gRPC, simgrpc,
  otelgrpc spans; DESIGN §38) and examples/streams (Kafka, Kinesis, a
  simulated log; DESIGN §39). HTTP is examples/kvstore.

## Next: the second unification (V) and a first release (R)

From the second architecture and design review (the reviews below, "After
the roadmap"). The first roadmap is done (Status above) with five planned
things left open: P3 (Check vs Validate), S4's one run record, S2's
context out of the chooser, S6's directive parser and step attributes,
P4/P6's `Recovery.Expect` and `qmeta.Healthy`. The examples of DESIGN
§38–40 and a first hour by a reviewer found the rest. In order; each
names what it needs.

- **V0. Verdicts keyed by instance. Done.** `VerdictCache` keyed verdicts
  by steps only, so a sweep's rows with different constants could share
  one; the key now holds the binding, its constants and the invariants.
- **V0'. Claims checked against the model. Done.** An expectation naming
  an action the model lacks or an invariant the runs did not check fails
  at once: `None(Violates("typo"))` held for nothing.
- **V1. Binding template namespaces.** Arguments also under `.args.X`;
  `process`, `outcome`, `obs`, `step` as argument names a lint error with
  the fix (they overwrite a same-named argument). Before any new example.
  *Done:* instead of a lint error, a parameter of a reserved name defaults
  to `.args.X` (the argument), so no binding needs a template for it.
- **V2. Stalls are errors.** `Sim.Run` returns `ErrStuck` with
  `Sim.Tasks()` when tasks other than daemons wait with no deadline;
  waits that may block forever declare it (`qenv.Daemon(ctx)`, the
  doubles' receives). Explore reports it as a run error, not a verdict.
  *Done:* `qenv.Daemon`, `qenv.WaitIdle` (Queue.Receive, simlog's Poll);
  the scheduler only (the native runtime runs in real time).
- **V3. Runs without a simulation: `qrun.Start`.** It registers the
  goroutine; `qtest.Record` and `qhegel.Recorder` use it instead of
  `qobs.SetGlobal`; `qobs.Strict` becomes a run key; `qenv.New` inherits
  `qrun.Current()`, and `qenv.WithContext` is deprecated (S2's rest).
  *Done, in part:* `qrun.Start`, used by Record and Install alongside the
  global recorder (goroutines a scenario starts cannot be followed);
  `qenv.New` inherits the run. Strict stays process-wide: a step that
  finds no recorder finds no run either, so a run key could never be
  read. WithContext stays: a chooser handed to another goroutine (or to
  gosim) carries its run, which a goroutine registration cannot.
- **V4. One run record** (S4's rest; needs V3). `qtest.Run{Seed, Steps,
  Choices, Faults(), Interrupted, Unscripted, Code, Err, Report}` for
  `Exploration.Runs`, `qhegel.Violation` (embeds it) and qgosim;
  `qgosim.Scenario` returns `func(qenv.Chooser) error`, so a gosim run is
  a `qtest.System`; `System.Sweep` takes the System's own `Run`, the row in
  the chooser's context. *Done:* `qtest.Run` is every workflow's record
  (`Explored` an alias, `Violation` embeds it); `qgosim.Scenario` is a
  System's Run; `Sweep(t, sw, nil)` runs the System's Run with
  `qtest.RowOf(c)`.
- **V5. One entry point** (P3's rest; needs V4). `qtest.Check`,
  `RecordAndCheck`, `qtest.Explore(binding, …)`, `SweepExplore`
  deprecated for `Validate`/`Record` + `.Expect` and `qtest.System`;
  README §3, gotogether-metastability, gotogether-traces and
  instrumentation.md updated. `Explore`, `Sweep`, `FollowTraces`, `Mutate`
  take `testing.TB`; `Fuzz` takes an `ExploreConfig`. *Done,* but `Sweep`
  keeps `*testing.T` (a subtest per row needs `t.Run`); every in-repo
  caller moved to Record/Validate + Expect or a System.
- **V6. First hour.** `gotogether scaffold -system` prints a runnable System
  test with a mutant claim; `examples/hello` (one service, one retry, one
  dedupe mutant, under 150 lines) is the README's first simulated
  example; examples/bank gains a System test. *Done:* scaffold -system
  (its test compiles, fails until Run records steps, and skips the
  mutant claim until one is injected); examples/hello (80 lines, in CI's
  demos); bank's `TestTellersAsASystem`.
- **V7. Messages, second pass.** A missing step field names the fields
  the step has and the `args:` fix (no Go template text); an unknown
  action says "did you mean" and lists them; QNT508 out of reports; one
  step numbering; failing reports print the instance's constants; the
  strict-recording panic is a failed run, not a dead test binary; "a
  apply" and "do not show the run conforms" reworded. *Done:* reports
  number steps from 1 ("step 2"), the recorded seq and source after the
  step; quint's error line only for errors the report cannot read; a
  failing report's `instance:` line; a missing field names the step's
  fields and the `args:` fix; unknown actions say "did you mean"; a
  strict run's escaped step is logged (`qobs.Escaped`) and fails the
  exploration.
- **V8. Quiet Hegel, and claiming nothing found.** `FindViolation` and
  `Shrink` default to Hegel's quiet verbosity and no reproduction blob
  for an expected failure (`GOTOGETHER_VERBOSE=1` restores them);
  `Shrink(...).Expect(t, qtest.Conforms())` states that Hegel finds
  nothing, as `FindTrigger(...).Expect(t, false)` does. *Done:* Hegel's
  os.Stdout is captured while it runs (it has no other output) and shown
  only for an unexpected outcome; examples/hello's TestHegelShrinks.
- **V9. Small duplicates, second pass.** One directive parser (a golden
  table binding and qweave both run); `qobs.Attributes` from
  `qtrace.Step.Attributes`; one checker cache and steps key for qtest and
  qhegel; simdeps lists qmut, qenv/weave and examples/twophase, or
  derives its list from qenv's importers. *Done:* binding splits
  directives as qweave does (it split `f(a, b)` in two), one table both
  test; `qobs.Attributes` is `Step.Attributes` as OTel attributes;
  `validate.Cached` and `qtrace.Key`/`Keyer` serve qtest and qhegel
  (qtest's cache handed a later test an earlier one's Dir and NoITF);
  simdeps adds every importer of qenv but qhegel.
- **V10. Package boundaries.** `qobs.OTelSink` to `otelio` (qobs and qenv
  OTel-free: smaller gosim translations); qmeta keeps the chain and
  `Recovery`, `Recovers`, `Assess`, `Of`, `OfSimulation`, `System`,
  `TriggerSpace`, `Runs`, `Count`, `Mostly`, `Simulate` move to qload
  (qmeta independent of qenv again; one `System` name); qenv's choosers
  and byte rule to `qenv/choice`, `Profile` to `qenv/profile`. *Done,*
  but OTelSink stays in qobs: qobs reads a step's thread from the span
  in its context, which needs otel/trace, and that imports
  otel/attribute, so moving the sink would break an API and save
  nothing. The recovery API and `SimulateTrajectory` (was
  `qmeta.Simulate`) are qload's; qmeta imports nothing of the module.
  qenv re-exports choice and profile (aliases), so callers are unchanged.
- **V11. Faults, completed** (needs V10). `FaultPoint` declares a
  `FaultKind` (errors named, `crash` always an option); one registry
  (`DeclareFault` into `Faults`); the doubles' options named
  (`request=duplicate` in reports and scripts); nemeses recorded by name;
  `qenv.NoFaults` and rates set by kind as reports print it
  (`Rates.Set("rpc.x.request", p)`), a zero `Rates` documented as
  `DefaultRates`; crashes per virtual second by default; Script with a
  partial order (per kind, `After` for dependencies). *Done,* but crashes
  per virtual second by default go with R2 (they change every
  exploration's crash frequency, so the tests' rates would need
  retuning, like V12's CrashWindow default). The registry is choice's
  (Declare, DeclareSite); recorded choices carry the option's name, a
  nemesis its own; `Rates.Set`, `NoFaults`; Script's `Follows`.
- **V12. Replay stability** (needs V11). Choice streams carry a format
  version (a corpus header); `qenv.Replay` reports a divergence (step,
  expected kind, got) as an error; then `Service.CrashWindow` on by
  default. *Done:* `Replayer.Err` (Explore reports it as the run's
  error), `EncodeChoices`' version header (`Bytes.Err` on another);
  CrashWindow's default is R2's, as R2 says.
- **V13. Evidence, completed** (P8's rest, P2/P4/P6's leftovers). Names
  the kinds and untried pairs and explains "one way"; `System.Mutate`
  returns Evidence; `Evidence` and `qcover.Summary` as JSON under
  `GOTOGETHER_KEEP` (what `gotogether cover -sim` and actionmap read); claims
  over chance (`qtest.Reaches(kind).InAtLeast(0.8)`, `NoSurvivors()`
  with marked equivalents); `Recovery.Expect`; `qmeta.Healthy{MaxU}`
  giving the predicate and the PromQL; `Experiment.Healthy` unset an
  error. *Done:* the report names untried and one-way pairs; Mutate's
  report has the baseline (`Evidence()`) and `Expect(t, NoSurvivors(...))`;
  `evidence.json` under GOTOGETHER_KEEP (the readers are later work);
  `Reaches(kind).Faulted().InAtLeast(p)`; `qload.Recovery.Expect` and
  `Err`; `qmeta.Healthy` and `HealthyRegion`.
- **V14. Lints that see more.** `gotogether lint -sim` includes `_test.go`
  (System `Run` closures live there); quintaction checks literal action
  names in `qobs.Record` against the binding. *Done:* lint -sim scans
  tests; literal Record actions are checked by `gotogether lint -binding
  -src` and quintaction, with a did-you-mean.
- **V15. Smaller fixes from the gotchas.** `FileSink` durable by default
  (the buffered one renamed); a thread used from two goroutines fails at
  record time; `qenv.Wait`/`Signal.Wait` fall back to real waits outside
  a simulation; qweave the default weaver for qenv/weave. *Done:*
  `FileSink` durable (`BufferedFileSink` the old one, `DurableFileSink`
  deprecated); `Recorder.Issues` (a Thread from two goroutines), which
  fails the run; real waits outside a simulation; qweave first in the
  docs (CI already wove edgepublish's sim with it).
- **V16. P7, finished.** gotogether-metastability, gotogether-traces and
  gotogether-hegel split into references; gotogether-start rows for gosim, weaving,
  Kubernetes controllers and fuzzing, with a ten-line System snippet; the
  `v.Summary` advice removed; a skills test that fails on deprecated
  identifiers. *Done:* the three skills split into references
  (simulation, experiment, alerts; emitting, binding, production-logs,
  patterns; modes, pitfalls); gotogether-start's new rows and System snippet;
  `v.Summary` advice gone; `TestSkillsAvoidDeprecated`.
- **V17. Models.** Refusals as steps in checkout (DESIGN §26's open item),
  with the model action and the mutants it kills. *Done:* `refuse(order,
  qty)`, enabled when stock < qty; the stock check's two mutants killed,
  51 of 70 (73%).

- **R1. Releasable modules.** A `go.work` for development; no `replace
  => ../` in qanalysis and qgosim; tagged core and sub-modules; `go
  install …/qweave/cmd/qweave@vX` and `…/qanalysis/cmd/quintvet@vX` in
  the skills instead of `go run -C`.
  *Waiting:* tags are outward, so they wait for the maintainer; and a
  `go.work` changes how every module's dependencies resolve (workspace
  MVS takes the highest version any example needs, so the core would
  build against versions its go.mod does not name), so it goes with the
  tags, CI moving to `GOWORK=off` per module.
- **R2. The breaking release, with the rename** (needs V1–V15). The
  `fault.` prefix out of user APIs; V12's CrashWindow default; one name
  per concept (`ExploreConfig.Coverage` → `CodeCoverage`,
  `Exploration.Coverage` → `FaultCoverage`, `Sim.Faults(bool)` →
  `SetFaults`, `Chain.Expect` → `Mean`, `qenv.Recorder` → `ChoiceLog`;
  qtest's `Config` and `Option` one); V5's deprecations removed; the
  module renamed (see the review's names).
- **R3. Real environments in CI.** A kwok job running fleetrollout's demo
  (`go install` kwokctl and kwok; no Docker), then D3's KWOK Stages from
  `sktrace.Quantiles` checked there. *Done:* `fleetrollout.KwokStages`,
  `cmd/kwok-stages`, kwok-demo.sh `TRACE=`; in CI the faithful run
  conforms and ignore-observed-generation is caught.

Order: V1–V2 before the next example; V3 → V4 → V5 → V6; V7–V9 alongside;
V10 → V11 → V12; V13–V16 alongside; R1 any time; R2 last; R3 independent.

## After the third review (W)

From the third architecture and design reviews (the reviews below, "Third
review"). Nothing is released, so compatibility is no constraint: shims,
aliases and deprecated names go. The module rename stays parked; R1's
tags wait for it. Decided with the maintainer: all of it, the global
recorder opt-in.

- **W1. Bugs.** Escaped steps compared by count (the 1000-message cap
  hid every later one); a Scripted kind with the prefix fires; `Rates.Of`
  and Swarm match at dots; a tool error names the first seed, not a map's;
  `Shrink` runs the System's Opts and the exploration's rates; the shrunk
  run's faults and a replay hint in Hegel's log; qload keeps `Sim.Run`'s
  error; `Recovers` judges against the offered load; `qhegel.Config.Parallel`
  (ignored) gone. Done, but `Shrink` with Opts and rates, which needs W4's
  `System.Rates` and goes with it; `Mostly`'s tie (half recovering counts
  as recovering) is documented, not changed.
- **W2. The global recorder opt-in.** `qtest.Record` and qhegel install
  it only with `qtest.Global()`; installs restore by identity and a second
  concurrent one fails; with no default global, an escaped step means one. Done
  (`qobs.InstallGlobal`; Record is strict and names `qtest.Global()` when a
  step escapes; `Recorder.Install(global)`).
- **W3. Removals.** V5's deprecations and their last callers; dead or
  internal-only exports (`Sim.Faults`, `Sim.Native`, `Sim.OnSettle`,
  `Sim.Fault`, `qobs.InContext`, `KeepIn`, `ReplayFiles`, `CheckRuns`,
  `GenerateTest`, the free `qtest.Expect`); qenv/choice and qenv/profile
  internal, qenv the API. Done. `Sim.Fault`, `GenerateTest`, `DeclaredFaults`,
  `FaultOptions` and qhegel's `CaseChecker`/`CheckCase`/`Verdict` are
  unexported; qenv's aliases of the internal choosers carry their own
  doc comments.
- **W4. One way to configure.** qtest's Options only (Config unexported,
  every option documented, `WithOptions` → `Quint`); `System{Binding, Run,
  Opts}`; qhegel takes `[]qtest.Option`; `ExploreConfig.Coverage` and
  `Verdicts` automatic; `Fuzz(f, rates)`; one way to sweep (the System's
  Run with RowOf). Done: `qtest.Settings`/`Resolve` let qhegel read the
  options (`Config.Opts`; `Validate`, `Keep`, `Recorder`, `Global`,
  `Constants` gone, as is `qhegel.Checker`); `Shrink(t, sys, rates)`
  applies the System's Opts at the exploration's rates; verdicts are
  cached for the test binary (`Exploration.Cached`); `qsweep.FaultKind`
  factors are turned off by the sweep. FindViolation keeps `Config`
  (its Mode and rounds are ignored, as documented).
- **W5. One name per concept.** The reviews' tables: `qload.System` →
  `Target`, `qenv.Recorder` → `ChoiceLog`, `qenv.Faults` → `DeclareFaults`,
  `Chain.Expect` → `Mean`, Evidence the one coverage accessor, recovery
  and trigger claims by name, and the rest. Done: also `qload.Config` → `SimConfig`,
  `qenv.Profile` → `TimeProfile`, `DeclareFault` → `DeclareFaultSite`,
  `qenv.Tx` unexported, `qrun.In` → `InContext`, qhegel's `Recorder`
  → `Case`, `Chooser` → `Choices`, `EnvChooser` → `ChoiceSource`,
  `Replay` → `VaryTraces`, `qtest.Sweep`/`Swept` → `SweepConfig`/
  `SweepResult`, `Evidence.Failing` → `FailingBy`; claims
  `qload.Recovered`/`StaysDegraded`, `SomeTrigger`/`NoTrigger`;
  `GOTOGETHER_BACKEND` gone (validate read only it, so CI's typescript step
  never reached validate). `qenv.Current` stays: unlike `qrun.Current` it
  is a simulation task's context only, which woven sync needs.
- **W6. The `fault.` prefix out** of everything users type or read; it
  stays in the Chooser protocol. Done: Rates keys, `Without`, `Swarm`, `Reaches`
  and Script kinds are bare, and a prefixed one panics with the fix;
  `NoFaults` is an empty rate map; run lines print
  `rpc.counter.request=duplicate`, not `fault.…=duplicate(2/3)`.
  `Store.CommitFaults` stays a field for now (W7 decides its default with
  CrashWindow's).
- **W7. Crashes per virtual time, CrashWindow on**, by default on every
  runtime; examples retuned, claims not loosened. Done in part, as decided with
  the maintainer: the crash window is on (`Service.NoCrashWindow` closes
  it; grpcledger's `CrashAfterCommit` goes). Crashes stay per scheduling
  step by default: measured, the examples' runs last from 14 ms
  (streams) to 80 s (checkout) of virtual time, so a 100 ms default
  period gave streams no crash chance and hello one, and no one period
  fits. `NemesisEvery` stays opt-in for load runs (qload sets it).
- **W8** moved to ROADMAP.md, extended after the fourth review (back
  here now: "W8. First hour and docs", below).

## The move, and what the fourth review adds (M, W9, R)

After W7, the architect and design agents reviewed the progress again and
a planner reconciled their findings with this roadmap. Their verdict: the
W and X plans hold. Two things change the order:
- The move to gotogether and the gosim repo comes before W8, so the docs
  are written once, under the new names.
- Release readiness comes before X, because X1, X2 and X8 write paths,
  environment variable names and workflows.

Decided with the maintainer:
- Skills are renamed with shorter suffixes: `gotogether-start`,
  `-model`, `-patterns`, `-mbt`, `-hegel`, `-traces`, `-faults`,
  `-metastability`, `-workflow`.
- quinthegel becomes `qhegel` in M5b.
- qhegel becomes a module of its own before any tag (R1), so the core
  needs only OTel and yaml.v3.
- The license is MIT.

Every commit leaves every module building and its tests green. Order:
M0 → M1 → M2 → M3 → M4 → M5a → M5b → M6 → W9 → R0 → W8 → R1 → R4 →
X. R5 happens when the maintainer says.

- **M0. Hygiene.** Done.
  - Remove the stale agent worktrees and their branches.
  - Summarize W in REVIEW.md, whose third-review tables were left empty.
- **M1. The gosim repo's history.** Done. chucklehead-dev/gosim's main
  fast-forwards from upstream ffd3a61 to the fork's tip (ac6b9b2).
- **M2. The fork, a module of its own.** Done.
  - One scripted top commit sets the module path to
    `github.com/chucklehead-dev/gosim`. It also changes
    `internal/gosimtool`'s `Module`, which is written into translated
    code. The script lives in FORK.md, so a rebase re-runs it.
  - Translation checks the Go version and names the supported range.
  - The MIT license and attribution stay.
  - Minimal CI.
  - Required by pseudo-version until R5.
- **M3. History to chucklehead-dev/gotogether.** Done. Push main only:
  gosim-fork lives in its own repo now. `origin` is repointed.
- **M4. qgosim on the gosim module.** Done.
  - Remove fetch-gosim.sh, its `REF`, the `.gosim-fork` replace, CI's
    fork checkout, and the "fetch first" notes.
  - simdeps fails if anything but qgosim and examples/gosim imports gosim.
- **M5a. The module path.** Done. `github.com/casselc/quint-connect-go` becomes
  `github.com/chucklehead-dev/gotogether`, by script.
- **M5b. Every other name.** Done.
  - The CLI (`cmd/gotogether`).
  - `QUINTGO_*` → `GOTOGETHER_*`.
  - Wire names: `#meta.quintgo`, `"source"`, `quintgo::expect`, the OTel
    tracer name and the golangci plugin. Error prefixes, temp-dir
    prefixes, skills and their symlinks; `skills install` removes old
    `quint-*` dirs.
  - Prose.
  - Kept: `quint.step`/`quint.action`, `//quint:*`, `QuintOutcome`,
    `QUINT_*` variables whose value goes to quint, the quint-connect
    mentions (the Rust crate), and the analyzers' `quint*` names (they
    check `//quint:` directives).
  - The old names join TestSkillsAvoidDeprecated.
- **M6. The old repository.** Done: its README says where it went. The session could not delete its gosim-fork branch (refused); the maintainer deletes it and archives the repository. A final README commit pointing to
  gotogether; the maintainer archives it. Its gosim-fork branch goes once
  M4 is green.
- **W9. API leftovers.** Done (W9a–f). Decided rather than changed:
  - `Expect` stays single-claim where the claims exclude each other
    (Recovery, TriggerSearch, MutationReport).
  - `qload.TB` stays: qload does not import testing.
  - `Settings`/`Resolve` stay exported, for qhegel as its own module.
  - The three ways to install a global recorder are documented, one job
    each.
  - `AllowEmpty` and `SkipIfEmpty` are two things.
  - Rates are an argument where nothing else applies.
  - `QUINT_SEED`/`QUINT_VERBOSE` stay, for parity with quint-connect.
  - `CommitFaults` stays opt-in, now tested.

  Found on the way: crossdb's stamps moved on every write-back, and split
  a create's pending and succeeded stamps (W9e).

  The plan as written:
  - qhegel:
    - `NewRecorder` → `NewCase`; `Case.Choices()` → `Recorded()`.
    - FindViolation gets a config without the Mode and Rounds it
      ignores.
    - `Case.Add` stops re-teeing (REVIEW S6).
  - Claims:
    - `Expect` is variadic everywhere.
    - `Recovery.Judge` is unexported.
    - `qload.TB` gives way to `testing.TB`.
  - Configuration:
    - One of `System.Opts`/`ExploreConfig.Opts`.
    - `Settings`/`Resolve` become internal.
    - One spelling for installing the global recorder.
    - `qtest.Global` and `qobs.Global` no longer share a name.
    - One of `AllowEmpty`/`SkipIfEmpty`.
  - Rates in one place across Explore, Fuzz and Shrink.
  - `QENV_CHECK` and glomers' `QUINT_STEPS_DIR` → `GOTOGETHER_*`. The
    rule: `QUINT_*` only where the value goes to quint.
  - crossdb's grace check keeps sub-second precision through the kube
    interceptor, or a test pins the boundary.
  - `Store.CommitFaults`' default, which W6 left to W7 and W7 did not
    decide.
  - `Sweep` keeps `*testing.T` (it needs `t.Run`), documented.

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

  (What R1 left unchecked is in ROADMAP.)

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
  - **Left.** In ROADMAP ("Left from X").
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
  - **Left.** In ROADMAP ("Left from X").
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
  - **Left.** In ROADMAP ("Left from X").
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

Deferred: in ROADMAP ("Left from X").

Order: X1 → X2 (stage 1 and the models' `quint run`) → X3 → X4 → X5 → X6
→ X2 (vacuity) → X7; X8 alongside.

## Fifth review: what X left (Y)

From the fifth architecture and design reviews (below, "Fifth review").
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

What Y left to consider is in ROADMAP ("Left from Y").

## Sixth review: whole product (Z1–Z4)

The first four items of ROADMAP's Z track (its intro and Z5–Z8 are
there).

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
  Left for Z7 (ROADMAP): `ErrStuck` only when no timer is set (a periodic timer
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
- **Z4. The API before tags. Done:** one seam between qtest and qhegel
  (`qtest.CheckCases`, `System.CheckCases`, `System.Judge`,
  `Expectation.Holds`) in place of Settings, Resolve,
  ExploreConfig.Replay and Shrink's fake TB, claims judged by type; the
  core's plumbing in internal/hooks, internal/runkeys and
  qenv/internal/keyed, dead exports cut, the sinks by durability, Swarm
  tested; sktrace an examples library; connect's fault scripts in qtest;
  KnownFault internal (a run-scoped check is not possible while woven
  fault points declare at init); Rates on the System, ShrunkTo a
  RunClaim, Signal.Wait on a duration, Run.ID, MutationReport.Expect
  variadic; qweave on qanalysis's x/tools. Decided against: typed
  options (CheckCases keeps every refusal in qtest, in one place, and a
  split would make users learn three option types for a mistake the
  refusals already name), merging qweave and qanalysis (a go -overlay
  tool and vet/golangci passes have different users; their x/tools now
  match), and Sweep on testing.TB (it needs t.Run). Kept as they are,
  reviewed: `TriggerSearch.Expect` returning the `*Recovery` (the trigger
  is the search's answer); no `SweepResult.Expect` (a row's claims run in
  its subtest, where its failure is named); `Bound.Parallel` (Exhaust
  takes no ExploreConfig); itf's `RecordOf`, `TupleOf` and `SetOf` (a
  driver's expected state is written with them); qrun public (an app's
  own per-run state is a `qrun.Key`); `validate.Cached` (a test binary
  that builds its own checkers typechecks each binding's model once).

## Sixth review: Z5–Z8

The rest of the Z track, as the roadmap had it when it was done.

- **Z5. Maintainability (S–M). Done:** an escaped step fails its own
  run (found through its goroutine's creator), so qtest's tests run in
  parallel (63s → 37s short); CI's test job a matrix, a tidy check, a
  workspace job (whose first run found qweave weaving nothing outside the
  go.work in use, now an error); done items and AGENTS' backstories in
  HISTORY, its commands and one area's gotchas in docs/agents/ (5.3k
  words to 3.8k); the README's package table checked against api/*.txt;
  doc comments' Go under the docs gate. Later: KnownFault's registry per
  run (it is the process's: under parallel tests one test's declarations
  can make another's misspelled Rates key look known); one exploration
  engine fed by run sources (Explore and Exhaust repeat their setup,
  Exploration has seven mode flags), one verdict method per claim, one
  memoizing checker for qtest and qhegel, qload's two recovery worlds
  made one.
- **Z6. A more faithful environment (S each). Done:** a request may
  arrive late (`rpc.<name>.delay`: a late original after its retry),
  which found lease's lock without fencing broken with true clocks and
  crossdb's cloud leaking a database to a create that landed after the
  delete (it now refuses a request past its deadline; the seventh review
  found that leak the simulation's own, its stamp translation, and the
  provider's creation grace enough without the refusal); pauses and gray
  failures (`Sim.Stalls`); a full disk, a failed fsync (Linux's: its
  writes lost though a later fsync succeeds), a corrupt read; a second
  double of one name panics; an exploration runs its first seed twice
  and fails on a difference; the doubles shown to reorder, redeliver and
  read skew where their contracts allow. Left: a lint for map ranges,
  math/rand and multi-case select in simulated code (the self-check
  catches what they do, not where).
- **Z7. Liveness (S, then M).** Done: Settle reports whether it
  settled (`*Unsettled`, a stuck settle included; `qtest.Settles()`)
  and records a settle in the trace (`qobs.Settled` for a real
  deployment); a binding's `eventually` vals, judged in the last state
  of a trace that recorded a settle, by every check (not in virtual time
  across a run: a val of the last state); a fair settle (tasks in turn);
  `Sim.SettleCore` (faults go on outside a core, VOPR's liveness mode);
  calls recorded at invocation (`qobs.Invoke`, `Call.Done`, pending
  calls in reports, `qtest.Completes()`). `Run(d)` ending at its horizon
  with tasks waiting judges nothing, on purpose: a run's faulty phase
  ends there, and only Settle asks for quiet. Left: `//quint:action`
  advice recording at entry (Invoke) as well as on return; an example
  under SettleCore (a quorum whose convergence is stated of the core);
  eventual properties in connect's MBT; what a pending call may have
  done is Z8's ambiguous steps.
- **Z8. Search and scale (M–L). Done:** PCT as a chooser
  (`qenv.NewPCT`, tasks picked by id, recorded as index choices);
  Linearize per key (`qtest.LinearizeByKey`) and a complete search after
  the guided one, candidates judged with the run's settle; explorations
  run Linearize, steps in a simulation carrying its virtual time; the
  history checker public for real databases (`qhistory`: Elle's
  list-append workload, Adya's anomalies, G-nonadjacent included);
  exploration steered by new steps, observed states, faults and code
  blocks (`ExploreConfig.Steer`); pending calls resolved
  (`qtest.ResolvePending`); examples/quorumkv, which uses Z7's and Z8's
  features and found two of the toolkit's misses (above) and its own
  first bug. **Deferred, for the next review:** footprint-based
  reduction for Exhaust, sound only if tasks share state solely through
  the doubles (Go code between seams touches any memory, a test's
  closures included): it needs an ownership declaration and a DPOR with
  sleep sets, or a passing Exhaust would prove less than it says;
  resolution searched together with linearization (Jepsen's :info in a
  concurrent history), and calls placed at one gap in any order; hidden
  actions (an effect no one recorded); Steer's features as counts (a
  second fault of a kind is never new), cuts at the choice where a new
  feature appeared, and unbounded observations (versions) that make
  every run new; the CLI's validate linearizing (quorumkv's demo runs its
  tests). Online validation stays deferred.


## Architecture and API review

Two reviews of the library as of DESIGN §16, one of its structure and one
of what users meet, merged and ranked. The roadmap's U track
(ROADMAP.md) orders them. Each item: what, where, the proposal, and
whether it breaks the public API.

### What is fine as it is

- The module split: core with OTel, yaml.v3 and hegel only; qweave and
  qgosim in modules of their own. The simdeps rule and the Hegel adapters
  gathered in quinthegel.
- The layering of qtrace, qobs, otelio, convert and validate, and sinks as
  the one way to compose recording.
- One Chooser for every source of choice, with option 0 nominal: Seeded,
  Hegel, Replay and Script are interchangeable. The library's best idea.
- The verdict vocabulary (`Conforms`, `Rejects`, `Mismatch`, `Violates`,
  `AnyOf`) and the mutants table as the shape of a test
  (examples/checkout/checkout_test.go `TestMutants`).
- `qgosim.Scenario` plugging into `qtest.Explore`: one scenario, many
  runtimes.
- qsweep without dependencies, qmeta independent of qenv, qload's
  arrivals from its own seeded stream (a trigger shrinks by its
  parameters, not its arrivals), and the doubles inside qenv.

### Structure

**S1. One way to find a run's state (qrun). Done.** qobs (`WithRecorder`
plus a goroutine fallback), qrole (`WithRoles`, no fallback) and the new
qcover each had their own lookup. qrole's woven calls in functions with no
context passed `context.TODO()` and counted into the shared
`qrole.Default`, mixing runs in parallel. `qrun.Key[T]`, `qrun.Get`
(context, then the goroutine's run) and one resolver installed by qenv
replace them; `qobs.SetFallback` is gone. pprof labels (B1) will use it.

**S2. Chooser wrappers: `Unwrap` and one walker.** `EntropyOf` and
`nemesisOf` switch on private types, `ContextOf` sees only the outermost
chooser, and `swarm.keep()` re-hoists the context by hand. A user's
wrapper, or `Script{Base: c}`, loses the context, the entropy (gosim 9×
slower) and nemesis-by-name. Proposal: `Wrapper{ Unwrap() Chooser }`,
`qenv.As[T](c)`, a Recorder that implements Entropy and ChooseNemesis
itself, and an embeddable `qenv.Layer`. With S1 the context can leave the
chooser (deprecate `WithContext`). Not breaking.

**S3. Declared fault kinds with named options.** Options are indices:
`kube.request` has 2 on Get and 3 on Update, so coverage's
`Injected[2]` mixes meanings and a `Scripted{Option: 2}` panics on a Get.
Kinds are spelled three ways ("store.commit", "fault.store.commit",
"nemesis.crash.p"). Proposal: `qenv.FaultKind("kube.request",
"unavailable", "lostResponse")`, `FaultOf` returning the option's name,
named options in Script, connect.Fault and coverage, declared kinds in
`Unreached`, and one spelling without "fault." in every user API.
Breaks `Scripted.Option` (int to string); `Fault(kind, n)` stays as a
shim.

**S4. One run record.** `qtest.Explored` and `quinthegel.Violation` differ;
a shrunk Hegel failure has no choices, so it cannot be replayed, run on
gosim, or kept as a fuzz seed. Choices and steps are separate sequences
(Script's `Progress` is wired by hand). Proposal: `Choice.Step` (steps
recorded before it), one `Run{Steps, Choices, Code, Interrupted, Report}`
from Explore, FindViolation and qgosim, and coverage as reducers over
`[]*Run`. Additive.

**S5. One entry for annotation hooks.** qrole's three aspects and
qenv/fault repeat the same template prelude. Proposal:
`qsite.Enter(ctx, "serve", args) func(error)` with observers (role counts,
pprof labels, an action's `begin`). Before B1.

**S6. Small duplicates.**
- Lint in qenv pulls go/ast into every gosim translation: move it to
  `internal/lint`, with one `Finding` type shared with the binding lint.
- Two directive parsers (binding's and qweave's) split `amount:f(a, b)`
  differently: one exported parser in core.
- `qobs.Attributes` re-implements the step schema.
- simdeps' package list is kept by hand: derive it.
- `qload`'s counter sink duplicates `qrole.MetricsSink`.
- `quinthegel.Recorder.Add` re-tees.
- `Explored.Faults` duplicates `Recorder.Faults`.

**S7. Two definitions of the orbit.** `qload.Sample.Orbit` (in hindsight:
attempts that will time out, and those waiting) and `qrole.Orbit`
(operations minus calls in flight) both calibrate the chain. Say which v
the chain means, and test both on one run.

### What users meet

**P1. `qtest.System`: the first hour in one value.** Users wire
`GenerateRun`, `FaultScript`, MemorySink, `Progress`, `WithContext` and
`NewRecorder` by hand (connect/faults_test.go `followTraces`, 40 lines);
Explore cannot take a chooser (checkout's TestFaultOrders rebuilds it);
`qenv.WithContext(quinthegel.Chooser(tc), rec.Context(...))` repeats.
Proposal:

```go
sys := qtest.System{Binding: "checkout.binding.yaml",
	Run: func(c qenv.Chooser) error { _, err := checkout.Run(c, cfg); return err }}
sys.Explore(t, qtest.Seeds(100)).Expect(t, qtest.Conforms())
sys.Shrink(t).Expect(t, shipOrCancel)            // FindViolation, recorder wired
sys.FollowTraces(t, spec, faults).Expect(t, qtest.Conforms())
sys.Sweep(t, space, cfgOf).Expect(...)
```

`Run` returns an error, so seeds stop calling `t.Fatal` off the test
goroutine. Explore takes `Chooser func(seed) qenv.Chooser`. `quintgo
scaffold` prints the skeleton. Additive.

**P2. Quantified expectations, and `Expect` on every result.**
`Exploration.Expect` means "every run conforms" for Conforms, which is
recognised by its description string, and "one run meets all" for
anything else. That hid NoIdempotencyKey's second bug.
`quinthegel.Violation` has only a summary string, so tests use regexes
and walk reports. Proposal:
- `qtest.Some(e)`, `All(e)`, `None(e)`, `Together(a, b)`;
- `Expect` on `Violation` and on `qmeta.Recovery`;
- `quinthegel.Want(...)` for `FindViolation`.
Distinct types make old calls fail to compile, so each is reviewed:
deliberately breaking.

**P3. Check vs Validate.** `Check` fails and `Validate` doesn't, `Record`
doesn't and `RecordAndCheck` does, so `qtest.Check(...).Expect(t,
Violates(...))` can never pass. Every call should return a `*Run` that
fails only through `.Expect`. Deprecate `Check` and `RecordAndCheck`.

**P4. One recovery API.** `Recovers`, `RecoversMostly`, `Assess`,
`Mostly`, `Runs`, `Count`, `Of`, `OfSimulation`, `FindTrigger(qload.Config)`
and `SearchTrigger(System)` overlap. Keep `qmeta.System` (from `Of`,
`OfSimulation`) with `Runs` and `Mostly`, and one
`quinthegel.FindTrigger(t, sys, space)`, and make "find" return a result
judged by `Expect` (FindViolation fails t when nothing is found;
FindTrigger returns nil).

**P5. Run-declared constants.** The checkout sweep passes the stock twice,
to the app and to the model (`Opts: stockOf`); rows failed until they
agreed. Proposal: a run declares its instance (`qobs.WithRunAttrs(ctx,
"stock", 2)`, binding constants as templates `{{.run.stock}}`), or a
factor names its constant (`qsweep.Ints("stock", 1, 2).Constant("STOCK")`).

**P6. Messages for the traps.**

| Trap | Message |
|---|---|
| Constants versus sizes | "all N runs fail at step 1: the binding's constants (STOCK=2) may not match the scenario's" |
| A Script that never completes | Explore reports `Remaining()`: "scripted faults never happened: [rpc.api.request] (waiting since step 14)" |
| λ ≥ μ after calibration | "calibrated λ=10.3 ≥ μ=10: the data are mostly from a storm; hold λ (CalibrateTimeout)" |
| Unhealthy baseline | "goodput before the trigger was 0.4/s against 3 offered: set Experiment.Healthy" |
| Reproducing a seed | "reproduce with QUINTGO_SEED=17 go test -run ..." |
| "all N runs conform; want X" | Append the unfaulted kinds from fault coverage |

The healthy set is written twice, as a closure and as PromQL
(`alerts_test.go`): `qmeta.Healthy{MaxU: 10}` should give both.

**P7. Docs and skills.**
- A short `quint-start` skill with a question-to-tool table.
- Mechanics (weaving, gosim, fault points, covering arrays) move into
  references the skills load on demand.
- One shared reference for verdicts and expectations.
- The table goes at the top of the README.

**P8. One evidence report.** Name every run-producing call an
exploration, and give its result one report.

```
Evidence: checkout, 260 runs, 13 rows
  Verdicts  253 conform · 7 violate chargedAtMostOnce (rows rpc.provider=on)
  Faults    6 kinds reached, pairs 15/15, one-way 6
  Configs   pairwise 13/13 rows
  Code      76.8% · per action · after an unfaulted point: … · indistinct: …
  Mutants   48 woven, 45 killed, 3 survive in covered code
```

- `x.Coverage()` returns `qtest.Coverage{Faults, Pairs, Rows, Code,
  Mutants}`.
- Coverage gaps become expectations (`qtest.Reaches`, `NoSurvivors`).
- The report is also written as JSON next to `QUINTGO_KEEP` artifacts.

### Roadmap dependencies

- A1 and C3 (code coverage): S1 first, done; S4 next, so per-step coverage
  aligns with choices.
- A3 (fuzzing): one byte decoder in core, `qenv.FromBytes`. The rule lives
  twice today, in quinthegel's chooser and in the gosim fork.
- A2 (mutation): P2's quantifiers, to say what "killed" means.
- B1 (labels): S5.
- C1 (analyzers): S6's lint move and directive parser.
- C2 (synctest): try `NewNative(c, nil)` inside `synctest.Test` before
  adding a runtime interface.
- D3 (lag chooser): S3.

### After the roadmap (second review)

An architecture review and a design review with a first hour checked the
finished roadmap against this document. Their corrections:
- "qmeta independent of qenv" no longer holds: qmeta's recovery property
  and `Simulate` import qload, and so qenv (ROADMAP V10).
- S2: `Unwrap`/`As` done; the Recorder still special-cased in `EntropyOf`
  and `nemesisOf`, no `Layer`, `WithContext` not deprecated (V3).
- S4: `Choice.Step` done; one run record not (V4).
- S6: the lint move and `Explored.Faults` done; the two directive parsers
  and `qobs.Attributes` remain (V9).
- P1: `System.Sweep` takes a second run function; `qgosim.Scenario` does
  not fit `System.Run`; receivers differ (V4, V5).
- P3: not done; `Check` and `RecordAndCheck` still fail before `.Expect`,
  and the docs teach them (V5).
- P2/P4: `Expect` on `qmeta.Recovery` dropped; `Recovers` and `Assess`
  remain beside `System`; claims were not checked against the model's
  names (fixed: ROADMAP V0').
- P6: the constants message fires only when every run fails at step 1;
  `qmeta.Healthy` not done (V7, V13).
- P7: only quint-fault-injection split; skills still teach retired APIs
  (V5, V16).
- P8: Evidence done but terse; JSON and coverage claims not (V13).
- S3's single spelling not done: reports print `rpc.x.request`, rates
  need `fault.rpc.x.request` (V11, R2).
- New: the verdict cache keyed by steps only (fixed: V0); binding
  templates let `process`, `outcome`, `obs`, `step` overwrite arguments
  (V1); `Sim.Run` returns nil on a stall (V2); `qobs.Strict` and
  `qtest.Record`'s `SetGlobal` are process-wide (V3); the sub-modules
  cannot be installed (R1).

#### Names

The toolkit outgrew "connect" (model-based testing only). Proposals, with
their proposers' picks:

| Name | For | Against | Picked by |
|---|---|---|---|
| quintet | five ways, one model; keeps Quint visible without claiming it | could read as part of the Quint project | design, streams |
| plumbline | a reference runs are checked against; CLI `plumb`; no brand borrowed | says nothing of Quint or Go | architect |
| quint-oracle | the model as every workflow's judge | long; hyphen vs package name | analyzers |
| quintgo | already the CLI, env vars and messages; no migration | reads as an official Quint binding | gRPC |
| conform | the one verdict | collides with Go projects (leebenson/conform, siderolabs/conform) | — |
| groundtruth | AGENTS' first convention | AWS SageMaker Ground Truth; long | — |
| quintessence, qgo, qkit, touchstone, assay, jig, verdict, faithful | — | generic or colliding | — |

The rename belongs in the breaking release (ROADMAP R2): the module path
changes anyway.


### Third review (after V1–V17)

Two reviews, architecture and design, with nothing released, so every
shim was in scope. The plan they led to is ROADMAP's W track; the full
reports were working files.

What they found, and where the W track fixed it:
- Bugs: escaped steps compared by count, prefixed Script kinds, `Rates.Of`
  matching, Shrink ignoring the System's options (W1).
- The process-wide global recorder installed by default (W2).
- Deprecated and internal-only exports (W3).
- Two ways to configure qtest, quinthegel and sweeps (W4).
- Two names for one concept across qload, qenv, qmeta, quinthegel and qtest (W5).
- The chooser's `fault.` prefix in user-facing kinds (W6).
- Crash defaults that hid post-commit crashes (W7).
- A first hour that starts nowhere in particular (W8).

### Fourth review (after W7)

Architecture and design again, and a planner reconciling them with the
roadmap. Both said the plan holds. They moved the rename and the gosim
split before W8, and release readiness before X. Their new findings, now
ROADMAP's M, W9 and R items:
- Published modules with `replace` directives, which cannot be consumed.
- The gosim fork under upstream's module path.
- Wire names to rename deliberately.
- quinthegel's leftovers.
- Claims' arity.
- Two configuration fields.
- Environment-variable prefixes.
- A stale Packages table and stale hello snippets.
- No license.

### Fifth review (after X)

Architecture and design/usability, on the X track. Both found its
mechanisms sound (the layering, Exhaust and the verdict cache, the history
checker, Script's by-name order, vacuity, simulate's holds and allows,
Disk and the FileSink finding, partitions and their heal) and the trust
around them short. Their findings are ROADMAP's Y track:
- a run's panic kills the test binary, so the self-checks' findings
  cannot be replayed (Y1);
- reproduce lines that judge other runs, and failure artifacts deleted
  with the test (Y2);
- a truncated Exhaust passes (Y3);
- a wall-clock-only clock model where Go measures intervals on the
  monotonic clock, which makes lease's jump finding the model's (Y4);
- corpus entries with no header, which forced every new fault kind to be
  opt-in (Y5);
- claims judged twice, `Exercised` and `NotVacuous` proving less than
  their names, Linearize's budget read as a rejection (Y6);
- Sim's growing field set, doubles wired two ways, global fault kinds
  (Y7);
- an API record that lists no methods or fields (Y8);
- options a workflow silently ignores (Y9);
- evidence.json unversioned, duplicated on reruns (Y10);
- qhegel's core-property tests pinned to a pre-X core (Y11);
- DESIGN without the X track, README tables behind (Y12).


### Sixth review (after Y)

Four reviews of the whole product: architecture and maintainability,
correctness (silent wrong answers, by probe), a newcomer's usability and
docs, missing features and lessons learned. Their findings are ROADMAP's
Z track. What they found:
- the layering holds, and the core is large: 37k lines, 511 top-level
  identifiers in api/core.txt, a qtest–qhegel seam of exports that exist
  for one caller, about 35 identifiers that are plumbing or have none;
- seven bugs where a check could be wrong without saying so, among them
  a crashed Capacity service that never serves again, a commit's lock
  wait invisible to deadlock detection, and Linearize hiding lost steps;
- a newcomer's first passing run that reads as failures, a Mismatch
  report that hides the model's value, a scaffold that lints and then
  fails to validate, and no word on using the toolkit outside this
  repository;
- of the recurring mistakes (a check that holds for nothing, a name that
  matches nothing, an option ignored, an environment kinder than its
  contract), the first three are now mostly prevented by structure, the
  last only partly (X5 checks one direction);
- liveness is the largest gap: Settle cannot say it settled, and nothing
  states "eventually" outside the settle pattern.

### Seventh review (after Z)

Four reviews of the whole product after Z: architecture and
maintainability, correctness by probe, a newcomer applying Z7's and Z8's
features to a store of their own, missing capabilities and the lessons
of the past tracks. Their findings are ROADMAP's T track. What they
found:
- in a simulation, Linearize lost the order a recorder already knew
  exactly: a completed call does not carry its invocation's time, nominal
  calls take no virtual time and a millisecond of slack makes them
  concurrent, so a client's read that missed its own write passed; with
  six more bugs where a check said nothing (eventual vals never judged,
  faults off after a settle, a settle that ignored other recorders, a
  resolution placed after the settle, a budget cut read as a violation,
  qhistory's internal reads);
- the cost of a verdict is now the gap to the state of the art: every
  candidate order a quint process replaying from init (quorumkv's Repick,
  20 runs of 15 steps, 11 minutes; Porcupine checks a thousand
  operations in milliseconds), with ITF and the Rust backend forced on
  where the TypeScript batch was meant to serve, and a beam that turns a
  contended rejection into no verdict whatever the budget;
- the searches are qtest's, bound to testing.TB, so the CLI cannot run
  them, written three times over;
- a newcomer following the docs weakens Linearize (no "invoked"), and
  cannot find linearizability, liveness or Pending in the start table or
  the verdicts;
- the most frequent mistake of this project's own development is a
  feature whose test passes with the feature turned off (every one of
  the last eight review fixes found one), then state at the wrong scope,
  environments kinder than their contracts (the faithful examples each
  new fault family broke), detection that hangs on seeds or timing, and a
  search's cut-off read as a verdict.

### T5 review

An independent review of T5's fifteen commits found four bugs in them:
- the store checker made quadratic, then cubic, by classifying a
  commit's whole component (2.9 s for 200 commits of a hot counter);
- `GOTOGETHER_ALL_FAULTS` runs reproduced without it;
- ResolvePending's bounds ignored the clocks' slack, which its
  brute force, written from the implementation's predicates on times
  10 ms apart, could not see;
- qmut's recursion fixed in one mode only.

Its gaps: each brute force missed a path (invocation records and domain
clocks, Without and failed runs, a bound on Cover's rows), the claim
power's point estimate, mutate-core passing on nothing. All fixed in
8917552..9d90a8b, but the nightly matrices and the Hegel properties,
left in ROADMAP T5. Its example for the new features is T9 (wallet).

### T6 review

An independent review of T6's six commits found:
- a schedule run alone with the empty schedule (the nominal run's own
  reproduce line, `schedule=`) ran the whole search: "alone" was a nil
  slice, which the nominal schedule is;
- the fault-name checks ran before finish's run errors, so a run that
  failed before building its doubles had its Rates keys called
  misspellings first;
- per-run names missed what a simulation under gosim chose, and two of
  their paths (a chosen kind never declared; a declaration in a run
  without names) were untested.
All fixed, each with a test that fails without it. Left in ROADMAP T6:
Script.Misnamed's registry, woven kinds' options, gosim's declared but
unchosen kinds. Its T9 guidance (folded into ROADMAP T9): SI cannot
lose an update in qenv.Store (first committer wins), qhistory reads
list-append histories only, and LinearizeByKey is unsound for
transfers.

### T9 review

An independent review of the wallet example (T9) found that it passed
everything while:
- the bank moved money on requests it must refuse (a transfer to its own
  source made money, to an unknown account destroyed it, a negative
  amount reversed it), which the model ruled out instead of refusing;
- `TestNoOutbox` caught its mutant in 1 run of 30 (Exhaust within one
  fault catches it every time);
- the ledger's posts were checked by id only, commits of unknown outcome
  were never on (and would have been recorded aborted: a false G1a),
  deposits never ran under faults, and the bug the example found was no
  mutant.
All fixed, each with a test; its lessons for the core are in ROADMAP T9.

### T2d reviews

Three reviews of the Go evaluator's integration with the searches:
- the proof that confirms a failing search (every prefix dropped as
  rejected, every merged pair one state) was sound but for a merge of two
  prefixes of one model state whose processes were numbered otherwise
  (`{{.process}}` renders the next step differently): the key now holds
  the numbering;
- leaving a found order to the run's check (ConfirmByCheck) missed a
  check failing only an eventual val, an exploration's marked reports
  (an undecided actor's failure made n/a), and a resolution taken after
  the evaluator wrongly rejected an earlier placement, whose invariants
  were then judged on another placement than quint's. All fixed, each
  with a test and an ablation.

### T7 plan

A planning review ranked T7's eleven capabilities by value against cost:
pending calls inside the linearization first (every real client history
has timeouts, and the two searches were refused together), upgrade runs
second (a new class of bug from the nemesis machinery), a polish batch,
bounded response, then hidden actions with online validation as one
item; DPOR, Hegel parallel machines, real-deployment Scripts and an
Antithesis sink deferred (ROADMAP T7). It proposed an example for each:
wallet's concurrent pending transfers, quorumkv's NewTiebreak under a
rolling upgrade, lease's SlowTakeover, quorumkv with handoff unrecorded.

### T8: raftkv

A design review planned the example: etcd's raft library, unchanged, on
qenv, with two models, since a bounded obligation is judged along a
trace in time order and a linearized one is a search's. Its risks held:
raft's election jitter is crypto/rand's (drawn from the run by swapping
the reader), and settling with ticking daemons worked as Daemon tasks.
What the runs taught, beyond the README's table:
- two faults of the toolkit's: a wait on a drifting clock rounded to no
  time, so raft's tick loop spun until the step limit; and a search the
  evaluator judged that ran out of budget was searched again with quint,
  minutes for the same no verdict (82 s for two such runs of ten);
- the planned lease-read mutant under drift found nothing until the
  leadership was moved to a node: an election is won by the fastest
  clock, whose lease ends first, so the unsafe case needs a transfer;
- a full rollout hides a format bug the upgrade causes (each node's
  restart on v2 replays its log); a paused one shows it;
- clients' retry policy decided most measurements: three attempts at a
  flat backoff gave up during the first election, leaving a third of the
  calls pending and every search cut short.

### Eighth review (after T)

Five reviews after T, their findings ROADMAP's S track:
- a probe found six wrong answers: `None` claims holding over runs with
  no verdict (and Mutate counting them as kills), misspelled names
  unchecked when no report named an invariant, a hidden search cut by its
  depth read as a rejection, a pending call rendered with its neighbour's
  process, `within` judged on completion times alone, and quint's two
  backends disagreeing on `range(5, 2)`;
- raftkv's review found its faithful nodes never reporting a lost
  snapshot, so a follower could stay behind for good (2 of 40 runs under
  heavier loss), and the cut search's rationale missing the case of an
  evaluator that wrongly accepts;
- architecture: the five-call judging protocol written out three times,
  a shared cached report mutated by Mark, binding exporting validate's
  code generation, four Linearize variants, a suite serial by the
  environment and leaking a temporary directory per check;
- the newcomer built a leader-elected counter in an hour and a half, all
  four goals met, and named twelve frictions: recording heals, two
  bindings for one system, handlers' state across a restart first among
  them;
- capabilities: a profile put 53% of a long linearization in the
  planner, not the evaluator; the most frequent bug since the sixth
  review is a verdict kind a consumer never judges; adoption (I/O
  adapters), a real-deployment nemesis and a per-PR tier are what a team
  shipping a Go service misses most.

### Ninth review (after E)

Two reviews after E, their findings ROADMAP's G track:
- a probe of the newest code reproduced three wrong answers: a seq gap
  (a dropped step) reported as a model FAIL and accepted by Mismatch,
  SameFailure and ShrinkActions; `Minimal` true when the budget ran out
  before the shrunk sequence's own rate and the last leak check; spec
  imports missed by a regex (an instance import with a string constant,
  one over lines), so the model cache, Verify and Bundle trusted an
  incomplete file list. Suspected: webjobs' within read from snapshot
  times, a fixed 1 ms of clock slack across hosts, MutateSteps counting
  a binding's refusal as a kill;
- architecture: the CLI links `testing` and qenv through qtest, so
  shrinking and replay exist only inside `go test`; validate exports CLI
  plumbing and the manifest's builder; qsql lacks the queries of an
  outbox claim and a conditional debit;
- a newcomer recorded an HTTP handler, validated its log, replayed a
  manifest and shrank a journey from the docs alone. Every documented
  name exists; the frictions were scaffold's paths relative to the
  working directory, reports naming deleted files, a per-run recorder
  for an HTTP server documented nowhere (only the serialising global
  one), installed skills linking into the repository, and the Bombadil
  adapter unpublished;
- both suggested an example on two hosts with a collector, which
  webjobs does not cover (G10).


### Tenth review (after GR3 slice 3)

Three reviews, their findings ROADMAP's H track:
- performance (docs/performance.md): a verdict's cost is quint
  processes, and a quint process is a typecheck (counters' model 4.7
  CPU s; a step's evaluation about 2 ms); the Go side of counters' suite
  was 2 CPU s of 449 once a killed node's pollers stopped spinning
  (7bc208e: 88% of its Go CPU, counters 1469 CPU s on CI). Fixed: short
  batches in one quint process (counters 1038 to 438 CPU s on CI,
  checkout 251 to 96, validate -29%, qtest -20%), binding's and qeval's
  quint processes in parallel with each spec typechecked once,
  GOTOGETHER_CHECK quadratic in a run's length (a Store transaction at
  4000 transactions 80 to 11 µs), a string per fault choice, CI's
  longest part from 23-31 minutes to 8-14;
- a designer's product review: the core reads as one product, grist as
  a second built on it (its own actor, CLI, environment variable, mutant
  harness, docs layout); the doubles grist's GR5-GR7 need are inside
  unpublished examples; no upcasting or snapshots; about 60 exported
  identifiers no user calls; four ways to plant a mutant;
- a newcomer did three tasks end to end (a service simulated against a
  fault with a mutant caught in 12 minutes; a grist app with a refusal
  and a declared dependency in 8 plus test time; an HTTP handler's run
  validated by the CLI in 3): every error said what to do next; the
  time went on rejections that do not say which condition failed, a
  fold silently changed by a new event type, Recorder.Close not closing
  its sink, and grist outside the CLI and the front page.

### H5's first half: the woven ABI

The designer's review counted about 60 exported identifiers no user
calls. Twenty were what woven code calls: qenv/weave's sync functions
(the aspects' advice), `qcover.Hit` and `Register` (`qweave -cover`),
`qmut.Register`, `On`, `Cond` and `Pick` (`qweave -mutate`). They moved
to package `woven` (DESIGN §60), documented as an ABI, with their state
in internal/wovenrt (qenv/internal/keyed's hooks, now with `Go`, the
registries, the run keys qcover and qmut set as functions). The import
graph is the constraint that shaped it: the core's own mutation testing
weaves core packages, and a woven package imports `woven`, so `woven`
imports nothing that could be mutated (qenv included: qenv installs the
hooks instead), and qweave never mutates woven, wovenrt or what they
import. No user code changed: tool files still name qenv/weave, whose
path keeps the aspects; qcover's and qmut's types and readers stayed.
storetest's twelve per-check functions went unexported in the same
item (`Checks` and `Run` were how every harness ran them). grist's
generator, CLI plumbing and limits wait for H4's renames across grist.

### H5's second half: grist's generator

What the designer's review counted in grist and no user calls, after
H4's renames. The generator (`Generate`, `Generated` with `Write` and
`Diff`, `Hole`, `Starter`) became grist/internal/gen: an app runs it
through its binary's `model` command and its tests diff it with
`gristtest.Generated`, and nobody called it by hand. It needed the
catalog's types without importing grist (whose model command calls it),
so they moved below both, to grist/internal/catalog, aliased by grist:
a user's `grist.Catalog`, `CatalogBlock` and `Guarantee` read as before
(api/grist.txt shows them as aliases, with their fields). `QuintName`
and `Schema` went with them. `MainArgs` and `UsageError` were the
command line's plumbing (`Main` now documents every command),
`QueryArgs` the API's, and nine constants (`CallTimeout`,
`DefaultMaxBody`, `DrainDelay`, `FlushTimeout`, `LiveBound`,
`MaxIDBytes`, `RetryAfter`, `SettleTimeout`, `ShutdownTimeout`) the
defaults of options a user sets by value: unexported, the values said
on the options. 18 identifiers (40 lines with their fields and
methods) left api/grist.txt; no example, the scaffold (`grist new`) or
gristtest's harnesses changed beyond an import. gristsim's timeouts
stayed: a simulated client's retries are reasoned about by them.

### H6: the grist path at the front

The designer's review found grist reading as a second product, the
newcomer found it nowhere in the CLI or the front page, and both counted
what every grist app wrote before its first claim. What changed:
- **`gristsim.System(binding, app, cfg)`** is the `qtest.System` every
  grist example spelled as a closure over `gristsim.Run` with
  `Mutant: qtest.Mutant()`; an app's helper is now a line (or none).
- **`ExploreConfig.ShortSeeds`**: twelve examples defined the same
  `seeds(n, short)` helper; the field is Seeds under `-short`. Where a
  test shared one count across explorations it shares the config, the
  Chooser set on a copy.
- **Arguments by type, zero and a negative included.** gristsim already
  drew a block's arguments from its type when `Gen` did not name it, but
  its integers were 1 to 3, so a validation's refusal (a quantity of none
  or fewer) was never drawn and the scaffold wrote a `gen` map for it.
  `Config.Ints` defaults to -1 to 3, the starter model's `INTS`; the
  scaffold's map is gone, and counters, which drew by type, still passes
  (its increments may now be negative, which its model admits).
- **The generator's warnings.** A fold silently changed by a new event
  type was one of the newcomer's time sinks: the rules still typecheck.
  The generator compares the events file it writes with the one there,
  and names each read model folding a new type (or no longer one) and
  each event whose fields changed, with the folds reading it; the model
  command prints them and `gristtest.Generated` reports them.
- **At the front**: grist in README's "What it does" and its question
  table, an Install line for `grist new`, a line in `gotogether help`,
  and a capability in AGENTS.md. docs/grist.md's sixteen §0 notes (what
  each slice and review changed, 1,900 lines) are HISTORY's "grist's build
  notes": the design note is the design.
- **Skills trimmed to triggers and steps.** Ten SKILL.md files held
  20,300 words, read whole whenever a skill loads. Each body moved,
  section by section and verbatim (links rewritten), into its
  references (`gotogether-faults`' seams, simulation, explore and beyond;
  `gotogether-start`'s questions and first hour; one or two per other
  skill), and each SKILL.md became its triggers, numbered steps and the
  references with when to read each: 3,400 words. A script checked that
  every line of every old body is in the new references.
- **One demo variable.** hello and kvstore took `MUTANT=<name>`, every
  other demo `MUTATION=<kebab-name>`. All take `MUTANT` now, named as
  the mutant is: its testdata/mutants directory (the overlay demos run
  `TestMutants/^<name>$` for any directory, so a new mutant needs no
  script change), or the mutation's Go name for the demos whose binaries
  take a `-mutation` flag (converted to the flag's kebab case in the
  script). `MUTATION=` is in TestSkillsAvoidDeprecated. Two demos could
  exit 0 on a failing check: kvstore's validations went through `grep`
  (now each validation's status counts), and edgepublish printed every
  scenario's verdict and exited 0 (now each scenario's is held to
  README's expected one: happy and rotation-race conform, the others do
  not). twophase's `MUTANT=earlyCommit` stops on an unsettled run
  (exit 2), as it did before.

### H7's first half: upcasting and snapshots (grist)

A team's first change to an event struct decoded every stored event of
the old shape into the new one silently, and every read folded its read
model's whole log. What was built (docs/grist.md §2.1, §2.2):
- **Versions as Go structs.** `grist.NewEvent[E](app, name,
  grist.Upcast(1, func(v EV1) E {...}), ...)`: the old struct kept under
  a name of its own, one upcaster per old version to the next, the
  version one more than the upcasters. Structs, not JSON transforms:
  grist reads everything else from types (schemas, tags, generated
  values), the compiler and Verify check the chain's links, and nothing
  is a string that can drift from a type. Reads upcast in
  `EventType.Decode` (folds, handlers, `Events`); appends write the
  newest; a stored version newer than the app's is a
  `*grist.NewerVersionError` by name (what grist-platform.md slice 4's
  mixed rollout needs of a v1 node), never decoded.
- **The model sees only the newest shapes**, and upcasting is checked to
  be a function it need not see: Verify (boot) runs each upcaster over
  generated values of its input (internal/values, gristsim's generator
  moved there) and refuses one that panics, answers twice differently or
  changes a tag (the index is the stored version's); `CheckUpcasters`
  (`verify`, `model`, `gristtest.Upcasts`) also perturbs each field of
  the input and refuses an upcaster none of whose outputs changes,
  unless it `Drops` the field. The relay re-renders a row's events
  stored at an older version (it reads them back and recomputes
  obs.strs and obs.ints from the upcast bodies), so an old log is judged
  by today's model: examples/library's `TestOldLogConforms` restores
  version 1's log (testdata/logs/v1.jsonl, recorded by `TestRecordLog`
  before the change: `gristtest.Capture`, `WriteLog`, `ReadLog`,
  `TierConfig.Log`) into the real tier and its store-side steps conform
  with today's after them; with the relay's re-rendering ablated, step
  3 (the first addition) mismatches. The clients' binding is not run on
  a restored log: its model starts empty, and no client saw the old log.
- **grist.lock**, written by `<app> model`: each version's shape (a hash
  of its JSON Schema and tags, its fields for a reader) and each read
  model's fingerprint. A recorded version whose struct changed is the
  generator's error, naming both shapes and the fix, so a struct
  changed in place fails `<app> model` and `gristtest.Generated` rather
  than a fold; an app older than the lock is refused too. Every grist
  example and grist new's starter write one.
- **Snapshots in the store** (`store.SnapshotStore`: memstore, simstore
  with `store.<name>.snapshot` faults, torn or lost; sqlstore's
  `grist_snapshots`; storetest's `Snapshots` on every tier), keyed by
  (read model, key, fingerprint), checked before use (a checksum, the
  fingerprint, a position within the read's, a decode onto a fresh
  initial value). The fingerprint hashes the name, version, keys, each
  folded type's newest shape and a probe (the fold's states over
  generated events), so a changed fold's old snapshots are never read;
  the version is the backstop, and the generator warns when the
  fingerprint changed at the same version. A read takes one when it
  folded `SnapshotEvery`'s events (default 1000).
- **Snapshots invisible to short logs.** The first version looked up a
  snapshot on every read: one more store transaction per read, which
  moved every exploration's schedule (counters' reach claims failed,
  and a stored choice sequence would no longer replay). A read now reads
  its first SnapshotEvery events first and looks up a snapshot only when
  there are that many (only such a read takes one), so a simulation's
  short logs read as before; a long log's read costs about twice
  SnapshotEvery's events whatever its length. sqlstore's index query
  takes the LIMIT for a read of several types too.
- **Checked against a fold from the start**: `App.CheckSnapshots`
  (`gristtest.Snapshots`) compares the stored snapshot, and one taken
  at each position that matters, plus its tail, with a fold of every
  event; library's `TestSnapshots` explores decisions made from
  snapshots (every two events) through a third of the puts torn or lost
  and node crashes, all conforming. Mutants, all caught:
  `snapshotPastItsPosition` and `fingerprintByVersion` (grist's own,
  grist/testdata/mutants, by CheckSnapshots), `dropsCopies` (library's:
  by CheckUpcasters and by the old log's `Mismatch("addBook")`).
- **Cost** (library's `TestSnapshotsMakeStartFast`, SQLite, a fresh
  node's first read of member a's loans): 10^4 events, 230 ms without
  snapshots and 30 ms from one; 10^5, 2.5 s and 25 ms (1,000 events
  read either way).
- **examples/library at version 2**: book-added's copies renamed count
  and a shelf added (`MainShelf` for version 1's), the model's
  `bookAdded(book, count, shelf)` its only shape.

After the eleventh review's architect (I1-I3): the fingerprint holds
each upcaster's probe (what it makes of generated values of its own
version), since a buggy upcaster and its fix shared one and the buggy
snapshots stayed in use (`TestFixedUpcasterRefolds`; mutant
`upcasterFixKeepsSnapshots`); grist.lock takes a field added to a
version, nothing else changed, with a warning (`TestLockTakesAnAddedField`),
as grist-platform §3.9 calls one safe; a new version's rollout
(`WritesVersion`, `gristtest.Upgrade`) is ROADMAP H7's next item, and
§3.9 says what does not work yet.

Left: schema changes beyond upcasting (grist.md §9: an event split, a field whose
meaning changed); a processor reading behind the head refolds from the
start when the only snapshot is later than its trigger.

### Versions that roll (I22, GR10 slice 4)

H7's first half left a new version unable to survive a rolling update:
appends wrote the newest at once, and a node still on the older release
refused the events by name. What was built (docs/grist.md §2.1,
docs/grist-platform.md §3.9):
- **`grist.WritesVersion(n, downcaster)`**, an option of `NewEvent`
  beside its upcasters (`grist.EventOption`): the release that adds a
  version reads it and writes version n; the next drops the option. The
  downcast is at append (a command's, a processor's emits and
  outcomes), after the rules, so blocks decide and steps record in the
  newest shape; an append version n cannot hold (downcast then upcast is
  another event) fails with the fix rather than store an event without
  it. Verify runs the downcaster over generated values (total, pure,
  tags kept), CheckUpcasters requires it to undo the upcasters. The
  catalog says `writes`. A step's observations are computed from the
  newest shape (`batchObs` upcasts an older body), and a relay renders as
  the newest release started with it (`Relay.RenderAs` for one whose
  older nodes start first). `TestWritesVersion`, with the downcast
  ablated, finds version 2 stored. A row records the version its
  observations were rendered in (`grist.rendered`, dropped before the
  step is recorded), so the relay reads back only an older release's
  rows: re-reading every row with a versioned event's moved counters'
  schedules enough that `TestFaithful`'s four short seeds stopped
  reaching a commit fault.
- **An older node serves nothing from a newer event.** A command or
  query whose read meets a `NewerVersionError` answers `ErrUnavailable`
  (503, retried under its key; a newer node serves it) and records no
  step; a processor's trigger waits as for a store error. Before, a
  processor failed the trigger (`grist.failed`, final) and a command
  answered a 500. The error's advice now names `WritesVersion`, and it
  wraps through the read's error (`%w`), where `%v` lost its type.
- **`<app> catalog -diff deployed.json`**: safe, readers first or
  breaking, each change with its fix, exit status 0, 3 or 4 (grist's
  internal/catalog `CompareCatalogs`, on the catalogs' JSON).
  `TestCatalogDiff` is the table.
- **gristsim's `Config.From` and `gristtest.Upgrade`**: the deployed
  release's nodes beside the new one's on one store, the rollout a
  Cluster's rolling restart (each replacement runs the new release) or
  one upgrade nemesis a node, finished before the settle; the clients
  send what both serve, the dependencies of both releases reach one
  double by name. An older node's refusal is counted on the call's step
  (`olderRefused`): `gristtest.ReadersFirst()` and `WritersFirst()`.
  Upgrade compares the catalogs first and fails t on a breaking change.
  gristsim's `TestRollout` is the mechanism without a model (refusals
  when written at once, none held back, every commit in the new shape).
- **examples/counters v2**: counter-created's creator renamed owner
  (version 2, written as 1); release 1 kept as the package `release1`;
  `TestRollingUpdate` (the rolling restart's shape: conforming,
  settling, completing, `ReadersFirst`, `NoClientErrors`) and the mutant
  `eagerV2` (`WritesVersion` dropped a release early:
  `Some(WritersFirst())`).
- **H7's leftovers**: the runtime snapshot audit (`grist.SnapshotAudit`:
  one in 100 reads from a snapshot refolds its key through the same
  position, answers and replaces a wrong snapshot, and counts it on
  `/metrics`; sampled by a hash of the snapshot and the read's position,
  so simulations replay; `TestSnapshotAudit`); `grist.Reindex`, which
  grist.md named as not built, dropped from the docs (a tag that must
  change is a new event type).

What the docs had wrong: grist-platform §9's slice 4 sketched counters
v2 as "a field added to an event, a new event behind a flag", but an
added field needs no version (grist.lock takes it), so it tests nothing
of a rollout: a rename does. Its acceptance expected `eagerV2` to fail
"the v1 nodes' folds" (`Some(Mismatch(...))`): with a v1 node serving
nothing from an event it cannot read, no step is wrong, and the
refusal is what shows. `gristsim.Cluster.Rollout` (maxSurge,
maxUnavailable) was not needed: the rolling restart with a disruption
budget is the rollout. §3.9 wrote `Upgrade(t, v1, v2, cfg)` and
`Sim.Upgrades` alone; Upgrade takes the binding, and returns a System
whose claims the test states. The header said nothing past slice 2 was
built, though slice 6 was. A new event type stays readers first with no
API to hold it back (the app's flag, §3.6).

### H7's second half: the doubles published

GR5-GR7 will build grist's streams, gRPC and controllers on the doubles
three examples wrote for themselves; an app outside the repository could
not import any of them. Each moved in a commit of its own (the file as
it was, so `git log --follow` reads its history), then became general.
Placement is by dependency weight: the core takes OTel and yaml.v3 only.
- **`qenv/simlog`, a package of the core.** It needs nothing but qenv,
  so it sits beside qhttp and qsql, and gristsim (GR5) can use it
  without a module. The seams were the pipeline's (`Writer`, `Reader`,
  `Record` with the pipeline's two ints); they are the log's now, values
  as bytes (the pipeline encodes `n:value`, as its real tiers did), so
  the real tiers implement the same interfaces. The commit step it
  recorded itself is the user's, from `Log.OnCommit` where the commit
  lands. Its fault kinds carry the log's name (`log.<name>.produce`,
  `rebalance.<log>.<consumer>`), as the core's doubles' do. It gained a
  contract check under `Sim.Check`, as the Queue has (offsets dense, each
  delivery the record that landed there with no hole, an idempotent
  producer's records each once and in order, commits only from the
  owner), with tests that each of three broken logs is caught.
- **`qenv/simgrpc`, a module** (google.golang.org/grpc). Its API was
  already general; its refused connection became the server process's
  for a core settle. It had no tests of its own: a hand-written
  ServiceDesc over a well-known type tests each fault's status code, the
  virtual timeout, statuses and crashes without generated code.
- **`qenv/kubesim`, a module** (client-go, controller-runtime). It knew
  one Deployment (`default/web`), recorded fleetrollout's `patch` and
  `scale` steps by name and replayed SimKube traces. Now any objects and
  scheme; the API server's rules for any object with a spec (generation,
  status kept, an `apierrors` conflict on a stale resourceVersion);
  every verb a seam; every Deployment rolled out; writes reported where
  they land (`Cluster.OnWrite`, with `AsWriter`'s writer), from which the
  example records its steps; `kube.<cluster>.request` as its kind. The
  replay stays in the example (sktrace is unpublished). For one
  Deployment the choices are the ones they were: fleetrollout's
  calibrated seed counts hold.
- **`qenv/simlog/kafkalog` and `qenv/simlog/kinesislog`, modules**
  (franz-go; the AWS SDK, with `kinesisfake`). Their commit step became
  `Reader.OnCommit`; kgo options pass through. They require the core at
  a commit with qenv/simlog, so they were pushed after it. Their tests
  write, read, commit and resume on kfake and the fake endpoint; the
  pipeline's runs on each moved to streams' tier package.
crossdb keeps its own kube interceptor: its seam translates
crossplane-runtime's timestamps, and moving it onto kubesim (a
time-translation option) is GR7's when it needs both. api/<module>.txt
records each module, CI's doubles job tests each on its own go.mod
(GOTOGETHER_CHECK and the race detector), simdeps covers them through
go.work.

### P8: skills in pavlov's shape

pavlov's newcomer review found its skills short, each with a
choose-your-path list, a results table and checklists, and ours long
(H6 had already moved their mechanics into references). Each SKILL.md
now has, in at most about 80 lines: its triggers (the description), a
"choose your path" list naming each reference and when to read it, the
steps, checklists (before code, before merging, per requirement), a
results table whose rows are the report lines the skill's tools print
(`K reject <action>`, `qenv: stuck`, `possible X: FAIL`, `<app> model:
warning: ...`), each with what it means and what to do next, and "run
it, do not infer it": the command that answers the question instead of
reasoning about the model or the run (`gotogether simulate`, `nav
-binding`, `nav -run ... -at fail`, a `reproduce:` line). The table
rows quote what the code prints, checked against its sources when
written. Eleven skills, 680 lines and 6,400 words of SKILL.md (H6 left
ten at 3,400 words of triggers and steps; before H6, 20,300), the
longest 73 lines; `TestSkillsShape` holds each to about 80 lines and
the five sections.

Two skills changed in substance. `gotogether-workflow` is model first:
acceptance scenarios, each a `possible` (or `qtest.Scenario` checked by
`qtest.Possible`), then safety (`holds`, `allows`), then liveness
(eventual vals at a settle), all on the model alone, then code, its
results table opening with what `simulate` says. `gotogether-migration`
is new, for a team arriving with code: one bounded context (its map a
comment at the top of the model), requirements extracted from code,
tests, logs and traces with a provenance comment each (its sources'
kinds and places, `assumed` when only one, a disagreement's decision
recorded on it), then the model with its `possible` scenarios and
claims, and the system's existing runs judged (legacy tests recorded,
spans converted, production logs validated) before any code changes,
every disagreement triaged into the code's bug, the model's mistake or
an undocumented requirement. pavlov keeps provenance as metadata on its
bthreads; ours is a comment beside the `val` or `possible` that states
the requirement, the model being the specification.

### The acyclic model layout (grist)

quint 0.33's REPL refuses a file that a file it imports imports back
(QNT101, "Multiple modules conflict on the same name"), though
typecheck and run accept it; docs/performance.md's D4 has the minimal
reproduction. grist's generator wrote exactly that: the app's
`model/<app>.qnt` held the rules (`<app>Rules`) and the invariants (the
bindings' modules `<app>` and `<app>Calls`, importing the skeleton),
and `model/<app>_gen.qnt` held the events, `<app>Gen` and
`<app>CallsGen`, the latter two importing the rules back. A session
that loads a model once (D4) needs the REPL, so the layout changed
(there were no grist apps outside the examples):

- `model/<app>_gen.qnt`, generated: `<app>Events` (imports grist);
- `model/<app>_rules.qnt`, the app's: `<app>Rules` (grist, the events);
- `model/<app>_steps.qnt`, generated: `<app>Gen`, `<app>CallsGen`
  (grist, the events, the rules);
- `model/<app>.qnt`, the app's: `<app>`, `<app>Calls` with the
  invariants (grist, the events, the rules, the steps).

Two hand-written files, not one: the invariants read the generated
state (`log`, `checkpoints`, a call's records), which lives with the
actions that call the rules, so the rules and the invariants cannot
share a file without a cycle. A state module of its own, imported by
both the invariants and the actions, would have kept one file at the
cost of a module reached by two paths, which quint's flattener has lost
names through before (the "imports the events and rules directly"
witness). The bindings' spec and modules are unchanged.

What holds as before: regeneration writes only `_gen` and `_steps`
(`model -init` writes each of the app's files that is missing, never one
beside an old layout); missing holes are read from `_rules` and
reported with stubs; `gristtest.Generated` diffs both generated files,
and its witness that the bindings' modules import the events and the
rules themselves still holds, the rules now `from "./<app>_rules"`
(the validator's Go evaluator compiles all three examples:
TestAgreesWithQuint, TestEvalModuleCompiles). New: `grist.Generate`
refuses a cycle of the model's imports, naming it, and the old layout
with the move (module `<app>Rules` as it is to `model/<app>_rules.qnt`;
in `<app>` and `<app>Calls`, `import <app>Rules.* from "./<app>_rules"`
and the skeleton's modules from `"./<app>_steps"`); `TestLayoutAcyclic`
(the starter's and the golden shop's import graphs, the old layout
refused) and `TestModelsLoadInREPL` (`quint -r <app>.qnt::<module>` for
each binding's module of the shop and the three examples, evaluating
init; red on the old layout). `grist.Starter` returns both files by
path. counters, wallet-grist, library and `grist new`'s scaffold moved
their rules by that one cut. D4, measured with the REPL working: no
gain on quint 0.33 (docs/performance.md).

### pavlov's lessons (after the tenth review)

Three reviews of pavlov (behavioral programming for Clojure: bthreads
that request, wait on and block events, composed append-only; the same
program run by its model checker), their findings ROADMAP's P track:
- a designer's: the checker is not free (environment bthreads are
  doubles, safety bthreads and hot bids a spec, `:possible` sets
  Exercised), but needs no translation or binding, at the price of spec
  and program sharing one author's understanding; grist's catalog is
  already additive and its generator plays the program describing
  itself; what grist lacks is block, so a cross-cutting rule edits every
  command that can emit its event;
- a newcomer's: pavlov's path to a checker's finding about an hour in
  one language, ours about an hour mostly learning Quint and a stronger
  finding (code against an independent model under faults, where every
  bug of the three earlier tasks was); its skills short with a choose-
  your-path list, a results table and checklists; a navigator would have
  saved most of the time spent on the rejection whose reason no report
  gave;
- a Go specialist's: pavlov's state is data (save, restore, exact keys,
  a breadth-first graph, 5.5-10 µs an edge, hot cycles found by a
  colouring search); ours is data only at the model level (qeval's
  Successors and Key), qenv's tasks are goroutines replayed from their
  choices, and grist's blocks are data-shaped enough to explore
  in-process; the way to pavlov's checking for free runs through qeval.

### P3 and P4: possibilities and scenarios

ROADMAP's P3 and P4 as built (DESIGN §59). The decisions they left:
- **Which checker first**: the Go evaluator's bounded search, then
  quint's sampling for what it did not find. Both on every possible
  doubled simulate's time for a confirmation TestAgreesWithQuint
  already makes; the evaluator's witness is the shortest and names the
  arguments, quint's names the actions alone. A sequence naming an
  argument is the evaluator's only: quint's ITF records no action's
  arguments.
- **quint's predicates in one run** (`--witnesses`), then a run per one
  found for its trace (it stops there): one sampling per possible cost
  a grist app a minute.
- **"Impossible" only after a complete search**: a grist model's log
  grows without bound, so its searches are always cut, and say "not
  found within the bound (2000 states, depth 4)"; vending's 25 states
  are a whole graph, and its strict variant's possibles say impossible.
- **A command outcome is "can answer so next"**, not "answered so": a
  refusal commits nothing to the log, so no predicate over the state
  says it happened; the step's own enabling (an identity left, the
  outcome of some drawn arguments) is one.
- **The generator's identities**: four, one per command, made the
  library's loan limit unreachable by the model on its own (a join, two
  books, three borrows and the refused fourth are six commands); ten
  drawn at random were spent by refusals before it (0 of 10000 traces).
  Thirty, taken in turn (`nextID`), reach it in 58 of 1000 and never
  draw a used one: the step's commands are no longer disabled by a
  draw, and two states differing by an identity's name no longer exist.
- **A scenario is an Expectation**, not a claim of its own, so Some,
  All, None, Mutate and Runs take it unchanged; its failure lines are
  the exploration's (per run, and the model's verdict on the sequence),
  added where a claim no run meets is reported.
- **Arguments compared as values** (`Take.With`, itf.ParseQuint both
  sides): a set's order and spaces are no difference; a literal itf
  cannot read is compared as text.
- **grist's Invocation** carries the model's parameter names (the
  generator's renames, `item_` for a field `item`), so a
  scenario written in Go values matches the binding's arguments
  without the author knowing the renames.
- **The review** (after landing): best first under a depth bound missed
  two of vending's three-step sequences within it (a state reached by a
  longer path first kept its depth: now expanded again when reached
  shallower); a scenario's outcome the binding passes to no parameter
  was dropped from the model's search, which then called possible what
  the outcome forbade (now no search, saying so); a With of a misspelled
  parameter made None hold for nothing (now failed, named); a `when`
  naming an action went to quint as a val (now the steps to write), and
  a mapping's misspelled key was ignored (now refused). quint's share
  moved into the first invariant's own run (its witnesses and its
  traces), dropping the per-predicate witness run: library's simulate
  64 s to 23 s, counters' 33 s to 16 s. Handing ids out in order lost
  nothing: the step never drew a used id (freshKey disabled it), and a
  reused key stays a recorded step's outcome, the action's
  `answered.contains(id)` branch, which quint replays. examples/hello
  carries a possible and a scenario as the README's first code.

### P2: the state-graph explorer

ROADMAP's P2 as built (DESIGN §61). The decisions it left:
- **The surface**: a mode of simulate (`-graph`, with `-states`,
  `-depth`, `-out`), not a command of its own: it answers simulate's
  question (does the model guarantee what `holds` says, allow what
  `allows` says, reach what `possible` says) over every reachable state
  instead of samples, as `-verify` does through Apalache. Beneath it
  `validate.Checker.Graph(GraphSearch)`; no qtest claim (a model-only
  check needs no run; `qtest.Possible` remains the model-first claim).
- **One core for Reach and Explore** (qeval's graph.go), Reach's
  sequence goals its best-first ordering: no second search.
  `Transitions` already returned labelled edges; the graph interns
  them, the branch's action and its own choices kept, the step's unused
  choices dropped.
- **Deadlocks fail**, unless `simulate.terminal` holds there (a new
  binding field, with `graphStates` and `graphDepth`); actions never
  enabled are reported, not failed.
- **Possibles under -graph** go to the evaluator at the graph's bounds,
  then to quint for what it did not find: the first version left them
  to the evaluator alone, and the grist examples' deep possibles
  (counters' payoutOutcome, library's borrow.limit) failed where plain
  simulate's sampling reaches them.
- **The default bound** stays the ROADMAP's 10^5 states: under a minute
  for most models, minutes where a step's choice product is large
  (bank, broadcast, kvstore), each of which a binding can bound.
- **The smoke run** over the repository's 64 bindings with invariants
  (`simulate -graph -states 20000`): 58 explored (six skip or have only
  eventual vals), 11 complete (hello, uniqueids, twophase, crossdb;
  vending and its strict variant, limiter, graph and graph_ends; qtest's
  journal and letters), the rest cut by
  the state bound; none stopped by the evaluator. Times in DESIGN §61
  (0.2 s to 188 s). Failures: the two examples' deadlocks (twophase's
  decided protocol, crossdb's spent ids), each a `terminal:` or more ids
  in its example; the grist examples' deep possibles before quint
  sampled them; and testdata fixtures whose invariants are unclassified
  on purpose (their violations are what their tests are about).

### P2's review

A model checker's reading of the explorer, its findings by severity:
- **Replays that quint refused.** A counterexample's step called the
  branch's action whatever the step did around it: a step that assigns
  more than its branch (`all { any { inc, dec }, n' = n + 1 }`) was
  replayed as `inc`, which quint does not typecheck. Each call is now
  checked by the evaluator first (the branch's call where it alone
  reaches the path's state, else step), and `Sampled` means what quint
  draws: a call with other successors than the path's, not a step that
  printed picks (twophase's deadlock, its picks deterministic, replays
  exactly now).
- **Labels merged past 200 characters.** Labels were interned by
  `Event.String`, which cuts a value short: two calls differing only
  past the cut were one label, and the second state's path named the
  first call. Interned by the events written in full.
- **An invariant unevaluable in one state hid violations in others**
  (the check stopped at the first Unsupported). It goes on; the verdict
  is never held, and a violation found is reported.
- **"holds" on a truncated graph.** The report printed `holds` for an
  invariant no explored state violates in a graph a bound cut; it says
  `bounded` now, "which proves nothing past them". `-out` with a
  relative path failed after the whole search (the module imports the
  model by a path relative to its own); quint's sampling of possibles
  under -graph is announced; a non-Boolean `terminal` is an error, not
  a deadlock.
- **Deadlocks under stutters.** A state with a self-loop is no
  deadlock, so a model that can always read (grist's queries) never
  has one: said in DESIGN §61, and the states only a stutter leaves
  are counted (`quiet`), never failed.
- **Sound as found**: Key (every value a bracketed or quoted Quint
  literal, Compare-canonical), every initial state a start, invariants
  checked in every state found (starts and states at the depth bound
  included), Complete only when no bound cut and nothing was refused,
  and edges whose labels drop the step's unused choices kept per target.
- **Cost**: a call's successors computed once per state, labels as
  lists, a state's Key once per assignment; at 20000 states kvstore
  137 s to 78, payd 72 to 20, bank 62 to 30 (DESIGN §61). A call with an
  operator argument (Choreo's step_with) is not remembered: the first
  version of the memo keyed it without the closure and lost twophase's
  commits, which TestGraphAgreesWithQuint caught and
  `TestTransitionsOperatorArguments` now does in -short.
- **Vacuous proofs.** crossdb's settledCorrectly "held" over its whole
  graph because its step never settles (settle, audit and secret are
  the binding's, judged on recorded runs). The report lists such
  actions (`outside`), the invariant left as the binding classifies it.
- **The examples' deadlocks**: twophase's 9 are its end (every node
  decided), declared as `terminal`, and the example is -graph's first
  demo (`TestGraphProvesInvariants`, `TestGraphFindsEarlyDecision`,
  `MODEL=early-decision ./run-demo.sh`). crossdb's 128 are the id
  pool's: see its commit.

### P6: liveness as cycles

ROADMAP's P6 as built (DESIGN §62). The decisions it left:
- **The surface**: a mode of `simulate -graph`, as P2's: each eventual
  val judged when the binding has one (an eventual-only binding is
  judged, where plain simulate skips it), classified by
  `simulate.holds` and `simulate.allows` as invariants are (a lasso
  fails a val in holds; one in allows, or unclassified, is reported),
  `-out`'s module with a `gtg_hot_<val>` run of the prefix and one turn
  of the cycle. Beneath it `GraphResult.Eventually`
  (`EventualVerdict`, `HotLasso`) and `GraphSearch.Eventually`,
  `Unfair`, `Stops`; the search itself is qeval's
  `Graph.HotComponents`.
- **The family is the action**, whatever its arguments, with TLA's
  stuttering (a step that changes nothing neither enables nor takes
  it). Per call was rejected: a grist processor's trigger argument
  changes with each event, so no call of it is continuously enabled,
  and every input would be obliged call by call.
- **Every action weakly fair by default**, `simulate.unfair` and
  `simulate.stops` (`-unfair`, `-stops`) to opt out: the most fairness,
  so no lasso is a false alarm for a missing declaration; the report
  names the action that ruled out each component's cycles, so a fair
  input or fault hiding one is visible. Grist's generated models need
  no declaration (their state grows: their only cycles are stutters).
- **In runs**: `Checker.MarkHot` (qtest on a run whose Settle failed,
  validate's CLI on every trace without a settle) puts the latest cycle
  of the replay's states with an eventual val false on the actor's
  result (`Result.Hot`), evidence beside the verdicts, none changed.
- **Soundness**: `TestHotComponentsAgainstBruteForce` (every closed
  walk's support on 3000 random graphs, under random fairness, all
  fair and none), failing with fairness ignored, the SCCs taken over the
  whole graph, or a stutter counted as enabling its action;
  hot.qnt's queue by hand (TestCheckerGraphEventually,
  TestSimulateGraphEventually, the lasso replayed in quint); the runs'
  side in TestMarkHot and TestUnsettledRunsHotCycle.
- **The smoke run** over the 23 bindings with eventual vals: one lasso
  under the default, quorumkv's converged (a probe may observe any
  value forever), and raftkv's converged once its clients stop: both
  bindings now say `allows: [converged]`. No model's liveness bug; the
  default needed no change. wallet-grist's `ledgerAcksUnposted` is a
  run-only bug (its runs settle; the model's ledgerPostServed stays
  enabled, weakly fair, until every answered call has its record).

### P6's review: liveness as cycles, checked by hand

A skeptical model-checking review of P6 (DESIGN §62), ranked:
- **The note naming what fairness rules out never counted single hot
  states.** HotComponents dropped a one-state component with a weakly
  fair action enabled, so in a model whose state only grows (every grist
  model: no cycles but stutters) "fairness rules out ..." never
  appeared, and the input hiding a stuck processor, which §62 promised
  it would name, was never named. Every component is now returned; the
  verdict counts `Stutters` apart from cycles and names every action the
  nearest leaves untaken.
- **The brute force missed one ablation**: a stutter counted as taking
  its family survived its 3000 graphs (the first failure is the
  3964th). One edge in four is now a stutter, there are 10 000 graphs,
  and `TestHotComponentsStutters` states the cases by hand (enabled only
  by a stutter: not obliged; taken only by one: missed).
- **A lasso's Obliged was its component's**, printed as "every weakly
  fair action enabled throughout it", which the cycle, visiting fewer
  states, may not have (requeue's storm: none for the component,
  advance for the cycle). It is the cycle's now, checked by the brute
  force.
- **The replay checked the val at the turn's end only**; it now expects
  it false after each step of the turn too (replayed in quint: hot.qnt's
  three bindings, requeue's storm, hot_ends).
- **A terminal end with the val false is a lasso**: a stutter in a state
  the step cannot leave, which a run settles in with the val false. It
  was already one; it is now said so (`HotLasso.Ends`, "the step
  cannot leave that state") and decided in §62: a declared end means the
  model stops, not that its obligations are met.
- **What was sound**: fairness per state (enabled by an edge to another
  state, so an action enabled only by a stutter obliges nothing, and
  one taken only as a stutter is missed: both now tested by hand); the
  states cut by the bound (not expanded, or expanded with an edge to -1)
  are left out of every component, which can miss a lasso (reported
  `bounded`) and never makes one (a component's states are whole, their
  enabledness exact); the copy MarkHot writes on (qtest's report shared
  by runs of the same steps is never written). MarkHot's wording claimed
  a storm or a livelock; a cycle of model states may be progress the
  model abstracts away (a counter that wraps) or one fairness rules out,
  so its docs say what repeated, not that it would forever.
- **Cost**: the liveness search is 14 to 18 ms of graphs of 2 to 18 s at
  20 000 states; nothing to optimise.
- **examples/requeue**, the storm as an application (§62), and **grist's
  commands as `simulate.unfair`, declined** (grist.md §3.3.1): no verdict
  changes on counters, library or wallet-grist, and a processor that
  never runs stays hidden behind the any-number effect, site and
  dependency actions, which cannot be declared unfair without false
  lassos.

### P1: blocking rules (grist)

ROADMAP's P1 as built (docs/grist.md §2.4.1). The decisions it left:
- **Which commands a rule guards** needs a static fact the catalog
  lacked: what a command appends (a decision returns `[]Event`). Commands
  declare it, `grist.Appends(Event...)`, like a processor's `Emits`; a
  command appending a ruled event without declaring it is the code's
  error at that append, so the catalog's guards and the model's guarded
  actions are exactly the commands that can run the rule. Verify's
  impossible event is then "no command's Appends, no processor's Emits
  or Delivers success". The price: one declaration on each command that
  appends the event, once, not the rule's check in each.
- **Refusals are declared by the rule** (`Refuses`, required), so the
  catalog can add them to each guarded command (`Refuses`, with `Rules`
  and `RuleRefuses` saying which are the rules'); a rule's undeclared
  rejection is the code's error, as a decision's is.
- **Order**: the batch's events in order, one event's rules in
  registration order, the first refusal winning; rules run only after
  an ok decision (a decision's own refusal comes first). This changed
  the library's refusal order: a member at the limit asking for a book
  with no copy free is now `unavailable` (the decision's) where it was
  `limit`, the model's `borrowOutcome` with it.
- **The hole's shape** is a refusal result, not a bool, so a rule may
  declare several: `<rule>Refusal(log, e): str` (`""` admits) and
  `<rule>Needs(e, refusal): Set[Read]`, the same pair a command's
  Outcome and Needs are. The guard (`<command>Ruled`, `rulesOf`) is
  generated in both steps modules only for an App with rules, so the
  vendored grist.qnt and every rule-less model are unchanged; a command
  whose rule refused has its decision's needs taken at `"ok"`.
- **Processors** fail visibly: a refused Emit or OnSuccess fails its
  trigger (`grist.failed` after its tries) with the rule named in the
  step's error (`rowStep.Error`, shown in the report), the first
  processor step to carry one.
- **The generated mutant** drops the rule in the test binary alone:
  `grist/internal/ruledrop`, set by gristtest's init from
  `GRIST_MUTANT=drop-<rule>`, so no production binary can drop a rule;
  `Mutants.App` runs it without an overlay and `DroppedRule` gives its
  claim, `Some(Rejects(<guarded command>))`.

examples/library's three-loan limit moved from `decideBorrow` into the
`loan-limit` rule, every claim kept and every mutant caught, and the
generated `drop-loan-limit` caught at `greedy`. One more read per borrow
moved every run's choices: `TestFaithful`'s first four seeds no longer
lost a mailer's answer, so it runs eight under -short. Its -short suite,
cold caches: 91.7 → 111.9 CPU s (68.7 → 79.8 s wall), the new mutant's
build and run and the four seeds. `grist new`'s capacity is a rule
too (its overlay mutant now `noInvalid`, the generated `drop-capacity`
beside it).

### GR10 designed: grist on a platform

docs/grist-platform.md, from the maintainer's aims of 2026-10-28
(Kubernetes-aware and -native, nothing mandatory; qload and qmeta to
size limits, retries and timeouts) and three reviewers' brainstorms (a
newcomer deploying `grist new`'s shop, a designer, a Go specialist),
whose ideas taken and left the doc's §11 lists. Found while reading
grist at 0da5013: every pod's node records as `<app>-1` (pid 1 in every
container), so a multi-pod run's merged steps get Reconstruct's
duplicate error and no verdict; `serve`'s drain (10 + 10 + 10 s) equals
the default grace, so a slow drain is killed in its flush; `-addr`
defaults to loopback. The reviewers disagreed on a Kubernetes Lease as
a hint; the design keeps no Lease at all (heartbeats and the fence in
the log), the specialist's minimal-client elector the alternative.
The maintainer decided the six questions the same day (the doc's §10):
five as recommended, and client-go isolated to executables of their own
reached as subcommands (`gotogether kube`, `<app> kube`, as `gotogether
nav` execs gotogether-nav), GR7's controller a client of the app's HTTP
API, a build tag rejected.

### GR10 slice 1: identity, probes and the drain

docs/grist-platform.md §9's slice 1, docs/HISTORY.md (grist §0.15). The node
records as `<app>/<node>/<incarnation>` (`grist.Identity`: GRIST_NODE,
POD_NAME or the host name, a nonce per start), so two pods' files, or a
restarted container's and its predecessor's, merge without
Reconstruct's duplicate error; counters' `TestRealTwoPods` runs two
processes of the app's binary on one SQLite file and conforms, its
mutant `pidIdentity` gets the duplicate error and no verdict. The drain
is a schedule (`grist.Drain`: a 5 s delay in a pod, then 10 s each for
shutdown, settle and flush; manifests are to give 45 s), `-grace`
budgeting it into less (`TestMainGraceBudgetsTheDrain`, scaled down).
`/livez` `/readyz` `/metrics` on `-health-addr`; loopback refused in a
pod; `GOMEMLIMIT` from the cgroup. The acceptance's "a `-grace 20s` run
records its settle where today's schedule is killed in the flush"
cannot happen with the chosen defaults (§0.15 says why); the test shows
the budget where delay and shutdown outrun the grace.

### GR10 slice 2: graceful termination and the lagging router

docs/grist-platform.md §9's slice 2, docs/HISTORY.md (grist §0.16). qenv makes a
cluster's two behaviours named choices: `nemesis.terminate.<proc>`
(`Sim.Terminations`: the signal cancels `qenv.Terminating`'s contexts,
the process drains and `Exit`s or is killed at its grace, then restarts;
option `short`; a budget as a PDB) and a router (`qenv.NewRouter`: an
endpoint per incarnation chosen by `route.<name>`, its view stale by
`route.<name>.lag`); `Service.Call` now stops at a cancelled context, as
a real client does. grist's drain is one schedule for Main and
gristsim's `Cluster` (every node serving the API behind the router,
each drained and restarted, a rolling restart), a refused try counted
on its call's step, `gristtest.NoClientErrors` judging them. counters'
`TestRollingRestart` (simulated) and `TestRealRollingRestart` (three
pods of its binary behind a router polling `/readyz`) conform, settle
and linearize with no try refused; `noDrainDelay` gets refused tries in
the router's lag, `stopNodeAtSignal` an unknown payout. What the
acceptance got wrong: no call of `noDrainDelay`'s fails (its tries are
retried under their keys: the claim counts tries), and the lag's
ablation and `stopNodeAtSignal` each needed the simulation made truer
(a try sent before the shutdown served; a call stopping at a cancelled
context). Two slice-1 defects fixed: the processor lag gauge never 0
for a caught-up processor while other events came, and a stopped poller
counted as running. Its cost: counters' `-short` gains about 25 s of
CPU (the rolling restart's three bindings and two mutants' builds and
runs); the real half, about 35 s of wall time and the mutant's three
rounds 50 s, is full mode.

### GR10 slice 6: sizing from the chain and the arithmetic

docs/grist-platform.md §9's slice 6, docs/HISTORY.md (grist §0.17). The
maintainer's ask, "qload or qmeta to estimate limits, retries,
timeouts": qmeta sizes against a declared `Burst` (`RecoveryAfter`, the
probability of being healthy within a bound after it; `Size`, the
fewest workers; `LimitFor`, the highest rate a one-at-a-time hop
survives; `TimeoutFor`, the latency floor then the shortest rung that
recovers), each search checked against brute force; `Params.Unit` makes
a grist app's millisecond service behind second timeouts solvable;
`qload.Layered` composes a Gateway's tries inside a client's attempts;
grist/client's `Policy` is its own retry decision for a simulation.
grist/gristload measures an app's service times from a run's steps and
writes sizing.yaml with every value's reason, formula, tool, margin,
caveat and the verdict on the setting in force; `<app> sizing` runs
`<app>-sizing`, a binary of its own (decision 4's pattern), so the app's
binary links no qload or qmeta. counters' acceptance: the mutant
`postRetries3` advised against and staying degraded (0 of 6 seeds) where
the recommended setting recovers (6 of 6), at 60/s, since at the
design's 40/s the busiest tenant's burst stays under its measured
capacity. The two findings are rows: replicas never speed up a hot
tenant (a rate limit instead), and a slow dependency's lag is no pod's
to clear. What the design got wrong is §8.4's list.

### Eleventh review (after H, P and GR10 slices 1, 2, 6)

Five reviews at 65e5d23 (H7's first half just landed), their findings
ROADMAP's I track. Each reviewer ran what they judged; numbers are
theirs, on a shared 4-core machine (load 19-36), so seconds are
inflated and back-to-back ratios are the reliable ones.

#### What each reviewer found

- **The architect** (37 modules, the core 58 packages and about 1,100
  exported identifiers): a fixed upcaster kept its read models'
  fingerprints, so snapshots folded through the bug stayed in use (the
  buggy and fixed apps shared `f48ce43b...`); upcasting cannot survive a
  rolling update (appends write the newest version, the lock refused an
  added field, an old node refuses a newer version), though
  grist-platform §3.9 calls an added field safe; H7's first half landed
  without its docs (grist.md taught `UpcastFrom` and `Reindex`, neither
  built; no skill named `Upcast`); three engines and two semantics for
  "can the model ...?" (the CLI samples where `qtest.Possible` fails,
  `GraphResult` walked by hand in tests), and three bounds in
  `binding.Simulation`; a bare expectation's quantifier implicit and
  varying; validate a god package (86 identifiers, 10.7k lines, the nav
  protocol among them); `SnapshotEvery` the one grist setting carried
  in a context; the step log, binding files, the woven ABI and the nav
  protocol unversioned; a grist read costing about 2×1000 events with
  no memo, failed snapshot puts ignored, retired fingerprints never
  pruned; seven core pseudo-versions across the published modules and
  no example building as a user would; one module for packages of very
  different maturity; grist's CI outside the time budget; record kinds
  as flags on `qtrace.Step`; docs that record history but not the
  current structure (DESIGN 4,688 lines, HISTORY 5,032); two placements
  for claims.
- **The usability designer** ran two personas. A backend developer with
  no formal methods reached "my own HTTP service, simulated under lost
  responses and crashes, a mutant caught" in about 35 minutes; a team
  lead added a grist feature through the generator in about 15 minutes
  plus 7 of silent tests. Frictions: `grist new` says to run a 343 s
  silent suite (the README's `-short`: 73 s); the quickstart promises
  `as expected` blocks and a shrink line that never print, so a
  newcomer never sees a catch; `item` is a quint built-in, missing from
  AGENTS' list; a zero-step run whose client was refused diagnosed as "a
  reconstruction issue" and "a deadlock"; upcasting with no user doc;
  docs/grist.md a design note where a guide is wanted; summaries in the
  implementer's words; help hiding `-graph` and front-loading
  validate's flags; nav's `-follow` errors not saying how to name a
  branch; the scaffold's first green needing an `Exercised` exception
  the newcomer cannot judge; a passing `qtest.Record` silent; a new event
  no read model folds unnoticed; stale numbers in hello; failure copies
  left in /tmp (173).
- **The formal-methods expert** built adversarial models and ran them:
  qeval accepted a set fold that agrees sorted and reversed (a
  round-robin shard over `Set(1, 33, 2)`: `-graph` "proved" an invariant
  quint refutes, a run quint rejects passed); an internal choice the
  expect observes gave Mismatch at different steps on reruns of one
  input (quint samples it); ResolvePending judged invariants on the
  first conforming placement; qhistory made a real-time edge of an
  Unknown transaction's completion (a false `G-single-realtime`); real
  time across recorders assumes 1 ms, unsaid. Overclaims: `-graph`'s
  "proof" rests on the evaluator alone; liveness's "no false alarm"
  holds only under per-action weak fairness and the check is □◇P, not
  ◇P; evaluator passes confirmed in 1 run of 50; Exhaust's "complete"
  without its bound; PCT's guarantee unconditioned; `All` over zero runs
  vacuous. Gaps against the state of the art: refinement, symmetry,
  partial-order reduction, fairness choices, Elle's other workloads,
  inductive proofs, sampling confidence.
- **The adopter** (a staff engineer, payments, a job system,
  controllers): migrate-payments' story holds (a production day's
  violation in two commands, 1.5 s cached); counters' binary behaves as
  designed (probes, drain, `Idempotency-Key`, a flipped refusal named
  with its false conjunct). Blockers: no tags, 780 commits in nine days
  by agents with one maintainer, weekly breaking renames, unversioned
  formats, a rumoured "grist" rename, Go ≥ 1.26.1; validation about n²
  (900 steps 54 s, 9000 over 10 minutes, killed); the toolchain's weight
  (Node, quint, its Rust evaluator; counters 253-440 CPU s a push); the
  real tier's fragile assumptions (OTLP files only, spans never sampled
  out, 1 ms skew); a report for a model's author, not on-call (three
  indices for one record, no pod or trace id, synthetic "enabled
  instead", truncated state, no `-json`); grist two slices of eleven
  into operations (no failover, no secrets from files, no schema
  versions, no retention, a thin `/metrics`).
- **The Go specialist** (race detector, shuffles, benchmarks): the
  concurrency is clean (every package raced under load passed), module
  hygiene very good, the scheduler measured (a bare step about 1 µs).
  Found: cancelling quint orphans the Rust evaluator (ppid 1, 72% CPU,
  deaf to SIGTERM), and checks run on `context.Background()`; grist's
  sqlstore reads one event per query (5-6x slower than a JOIN on SQLite,
  a round trip each on PostgreSQL); `TestFaultPoints` order-dependent,
  and `Coverage.Unreached` counting other tests' sites in a user's
  suite; a step's recording 10-18 µs and 40 allocs; Reconstruct about 6
  KB a step (100k steps 0.9 GB); qeval's explore allocation-bound (270
  KB a state); the race job missing qtest and six more; Hegel's output
  captured by assigning `os.Stdout`; five core pins; small scheduler
  costs; 13 benchmarks in the repository; a `go` directive stricter than
  needed.

#### What is kept

All five, in their words: errors that name the fix (the architect's
"no other part of the product does as much for users"); the rejection
report with its false conjunct (the designer's "best-in-class", the
adopter's 3 a.m. stand-in); the generator's holes and the catalog as
the one source; the guards on the surface (api/*.txt, docsnippets,
pkgstate, simdeps, the deprecated list); the dependency rule (the core
on OTel and yaml.v3, everything heavy a module of its own, checked by
`go list -deps`); one judging path and the verdict matrix; option 0
nominal, named choices, `qrun.Key`; brute force against every search,
and ablation; three-valued verdicts used consistently; Linearize with
proofs quint re-checks; the evaluator refused rather than guessed
outside its fragment; the graph explorer's vocabulary (complete,
truncated, holds, bounded); Elle's list-append, one classifier for the
store and a real database; Exhaust's exactly-once enumeration;
grist's stated limits on obligations; hello as the one front door;
overlay mutants; the skills' short shape; `simulate -graph`'s proofs
and lassos in words; migrate-payments as the pitch; grist's
operationally literate defaults; the CI's CPU budgets and GOWORK=off.
What gotogether does that nothing comparable does (the formal
reviewer's list, worth advertising): one binding judging a simulated
tier, a real one and production telemetry; model-judged shrinking;
possibilities on both the model and the runs; linearizability with
pending calls under a Quint spec, refuted with proofs; a CTMC-predicted,
simulation-confirmed metastability verdict.

#### Decisions in the synthesis

Merged across reviewers:
- **One track, I.** The architect proposed "I: integrity before tags",
  the designer "I: the first hour", the adopter "AD"; one track, in the
  maintainer's order (wrong answers, adoption, grist, usability,
  performance, bold), AD's and the designer's items placed by kind.
  3b20911 cites the architect's draft numbers I1-I3; ROADMAP maps them.
- **The upcaster and versions** (architect F1-F3, designer F5): I1,
  I22 (pointing at H7's Next, not repeating it) and I27's docs. Already
  in hand: the fingerprint and the added field (3b20911), the docs
  (dc959e8).
- **Versioned formats** (architect F8, adopter B1 and its "stability
  program"): I13 for the mechanics, the policy the maintainer's.
- **Pseudo-versions** (architect F10, Go specialist M7, adopter B8):
  I14, one item.
- **`doctor`** (architect: skews; adopter: versions, cache, variables):
  one `gotogether doctor` in I21. The designer's `grist doctor` is a
  different thing (an app's checklist) and goes into the generator's
  warnings (I26).
- **The report viewer** (adopter) **and nav in the browser** (designer):
  I47, a static page from a manifest bundle first (no server, shareable
  in an incident channel), `nav -serve` after.
- **Real time across hosts** (formal F5, adopter B4, G8): the named
  assumption is I7 (in progress), the declared skew stays G8's.
- **CI cost and the grist tier** (architect F12, adopter B3, designer
  idea 14, G12): I15 and I29.
- **Examples for the newest features** (all five's section 4): I35 for
  tutorials, each other example with the item it demonstrates.
  examples/requeue (f21ca9f) answers "P6 has no example", so the
  architect's jobqueue and quorumkv liveness examples are not added;
  the run-side one waits for `NoHotCycle`, the strong-fairness one for
  fairness options.

Conflicts, and the pick:
- **The evaluator's share in summaries.** The designer would hide "judged
  by the Go evaluator 30, 2 also by quint" behind verbose; the formal
  reviewer would print the sampled share more prominently. Picked:
  kept, in the user's words ("30 runs, all conform (quint confirmed
  2)"), the breakdown behind verbose (I9, I31). A summary says what
  decided it; the complaint was the wording.
- **The bare expectation's quantifier.** The architect offered a
  required quantifier or a printed one. Picked: both, required except on
  the claims whose names say "every run" (`Conforms`, `Settles`,
  `Completes`, `Finishes`), with a panic naming the fix (I16): printing
  alone leaves the test's reader guessing.
- **Sound folds** (formal F1's three options). Preferred: permutations
  up to about 6 elements and a whitelist of commutative shapes above,
  else `Unsupported`; not quint's own set order, which no version
  promises. The agent fixing it decides; this is the review's
  preference.
- **An internal choice** (formal F2): the lint first (S, prevents it),
  then pinned-witness replays (M, removes it), as the reviewer ordered.
- **Scale first by profiling.** The adopter's partitioning and
  streaming, the Go specialist's Reconstruct work: Reconstruct's
  two-line presizing first, then the quadratic profiled (the adopter
  measured it in the evaluator), then partitioning declared in the
  binding (I19). Partitioning before the profile could hide a fixable
  hot spot.
- **One search bound in the binding** (architect F4) against the
  architect's own brainstorm of the model's facts leaving the binding:
  the bound now (I17), the move a bold idea (I51).
- **grist's guide.** The designer's docs/grist-guide.md or grist's
  README, the architect's grist.md marked built and designed. Picked:
  grist/README.md (pkg.go.dev shows it as the module's page) and the
  marks (I27).
- **The JSONL encoder after the step log's version** (Go specialist M2,
  architect F8): I37 after I13, so the encoder's golden test pins the
  versioned format.
- **One core module or a runtime module** (architect F11): the
  maintainer's; recommended a long v0.x, carving after GR4-GR7.
- **GR10's order** (adopter: failover before anyone runs on managed
  PostgreSQL): slice 3 beside slice 4, recommended to the maintainer.
- **The Go floor** (Go specialist L4, adopter B1): `go 1.25` for the
  core, after a CI build at that toolchain; the maintainer's.

In progress when this was written: formal F1-F5 (I2, I4-I7), the
`-graph` cross-check (I3) and the liveness wording (I8), by one agent;
the upcaster fingerprint (I1), the lock's gap for a rolling update
(I22) and H7's docs (I27), by another.

#### Brainstorm

| idea | value | cost |
|---|---|---|
| Model claims through one Expect (`qtest.Model(t, b).Expect(t, Proves, Refutes, Possible, Live, NoDeadlocks)`) | model-first becomes a test in CI; retires the GraphResult walks | M |
| A model diff on what a binding observes | model changes reviewed like code; a loosened check visible | M |
| Refinement mappings over the graph | spec against mechanism per instance, not sampled | M |
| Symmetry reduction, fairness on demand, Apalache inductive invariants | larger instances; liveness of locks and retries; unbounded proofs where they matter | M each |
| Sleep sets, then DPOR, from the doubles' footprints | Exhaust at Delays 2-3 on real systems | M, then L |
| Differential fuzzing of qeval against quint | the next unsound fold found by the nightly | M |
| grist's models built as qeval IR from the catalog | grist's suites lose most of their quint CPU; P9's judge | L |
| qeval compiled to closures | 5-10x on the graph and liveness | L |
| A compiled-model artifact | no Node or quint in validate-only pipelines | M |
| Entity partitioning and streaming validation | a production day judged | M-L |
| A trace-capture SDK and an OTel Collector processor | brownfield capture in an afternoon; sampling solved for step-bearing spans | M each |
| A canary gate and continuous conformance for any service | "the spec gates the deploy"; a correctness SLO | M |
| kubesim as "envtest with faults" | controller teams' lowest entry | M |
| `gotogether init`, a model from a Go type, a model proposed from recorded REST runs | the blank page removed for non-FM users | M-L |
| A static report page, then nav in the browser | incident collaboration, PR review | M |
| A WASM playground on the Go evaluator | the "aha" in 60 s without quint | L |
| `gristtest.StoredLogs` | every kept production log judged by today's upcasters and model | S |
| `qobs.Sometimes`, `qtest.NoHotCycle`, typed fault names | code-side possibilities; livelocks as claims; misspellings at compile time | S each |
| A runtime module at v1 | production importers stable before the analysis side | M (the maintainer's) |

### I13 and I14: formats versioned, one core version

The eleventh review's I13 and I14 (DESIGN §63), on the policy the I
track's "For the maintainer" recommends, kept in one table
(internal/formats) so the maintainer's decision is a field per format:
- **Formats named.** The step log's first line is a format record
  (`{"quint.format":"gotogether.steps/1"}`), written by JSONLSink with
  the first step (one write per step, as qenv.Disk's schedules had it)
  and by WriteJSONL; OTel steps carry it on their span. A log without
  one is steps/1: everything written before had no name, and a major 0
  would invent an earlier format. Bindings and rule files take an
  optional `format:`, read before the strict decode so a later major is
  refused with the upgrade rather than its keys "unknown". The corpus
  entry, named `gotogether.run/1` like the manifest's run/2, is
  `gotogether.corpus/1`, the old name read as it (raftkv's entries).
  The nav protocol is named in a session's first line and a node
  (`validate.NavProtocol`); both navigators check it. qdriver's
  requests/1, the manifest, the evidence and BytesVersion (now
  `gotogether.choices/1`'s major) go through the same table, so there is
  one scheme, not two. Derived files (manifest, evidence) and the byte
  rule still read their current major only, each refusal saying how to
  write it anew; data nobody can write again is read from major 1 for
  good.
- **The woven ABI.** `woven.ABI1`, referenced by every file qweave
  -cover or -mutate writes (`var _ = quintwoven.ABI1`), and qweave
  refuses a module whose core lacks it, naming the core and the fix
  (testdata/oldabi, a fake core of ABI0). `qweave -version` prints it.
- **One core version.** `ci/bump-core.sh` moves every published module
  (qhegel, qweave, qanalysis, qgosim, grist, qnav and the four doubles)
  to one commit; `TestCorePinsAgree` fails while the pins differ (seven
  commits before) and checks the script's list against every go.mod
  without a replace. No module may lag: a user gets the newest core of
  the pins anyway. CI's `consumer` job builds ci/consumer (an order-sync
  service on simgrpc, simlog, kafkalog and qhegel, explored with
  `qtest.System`) outside the repository with no replace and no
  workspace, at the satellites' commit with the core at their pins, then
  with the core at the commit.
- **`gotogether doctor`**, I21's version-reporting core: the CLI and its
  ABI, the module's core and published modules, qweave, gotogether-nav
  and named binaries' cores, quint, the evaluator its version expects,
  Node, the formats read, the variables in effect; each skew with its
  fix, `-strict` exiting 1.

Ablated: a step log, binding, rule file, corpus entry or nav session of
a later major is read (or "unknown field") without the table; the ABI
check off, qweave weaves against ABI0 silently; the pin check finds
eight commits on the tree before the bump.

### P7: grist's obligations generated

ROADMAP's P7 as built (docs/grist.md §3.3.1). The decisions it left:
- **One val per block, named from it**: `<processor>EventuallyHandled`
  for every processor, `<call>EventuallyServed` for every Keyed call a
  processor uses, written into module `<app>Gen` (the generated steps
  file, never the app's) after the actions. A Generate refuses an app's
  model defining one of those names (quint would refuse the name defined
  twice), naming the fix: delete the hand-written val.
- **Which bindings**: the store's default binding, and a variant that
  judges every invariant of the default binding (the rule by which a
  variant compares the observations: one that is a default binding with
  more vals); a variant judging only its own vals (counters' `exactly`,
  `once`) does not, nor the clients' binding (no processors). The
  obligations follow the app's own eventual vals in the list.
- **At least once**: the checkpoint passed every trigger, and the
  trigger was skipped (`<p>Skips`), had an ok effect step, or has its
  `grist.failed` (a Fail is recorded as the skip action with it). The
  model's checkpoint and skip actions already require the second
  conjunct; stated anyway, so the val says what the declaration
  promises whatever the actions admit.
- **At most once promises the checkpoint only**, not "an outcome event
  logged" as the item was first written: the checkpoint commits first,
  and a crash after it loses the effect, or after the effect its
  outcome, by right (§0.6), at a declared crash point or a nemesis's
  step, and no step records which. An outcome obligation would fail
  faithful explorations with crashes; `atMostOnceOutcomesLogged` stays
  a test-only claim (counters' `exactly`).
- **A Keyed call**: every call its caller saw answered (ok or a declared
  refusal) has the dependency's served record of its key. An unknown
  answer promises nothing: a lost request, a lost answer and a crash
  during the call leave it, and the caller's step is the only record
  then (a pending call is no step). An unkeyed call has none: its
  dependency keeps nothing per key, and a third party's records no site
  (counters' mailer on the real tier, the reason for its `real` variant).
- **Faults that prevent progress**: the obligations are a live
  system's, judged only at a settle, which qenv records only when
  nothing but daemons is at work. gristsim's supervisor restarts every
  node, so no node stays down for good; the real tier's Settle waits for
  every processor's last trigger and fails the test instead of recording
  a settle; a settle finding work pending is `Unsettled`, which
  `qtest.Settles()` judges, never a violation. What remains, a
  simulation of one's own leaving a node down (outside `SettleCore`'s
  core, not restarted), is the user's settle to withhold: qenv counts no
  task of a down process as pending. gristsim does not guard it, having
  no such node.
- **The ablation**: gristsim's `TestObligations`, a board app with a
  processor of each shape. A handler that never returns (the poller, a
  daemon, waits as it waits for appends, so the run settles) violates
  its processor's obligation, for each of Emits, at least once and at
  most once; a dependency answering a Keyed call it never applied (a
  transport recording no site) violates the call's. With the vals not
  generated each claim names an eventual val the runs did not check, and
  fails. A Skip where the rules say Effect is caught before, as the skip
  action's `Rejects` (`TestDeliversSkip`), never reaching the settle.

Removed as exactly generated: counters' `everyCounterWelcomedOrFailed`
(`welcomeEventuallyHandled`) and `everyRetirementHandled`
(`payoutEventuallyHandled`, now judged by the default binding too),
wallet-grist's `allPosted` (`postEventuallyHandled`), `grist new`'s
`everyAdditionTallied` (`tallyEventuallyHandled`). Kept, as stronger:
library's `everyBorrowReceipted`, counters' `everyPaymentAnswerServed`
and `everyMailAnswerServed` (field for field; the mailer unkeyed),
wallet-grist's `postedApplied`, the scaffold's `everyAdditionShelved`
and the test-only vals.

The cost, each example's -short suite before and after (user+sys CPU s,
two rounds interleaved against a checkout of the commit before, warm
build cache, a 4-core machine shared with other agents' tests): counters
273.6 and 204.1 → 245.1 and 268.0 (means 239 → 257, +7%), library 134.4
and 115.3 → 129.0 and 126.0 (125 → 128, +2%), wallet-grist 238.6 and
193.2 → 228.5 and 209.2 (216 → 219, +1%): within the rounds' own spread
(up to 30% between two runs of one side). No growth is measurable at this
noise: an eventual val is judged in a settled trace's last state, and
every example already had some. gristsim's `TestObligations` adds about 25 s
wall to grist's -short (three explorations in parallel), 75 s in full.

**The review.** Soundness under faults: the board app, faithful, with a
Skip, an Emits Fail, an at-least-once effect whose Keyed call is refused
(its key's answer replayed on every try, so `grist.failed`) and an
at-most-once one, over one and two nodes and tenants, commit faults,
every fault kind and crashes at 5 in 100 steps: 144 runs, every one
conforming, no obligation violated (two nodes and two tenants with the
refusals at that crash rate ran out of scheduling steps, the per-step
crash rate's livelock, and were not judged). A correct app can still
violate one where the settle cannot see the work: a poller is a daemon
holding a trigger as it is waiting for appends, so a settle shorter than
the work in hand (gristsim's is a minute; the backoff caps at 320 ms, a
call's timeout is a second) judges it mid-retry, and a node left down
for good (a simulation of one's own) has no pending task. Both left,
each needing qenv to tell a busy daemon or a down process from a quiet
one (a daemon mark held only while idle, or Settle naming down
processes), which Settle's other users (quorumkv's node down for good
under SettleCore) would meet too. The `real` variant: it judges every
default invariant, so it judges the call obligations, which a `Keyed`
third party with no site fails on that tier (decision 4's case); no app
here has one (counters' mailer is unkeyed, library's mailer is in
process on its real tier), and no mark leaves an obligation out of a
variant: left, documented in §3.3.1's limits. At most once: the
checkpoint is all a trace can promise, an effect step's uniqueness is
already the actions' (`atMostOnceEffect`), and an outcome needs to know
no crash fell after the effect, which no step records. The processor
obligations are equivalent to their checkpoint over reachable states:
what they catch is a processor stopping short, never a wrong outcome.
Fixed: an obligation's name defined by the app's rules was quint's
QNT101 in the generated file, now the generator's error naming the
rules' file; `TestObligations` judges every case with one generated
model (one catalog: the stall is in a handler), so quint compiles it
once, the -short run 16.6 → 11.0 s wall (39 → 20 CPU s), full 37 → 17.
The example: wallet-grist's `ledgerAcksUnposted`, a proxy acknowledging
every post and forwarding none, takes only steps the actions admit and
is caught by `ledgerPostEventuallyServed` alone; with the obligations
out of the binding every run conforms. Pre-existing, not P7's: a
`Delivers` handler's Fail is a Mismatch of its skip (no rule says a
trigger fails), so the at-least-once obligation's `grist.failed` is
only an effect's that failed every try.

### Eleventh review: silent wrong answers

The formal-methods review (review 11) built adversarial inputs and found
verdicts quint does not give. Each reproduction is a regression test now
(qtest/sound_test.go and the packages' own), failing without its fix:
- **F1, a set fold's order** (critical). The Go evaluator folded a set
  sorted and reversed and took agreement as order-independence. A round
  robin over `Set(1, 33, 2)` agrees both ways and not in quint's order
  (which is no documented one: TypeScript's hash order depends on the
  insertion order too, Rust's is another), so `simulate -graph` said
  "holds" over a complete graph of an invariant quint violates, and a
  replay passed a step quint rejects. A fold is now the evaluator's only
  when every order gives it: an operator of a shape that commutes (sums,
  min and max, unions), or a set of at most 10 elements tried in every
  order; otherwise Unsupported, and quint judges. The repository's folds
  (grist's min, the examples' sums and maxima) are all of the shapes;
  `TestAgreesWithQuint` agrees on every model as before.
- **O1, `-graph`'s "holds" on the evaluator alone.** A complete graph
  is now checked against quint's sampling of the instance (up to 50
  traces): a state quint reaches that the graph lacks is an evaluator
  bug, a FAIL that voids the graph's verdicts. With F1's old fold
  restored, rr's run fails on its first sampled state
  (`TestSimulateGraphCheckedByQuint`, by a tampered graph). Truncated
  graphs claim no proof and are not checked.
- **O2, liveness's wording.** DESIGN §62 said a hot cycle is "a
  behaviour that never makes it true" and that no lasso is a false
  alarm for want of a declaration. What is checked is □◇P (P at
  quiescence), under weak fairness per action: a lasso's prefix may pass
  through states where the val holds, and per-call or strong fairness
  oblige more, so a lasso may be no behaviour of a system that has them
  and `live` holds only for a system at least that fair. §62, the
  `cycles` line of the report, the doc comments and the skills say so.
- **F2, a choice inside a bound action** (high). An action that chooses
  (`any { x' = 1, ..., x' = 4 }`, a die's nondet) whose expect reads the
  result: quint's replay draws the choice, and three validations of one
  input failed after step 2, step 1 and step 1; `lint -binding` said the
  binding fits. A failure at or after such a step is now no verdict
  (`the model's choice, sampled`), its issue naming the action, the
  choice and the fix, and the lint warns of it
  (`TestSoundInternalChoiceIsNoVerdict`, `TestLintInternalChoice`). No
  example's binding has one. Pinned replays (the evaluator's witness
  written into the replay, so quint confirms rather than draws) are left
  for ROADMAP I4: they need a replay of a step that names its choice,
  which no binding template writes yet.
- **F3, ResolvePending's first placement** (high). The run's invariants
  were judged on the first placement of its pending calls that conforms;
  one that violates an invariant hid a later one that keeps them, which
  Linearize's second search (orderIssue) found: the same history was
  Violates with ResolvePending alone, not with Linearize too. The
  resolution now searches its later placements for one that conforms
  with every invariant holding, and a cut search is no verdict
  (`TestSoundResolveKeepsInvariants`,
  `TestResolvePendingInvariantsAgainstBruteForce`).
- **F5, clocks across recorders** (medium). Linearize ordered two
  recorders' steps by their clocks within a fixed 1 ms and said nothing
  of it: a `NotLinearizable` proof was true only under that clock
  assumption, which quint's re-check cannot touch. The verdict now names
  the orders it took from clocks, their recorders, the closest gap and
  the skew assumed; the skew is a setting (`qtest.ClockSkew`,
  `validate -skew`, `SearchOptions.Skew`, `LinearizeOptions.Skew`): a
  stale read 2 ms after a write on another host is a Mismatch "assuming
  the clocks within 1ms", and linearizable with a skew of 5 ms
  (`TestSoundClockSkewNamed`).
- **F4, an unknown outcome's timeout as its completion** (medium).
  qhistory's real-time edges used `Completed` of every committed or seen
  transaction, an Unknown one's too: a timeout at 1 of an append a later
  read saw made T2's empty read at 2 a G-single-realtime, though the
  append may have committed after 2. An Unknown transaction's completion
  is now no time, as Elle's `:info` (`TestUnknownCompletionOrdersNothing`;
  `TestStrictAgainstBruteForce` now draws Unknown transactions too, and
  fails without the fix). qenv's store history (internal/history, the
  same classifier) has no real-time edges and only committed
  transactions, so nothing there changes.

### I11: the evaluator against quint on generated expressions

ROADMAP's I11, from the formal-methods review's idea 3: the evaluator
decides passing runs (DESIGN §56), and what checked it (TestAgreesWithQuint,
the nightly's shadow, a sampled 1 in 50 passes) saw only what the
repository's models use. internal/qeval's differential test now
generates what they do not, and has quint's two backends judge it.

The decisions:
- **Hand-rolled on math/rand, not Hegel.** The core does not import
  Hegel, and internal/hegelprops would have to reach qeval's unexported
  evaluation for actions. A seed makes one batch, the same everywhere;
  a disagreement names it (`GOTOGETHER_QEVAL_FUZZ_SEED=n`).
- **Batches through `quint test`, not the REPL or `quint run`.** One
  module of a val (or an action), a variable of its type and a run
  assigning it per entry: each run's ITF is quint's value, each failing
  run its own error, so one entry's division by zero leaves the other
  199 judged. Three processes a batch: a compile (the evaluator's IR) and
  a test per backend.
- **Actions judged two ways.** quint samples one successor; the
  evaluator's set must hold it, and must equal the action read as an
  expression (a choice's flattened map, a guard's `if`, `any`'s union),
  which quint judges as any other entry: a successor the evaluator
  misses is found though quint never reports the set.
- **Actions from a state too**: half the actions start where their
  run's first step puts them (`(v' = s).then(a)`) and read v; their
  successors' expression reads s in its place.
- **Shrunk by batches of reductions**: every one-step-smaller tree in
  one module, the smallest that still differs the same way kept, until
  none does.
- **Full mode only**: a batch of 200 costs about 70 CPU s, most of it
  quint's typechecker (three typechecks of the generated expressions) and
  the Rust backend's runs. Two batches of 100 per push, 80 of 200 a night
  (`qeval-differential`, about 25 minutes on four cores).

What it found, at its first runs:
- **A let evaluated eagerly** (fixed). `{ val n = 1 / 0; 0 }` is 0 on
  both backends: quint evaluates a let where its name is read. The
  evaluator bound it at once and threw a division by zero, a map's
  missing key, a list's empty head. A let's failure is now thrown where
  its name is read (`bindLet`).
- **exists and forall stopped at the evaluator's first witness**
  (fixed). TypeScript evaluates every element (`Set(5, 9, 33).forall(x
  => if (x == 33) 1 / 0 > 0 else false)` is a division by zero), Rust
  stops at a witness in its own order (false); the evaluator gave false,
  a pass where TypeScript fails. Every element is now evaluated; a
  failure is the model's runtime error when no element decides (both
  backends fail), and Unsupported when one does.
- **setToMap kept the evaluator's choice of a key's two values**
  (fixed). `Set(("k", ["a", "a", ""]), ("k", ["ab", "a",
  "ab"])).setToMap()` is Map("k" -> ["a", "a", ""]) on Rust, and the
  evaluator kept the greatest pair's: a value quint contradicts, found
  by an action's successors (a choice over a constant set mapped to a
  setToMap) once the generator's actions started from states too. A
  key's second pair is now Unsupported.
- **quint's Rust backend writes integers past 2^53 into ITF as
  bignumber.js objects** (`{"s": 1, "e": 18, "c": [46116,
  86018427387904]}` for 2^62), in `quint run` and `quint test` alike;
  TypeScript writes `#bigint`. The harness reads them back (a record of
  the generator's has no fields e and s). The toolkit's own ITF reader
  does not (ROADMAP I11's note): a state with such an integer from a
  Rust trace reads as a record.
- **Tests that fail without it**: each fix undone fails
  TestDifferentialFindings (and both backends' judgement of its case);
  an evaluator whose `%` is Euclidean fails the push tier's second batch,
  shrunk from a 300-character `any {}` to `v8' = (1 % (-2))` (the
  evaluator -1, quint 1).
- **Harness lessons**: the Rust backend refuses a whole module with a
  literal past 64 bits (every run, QNT600), so the generator writes
  none; a pipe loses what node has not written when it exits (the
  TypeScript report stopped after 23 failures), so compile and test
  write to files; `quint compile` prints nothing for a parse error,
  typecheck does; a let's body starting with `(` applies the value
  before it (`val n = r.c` then `(x)` is `r.c(x)`): lets end in `;`.

### I23: sqlstore reads in one query

ROADMAP's I23, from the Go specialist's finding (review 11): a read
fetched the index's positions, then each event in a statement of its
own. Now a read is the head, then one statement per type of the
selector (a selector's types are a property of the read model, not of
the log):

    SELECT grist_events.pos, ..., grist_events.body FROM grist_index
      JOIN grist_events ON grist_events.tenant = grist_index.tenant AND grist_events.pos = grist_index.pos
      WHERE grist_index.tenant = $1 AND grist_index.k = $2 AND grist_index.pos > $3 AND grist_index.pos <= $4
      ORDER BY grist_index.pos [ASC|DESC] [LIMIT $5]

The decisions:
- **qsql gained a join, rather than sqlstore a dialect's statement.**
  sqlstore's promise is one SQL on every dialect, so the simulated tier
  runs the real tier's statements. qsql's join is a lookup: ON fixes the
  joined table's whole primary key from the first table's columns, an
  inner join, every column qualified, WHERE and ORDER BY on the first
  table, no aliases and no FOR UPDATE (TestJoinAgainstBruteForce, payd's
  TestQsqlAgainstSQLite). Each joined row is a read of its own, so at
  read committed a commit may fall between them, where PostgreSQL reads
  one snapshot; events below the head never change, so a read sees the
  same.
- **One statement per type, merged in Go**, not `k IN (...)` or a
  `UNION`: qsql ranges one key prefix at a time, and a type's events are
  no other type's, so the merge is a sort. A type named twice is read
  once (storetest's reads now check it; the old code deduplicated
  positions). With two tags or more the second is checked on the rows,
  and a type's rows are read until the limit matches.
- **A condition with at most one tag still reads positions only**, under
  the head lock; one of more tags reads through the join, until a match.
- **The tags column is decoded without encoding/json** when it is what
  json.Marshal writes for strings that need no escape (a third of a
  read's time went to json.Unmarshal); anything else falls back to it
  (TestDecodeTagsAgainstJSON, against json.Unmarshal on marshalled,
  cut and mutated inputs).

`BenchmarkRead` (grist/sqlstore: events alternating two types, each
tagged with one of 10 accounts and one of 3 kinds; medians of 3; SQLite
a temp file, PostgreSQL 16 over a local socket; qsql the simulation's
Store at read committed):

| read | events returned | SQLite before → after | PostgreSQL before → after | qsql before → after |
|---|---|---|---|---|
| 10^3, both types | 1,000 | 38.7 → 7.7 ms | 49.9 → 6.2 ms | 69.5 → 49.7 ms |
| 10^3, one type | 500 | 19.3 → 2.8 ms | 21.3 → 2.7 ms | 32.3 → 22.3 ms |
| 10^3, one account | 100 | 3.3 → 0.75 ms | 4.9 → 0.69 ms | 6.0 → 5.5 ms |
| 10^4, both types | 10,000 | 368 → 83 ms | 480 → 65 ms | 559 → 333 ms |
| 10^4, one type | 5,000 | 158 → 37 ms | 262 → 31 ms | 342 → 226 ms |
| 10^4, one account | 1,000 | 36.6 → 8.4 ms | 45.0 → 6.2 ms | 78.2 → 66.8 ms |
| 10^4, two tags | 333 | 41.4 → 6.9 ms | 42.2 → 5.3 ms | 73.7 → 40.7 ms |
| 10^4, LIMIT 100 | 100 | 4.3 → 1.6 ms | 6.0 → 1.4 ms | 23.9 → 28.4 ms |

About 37 µs an event before on SQLite, 8 after; 48 before on
PostgreSQL over a socket, 6.5 after (and one round trip per type, not
per event, over a network). Allocations fell by two thirds (10^4 events:
550,000 to 180,000). qsql gains least: every row the join looks up is a
read of the Store, a scheduling point, as the statement per event was;
its LIMIT 100 is slower by the noise of a range it materializes whole
either way.

The indexes: SQLite searches grist_index's primary key for the range
and grist_events' by (tenant, pos), no scan and no sort
(TestReadPlanOnSQLite, which fails if ON drops the tenant: `SCAN
grist_events`). PostgreSQL 16, ten tenants of 10^4 events, ANALYZEd:
an account's events with a LIMIT are a nested loop of an index-only scan
of grist_index_pkey and an index scan of grist_events_pkey (0.4 ms for
100); a fifth of a tenant's events (2,000) a hash join of bitmap scans
of both primary keys, then a sort (3.6 ms), the planner's choice for a
large share; one tenant alone, a sequential scan of grist_events (the
tenant is the table). No index is missing.

The adopter's other store findings are not the read path: every append
in a tenant serializing on its head row is by design (dense positions,
conditions under the lock), and is in I25 with LISTEN/NOTIFY; steps and
markers growing forever was in I25 already (retention).

### I21: packaging, and a model compiled for the evaluator alone

ROADMAP's I21, from the adopter's review (review 11, AD3: every
adopting repository copied `ci/install-quint.sh`, pinned quint by hand
and had no word on what to run when). DESIGN §64 has the design. What
was built, and decided:
- **A composite Action, not a reusable workflow.** It runs in the
  adopter's job, after their checkout and build, where a reusable
  workflow would be a job of its own that cannot see their binary under
  test. It reuses install-quint.sh from its own checkout, caches `~/.quint`
  and gotogether's cache of quint's typechecks and compiles, builds the
  CLI from its own source by default (the CLI is the ref the action is
  pinned at: one pin, not two that drift), runs `doctor`, and writes the
  `RESULT:` lines to the job summary, a failing one an annotation. CI's
  `action` job uses it on hello (a pass, then the noDedupe mutant's runs
  failing the step), `action-compiled` with no quint at all.
- **An image built, published nowhere.** `ci/image/Dockerfile` pins Go,
  Node (checked against nodejs.org's SHASUMS256), quint and its
  evaluator; CI's `image` job builds it and runs `doctor -strict` and a
  validation inside. Which registry and name is the maintainer's call.
- **doctor, extended**: the quint cache's place and size, a quint older
  than 0.32 as a skew, `-binding` (a binding's format, its spec found)
  and `-compiled` (a compiled model checked as validate checks it).
  Still missing from the entry's list: qhegel's version beyond what the
  module requires (doctor lists the published modules it resolves).
- **The tiering guide is a document**, docs/ci.md, with this
  repository's costs: per push, nightly, per release.
- **The compiled model (the M half).** `gotogether compile` and
  `validate -compiled` (DESIGN §64). The soundness rules come from the
  eleventh review's silent wrong answers: the evaluator's refusals (F1's
  set fold, F2's choice) are no verdict, "needs quint", where the default
  path hands them to quint; nothing is sampled, and the report says the
  evaluator alone judged; staleness is refused by the binding's and the
  sources' sha256 and by writing the evaluation module again and
  comparing it, text for text, so an artifact of an older toolkit whose
  module differs is refused rather than judged against vals it does not
  have. Failures are written in quint's terms from the evaluator's walk
  (a disabled step against an expect false told apart by whether the
  walk kept a state after it; each invariant's first failing state found
  by evaluating it on every state, as each of quint's invariant runs
  reports its own), and `TestCompiledAgreesWithQuint` compares reason,
  kind, index and call with quint's on the account and within models
  (ablated: the expect-false case read as disabled fails it).
  `TestCompiledStaleRefused` fails with any of its checks removed;
  `TestCompiledUnsupportedIsNoVerdict` with a refusal taken as a pass.

### Eleventh review: the first hour (I28–I32)

The usability designer's two newcomers (a REST developer, a team lead
trying grist) lost most of their first hour to output that did not
say what happened. What changed, each with a test that fails without it:
- **A catch, shown** (I28). hello's `go test -v` printed no catch: a
  passing expected-failure claim logged its whole report, and
  `qtest.Mutants` dropped it (its first line has no "runs, "). The claim
  now logs three lines (the run and its faults, the step and why the
  model refuses it, `navigate:`; `Run.Brief`), the report under
  `GOTOGETHER_VERBOSE=1`; Mutants passes catches on through `t.Output`,
  under the binary's own file:line; qhegel's shrink is three lines too.
  The README quotes hello's real output.
- **First runs** (I29). `grist new` said `go test ./...` (343 s, silent);
  it says `go test -short -v ./...` with its time, and that the first
  run downloads the toolkit. An exploration writes a progress line every
  10 s to the terminal (`/dev/tty`: go test holds back a package's
  stderr until it ends when it tests several; none without a terminal,
  `GOTOGETHER_PROGRESS=0` none at all). A short exploration
  (`ShortSeeds`) tops up its seeds, a batch at a time up to `Seeds`,
  while a kind its Rates set was reached and faulted by no run: a
  `Reaches(k).Faulted()` claim fails under -short only where it would in
  full (a grist app's commit faults, missed by 3 seeds).
- **Reserved names** (I30). AGENTS.md's hand-kept list missed `item`,
  `int`, `str`, `bool` and `temporal`. internal/quintnames is the table,
  checked against quint's `builtinNames` and its lexer's keywords;
  LoadModel's error carries quint's own errors (with `--out`, quint
  writes them there, and the error said "exit status 1") and names the
  rename; scaffold writes `item_: '{{.item}}'`; `lint -binding -src`
  names a missing mapping; grist's `QuintTaken` gained int, str, bool.
- **Reports** (I31). "30 runs, all conform (quint confirmed 2)", the
  evaluator's breakdown verbose; actions only bound actions call (a
  grist model's appends: `Model.CalledBy`) are no steps; "every run
  rejected at its first step, addItem"; Record's passing Expect logs a
  line; "the run" for one unnamed actor; "steps", not "calls"; "the then
  branch of if". A zero-step run is "record no step", with "no faults",
  and qhttp's refusals before a server listened reach its report
  (`hooks.Notes`) and the navigator's node (it read pass). Failure
  copies go to `$TMPDIR/gotogether/`, said to stay.
- **help and nav** (I32). `gotogether help` is one line per command (the
  question it answers, an example; `-graph` on simulate's), flags in each
  `-h`; `validate`, `itf` and `convert` without their inputs say what
  they need; nav's path error numbers the branches (`#1 apply(1)`) and
  says bare numbers are a run's options; a disabled action explained
  under a nondet it never reads names no pick (hello's `done` was "id =
  1, the one closest to enabled"); a run node's title is in words first
  ("after 4 choices: client ran 4 times").

### I27 and I34: the structure written down

ROADMAP's I27 and I34, from the designer's F5, F6 and F19 and the
architect's F3 and F14 (review 11): grist's only user doc was a design
note that opened with slices and a codename, and taught two names
(`UpcastFrom`, `Reindex`) never built; the toolkit's structure was in
AGENTS.md's terse index and DESIGN's 64 sections in build order.

**I27, grist's user guide.** docs/grist-guide.md, what is built and
nothing else: `grist new` and the loop; events, read models, reads,
commands, rules, queries, processors, dependencies and calls,
authorization; the HTTP API's routes and codes and grist/client; the
binary's commands; the generated model (what the generator writes, the
holes it names, quoted from a real run: a `remove-item` added to a
fresh app, its three holes and the fold warning), testing, versioning
with the tutorial on library's book-added (the lock's refusal, the
upcaster, the `dropsCopies` mutant's CheckUpcasters finding and its
`Mismatch("addBook")` on the kept log, readers first), snapshots,
production, and a troubleshooting table of the messages as the code
writes them. Decided:
- **A guide in docs/, the note kept for maintainers.** grist/README.md
  (the roadmap's first idea) would be pkg.go.dev's page, where relative
  links to examples break; it waits for the module's tag. grist.md opens
  with "Building an app? Read the user guide" and a table marking each
  section built or designed.
- **Prose names checked.** internal/docsnippets checked fenced blocks
  only, and only the core's packages: grist's eight are in its map now
  (grist.md's `grist.Role` in two proposed blocks was caught and made
  `grist.Callers`), and TestProseNames checks every backticked
  `pkg.Ident` outside fences against its package's exports. A design
  says what it has not built with `<!-- not built: grist.Within ... -->`
  under its section's heading (22 names in grist.md, grist-platform.md
  and grist-instrumentation.md), and a marker naming what is exported
  fails, so a built name cannot stay marked. Records are exempt (HISTORY,
  ROADMAP, CHANGELOG, DESIGN, refactors, performance, the TRIAL.md
  friction logs): they name what was. instrumentation.md's
  `binding.StepsToITF` (a method) became `Binding.StepsToITF`.
- **The skill links the guide.** The grist skill's versions reference
  shrank to the rules an agent must not get wrong and its results table,
  the walk-through linked; loop.md and triage.md link the guide (by URL:
  installed skills carry no docs/).

### H4: one vocabulary

ROADMAP's H4 as built, with two leftovers of P7's review. The decisions
it left:
- **Caller, not actor**: "actor" is the core's, a recorder's sequence of
  steps (qobs.WithActor, `actor <name>:` in a report, a binding's
  `actors:`), and grist meant both by it: a commit's actor the tenant, a
  client's `<tenant>/clients`, and a Policy's authenticated principal.
  Only the principal is renamed (`WithCaller`, `CallerOf`, `Callers`,
  `CallerFromHeader`, `Envelope.Caller`, `Reads.Caller`,
  `CallerHeader`, `-caller-header`); `gristsim.ClientsActor` and every
  `actor` a site or commit records stay. The step field it is recorded
  as went with it (`principal` to `caller`), so the vocabulary is one
  from the API to the model's argument records: the cost is every app's
  rules that read `c.principal` (counters' two), and a regeneration.
- **`Guarantee`** for `grist.Delivery`; the option stays `Delivers`
  (what a processor does), its argument the guarantee.
- **The catalog's claims are qtest's, the catalog grist's.** grist
  imports no qtest (a production binary would link the validator), so
  `gristtest.Catalog(App)` builds a `qtest.Catalog` (a name and a Take
  per block and outcome, each with its hint), and the claims are its
  methods: `Exercised(except...)` and `Take(name)`. Not a second
  `qtest.Exercised(catalog, ...)`: the model-wide Exercised's exceptions
  name actions, fault kinds and nemeses, the catalog's blocks by
  outcome, and one function taking both would read the one as the
  other. Any system can list its own Catalog. `gristtest.Scenario`
  stays, built from `grist.Invocation` values, the catalog's typed
  arguments.
- **The overlay harness in the core**, `qtest.Mutants`: its mutants the
  directories of `testdata/mutants` (a new directory is a new mutant,
  and a `TestMutant` whose `default` fails says what catches it);
  `Package` for a harness package replacing another's files (payd's
  and webjobs'), `Overlay(t, name)` for `go build` of a mutant program
  (webjobs' server under -short), `Generated` for mutants a binary makes
  of itself (grist's dropped rules: `gristtest.RuleMutants`, whose init
  reads `qtest.Mutant()`), `Show` for the output lines a harness wants
  logged. `GOTOGETHER_MUTANT` is qmut's variable already (a number, or
  `list`): `Mutant()` reports neither, and a directory named so is
  refused, so one variable names a mutant of either kind.
- **Overlays the taught way.** examples/hello's `Mutation` enum is a
  file now (`testdata/mutants/noDedupe/hello.go`): its NoDedupe tests,
  the exhaustive search of one fault and Hegel's shrink moved into
  `TestMutant`'s case, the faithful Hegel run left as
  `TestHegelFindsNothing`, and `qweave -mutate` weaves 10 mutants of it
  where the compound condition made 8. `gotogether scaffold -system`
  writes `TestMutant` and `TestMutants` instead of an enum (skipped
  until a mutant is planted, failed under GOTOGETHER_STRICT=1); `grist
  new`'s starter had overlays already and uses the core's harness. The
  other examples' enums stay (checkout's, wallet's configurations are
  their own API); the skills teach overlays first, qmut as the
  generated way.
- **A call's obligation where the variant judges its records** (P7's
  review, decision 4 of docs/grist-instrumentation.md §5): a variant
  that judges every default invariant gets the processors' obligations,
  and a Keyed call's `<call>EventuallyServed` only where one of its own
  vals reads `<call>Records`, directly or through a def of the model or
  the rules (comments left out). A third party's dependency on a real
  tier records no site, and the obligation would have failed every run
  there. counters' `real` judges the bank's records and keeps its
  obligation; the shop golden's `audit` judges the warehouse's picks,
  not its holds, and lost `warehouseHoldEventuallyServed`.
  `TestCallObligationsFollowSiteClaims` fails without the filter, and
  with the defs' closure taken out.
- **Left, as ROADMAP items** (GR10b, GR10c): a poller a daemon only
  while it waits on the store, so a settle waits for work in hand and a
  hung handler reads `Unsettled`; and `Settle` naming the processes down
  at the settle, so a node left down for good does not have its triggers
  judged as a live one's. Both are qenv's: Settle's other users
  (quorumkv's node down for good) would meet them too.
- **H4's review.** A Package harness's mutants (payd's, webjobs') did
  not navigate: the navigators overlaid the harness's own directory and
  failed; `GOTOGETHER_MUTANT_PACKAGE`, set by `Mutants.Run` and passed
  on by the navigate line, names the package. An overlay that changes
  nothing (the package's files but for comments and layout, compared
  token by token with directives kept) fails, as do a `Dir` given that
  does not exist and a name a shell or `-run` would not keep;
  `Catalog.Exercised` with nothing left to want panics. One variable,
  `GOTOGETHER_MUTANT`, stays for both kinds of mutant: a woven mutant is
  on per run (`qmut.WithMutant`) or per process by number, an overlay
  mutant per binary by name, the two value spaces kept apart by the
  refusal of a number or `list` as a name, and no binary is both (an
  overlay's binary is built by the plain go command, which weaves
  nothing). Two variables would buy nothing a refusal does not, and cost
  a second name in every reproduce and navigate line. The cost that
  remains: a package whose tests need weaving cannot use `qtest.Mutants`.
  examples/kvstore's `Mutation` enum became five overlays of `kv/`
  (`qtest.Mutants{Package: "kv"}`), and its faithful load claims a
  hand-built `qtest.Catalog`, the catalog's shape outside grist.

### E: a run gotogether did not drive

From the feedback of an agent using gotogether in another project (an
observability app explored by Bombadil), planned by a design review as
eight items, each built and then reviewed on its own: DESIGN §54 (§53
for E7). Done: a finished run stated (`qtest.Finishes`, `validate
-require-settle`, `complete: true`, `Report.Finished`); strict mode
(`GOTOGETHER_STRICT`, `qtest.Optional`, `evidence -expect`); step
references (`quint.ref`, `qobs.Ref`); run manifests (`gotogether.run/1`,
`-manifest`, `-run-info`, `-replay`, bundles, models cached by
content); examples/webjobs with Bombadil and `bombadiltrace`;
observation mutants (`qtrace.Mutations`, `qtest.MutateSteps`); absence
diagnostics and watermarks; and `qtest.ShrinkActions` (ddmin,
`SameFailure`, `ErrDiverged`, `Calibration`, the reset contract), with
webjobs' mutants shrunk to one to three actions.

The reviews found:
- E1–E2: the finish judged twice and disagreeing (a placed pending call
  passing the CLI, an ITF input passing a finish it cannot record, a
  binding with no eventual val passing a settled prefix), now one
  `Report.Finished`; strict mode not reaching the core's own skips (now
  internal/skip), and `evidence -expect` missing other modules'
  packages;
- E3–E4: a replay checking another model than it judged (the spec's old
  path hashed, its imports not), an ITF input's instance not recorded,
  the check recorded not the one judged, evidence that replayed only on
  the machine that wrote it (now `Manifest.Bundle`), the backend not
  kept; a cached verdict's failing step not found without a seq;
- E5: calls completed by the next change of the page, so a cancel
  answered 500 and ignored passed; the page's end unstated, so a page
  that stopped polling passed; probes now test each way the first
  version passed, and a Go client runs the real server per push;
- E6: kills that were Reconstruct's (a reactor across Lamport keys),
  now left out or `FateMalformed`; retimes past their actor's next step;
  a brute force sharing code with what it checked. On webjobs it found
  two gaps in the model (a job created twice, a submit answered 199);
- E7: listed in DESIGN §53;
- E8: the reset contract requiring an empty rerun to record nothing,
  which a fresh server's shutdown breaks (now two empty reruns
  compared); reruns with no verdict counted as misses; leaks of the
  actions themselves unchecked; Keep's default taken from a run that
  might have had no verdict; two ways of failing read only as a leak;
  SameFailure not in the replay's order, a mismatch without its fields;
  two shrinks overwriting one directory's artifacts. webjobs' review
  found a rerun that ended before the page saw its job end failing the
  within on the faithful server; every shrunk list is now checked there.
  Not fixed, and said: state the actions leave without a step of its
  own passes every check; only a fresh environment per rerun is safe.

### The E track as it closed

ROADMAP's E section when its last item (E8) was done, moved here whole
(E8's webjobs integration, done after the section last changed, brought
up to date); what it left is in ROADMAP's "Left from E".

#### A run gotogether did not drive (E)

From the feedback of an agent using gotogether in another project (goo:
an observability app checked with Bombadil, Antithesis's browser
explorer). Planned by a design review; sizes as in S. Order: E1 → E8.

- **E1. Completion stated (S). Done:** `qtest.Finishes()` (conforms,
  Settles, every eventual val judged and true, Completes; three-valued; a
  binding with no eventual val finishes no run; a miss names the part
  and the fix), `validate -require-settle` (the same judgment,
  `Report.Finished`: `incomplete (no settle)`, `(pending calls)`, a call
  `-resolve-pending` placed included; `no verdict (lost tail)`, `(ITF
  trace)`, exit 1; a binding with no eventual val refused), a
  binding's `complete: true` (the CLI's default only, not a test's
  claim; needs an eventual val); patterns and verdicts docs ("a prefix
  vs a finished run").
- **E2. Strict mode (S). Done:** `GOTOGETHER_STRICT=1` turns every
  toolkit skip (RequireQuint, Mutate with no mutants woven, Record's,
  Explore's and qhegel.Shrink's SkipIfEmpty, the scaffold's mutant test)
  into a failure with its message; `qtest.Optional(t, reason)` for users'
  own gated skips; `evidence -expect checks.txt` (tests, subtests or
  exploration ids, optionally after a package) fails naming a check that
  wrote no evidence (packages by import path). CI runs strict, the core's
  own tests through internal/skip, but for the examples' unwoven mutation
  and coverage tests (skipped by name) and edgepublish's unwoven run.
  Left: the nightly is not strict (its fresh-seed runs include the
  unwoven mutation tests); evidence a renamed test left in a reused
  directory still counts; Hegel checks write no evidence (Validate's and
  Record's write their manifests, E4).
- **E3. Step references (S). Done:** `quint.ref` (`Step.Ref`: a step's
  link to its artifact, a driver's trace line, a screenshot),
  `qobs.Ref(s)` in a key's place (a typed value, so a parameter named
  ref stays an argument) and a log record's `quint.ref`; kept by step
  logs, OTel events (the event's or span's, never a resource's), convert
  rules (`ref:`, default the span's own) and from an invocation to its
  completion; shown at the failing step (`ref:`), in `-history` and
  `steps`. No verdict or cache key reads it; a cached verdict shows the
  run's own failing step's ref. `Filter` drops refs when it sanitizes,
  unless `KeepRefs` (`steps -keep-refs`).
- **E4. Run manifest (M). Done:** `gotogether.run/1`
  (`validate.Manifest`): gotogether's version (build info) and quint's,
  the binding, spec and convert rules with their sha256, the instance's
  constants and invariants, how it was judged (searches,
  -require-settle, declared losses), each input's path, hash, step count
  and loss records, the verdict (`VerdictOf`: by actor, its reason, the
  failing step with its ref), and the caller's driver and artifacts
  (hashed). Written by `validate -manifest` (driver and artifacts from a
  `-run-info` JSON file the driver writes), by qtest's Validate and
  Record runs under GOTOGETHER_EVIDENCE (`run<N>.json` beside a copy of
  the step log; `qtest.Driver`, `qtest.Artifacts`, `Run.Manifest`, so
  `Runs` gathers runs that each have one), and by `Checker.Manifest` for
  callers. Paths under the manifest's directory are relative: the
  directory moves whole. `validate -replay` refuses a changed input,
  binding or spec (a changed artifact warns), takes no flag the manifest
  decides, and exits 1 on another verdict. `gotogether evidence` lists
  manifests (role external), and `-expect` names them as explorations.
  Review fixes: the spec checked is the one the binding resolves now,
  with its imports (hashed transitively); an ITF input records its own
  instance; the check recorded is the one judged (a complete binding's
  finish check in the CLI, none in qtest); qtest bundles every file
  (`Manifest.Bundle`), so an evidence directory replays elsewhere;
  gotogether's version is a dependency's when it is one; the backend is
  recorded and kept on replay; a reworded reason is the same verdict
  (`Same` compares kinds); a later version's manifest is skipped by
  `evidence`. Left: no option sanitizes a manifest itself (sanitize the
  log, then validate it with -manifest). E3's: a cached verdict's
  failing step is found with no seq too, its recorder and process
  rebased; qhegel caches summaries without refs; a zero step time is
  written as none.
- **E5. A Bombadil example (M). Done:** examples/webjobs, a
  standard-library web app (one HTML+JS page: submit, 202 accepted, a
  background job, poll until ready, cancel) explored by Bombadil 0.7.9
  (`bombadil/spec.ts`: custom actions ui.submit, a double click, and
  ui.cancel; the `result` extract). The server records its effects on
  recorder `server` where it makes them (created, finished at the
  commit, cancelled, drained, then a settle); `bombadiltrace` turns
  trace.jsonl into invocation records completed only by what a snapshot
  first shows (submit by a new id, cancel by a status change; a job
  shown ready by polling a step of its own), each with `quint.ref`
  (line and screenshot), Bombadil's own times, call ids and order,
  written as steps, never re-recorded; a trace outside the page's
  contract refused, a torn line a lost tail. The model (a ghost `done`,
  `eventually: allResolved`, a `within`) and a `complete: true`
  binding. Overlay mutants (readyEarly, doubleJob, cancelIgnored), each
  caught by its invariant. Per push (the examples loop, -short):
  recorded 3s runs of the real Bombadil checked offline (faithful
  `Finishes`, its CLI manifest from `-run-info` replayed; each mutant's
  claim; a pending click; a torn trace; `TestTimesKept`, goo's point as
  a test). Nightly (`webjobs-live`, strict): 45s live runs of the
  faithful app (manifest replayed, `--reproduce` best effort: a
  divergence's prefix must conform) and of each mutant. The reference
  page skills/gotogether-traces/references/external-drivers.md (a
  settle is not a collector drained: E7). Review fixes: a call is
  completed only by its own request's answer (the page's `#log` of
  requests, numbered as sent, read by the extract; the invocation's
  arguments are what the request sent), never by the next change of the
  page; the page's end stated (`pageShowsEnds`, a 500ms `within` on a
  committed job shown; `?until=` ends a live journey's submissions before
  the time limit); `finishedOnce`, `noCommitAfterCancel`,
  `noFailedRequest`; Bombadil's exit 2 fails a faithful run; mutants
  cancelBroken, statusStale and commitAfterCancel; a Go client
  (`Client`) runs the real server per push, faithful and per overlay;
  probes of each way the first version passed; core:
  `qtest.Undecided`, and Reconstruct's check of an invocation record
  against its completion. Left: Bombadil's reproduction diverges on a
  timing-dependent page; one host's wall clock orders the page and the
  server.
- **E6. Observation mutants (S–M). Done:** `qtrace.Mutations` (drop,
  dup at the next seq, swap within one recorder and incarnation,
  corrupt each argument and observation, retime past the next step in
  time, reactor into the next actor's process; `MutateOps`,
  `MutateLimit` spread over the log), each keeping the recorder's
  contract (dense seqs, calls and losses renumbered), checked against
  every mutation of a small log written out by hand and against
  Reconstruct's error issues on a log with calls, a settle and a loss.
  `qtest.MutateSteps`: killed (fails where the original conformed,
  refused by the binding, or unfinished where the original finished),
  survived, undecided; `Killed`/`Survived` over `StepOp` selectors; a
  corruption of a field the binding does not read marked `Unread`;
  `JudgeStepMutants` and `gotogether validate -mutate-steps`. bank's
  deposit balance off by one killed, surviving with its expect ablated;
  webjobs' recorded journey (`TestObservationMutants`, full mode and
  nightly): a job created twice and a submit answered 199 survive, two
  gaps in the model. Review fixes: candidates Reconstruct would refuse
  left out (a reactor into an actor whose Lamport keys contradict its
  thread), a mutant refused anyway `FateMalformed`, never killed; a
  retime never passes its actor's next step (a within judged, not left
  unjudged), a settle or a watermark, and a survivor with its bounds
  unjudged is undecided; the brute force a naive enumeration over six
  shapes (incarnations on two recorders, Lamport keys across actors,
  watermarks, calls, losses, no seqs). Left: a mutant per pair of mutations; retime
  earlier; a corruption of the outcome and the error text.
- **E7. Absence diagnostics (M). Done** (DESIGN §53): a watermark record
  (`quint.complete_through_unix_nano`; `qobs.Recorder.CompleteThrough`,
  `qtrace.MarkComplete`, `validate -complete-through r=T`, `*=T`; kept by
  readers, Filter and searches) and the report's `absent:` section
  naming each absent step's cause: dropped (a gap, a lost tail), uncollected
  (past the watermark, or no settle and the trace ended first), or a
  defect (the only FAIL); `qtest.Absent(name, cause)`. A within is judged
  where its watermark reaches its window though the run never settled;
  a settle no watermark reaches settles nothing; a failure a gap may
  explain is no verdict. A log with no watermark is taken whole, as
  before. Left: a collector-side writer (an OTel exporter that emits the
  record after a flush); a pending call's window (a caller's timeout).
- **E8. External shrinking (L). Core done:** `qtest.ShrinkActions`
  (internal/ddmin: ddmin over index sets, three-valued, 1-minimal; each
  candidate rerun after a `Resetter`, its steps judged through the
  verdict cache), the reset contract checked first (two reruns of no
  actions record the same steps, up to names and times, not none: a
  fresh server's shutdown steps repeat, a leak differs; the original's
  first two reruns fail alike), `SameFailure` the
  failure kept by default, `ErrDiverged` never a pass, `Calibration`
  (n = ⌈ln α / ln(1−p)⌉ from the original's 10 reruns, p at its Wilson
  lower bound: from the rate measured, a half-rate failure reached its
  1-minimal sequence in 933 of 1000 shrinks, under 1−α, the bound in
  987); the minimal run's manifest with actions.json and shrink.json.
  Review fixes: a rerun with no verdict for the failure is undecided,
  never a miss (SameFailure unknown with a lost tail, an undecided actor
  or a conforming actor's invariant unjudged); the empty rerun repeated
  after the baseline and at the end (leaked work), the empty sequence
  never a failure, a shrunk sequence's rate far below the original's
  not minimal; Keep's default from the first decided failure, two ways
  of failing asking for Keep; SameFailure by the replay's order and a
  mismatch's fields; each shrink's artifacts apart. Left: state the
  actions leave without a step of its own (a counter Reset forgets)
  passes every check: only a fresh environment per rerun is safe. Checked against
  brute force on up to 8 letters, and with quint on letters.qnt.
  webjobs' integration done (keyed jobs, `journey.Client` Plan/Play,
  `bombadiltrace.Actions`; every mutant shrunk, `TestShrink` and
  `TestShrinkRecorded`). Left: Bombadil's `--reproduce` as the
  rerun; candidates judged in batches (each is a quint check, a second
  or two).
- **Left from E1–E5.**
  - A step's process is per actor: two recorders in one actor (a page and
    its server) must share one, or Reconstruct reports an overlap, so a
    client and its server cannot be one actor's distinct locations, and
    a crash's semantics attach to the shared name (webjobs, raftkv share
    one by necessity). Separate location from incarnation.
  - A Go API for a manifest's replay (`validate.Replay`); webjobs' tests
    shell out to the CLI.
  - CI's per-package time budgets were measured while packages share the
    job's CPUs: a package made parallel slowed its neighbours past their
    budgets five times in a day. Done: the budgets are CPU seconds, the
    test binary's and its descendants' (quint, node, its evaluator),
    counted by internal/cputime under `go test -exec`. On 4 cores binding
    took 107 s wall alone and 287-324 s in the full run, its CPU
    165-193 s; validate's CPU stayed within 1168-1366 s, its wall
    750-1000 s. Left: the lines come from this machine's cores, so CI's
    first run with them resets them from the sorted file each job prints;
    a regression that only waits (a longer sleep, a real-time poll) costs
    no CPU and is left to the job's timeout.
  - webjobs' -short takes ~78 s (seven overlay binaries and the client
    runs), past S10's 60 s.

### S13: a real deployment's nemesis

Planned by a design review (qnemesis in the core, raftkv over real TCP),
built as two parts in parallel and a harness, each reviewed: DESIGN §52.
The harness's review found the toolkit's own protection (declaring a
killed process's tail lost) leaving node bugs without a verdict; raftkv,
which records before its effects, does without it.

### S12: adoption

Planned by a design review (what of database/sql and net/http can run
one task at a time), built as two adapters in parallel and an example,
each reviewed: DESIGN §51. payd found the adapters' last bug, a query
unwound by a crash deadlocking its own deferred Rollback.

### S11: partial logs

Planned by a design review (a lost head is §49's search from another
first frontier), built in two stages, each reviewed: DESIGN §50. The
first review's six bugs, three of them wrong verdicts, are listed there.

### The T track as it closed

ROADMAP's T section when its last item (T8) was done, moved here whole;
what it left is in ROADMAP's "Left from T".

#### Seventh review: whole product (T)

From four reviews after Z (HISTORY, "Seventh review"). In order, bugs
first; each item reviewed against its bullet before the next. Sizes: S
under a week, M two to four, L more.

- **T1. Silent wrong answers (S each).**
  - Real time from what the recorder knows: a completion takes its
    invocation record's time (`"invoked"` becomes an override), steps of
    one recorder are ordered exactly by seq (a completion after another's
    invocation record), no slack between a simulation's steps, and a
    linearized actor no step of which has an invocation time is warned
    of. quorumkv stops passing `"invoked"` from the process's clock.
  - A linearization cut by its budget is no verdict for the run's
    invariants and eventual vals too, not only its conformance.
  - Eventual vals judged in no run say so: an issue on each unsettled
    actor of a binding with `eventually:`, and a claim that would hold
    for nothing (Conforms, None(Violates(eventually ...))) fails when no
    run settled.
  - ResolvePending places a call's effect before the first settle after
    its invocation (the system was quiet there).
  - Faults after Settle returns: they stay off (a probe or an audit after
    it runs on the settled system), said in Settle's doc and the faults
    skill; `Sim.ResumeFaults` starts a faulty phase again.
  - A settle settles a trace only if no other recorder recorded a step
    later in time.
  - The complete search's beam is the budget's, and a cut names its real
    limit.
  - qhistory: a transaction's reads of its own appends in their order;
    garbage in any read, and no "G1b of transaction 0".
  - Among orders that conform, one whose invariants hold is preferred (a
    mutant must not look caught by the order picked).
  - The warning for a step invoked after it completed names the fix
    (Invoke and Done, no "invoked"; Sim.Now). A virtual `qenv.Now` on a
    simulation's creating goroutine was tried and reverted: that
    goroutine's Sleep and Since stay real, and a deadline loop there hung.
- **T2. The cost of a verdict.** a and b done: verdicts-only checks for
  the searches, an exploration's searched side by side, candidates
  memoized by prefix (quorumkv's Repick 297 s to 138 s); left of b, one
  memoizing checker shared with qhegel and across an exploration's runs.
  - **a (S).** A conformance-only search mode in validate (no ITF, the
    TypeScript batch, no detail phase), the per-run searches of an
    exploration in parallel, the complete search given what is left of
    the budget.
  - **b (S–M).** Candidates memoized in a prefix trie (an extension of a
    rejected prefix decided without quint), the complete search starting
    from the guided one's accepted prefixes, one memoizing checker for
    qtest and qhegel.
  - **c (M). Done** (merging prefixes of one state, from the ITF of the
    candidates quint checks whole; five commuting deposits' proof in 146
    orders, not 456). Left: extending a prefix from its state as a
    literal (each candidate replays from the start), states for the
    prefixes the memo accepts (kept apart now), merging in the
    invariants' second search (its checker writes no ITF), ITF only for
    the complete search's batches, and a chunk's stale ITF removed before
    each call. Reviewed: the merge's state is more than the model's when
    the binding infers restarts (the last process), and candidates are
    planned within their history (PlanWithin).
  - **d (L). Done** (DESIGN §45): `internal/qeval` over quint's IR of the
    binding's evaluation module, judging Linearize's candidates, quint
    confirming each search's conclusion; checked against quint on every
    model of the repository and a table of builtins at their edges on
    both backends; reviewed twice (the second found three silent wrong
    verdicts, now fixed: a search that finds no order is quint's alone).
    quorumkv's faithful exploration 20 s to 13 s. A failing search is
    confirmed from its proof in one quint batch (`qtrace.LinearizeProof`):
    the contended mutants 214 s to 62 s. An order (or a resolution) the
    evaluator finds is confirmed by the run's own check
    (`SearchOptions.ConfirmByCheck`, `Search.Confirm`), and
    ResolvePending's placements are judged by the evaluator: the
    contended mutants to 43 s, the faithful exploration to 4 s, wallet's
    pending transfers 44 s to 12 s.
    Also: LinearizeByKey's partitions side by side, and a history real
    time orders alone not searched (`qtrace.ForcedOrder`). Left: a
    claim is judged in its history's instance (identifiers interned in the
    history's order) and the run's check in the order's: the same verdict
    only for a model symmetric in interned identifiers, which a check of
    the claim in the check's instance would not need.
    `GOTOGETHER_EVAL=shadow` judges with both, a search's batches and
    each actor of every run checked, and fails on a disagreement,
    nightly on every example and the core's search tests
    (`evaluator-shadow`). Not done, by measurement: extending each
    candidate from its prefix's held state rather than replaying it from
    init (the contended mutants spend 2.3 s of 32 s in Go, the rest in
    quint processes); a Hegel property drawing states against quint
    (quint cannot start from a drawn state; the shadow runs and
    TestAgreesWithQuint's traces cover the reachable ones).
- **T3. The searches in the right layer (M). Done:** validate's
  `Checker.Search` and `Search.Mark`, the CLI's `-linearize`,
  `-linearize-key`, `-resolve-pending`; `quint.assumed` in step logs,
  `InvariantResult.Eventually`, `Run.Recorded`, the chooser protocols on
  Chooser's doc. Kept: `qtrace.Linearize` public (validate calls it), the
  three Linearize options (each reads as what it does; the refusal names
  the conflict with ResolvePending). Left: one goroutine lookup per step.
  As planned: `validate.Checker.Search`
  (Linearize and ResolvePending as generators over one candidate
  engine), called by qtest and by the CLI (`validate -linearize[=key]`);
  `qtrace.Linearize` unexported; one re-sequencing helper; `Run.Recorded`
  beside the judged `Run.Steps`; `Ambiguous` written to step logs;
  `InvariantResult.Eventually`; one `Linearize` option (by key, budget)
  and `ResolvePending(budget)`; the scheduler's task and nemesis chooser
  protocols exported or documented on Chooser; one goroutine lookup per
  step.
- **T4. A newcomer's path to Z7 and Z8 (S each). Done**, but: temporary
  directories (a failing exploration's are kept on purpose, for its
  reproduce line; steered runs' choices pile up under the temp dir).
  As planned: Start-table rows for
  linearizability, liveness and convergence; verdicts.md on Pending,
  Linearize and "no verdict (linearize budget)"; quorumkv's README with
  the code a user copies, PCT's arguments and PCT against Steer; scaffold
  writes a Settle when the binding has `eventually:`; a Mismatch of a
  client history in recorded order suggests Linearize; a run's
  scheduling steps reported (PCT's Steps); claim wording ("some run
  showing a conforming run that violates"); pending and settle counts in
  summaries; `GOTOGETHER_KEEP` keeping step logs; a failure's step log
  as recorded, its judged order beside it; temporary directories
  removed; quint's reserved names in the model skill; the doc drift the
  review listed (explorations linearize, refusal texts, DESIGN's X4).
- **T5. The lessons as structure (S–M). Done**, reviewed: the toolkit's
  own mutation testing (`ci/mutate-core.sh`, `GOTOGETHER_MUTANT`; the
  nightly holds qhistory and qsweep at 0 survivors, internal/depgraph at
  1 equivalent); brute-force comparisons for Linearize (registers,
  invocation records, domain clocks), ResolvePending's placements,
  Exhaust's schedules, the isolation classifier and Cover, each ablated,
  and Linearize against Porcupine in internal/hegelprops; one isolation
  classifier for qhistory and `GOTOGETHER_CHECK` (G-nonadjacent at SI,
  linear per commit); package-level state stating its scope; the
  simulation lint over the core; claim power (with the rate's lower
  bound) and the examples' seeds raised to it; `GOTOGETHER_ALL_FAULTS`;
  a CI time budget; the rule in AGENTS. What it found: Linearize's cut
  search read as a proof, ResolvePending's settle bound and slack,
  qweave dropping an argument, qmut recursing into qenv. Left:
  - a nightly that runs every example under `GOTOGETHER_ALL_FAULTS`
    (Exhausts bounded first), each changed verdict a fix or a reasoned
    Without. Its first run (the examples' short suites) found a healer
    keeping runs unsettled (fixed), hello's client giving up within a
    partition (its model has none: a Without to give), and Exhausts out
    of MaxRuns in hello and lease;
  - qenv in the mutation nightly with `GOTOGETHER_CHECK=1` and its bar
    (a first run without it: 288 of 895 survive, in sim.go 50,
    internal/profile 44, store.go 34, native.go 31, keyed.go 19,
    queue.go 14, the rest under 15 a file), and a bar for qtrace's
    guided-search survivors (56 of 167 survive now, held there as a
    ratchet);
  - the brute forces as Hegel properties, their sizes widened nightly;
- **T6. The exploration engine (M), before tags. Done** (DESIGN §44):
  sources (seeds, corpus, a file, steered, schedules) feeding one engine
  for Explore and Exhaust, whose nominal run now has the determinism
  check; a run's origin in one field; what only a System method sets an
  internal spec; fault names known per run; reviewed (HISTORY, "T6
  review"). Left: finish split into its stages as functions of the
  engine; a corpus for Exhaust (schedules are their own regression);
  `Script.Misnamed` against the run's names, not the process's (a
  nemesis only another test registered passes it); a woven kind's
  options still the process's (static at init); under gosim, kinds a
  run declared but never chose are unknown to it.
- **T7. Capabilities (after T2d).** Planned by a review (HISTORY, "T7
  plan"), in this order:
  - **a (M). Pending calls inside the linearization. Done** (Jepsen's
    :info; DESIGN §46; wallet's TestPendingTransfersLinearized). Left: a partition with
    pending calls and no steps places nothing, a pending effect is never
    placed after a restart the binding infers (shared with ResolvePending),
    and pending calls' arguments are not interned with the history's):
    Linearize with ResolvePending, a pending call an optional node of the
    search, bounded below by its invocation and above by the first step
    after a settle that followed it; dropped is "no effect". Checked
    against brute force (every subset of pending calls, every order);
    confirmed by the same proof. And `qtrace.HistorySVG`, a failing
    history drawn (lanes, bars, the failing step). Example: wallet's
    concurrent pending transfers (NoDedupe's retried timeout).
  - **b (S–M). Upgrade runs. Done** (DESIGN §47): `Process.RestartAs`,
    `Upgrade`, `Version`, `Sim.Upgrades` (an `upgrade` nemesis family,
    Scripts and Exhaust unchanged); a mixed-version encoding caught by
    Exhaust within one fault, nothing without the versions. The example
    moves to T8 (raftkv's nodes upgrade naturally): quorumkv's
    NewTiebreak needs its clients versioned with its replicas, which are
    no processes.
  - **c (S). Polish**: Steer cutting where a feature appeared (done: the
    journal's restart in 43 steered runs of 180, 7 before; features as
    counts measured no gain there, not done); qlint flagging math/rand and
    multi-case select (done); qhistory's strict serializability
    (done: `StrictSerializable`, real-time edges from `Txn.Invoked` and
    `Completed`, checked against brute force).
  - **d (M). Bounded response. Done** (DESIGN §48): a binding's `within:`
    (after a val, another within a bound), not judged for a run cut
    short; `Checker.Key` for verdict caches. The example moves to T8
    (raftkv's election within a bound of a heal): lease's model has no
    takeover to bound.
  - **e (L). Hidden actions. Done** (DESIGN §49): a binding's `hidden:`
    (nullary actions no step records), the states they reach between
    steps (at most `hiddenDepth`) searched by the evaluator, invariants
    kept on one path, checked against brute force; reviewed (the
    initial state's violations, joint invariants, shadow mode, lint).
    Open, offline first: partial logs (a recorder's steps lost at its
    start or end), online validation (`validate -follow`, judging a
    growing log), and confirming a hidden verdict with quint (a proof of
    "no path", as Linearize's). The example moves to T8 (raftkv's
    messages unrecorded): quorumkv's model is a register, with no handoff to hide.
  - Deferred: map-range lint (qanalysis, typed), qhistory rw-registers,
    sleep-set DPOR in Exhaust (until raftkv measures Exhaust), Hegel
    parallel machines (Explore + Choices + Linearize cover it), a Script
    against a real deployment and an Antithesis sink (modules of their
    own, when a user asks).
- **T9. An example: wallet (M, the T5 review's). Done** (examples/wallet):
  transfers on a `qenv.Store` with request-id dedupe, an outbox to a
  ledger, retrying clients over a `qenv.Service`, a credit line two
  accounts share; Exhaust (NoDedupe within one fault), isolation levels
  (ReadThenWrite at read committed, WriteSkew at SI, qhistory naming it
  G2 from the store's history), NoOutbox after a settle, ReplicaRead
  linearized per account, ResolvePending for crashed clients,
  `GOTOGETHER_ALL_FAULTS`. Its first faithful version applied a
  duplicated request twice at read committed (the request's record read
  unlocked), now the UnlockedRecord mutant. Reviewed (HISTORY, "T9
  review"): invalid requests are refused in the model and the bank
  (NoValidation), posts carry their moves, commits of unknown outcome are
  on (and Unknown in the history), NoOutbox is found by Exhaust. Left:
  WallClockDedupe (a dedupe window on the process's clock, broken only
  by clock faults); the optional real database. What it taught the core:
  - LinearizeByKey cost three times Linearize on its small per-account
    history (28 s against 9 s for five runs): its partitions were searched
    one after another, each paying a second quint call for the
    invariants, and the probe makes one partition per account, each a
    quint call for one step. Now partitions are searched side by side (28
    to 25 s: an exploration's runs already kept the CPUs busy) and a
    partition real time orders alone is not searched (`qtrace.ForcedOrder`:
    to 15 s). Next: check invariants in the first search, batch the
    partitions' candidates into one quint process.
  - PCT at depth 2 found the wide write-skew window in 1 run of 40,
    uniform seeds in 39; but its `steps` (400) was mis-sized for a run of
    about 60 choices, so the change point mostly fell past the end (sized
    to the run: 16 to 18 of 40). Default PCT's steps to the nominal run's
    choice count, which an exploration knows from its determinism run, and
    say in the skill to size it.
  - `GOTOGETHER_ALL_FAULTS` turns on every nemesis family but not a
    store's commit faults (`Store.CommitFaults`, opt-in per store): "every
    fault family" leaves out the one a dedupe-by-id service exists for.
- **T8. An example: raftkv (M, after T2d). Done** (examples/raftkv):
  etcd's raft library, unchanged, as a three-node store on qenv, its
  election entropy drawn from the run; two models (the clients' history
  linearized whole, 200 operations within a budget of 50000; leadership:
  one leader a term, a leader within 300 ms of a heal); pending calls
  placed; a lease read made stale by drift only after a leadership
  transfer (`Exhaust` over the drifts); a vote not persisted; an upgrade
  writing its format too soon, caught by a paused rollout. It found two
  faults of the toolkit's (a drifting wait rounded to none; a cut search
  searched again with quint). Left: hidden messages (`hidden:` with
  raft's campaigns unrecorded), and Linearize's cost on whole histories
  of hundreds of operations (each candidate replayed from init, about 20
  s a history: extending a prefix's held state, T2d's next step).


## grist's build notes (docs/grist.md's §0 to §0.16)

What each slice of grist, and each review of it, changed in its design
note, in order: docs/grist.md's §0 to §0.16 until H6 moved them here
(the note keeps the design; these are how it got there). A bare §
number below is the design note's (docs/grist.md), a §0.x one of these
notes.

### grist §0: What the review changed, and where the code disagreed

Adopted: tenants as actors with one fixed process and a domain clock
(docs/grist.md §3.1); reads pinned to one position (docs/grist.md §2.3); processors reading as of
their trigger (docs/grist.md §2.6); checkpoints in a table (docs/grist.md §2.6, docs/grist.md §10.2); verdicts as
Rejects and Mismatch where the model's precondition or expectation is
what fails (docs/grist.md §3.2); qsql's real prerequisite as its own item (docs/grist.md §8.2); the
SQL tier's isolation, retries and timeouts (docs/grist.md §2.10); idempotency in full
(docs/grist.md §2.4); the binding's typed values and recorded read set (docs/grist.md §3.2);
client-side steps (docs/grist.md §3.1); three slices instead of one spike (docs/grist.md §8).

Where the code said something other than the review, or than the first
version of this note:

- **The ordering rule is DESIGN docs/grist.md §3 and docs/grist.md §4, not docs/grist.md §6.** `qtrace.build`
  orders by a domain clock only within one (actor, process), equal
  values concurrent, every step of a value before every step of a
  larger one; hence the fixed process (docs/grist.md §3.1). Thread (program order)
  edges apply too, so the encoding must agree with each task's order:
  the review's 2·P+1 holds for queries and rejections, but a
  processor's checkpoint-only step at 2·T+1 (T its trigger) would make
  a cycle (emit at 7 for trigger 5, then checkpoint trigger 6: 14
  before 13 on one thread). docs/grist.md §3.1 uses the head its transaction read.
- **A settle is voided by a later step on any recorder**, not only its
  own (beyond `RealTimeSlack`): probes on a separate recorder are sound
  only while they take no virtual time, so docs/grist.md §3.6 settles again after them.
- **qsql already has `ORDER BY pk [ASC|DESC]` and `LIMIT`.** It lacked
  keys of several columns (42P16, "qsql keys rows by one column": the
  first note's tables were keyed by two and three), ranges, `RETURNING`
  and `DO UPDATE`, which slice 2's first core item added (docs/grist.md §8.2); its
  isolation is store-wide.
- **`qenv.FaultPoint` has one crash option**, `crash`: the effect's two
  crash points are a `DeclareFaults` kind and `Sim.FaultOf` (docs/grist.md §3.5).
- **qobs encodes structs with `fmt.Sprint`**, not JSON, and binding's
  `jsonElem` renders objects as quoted strings and floats truncated:
  grist encodes events itself, and binding needs a typed function (docs/grist.md §3.2;
  `toQuint`, added for slice 2, docs/grist.md §8.2).
- **One System has one binding, and qtest had no actor filter**: a
  run records commit steps or client steps, not both (docs/grist.md §3.1). Slice 2's
  third core item adds the filter (a binding's `actors:`, docs/grist.md §8.2).
- **`Scan` walks every row of the store** (a Serializable commit again
  per scanned prefix), with no range: simstore's cost grows with the
  store, not the tenant (docs/grist.md §2.10).
- **`GetForUpdate` under Snapshot and Serializable aborts with
  ErrConflict** if the key changed since the snapshot: a lost race for
  the head is transient, not a failed condition (docs/grist.md §2.4).
- **qlint has no map-range rule**: gristpure needs one written (docs/grist.md §4.1).
- **`GOTOGETHER_STRICT=1` turns toolkit skips into failures**
  (`qtest.Optional` included), nothing more: stubs fail under it only if
  reported through `qtest.Optional` (docs/grist.md §3.2).

Decided differently from the review: `leakTenant` is dropped, tenant
isolation being a contract-suite test with a mutant store (docs/grist.md §3.3). (The
first version also decided that rejections are not remembered under
their idempotency key; slice 1's review showed that wrong, §0.2.)

### grist §0.1: What building slice 1 changed

Implementing slice 1 (grist/, examples/counters) showed this note wrong
or incomplete in these places; each is fixed where it stands below.

- **Claim 1 runs at ReadCommitted.** Under Snapshot and Serializable the
  loser of a race for the head gets `ErrConflict`, a transient error
  whose retry starts again at the marker and decides again on fresh
  reads: that closes most of the blind create's window (2 of 40 runs
  race). At ReadCommitted the loser waits for the head's lock and
  commits blind (25 of 40). After slice 1's review counters' shape
  showed none under Serializable (0 of 120 runs) and claim 1 is
  ReadCommitted's (30 of 40, docs/grist.md §8.1). **The window is narrowed, not
  closed** (§0.8): a decision's reads are taken before its append's
  transaction begins, so no isolation level protects an `Unconditioned`
  decision; examples/wallet-grist's `readThenWrite` commits an overdraft
  at Serializable, and counters' `blind` a second create of a name at
  four clients.
- **The read set's step fields are `readPos` and `reads`**, not
  `read.pos` and `read.queries`: a model parameter is a Quint identifier,
  and a binding gives templates only where a parameter is not the
  same-named step field. (The recorded read set was the *condition*,
  `[]` for an `Unconditioned` command; since slice 1's review it is what
  the decision read, whatever the condition, §0.2.)
- **The effect step is `<processor>Effect`** (`welcomeEffect`), not
  `<processor>.effect`: a step's action is its model action's name. Its
  arguments are `trigger`, `key` and `ok`. The fault kind keeps the dots
  (`grist.welcome.effect`).
- **A processor's steps are on one thread per poller** (`qobs.Thread`):
  an effect step and a checkpoint-only commit can both sit at 2·H+1 and
  on the same `order.p.<processor>` value, and only program order puts
  the effect first.
- **A refusal records the head and an empty batch** (`obs.head`,
  `obs.events: []`), so one `expect:` serves a command's every step.
  Every commit records its batch as `obs.events` (types), `obs.strs` and
  `obs.ints` (the events' string and integer fields in struct order):
  scalars and lists, all slice 1's binding has. Processor steps add
  `obs.checkpoint`.
- **The model keeps two ghost variables** beside the log and the
  checkpoints: `appended` (the last step's batch, what its observations
  are compared with) and `effects` (the effect steps, what at-least-once
  and `welcomedOnce` count). The model module `export`s grist.qnt's and
  the library's defs, or the binding's expects cannot name them.
- **`grist.failed` carries the processor and the trigger, not the
  error's text**: the model could not compute it. A handler's error is
  `grist.Fail(err)`, tried `EffectTries` times like an effect's.
- **The Store interface has `Name`, `Marker` and `Checkpoint`**, and
  `Query` an `After` (a poller reads after its checkpoint); `Batch`
  carries `Marker`, `Checkpoint` (a compare-and-set), `Condition`, `Time`,
  `Cause` and the commit hook. A failed checkpoint is a
  `*CheckpointMoved`; a marker that exists a `*ConditionFailed` naming it.
  The node (pollers, the effect inline in its poller) is `App.Start`, not
  a `runtime/` package. (The types lived in package grist; slice 1's
  review moved them to `grist/store`, §0.2.)
- **A command's result is a `grist.Receipt`** (`Result` is a processor
  handler's).
- **Keyed read models take one key in slice 1**, and a query one tag: the
  read set is (type, tag, value) triples. `Keys("account", "currency")`
  waits for slice 2's typed binding function.
- **A poller is one task per (processor, tenant)**, waiting on the
  store's signal (`Store.Wait`) rather than polling every 250 ms in a
  simulation, so a quiet system is quiet for `Sim.Settle`; tenants are
  the store's when the node starts.
- **gristsim restarts the node from an `OnCrash` hook**: an effect's crash
  point crashes the node from its own task, which a nemesis's
  crash-and-restart does not cover. The node serves commands and queries
  over a `qenv.Service` ("api"), so requests and answers are lost and
  late; its configuration has `Send` (which commands and queries the
  workload sends) and `Setup` (the application's doubles, attached to the
  node's contexts).

### grist §0.2: What the review of slice 1 changed

A review of slice 1 probed it (a reviewer's tests against grist and the
example) and found these; each is fixed, with a test that fails without
the fix, and stated where it stands below.

- **A key's first answer is final, a refusal's too.** A rejection wrote
  no marker, so a late or duplicated request decided again and could
  commit after its client was told 422 (4 of 40 runs at the clients with
  the API delaying and duplicating 30% of requests). A refusal now
  commits its marker alone (`Marker.Outcome`, the rejection's name),
  conditioned on its reads like a commit; a conflict commits a
  `conflict` marker, unconditioned. The marker is checked under the head
  lock (the store's), so a refused key cannot commit later; the cost is
  that every refusal takes the tenant's lock (docs/grist.md §2.4). The model's
  `freshCause(log, id)` (no event of the id) is `freshKey(answered, id)`
  over a ghost set of answered ids, which a committed refusal joins.
- **The marker's hash is the command's**: its name, version and
  canonical arguments. It was the arguments' only, so another command
  under the same id with JSON-equal arguments got the first's receipt.
- **Reads are checked against the App.** A tag key no type has (the Go
  field's name where the JSON name is the key), a keyed read model keyed
  by a field that is no tag, a misspelled type or another App's event
  matched nothing and conditioned nothing: grain's race with no
  `Unconditioned` in sight. Every read is now the decision's error when
  its type is not the App's or a tag key not the type's;
  `NewKeyedReadModel` refuses a key that is no tag of every handler's
  type (Verify); `Exists` takes an `EventName` whose method is
  unexported, so a read model or a command does not pass for one.
- **Under at least once, the committed run decides the outcome.** An ok
  send, a crash before the checkpoint, then three failed sends on the
  restarted node checkpoint `grist.failed`; the model insisted on
  `notified` (any ok effect wins) and rejected the faithful app. The
  effect's checkpoint step records `ok` (which outcome it commits), and
  grist.qnt's `atLeastOnceDone` admits notified after an ok effect step
  and `grist.failed` after `EFFECT_TRIES` failed ones, whichever the run
  committed: a library def every at-least-once application uses.
- **An unknown outcome at the end of the transient budget** returned an
  error though the command had committed. The marker is read once more
  before giving up, and a command that gives up after an append whose
  outcome is unknown returns `ErrOutcomeUnknown` (retry with the same
  id), one that never appended `ErrUnavailable`; gristsim's clients
  retry both under the same id, as for a lost answer.
- **The model checks the read set, not only the condition.** With
  `readPos` and `reads` the runtime's own report, `conditionHolds` was
  circular: a runtime that dropped a read from the read set dropped it
  from the condition too, and the model agreed. grist.qnt's `covers`
  states what a decision's rule needs (create-counter: whether a counter
  has the name), and the model requires it of the recorded read set at
  every committed step, so losing coverage is caught at every commit.
  The read set is recorded whatever the condition (an `Unconditioned`
  command records what it read), so the blind mutant is caught by the
  race, as claim 1 says, not by its missing condition. `conditionHolds`
  also requires `0 ≤ readPos ≤ head`; a conflict requires the condition
  *not* to have held.
- **The claims test the mechanism.** Claim 1 gains its contrast (the
  faithful app at claim 1's configuration: `None(Rejects("createCounter"))`,
  which fails in 13 of 20 runs with the runtime's condition taken out);
  a claim of duplicated and late requests (the API duplicating and
  delaying three in ten) catches the marker taken out; claim 2 runs one
  tenant and two letters, and drops Snapshot, whose runs are
  Serializable's byte for byte (every append writes the head).
- **The bindings compare more**: every event's cause (`obs.causes`, a
  (kind, name) pair, in `batchIs`), the effect's key (`keyStable`: the
  same key on every effect step of a trigger, no other trigger's), and
  a refusal's head and empty batch (it commits now).
- **A commit's step is written in its transaction.** `Batch.OnCommit`
  running even when the outcome is unknown is qenv's property, not
  database/sql's: a node that dies between COMMIT and the hook loses the
  step. `Batch.Step` carries the step (its fields but those the head
  gives), which a `store.StepLog` writes in the batch's transaction;
  memstore and simstore keep one, storetest checks it, and slice 2's
  sqlstore records from its rows (docs/grist.md §2.10, docs/grist.md §3.8).
- **Zero values are invalid**: the zero `Result`, an `Effect` without a
  function or of a delivery slice 1 lacks, the zero `Option`; each is
  the handler's error (`grist.failed`) or a Verify problem.
- **grist.qnt is checked in grist's own tests**: typechecked, its defs
  run on small values (`quint test`), and a small machine over them run
  (`quint run`), grist/model/model_test.go.
- **storetest checks concurrency**: tasks appending at once with
  conditions, checkpoint compare-and-sets and markers, the committed log
  replayed through `CheckCondition` (a mutant store checking conditions
  outside its lock fails it); `Wait` woken by an append (a mutant whose
  `Wait` returns at once fails it); and no slice shared with a reader or
  a writer (a fold mutating its events in place corrupts nothing).
- **API, before slice 2**: the store's contract is package `grist/store`
  (`Store`, `Batch`, `Condition`, `Marker`, `Selector`, the errors,
  `CheckCondition`, `Select`, `NewReceipt`), with later methods as
  optional interfaces (`StepLog`); grist keeps aliases of the types an
  application meets (`grist.Event`, `grist.Stored`, `grist.Position`,
  ...). The raw filter `grist.Query` is `Selector`, read with
  `Reads.Select`, beside the typed `grist.Events[E](r, t, tags...)`.
  `PosKey`, `StepFields` and `Quiet` are internal, as are gristsim's
  request and response. The tries and the batch size are options
  (`ConditionTries`, `TransientTries`, `EffectTries`, `BatchSize`) of
  one `grist.Option` type for every block (queries take `Authorize`
  later), defaults 5, 10, 3 and 100. `storetest.Checks` is a function.
  `App.Catalog()` is the registry as data, for slice 2's generator. The
  slice-2 read-set encoding is chosen: grist.qnt's `Read` is `{ typ,
  tags: Set[(str, str)] }`, which slice 1's binding builds from the
  recorded triples (`readsOf`) and slice 2's typed template function
  will record directly.

### grist §0.3: What building slice 2's SQL store changed

Slice 2's first part (grist/sqlstore, the relay, gristsim's SQL tier,
examples/counters' SQL and real tiers) decided these; each is stated
where it stands below.

- **Every tier records commits from the step log; there is no commit
  hook.** `grist.Relay` reads each tenant's committed step rows in order
  (`store.StepLog.Steps`, waking on `WaitSteps`: a refusal commits its
  marker and its step without moving the head, so `Wait` would sleep
  through it) and records them; `App.Start` runs one relay daemon per
  tenant and refuses a store without a step log. memstore and simstore
  switched too: one code path, so the simulation exercises the recording
  the real tier uses, and `Batch.OnCommit` (which only qenv could run
  even when the caller was told the outcome is unknown) is gone. A row
  holds the batch's steps (one per trigger of an `Emit` batch) with every
  field but those the head gives (`obs.head`, `order.pos`), which the
  relay adds from the row's head and each step's event count.
- **A relayed step is the same record however often it is relayed.** It
  is recorded under a recorder of its own, `grist/<store>/<tenant>`
  (`grist.RecorderID`), numbered by its place in the tenant's step log
  and timed by its commit (the row's time, taken under the tenant's
  lock since §0.4: the simulation's true time, or the wall clock's), on
  one thread (commit order). A relay that starts afresh (a
  restarted process, a second node) records the log from its first row,
  and `qtrace.Reconstruct` keeps one copy of each (an info issue, "seen
  twice"), so no relay progress has to be durable. gristsim holds one
  relay across the node's incarnations (`grist.WithRelay`, a
  `StartOption` since §0.4), which resumes
  where the crashed one stopped. `Relay.Behind` says how many rows are
  unrecorded: a real tier's harness waits for 0 before its settle.
  Recorded through `qobs.Record` instead, a second relay's copies are new
  steps (`TestRelayRecordsCommitsOnce` fails so).
- **A processor's order key separates its effect and its handling**:
  `order.p.<processor>` is 2·T on T's effect steps and 2·T+1 on its
  handling step (slice 1: T on both, the poller's thread putting the
  effect first; the handling step is now the relay's, on another thread).
- **Every gristsim run records both sides**: the clients' calls under
  `<tenant>/clients` (`gristsim.ClientsActor`; `<tenant>.clients` until
  §0.4), the store's steps under
  the tenant; a binding's `actors:` picks its side, and
  `System.WithBinding(calls, qtest.Linearize())` judges the same runs at
  the clients. `Config.Record` (`AtCommits`, `AtClients`) and the quiet
  context are gone. The relay's tasks change the schedule, so slice 1's
  measured rates moved (examples/counters' README has today's).
- **sqlstore runs one SQL on every dialect** (docs/grist.md §2.10): the head lock and
  the new head and step number in the first statement, then the marker,
  checkpoint and condition under it; markers keep their receipt as its
  last position and event count; an event's tags are a JSON column
  beside its body (both TEXT, which SQLite does not coerce). `Dialect`
  (`Postgres`, `SQLite`, `QSQL`) says only the default isolation
  (PostgreSQL's and qsql's read committed, stated; SQLite's default,
  serializable with one writer), PostgreSQL's per-transaction
  `set_config` of `lock_timeout` and
  `idle_in_transaction_session_timeout` (one statement, local to the
  transaction, so no connection hook), and SQLite's mode check. grist
  imports no driver; its tests import modernc.org/sqlite and pgx.
- **sqlstore retries nothing itself.** A transient error (40001 from the
  head lock under snapshot or serializable, 40P01, 55P03, SQLITE_BUSY
  after `busy_timeout`, a lost connection, a commit of unknown outcome)
  goes back to grist, whose retry starts at the key's marker and decides
  again, as with simstore: an internal retry of the same batch would keep
  a stale decision (harmless under its condition, but the claims'
  mechanism would differ between tiers). What no retry fixes is new in
  the contract: `store.Permanent` (`*store.PermanentError`), which
  `store.Transient` excludes, so a missing table is an error at once, not
  ten tries and "outcome unknown"; a processor pauses before reading
  again.
- **Waiting**: `Wait` and `WaitSteps` wake on every COMMIT this Store
  tried (an unknown outcome may have committed; a waiter that slept
  through it would sleep through its step), re-read if a COMMIT happened
  while they read, and poll every 250 ms outside a simulation for other
  processes' appends; inside one they poll never, so a settled run is
  quiet (§0.4: on qsql they wait on the database's own signal, so a
  second Store's commits wake them too, and return once satisfied).
- **The contract on every tier**: storetest passes over sqlstore on qsql
  at Serializable, Snapshot and ReadCommitted with the Store's history
  checker on, on SQLite (a temp file, goroutines appending at once), and
  on PostgreSQL 16 (a scratch server on /dev/shm, `GRIST_POSTGRES`, at
  read committed, repeatable read and serializable; in CI since §0.4, a
  service container of the grist job), with `TestPostgresLockTimeout` (a stuck holder of the head
  lock makes an append 55P03 after `lock_timeout`). The head lock taken
  after the condition instead fails `ConcurrentAgainstBruteForce` at read
  committed on qsql and on PostgreSQL (SQLite's single writer passes:
  its transactions serialize whatever the statements' order). The suite
  now requires a step log and checks `WaitSteps` against a mutant whose
  wait is on the head.
- **The SQL tier commits less per run**: every statement is a scheduling
  point, so the nemesis (per scheduling step) crashes the node about
  three times as often; examples/counters' SQL claims raise the commit
  fault's rate to reach it in few seeds.

### grist §0.4: What the review of slice 2's first part changed

A review of the relay, grist/sqlstore and examples/counters' SQL and
real tiers found these; each is fixed with a test that fails without the
fix (ablated before committing), and stated where it stands below.

- **A node handles tenants created after it started.** The relay and the
  pollers covered the tenants listed at `App.Start`: a later tenant's
  commits were never recorded (`Relay.Behind` stayed at their count) and
  its triggers never handled. The node now runs a watcher task that
  waits on the store's new optional interface `store.TenantFeed`
  (`WaitTenants(ctx, n)`: until the store has more than n tenants) and
  starts the relay and pollers of each new tenant; every tier implements
  it, `App.Start` requires it, and storetest checks it (`TenantFeed`,
  against a mutant whose wait returns at once). A wait, not a poll: a
  timer would keep a settled simulation busy.
- **Two stores over one database wake each other.** A simstore or a
  sqlstore on qsql woke its waiters only at its own appends and, in a
  simulation, polled never: a second Store's commits were slept through
  and, with a relay waiting on the first, their steps silently lost.
  The core's `qenv.Store` now notifies a signal at every commit that
  writes (`Store.Changed`, with `Store.Version` to tell a commit that
  fell between a read and its wait), `qsql.StoreOf` names the Store a
  qsql database runs on, and both tiers wait on it; on SQLite and
  PostgreSQL another Store's commits are found by `Options.Poll`.
  `storetest.TwoStores(ctx, a, b)` checks a pair (on simstore, qsql and
  SQLite), failing for a Store's own signal. Refusing a second Store
  was the alternative; two nodes over one database are the real tier's
  normal shape, so they are made to work.
- **A step row has a version, `"v": 1`.** A row of another version (a
  newer grist's, or one no grist wrote) is a `rowError`: its tenant's
  relay stops, and `Relay.Err()` names the row and the fix (upgrade the
  node) instead of retrying forever; transient read errors are retried
  with backoff and reported by `Err` until a read succeeds (`gristsim.Run`
  returns it, examples/counters' harness fails on it).
- **A relayed step's time is its commit's.** The row was encoded with
  the decision's time, before the append, so a command that decided first
  and committed second carried the earlier time at the later seq. A row
  is now timed by the store under the tenant's lock (`store.StepRow.Time`,
  `store.StepTime`: the simulation's true time, else the wall clock's;
  sqlstore's `grist_steps.at_ns`), so seq order is time order; storetest's
  `StepLog` checks it.
- **An error before COMMIT says nothing committed.** Every transient
  error set "may have committed", so a lost race for the head lock
  (40001, 55P03 on its UPDATE) ran out as `ErrOutcomeUnknown`. A tier
  now marks an error before its COMMIT `store.NotCommitted`
  (`*NotCommittedError`; sqlstore every statement's before COMMIT,
  simstore a lost lock and a refused validation), `store.MayHaveCommitted`
  tells the two, and `Execute` answers `ErrUnavailable` when every
  transient error came before a commit. A context cancelled while an
  append committed is `ErrOutcomeUnknown` (wrapping `context.Canceled`),
  not a bare cancel that reads as "nothing happened".
- **Text no store holds alike is grist's error.** PostgreSQL refuses a
  NUL byte and invalid UTF-8 (22021) where SQLite and memstore take them,
  and JSON rewrites invalid UTF-8: tenants, ids, emitted events' tags,
  read tags and block names are checked in grist, on every tier, with
  the fix (encode binary data). A tenant name holds no slash
  (`grist.CheckTenant`), so a run's client-side actor is
  `<tenant>/clients` (was `<tenant>.clients`, which a tenant could be
  named); the bindings say `actors: ['!*/clients']` and `['*/clients']`.
  `RecorderID` path-escapes the store's and the tenant's names.
- **sqlstore's waits return once satisfied** (a commit during a read
  that already found what it waited for once made it read again), and
  `Open` validates its options: a negative duration, a name with NUL, a
  stated isolation level on SQLite (whose driver takes any and runs its
  one), and one transaction at the level, so pgx's "unsupported
  isolation" (no SQLSTATE: transient) is `Open`'s error with the fix.
- **SQLite's DSN drops `_txlock=immediate`**: its stated reason (a
  deferred transaction's upgrade from read to write fails with
  SQLITE_BUSY) does not apply, since an append's first statement is the
  head lock's write; deferred transactions take the write lock at it.
- **A relay with no recorder records as `qobs.Record` does**: dropped,
  or in a strict run an escape that fails it (it advanced in silence).
- **The relay's API**: `App.Start(ctx, s, grist.WithRelay(r))` (a
  `StartOption`; was a context value, and a relay of another store was
  ignored: now Start's error); `Relay.Flush` may run beside a node of the
  same Relay (a per-tenant lock held only between a row's progress and
  its record, where no seam is reached, so a crashed node's frozen task
  never holds it; each row recorded once, in order); `Relay.Behind` is
  the store's count of rows less the relay's progress
  (`store.StepLog.StepCount`), not a scan of the rows.
- **storetest draws selectors of two tags** (one or two types) in
  `ReadsAgainstBruteForce` and `ConditionsAgainstBruteForce`, on every
  tier, PostgreSQL included; a store honouring a selector's first tag
  alone fails both.
- **TestRealSQLite proves what it waits for.** The clients record their
  calls (`gristsim.Do`: `<tenant>/clients`), judged by
  `counters.calls.binding.yaml` too; every answer a client got is checked
  against the store's side (a lost refusal step passes both bindings:
  it commits no event); the first node is killed mid-phase (its sink dies
  at once, its relay behind, the clients still committing), the second
  phase creates new counters, and a mailer taking 20 ms makes a settle
  recorded without waiting (`quiet`) fail the eventual val. Each of the
  two ablations (a refusal's step dropped, the wait removed) fails it.
- **Restarts still re-record from row 1: deferred.** A relay that starts
  afresh records the tenant's log from its first row (Reconstruct drops
  the copies), which costs a restart O(log) and rewrites, in a new file,
  history an older file holds. The design, not built: a durable cursor
  per (tenant, sink) in the store (`grist_relay(tenant, sink, seq)`,
  written after the sink wrote the rows: at least once, a crash between
  the two re-recording one batch), and a relay declared to start after
  row N that records a lost-prefix record first, so a file whose steps
  begin at seq N+1 is judged as a suffix rather than reported as a gap
  (a qtrace change: an instance's state at the prefix is unknown, so the
  binding needs a way to start the model there, or the prefix's file is
  validated with it). Until then, keep a node's step files together and
  validate them as one run.
- **Cost**: examples/counters' SQL-tier claims that repeat a mechanism
  another per-push test covers run in full mode only
  (`TestSQLDuplicateRequests`, `TestSQLFaithfulAtClaim1`, `blind`'s and
  `dispatchDirect`'s SQL-tier halves, `TestSQLTier`'s Serializable half,
  `TestFaithfulAtClients`); its -short CPU went from 303 to 206 s here.
  The grist job runs PostgreSQL 16 as a service container, so the
  PostgreSQL contract runs in CI.

### grist §0.5: What building slice 2's API changed

Slice 2's second part (the HTTP API, `grist/client`, gristsim's HTTP
tier, `grist.Main`, examples/counters over HTTP) decided these; each is
stated where it stands below.

- **The adapter is package grist's, not `grist/api`.** `grist.Main`
  serves it, and the adapter reads what a block declares (its policy,
  `Internal`), which a separate package could only read through exported
  hooks: `grist.NewHandler(app, store, opts...)` is a plain
  `http.Handler` (net/http only), served by `qhttp.NewServer` in a
  simulation and by `http.Server` on the real tier. grist imports
  `grist/sqlstore` for Main (no driver: the binary imports one).
- **Routes and codes** (docs/grist.md §5): `POST /t/{tenant}/commands/{name}`, the
  `Idempotency-Key` header the command id (required: 400 without it);
  `GET /t/{tenant}/queries/{name}`, the arguments as URL parameters by
  JSON name (a string as it is, anything else as JSON: `queryArgs`, and
  `client.Params` its inverse). A command's receipt is **200** on its
  first answer and on every retry: the key's answer is the same
  response, which a 201 for the first and a 200 for a replay would not
  be (grist cannot tell them apart: a retry after a lost answer finds
  the marker). A rejection is **422** with its name; a reused key is
  **422** `idempotency-key-reused` too (the IETF Idempotency-Key draft's
  code for a key sent with another payload; the key's own answer
  stands; §0.7: the hash is of the arguments' canonical value); a
  conflict after the tries **409**; `ErrOutcomeUnknown` and
  `ErrUnavailable` **503** with `Retry-After: 1` and `{"retry":
  "outcome-unknown" | "unavailable"}`, to be sent again under the same
  key; arguments that do not decode (a misspelled field included: the
  body is decoded strictly; §0.7: a required field missing, invalid
  UTF-8, a NUL byte, an integer past int64 too) 400; a body past
  `MaxBody` 413 (§0.7); an unknown block or tenant 404 (an Internal
  block too, §0.7); the decision's own error 500.
- **Deny by default, at the adapter** (docs/grist.md §4.3): one `grist.Option`,
  `Authorize(Policy)` (a `func(caller, tenant string) bool`; policies
  `Authenticated`, `Callers(...)`) or `Internal()`, on commands and
  queries; both on one block is a Verify problem. `NewHandler` refuses
  an App with a block that has neither, naming each (the adapter's
  boot): `App.Verify` does not, since gristsim's in-process clients and
  tests call blocks directly. A refused request is 403 (`ErrDenied`).
  The caller is the request context's (`WithCaller`), set by the
  application's authentication (`Middleware`) or, behind an
  authenticating proxy and in tests, `CallerFromHeader` (§0.7: only for
  a request the middleware left without a caller).
- **A denial records nothing at the store.** The note had it a refused
  step (`outcome: denied`); but nothing ran, nothing was read and
  nothing committed, and the server records only what its blocks do
  (commits from the step log, refusals at their commit, queries and
  reused keys on return): the handler records nothing of its own. The
  client records its 403 as its call's outcome, `denied` (§0.7: a
  client-side model's outcome; a refusal that depends on the state, a
  counter's creator, is the decision's, `Reads.Caller`).
- **The client records its side**: `grist/client`'s `Client.Do` records
  each call as gristsim's clients do (`<tenant>/clients`, invoked before
  the first request, done with the answer, a lost answer or a 503 sent
  again under the same key after `RetryDelay` or the `Retry-After`; at
  most `MaxAttempts`, then pending), one recording with gristsim's
  (`grist/internal/calls`). A request that got no answer is
  `ErrOutcomeUnknown` for a command (it may have committed),
  `ErrUnavailable` for a query (§0.7: retries back off, jittered,
  within a retry budget, and a 5xx that is not grist's is an unknown
  outcome too).
- **gristsim's HTTP tier** (`Config.HTTP`): the node serves
  `NewHandler` on a `qhttp.Server` named `api` from its start function
  (every incarnation listens again), the clients are `client.Client`s
  over qhttp's Transport (2 s of virtual time per request), so the
  network's faults are qhttp's under the same kinds as the Service
  tier's (`rpc.api.request`, `rpc.api.delay`, `rpc.api.response`, the
  node's crashes). examples/counters' `TestHTTPTier` is claim 2 there, judged
  at the clients per push (a retry under a new key is caught there alone:
  9 of 20 runs, 12 of 20 with answers lost at 0.15 since §0.7; the store admits each key as a new command) and at the
  store in full mode.
- **The real tier is HTTP**: examples/counters' `TestRealHTTP` (was
  `TestRealSQLite`, whose clients called `App.Execute` in process) serves
  `NewHandler` on a real `http.Server` (a loopback port) over a SQLite
  file, four clients calling it with `grist/client`; when the first node
  is killed the second takes the server's traffic at once, and both
  bindings judge the file.
- **`grist.Main(app, opts...)`**: `catalog` (the registry as JSON, each
  type a JSON Schema derived by reflection: `catalog.Schema` (grist/internal/catalog), the
  catalog's `MarshalJSON`), `verify`, and `serve -db sqlite:PATH |
  postgres:DSN -addr -tenant -steps -caller-header -poll`: the API and the
  node on sqlstore, every step to a `PerStep` file (a new one each
  start), the application's dependencies attached by `Dependencies`;
  SIGINT or SIGTERM shut it down, the relay flushed into the file first
  (`Relay.Flush`: a row the relay had not reached would otherwise be
  only the next node's, in its own file). `mainArgs` is the testable
  form. examples/counters' `cmd/counters` is one, and `SERVE=1
  ./run-demo.sh` serves it, drives it with curl and validates its file.
- **Ids starting with `grist:` are grist's** (an at-most-once outcome's
  marker, docs/grist.md §2.6): a command under one is refused (400 at the API).

### grist §0.6: What building `AtMostOnce` changed

- **The semantics** (§0.7: an error's outcome is `grist.unknown` unless
  f says `NotApplied`): the checkpoint commits first, alone (its step
  records `ok: false`: no outcome is known when it commits); then f runs
  **once** (`EffectTries` does not apply: a try that failed may have
  taken effect, and trying again would be at least once); its effect
  step is recorded; then the outcome event, `OnSuccess`'s after an ok
  effect or `grist.failed` after a failed one, commits **in a commit of
  its own**, under a marker keyed by the effect key (`grist:<key>`, so a
  retry of an outcome whose answer was lost appends it once; ids
  starting with `grist:` are refused to commands). A crash between the
  checkpoint and f loses the effect, one after f its outcome: no restart
  runs either again. A checkpoint commit whose answer was lost counts as
  another poller's (its effect is not run): at most once, and before
  leases (docs/grist.md §3.4) the safe reading.
- **Its steps**: the handling step `<processor>` (trigger, `ok: false`,
  the checkpoint), the effect step `<processor>Effect`, and the outcome
  step **`<processor>Outcome`** (trigger, key, `ok`; the events), relayed
  from its row. `App` registration claims `<processor>Outcome` as a
  Quint name too. Order: `order.p.<processor>` is 2·T for an
  at-most-once checkpoint and 2·T+1 for its effect step (at least once
  keeps 2·T for effects and 2·T+1 for its checkpoint); the outcome step
  has `order.pos` only (2·P, after the effect's 2·H+1 since it appends).
- **The crash points are the same kind** (`grist.<processor>.effect`,
  `crashBefore` and `crashAfter`, just before and just after f, whatever
  the delivery): at least once both precede the checkpoint (crashAfter
  duplicates), at most once both follow it (crashBefore loses the
  effect, crashAfter its outcome). One kind, one option list.
- **grist.qnt** gains `triesOf`, `holds`, `atMostOnceFirst` (the
  checkpoint before any effect step of its trigger, `ok` false),
  `atMostOnceEffect` (one effect step, after the checkpoint),
  `atMostOnceDone` (the outcome agrees with the one effect step and comes
  once: the success's events, which must name the trigger, and the
  trigger's `grist.failed` not in the log yet) and `atMostOnceOutcome`;
  checked in grist/model's tests on small values (§0.7 adds `unknown`,
  `holdsUnknown` and `atMostOnceOutcomesLogged`).
- **examples/counters' at-most-once processor is `payout`**, on a new
  command `retire-counter` (`counter-retired{counter, total}`, refused
  for an unknown counter): it pays the counter's total out through a
  simulated `bank` (`qenv.Service`), a duplicate payment being worse
  than a lost one, `OnSuccess(paid-out{counter, retired})`. Its claims
  (`TestPayoutAtMostOnce`): a scripted `crashBefore` loses the payment
  and the model admits it (`counters.retiring.binding.yaml`:
  `Conforms`, `All(Settles)`, the eventual val `everyRetirementHandled`
  needing only the checkpoint); a test-only binding
  (`counters.exactly.binding.yaml`, the eventual val
  `everyRetirementPaidOnce`) rejects those runs, `All(Violates(...))`;
  `crashAfter` and no faults conform under it (no payment twice, none
  lost). With grist's `AtMostOnce` run as at least once, every run
  rejects `payoutEffect`. Runs that retire are judged by their own
  binding; the other claims send no `retire-counter` and keep their
  bindings (a binding's eventual vals each cost a replay).
- **`checkpointFirst`** (the note's mutant): welcome declared
  `AtMostOnce` while the model says at least once:
  `Some(Rejects("welcome"))`, 10 of 10 runs (its checkpoint step before
  any effect step, which `atLeastOnceDone` refuses). The model gained
  `welcomeOutcome`, never enabled (an at-least-once outcome commits with
  its checkpoint), so the mutant's outcome steps are a model's action.
  The note's second claim (with a crash between, `Violates` of
  `everyCounterWelcomedOrFailed` in a model variant whose welcome is at
  most once) is `payout`'s: a real at-most-once processor, admitted by
  its model and rejected by a test-only exactly-once val. The mutant
  runs in full mode (its claim repeats `TestPayoutAtMostOnce`'s and
  grist's mechanism; building its binary is most of its cost).

### grist §0.7: What the review of slice 2's second part changed

A review of the HTTP API, grist/client, gristsim's HTTP tier,
`grist.Main` and `AtMostOnce` found these; each is fixed with a test
that fails without the fix (ablated before committing), and stated where
it stands below (§0.5 and §0.6 point here where it replaces them).

- **The application's authentication wins over `CallerFromHeader`.** The
  header's caller replaced the context's, so a request a middleware
  authenticated as a viewer that named itself an editor in the header
  was an editor's. The header now names the caller only of a request the
  context carries none for (`CallerOf` empty): a proxy that
  authenticates sets it, and a middleware's caller is never overridden
  (`TestCallerHeaderDoesNotOverride`). Refusing both options together was
  the alternative; a middleware is also logging and limits, so the
  precedence is stated instead.
- **Main drains before the node stops.** The node's context was the
  signal's, so at SIGTERM the processors and the relay stopped while
  the server still answered: an at-most-once effect in flight had its
  context cancelled and its outcome never appended (a payment taken,
  recorded nowhere). The node now runs on `context.WithoutCancel` of the
  signal's context and is cancelled after `Shutdown` returns
  (`shutdownTimeout`, 10 s), and the relay's flush has its own
  `flushTimeout` (it shared the shutdown's 5 s, which slow requests
  could use up). `TestMainShutdownDrains`: an effect in flight at the
  signal while a client trickles a body across it runs to its end with
  its context live, its outcome is in the steps file, the trickled
  request is answered. An effect longer than the drain is still
  cancelled when the node stops.
- **Main's server bounds every phase** (`ReadHeaderTimeout` 10 s,
  `ReadTimeout` 1 m, `WriteTimeout` 2 m, `IdleTimeout` 2 m,
  `MaxHeaderBytes` 64 KiB; it set only the first), and a command's body
  bound is a `ServeOption`, `MaxBody(n)` (`defaultMaxBody`, 1 MiB; was
  the constant `MaxBody`): a larger body is **413**, was 400.
- **Arguments are decoded exactly, and refused as the client's error.**
  A step's fields were decoded through `float64`: an integer past 2^53
  was recorded rounded, `MaxInt64` as `MinInt64`, while the events
  decoded exactly. `rec.StepFields` now decodes into the block's type
  (integers exact; a field the body names in another case, which
  encoding/json takes, is recorded by its JSON name: the client's steps
  lost it, recording from the raw body) and refuses an unsigned value
  past int64 with the fix. Every adapter path decodes through
  `decodeArgs`: valid UTF-8 (encoding/json rewrote invalid UTF-8 into
  U+FFFD silently), strictly, **every field required but a pointer's**
  (a missing field and a `null` body decided on the zero value while the
  catalog's schema said required: the schema is now enforced, and says
  a pointer's field optional and a pointer, slice or map nullable), and
  every string text a store holds (a NUL byte in an argument a decision
  read as a tag was the decision's error, a 500). Its errors are
  `ErrBadArgs`, a **400**; typed `Execute` and `QueryBlock.Run` refuse
  the same before anything runs (`TestAPIRefusesBadArgs`,
  `TestStepFieldsExact`, internal/rec's `TestStepFields`).
- **A retry across a deploy is the same command.** The marker's hash
  covered the re-encoded typed arguments, so a field added to the
  command's struct between a try and its retry (its zero in the retry's
  encoding) made the retry `idempotency-key-reused`. The hash now covers
  the arguments' value, canonical: members sorted, every zero-valued
  member left out at every depth (`canonicalArgs`), so a field added
  with a zero default or fields reordered keep the key's answer, while a
  retry that sets the new field, or renames one, is other arguments (a
  reused key). Hashing the body as received was the alternative; it
  would make the in-process path (typed arguments, every field encoded)
  and the API's disagree, and a client that adds `"note": ""` another
  command. A change of meaning under the same fields is the command's
  version's to state (`TestRetryAcrossDeploy`). Markers written before
  this change hash otherwise: a retry in flight across the upgrade is a
  reused key (pre-release, no store to migrate).
- **Query parameters**: `queryArgs` takes a pointer to a string as a
  string (`?name=a` for a `*string` was "no JSON value", 400), leaves an
  absent pointer nil, and refuses a parameter that is not UTF-8;
  `client.Params` leaves a null (nil pointer) out, and the two are each
  other's inverse (`TestQueryArgsParams`).
- **An Internal block is 404, as an unknown one**, and a 404 lists only
  the blocks the API serves: a 403 for an internal block, and the
  unknown block's list of every command, told a client the internal
  names.
- **Ids are bounded**: `maxIDBytes` (255) in `checkID`, so an
  `Idempotency-Key` past it is a 400; a longer one reached PostgreSQL's
  btree limit (54000), which sqlstore classed transient (retried for
  ever): class 54 is now permanent.
- **`Idempotent-Replayed: true`** (`ReplayedHeader`) marks a command's
  answer taken from its key's marker before deciding (an earlier request
  was answered; `TestAPIReplayedHeader`), receipts and refusals alike.
- **grist/client backs off, within a budget, and reads a foreign 5xx as
  an unknown outcome.** Its retries waited a fixed `RetryDelay` (or the
  503's `Retry-After`) up to 50 times per call, so a server that stopped
  answering met every client's every call, retried at once, for ever: a
  retry storm (examples/retrystorm: retries that multiply an overloaded
  server's load keep it from recovering after the overload is gone, and
  qmeta's chain and the load simulation agree that backoff alone keeps
  the storm while a retry budget ends it). A retry now waits an
  exponential backoff (`RetryDelay` doubling to `MaxRetryDelay`, 5 s;
  "equal jitter", half of each wait drawn from a hash of the call and
  the retry, so a simulation stays a function of its choices), at least
  a 503's `Retry-After`, and only while the Client's `Budget` (a token
  bucket every call fills by `Ratio`, 0.2, holding at most `Max`, 10:
  Finagle's defaults without its time-based reserve) holds a retry; a
  call the budget refuses ends with its last error, a command's pending.
  A 5xx without grist's body (a proxy's 502 or 504, a load balancer's
  bare 503) was a final `StatusError`, so a command whose answer a proxy
  lost was told failed: it is now an unknown outcome, sent again under
  its key (a query's: unavailable). `TestClientBackoffAndBudget`.
  gristsim's HTTP tier gives its clients a budget every call refills
  (`MaxAttempts` still bounds each): its calls are judged by a
  client-side model, and a call the default budget left pending after
  its effect committed is one that model cannot place (a full-mode run
  of `TestHTTPTier` found one).
- **The real tier exercises the same-key retry.** `TestRealHTTP` lost no
  answer, so its clients never retried a command under its key. Its
  server now loses every fifth command's answer after serving it (the
  connection closed without a response); the clients retry under the
  key, the marker answers (`Idempotent-Replayed`, counted: the test
  fails if none was replayed), and both bindings and the answers' check
  judge the run. With the client retrying under another key, the
  answers' check fails (the store recorded no step of the client's id).

- **An at-most-once outcome that never comes is caught.** The model's
  outcome action is optional (a crash after the effect loses it, by
  right), so a runtime that returned before appending any outcome, or an
  app whose payout declared no success event, conformed to every action
  and to `everyRetirementPaidOnce` (which counted effect steps).
  grist.qnt gains `atMostOnceOutcomesLogged(log, effects, processor,
  success)`, a library def for an eventual val: every effect step of the
  processor has its outcome in the log. examples/counters states it as
  `everyPayoutOutcomeLogged` in the test-only `counters.exactly.binding.yaml`:
  it holds without faults, under `crashBefore` (no payment, no outcome
  owed) and with the bank's answer lost, and every run violates it under
  `crashAfter`; with grist returning before an ok effect's outcome every
  run violates it (an ablation), and so does the app mutant
  `silentPayout` (no `OnSuccess`: 6 of 6 runs, full mode), whose runs
  the retiring binding admits.
- **At most once with two pollers is checked.** `gristsim.Config.Nodes`
  runs n node processes on the one store (node, node2, ...: each starts
  the App, its pollers racing the others' for every checkpoint; the
  first serves the API; one Relay, so each row is recorded once; each
  restarted when it crashes). examples/counters' `TestPayoutTwoNodes`:
  two nodes without faults conform to the retiring binding (per push, 4
  seeds) and pay every retirement exactly once (full mode); grist's
  branch for a checkpoint append that failed after which the checkpoint
  had moved (counted as another poller's, its effect left alone) is
  reached, counted from the run's log (`Sim.Log`, which grist now
  writes at that branch: `qtest.Reaches` names fault kinds, and the
  branch is none). With that branch taking the moved checkpoint for its
  own (the review's mutant in `commitAt`), 10 of 10 runs reject
  `payoutEffect` (`atMostOnceEffect` refuses a second payment step);
  every claim with one node passed it. grist's
  `TestEffectAtMostOnceLostCheckpoint` is the unit test: an append
  answered with an error after another poller moved the checkpoint runs
  no effect.
- **A poller re-reads its checkpoint after reading its triggers.** Two
  nodes through crashes found it (2 of 10 runs of `TestPayoutTwoNodes`'
  faulty half, a reconstruction cycle at `welcomeEffect`): the
  checkpoint and the triggers are two reads, and another poller's commit
  between them left this poller handling a trigger already checkpointed,
  with the newer head, so its at-least-once effect step came after the
  other's checkpoint on the tenant's clock (`order.pos`) and before it
  among the processor's (`order.p`). A checkpoint unmoved at a second read
  was the same when the head was read; a moved one is read again
  (`TestPollerCheckpointRaced`; without the re-read it fails).
- **The payout under faults, per push**: `TestPayoutTwoNodes` then runs
  two nodes through commit faults and crashes (3 seeds per push, 10 in
  full mode; `Conforms`, `All(Settles)`, commit faults reached): before,
  nothing model-judged ran the payout per push under any fault but the
  scripted `crashBefore`.
- **An unknown outcome is not a failure.** At most once, f's error
  committed `grist.failed`, read as "not paid", for a payment whose
  answer was lost (paid, and recorded failed). An error is now unknown
  unless f wraps it in `grist.NotApplied` (the dependency said it did
  not happen): an unknown outcome appends **`grist.unknown`**
  (`{processor, error, trigger}`, the error's text carried, for
  reconciliation; registered in every App beside `grist.failed`), a
  known one `grist.failed`. The outcome step records `known` and
  `error`; grist.qnt's `atMostOnceOutcome(processor, trigger, ok, known,
  error, success)` and `unknown(...)`, and `atMostOnceDone` refuses a
  second outcome of either kind. A node stopping after f ran still tries
  to append its outcome (its context's cancellation does not stop the
  append: `defaultTransientTries` tries). At least once is unchanged
  (every error retried).
- **The simulated bank records its payments** (`bankPays`: actor the
  tenant, the key, the counter and the amount), where it makes them:
  an effect's site records the effect. `everyRetirementPaidOnce` now
  counts the bank's payments under each retirement's effect key, so a
  payment made twice inside f, or made while its answer was lost, is
  seen whatever the effect step says (with the answer lost: paid once,
  both vals hold). `Bank.Pay` takes the tenant.
- **Decisions see the caller, and steps record it.** A decision or query
  could not tell who asked (`Envelope.Caller` reached the policy only),
  and no step recorded it, so no model could judge a rule over the
  caller. `Reads.Caller()` is the envelope's caller ("" for a processor's
  handler), and every command's and query's step records it as
  **`caller`** (a reserved argument name; `principal` until H4), the
  store's side and the client's alike (`TestCallerInDecisionsAndSteps`). examples/counters'
  `retire-counter` is its creator's only: `counter-created` records its
  `creator` (`Reads.Caller` at creation), and another caller's retire is
  refused `not-creator`, a decision's refusal (422, committed as the
  key's answer and recorded at the store; a `Policy` sees the caller and
  the tenant, not the counter). The model's `retireCounter(counter, id,
  caller, ...)` requires `createdBy(log, counter, caller)` for ok
  and its negation for `not-creator`; `TestRetireByCreatorOnly`: two
  clients creating and retiring conform, and some retire is refused so;
  without the check (the `anyoneRetires` mutant, full mode) 6 of 6 runs
  reject `retireCounter`. A policy's denial (403) still records nothing
  at the store (nothing ran); `denied` is now an outcome the calls
  binding knows (`countersCalls`' `denied` action: nothing appended, the
  key unanswered), so a client-side run with a denial is judged rather
  than failing on an unknown outcome.
- **`checkpointFirst`'s claim says what it shows.** `Some(Rejects("welcome"))`
  is the at-least-once model refusing a checkpoint step before any
  effect step of its trigger (`atLeastOnceDone`): the mutant's every
  welcome comes so, which no other bug of the counters' runs makes
  without a crash (the faithful app's runs at that configuration reject
  nothing). It shows a declaration contradicting the model, not a lost
  mail; the lost effect at most once is `payout`'s claim.

### grist §0.8: What wallet-grist's trial changed

A newcomer agent built examples/wallet's bank on grist
(examples/wallet-grist, its TRIAL.md) from the skill and the docs alone.
What it found, each fixed where it stands:

- **`Unconditioned` is unprotected at every isolation level.** The
  skill and counters' README said the race's loser retries a transient
  conflict under Serializable and decides again, so the window closes;
  `readThenWrite` (wallet's `transfer` `Unconditioned`) committed an
  overdraft at Serializable in 2 of 20 runs (3 of 60 re-measured), a
  transfer decided at position 0 committing after the other's. A
  decision's reads are store reads of their own, pinned before the
  append, whose transaction begins after them (simstore takes its
  snapshot at `Begin`): a loser retries only when the two appends
  overlap. counters' 0 of 120 was its shape's luck, not a property:
  `blind` at Serializable with four clients creating three counters each
  commits a second create of a name in 2 of 60 runs.
  `TestUnconditionedAtEveryLevel` (grist) pins it: a create of a name
  another create took between the read and the append commits blind on
  simstore at Serializable, Snapshot and ReadCommitted, and is refused
  conditioned. wallet-grist's `readThenWrite` claim at Serializable is
  `Some(Rejects("transfer"))`. Reads inside the append's transaction were
  not built: the condition already protects at every level and on every
  store (docs/grist.md §2.4).
- **`grist.Main serve` records a settle** on shutdown, once the server
  drained, every poller of the node waits with nothing after its
  checkpoint (an effect in flight is work: an at-most-once one's
  outcome is waited for) and the relay caught up (`settleTimeout`; none,
  said on stderr, if the node does not quiet): a served run's eventual
  vals are judged (`TestMainServeSettles`; counters' `SERVE=1` demo).
- **An exploration's summary names the untaken actions**: "model
  actions taken 5 of 11 (never: ...)", of the model's actions but `init`,
  `step` and frame helpers. An action only other actions call (a grist
  model's `appends`, `refuses`) is one of them and shows as never taken.
- **The real tier's "no model-level ITF" log says the verdict is
  unaffected**: the ITF file (`Run.ITF`) is written in-process from
  literal arguments only, and `readsOf(...)` is a call; quint, which
  judges, evaluates it.
- **The skill** links reference pages (the model's rules, multi-entity
  commands), says that a declared guarantee contradicting the model is
  rejected at its first step (the lost effect it would cause is never
  reached), documents a double's site record (`actor` the tenant,
  `process` the App's name), and names Quint's reserved and built-in
  names (`to`, `of`, `type`, `size`, `map`, ...), which a Go field's JSON
  name must avoid. `Catalog`'s doc comment no longer reads as if the
  generator (docs/grist.md §3.2) were built.

### grist §0.9: What building the generator changed (GR2)

GR2 (docs/ROADMAP.md): onboarding with minimal work, done when a trial
agent writes no binding by hand. The trial (§0.8, wallet-grist's
TRIAL.md) wrote 200 of 314 model lines, three bindings, an 80-line
mutant harness and a 120-line real-tier harness by hand. Built, each
with a test that fails without it:

- **The generator is the app's binary's** (`<app> model`, `grist.Main`;
  grist/internal/gen's `Generate`, `Starter`), not a `cmd/grist` that would have
  to load an application's catalog it cannot import: only the app's own
  process has its `App`. It needs no binding or validate (text from the
  `Catalog`), so a production binary links nothing more.
- **Shapes the generator needs are declared, and enforced.** The
  catalog lacked each command's refusals and each processor's result:
  `grist.Refuses(rejections...)` on a command (a decision returning an
  undeclared rejection is its error, not remembered), `grist.Emits(types...)`
  or `grist.Delivers(g, success...)` on a processor (g a `grist.Guarantee`; a result that
  contradicts it fails the trigger: tried, then `grist.failed`; Skip and
  Fail allowed under any, though a Delivers processor's have no step in
  its generated model: §0.10). A declaration the code could contradict
  silently would make the generated model wrong silently
  (`TestRefusesDeclared`, `TestShapeDeclared`; each fails with its
  enforcement taken out). A processor declaring neither is the
  generator's error.
- **What it writes, and what the app writes.** `model/<app>_gen.qnt`
  holds three modules: `<app>Events` (an `Event` constructor per type,
  accessors, reads, each read model's selection of the log and the reads
  a read of it records, a record type per block's arguments with
  `caller`, per query's result, per site's record), `<app>Gen` (the
  store's side: every variable, an action per command with its reused
  key, conflict, ok and declared refusals over `freshKey`, `covers` and
  `conditionHolds`, per query, per processor by its declared shape over
  grist.qnt's at-least-once and at-most-once defs, per site; every frame
  condition; `step`) and `<app>CallsGen` (the clients'). Beside it the
  vendored `grist.qnt`, `<app>.binding.yaml`, `<app>.calls.binding.yaml`
  and a binding per test-only variant. The app writes
  `model/<app>.qnt`: module `<app>Rules` with the holes (per command
  `<c>Outcome`, `<c>Events`, `<c>Needs`; per query `<q>Answer`; per
  processor `<p>Emits` or `<p>Success`; per site `<s>Admits`; `STRS`
  and `INTS` for `step`) and the read models' folds; module `<app>` and
  `<app>Calls`, importing the skeleton, with the invariants. **Folds
  are holes**, not generated: a fold is a Go closure the generator
  cannot read, and an analyzer that translated one (gristpure's facts,
  docs/grist.md §4.1) is slice 3's; the generated selection (`totalsEvents(log, c)`)
  leaves a fold one line. **A rule is a function** (the outcome its
  decision reaches on the log), not a predicate per outcome: the Go
  decision is one, it makes `step` able to drive every command, and a
  generated action checks `result == <c>Outcome(log, c)`.
- **Regeneration preserves the holes by construction**: the skeleton is
  a module of its own file, which the app's model imports and the
  generator overwrites whole; the app's file is never written but by
  `model -init` when it does not exist. Markers inside one file were the
  alternative; they make a stale region a merge, where a module makes it
  a quint error naming the hole. A hole takes its block's arguments as a
  record (`c.counter`), so a field added to a command changes no hole's
  signature; a new block's holes are reported missing, each with a stub.
- **Invariants are marked, not listed**: `// grist: invariant|eventually|violable
  [bindings]` on the line before a val (`default` the store's binding,
  another name a test-only variant `<app>.<name>.binding.yaml`; in
  `<app>Calls`, the clients' binding). counters' `once`, `exactly` and
  `retiring` are three such variants. A variant keeps its own eventual
  vals so a per-push binding pays for few replays; one that judges only
  its own vals compares no observation (the default binding's claims
  do), one that judges every default invariant is a default binding with
  more vals and compares them, as counters' hand-written variants did.
- **The binding module exports the skeleton**, so binding's `LoadModel`
  follows whole-module exports transitively (a core change:
  `TestLoadModelFollowsExports`). And the app's modules import grist,
  the events and the rules themselves beside the skeleton: quint's
  flattener (`quint compile`, which the validator's Go evaluator runs
  on a module importing the binding's from another directory) loses
  names re-exported twice, and without the evaluator every
  linearization's candidates were checked by quint processes:
  wallet-grist's linearized claims cost twice the CPU, with nothing
  failing. `gristtest.Generated` fails a module without those imports
  (ablated: it does), without running quint (every validation
  typechecks the model).
- **The read set is a parameter of triples** (`reads: Set[(str, str,
  str)]`, `readsOf` in the action), which the binding's default template
  renders as literals: a generated binding needs no template for it,
  and the "no model-level ITF" log (§0.8) is gone. A set, not a list
  (§0.10): a read set has no order, and the generated `step`'s
  `triplesOf` folding a set into a list was outside the Go evaluator's
  fragment.
- **Names**: a parameter Quint reserves (`of`, `item`, `size`: quint's
  built-ins, read from its own list) is renamed (`of_`, with the binding's
  template); an integer tag, a pointer argument and a JSON name that is
  no identifier are the generator's errors with the fix.
- **`grist new <name>`** (`grist/cmd/grist`) writes a module (an App of
  two events, a keyed read model, a command with two refusals, a query, a
  processor that emits; its binary, its rules, a gristsim test with
  `Expect` claims, a mutant, a real-tier test, `run-demo.sh`, a README),
  runs `go mod tidy` and the app's `model` command; `TestNewIsGreen` runs
  `go test -short ./...` in it (full mode, about a CPU minute).
- **Harnesses** (grist/gristtest): `Generated(t, App)` (the CI diff: the
  checked-in skeleton, grist.qnt and bindings are the catalog's, every
  hole defined, both modules importing the events and rules;
  `GRIST_UPDATE=1` writes them);
  the overlay mutants, moved to the core in H4 (`qtest.Mutants{FullOnly,
  Generated}.Run(t)` and `qtest.Mutant()`: each mutant a directory of
  `testdata/mutants`, found there, its binary built with a go `-overlay`
  of its `*.go`, told its name by `GOTOGETHER_MUTANT`: no marker file;
  `gristtest.RuleMutants(App)` the generated ones); `NewTier(t, App, TierConfig)`
  (the real tier: SQLite, real HTTP, nodes started and killed, lost
  answers, `Settle` waiting for every processor of the catalog and the
  relay, `Check` validating both bindings and `Answered`, the
  every-client-answer-has-its-store-step cross-check). In grist:
  `grist.NewDep[T]` (a typed dependency: `With`, `Of`, a fallback) for
  each app's context key, and `grist.NewSite[T]` (`Record(ctx, tenant,
  v)`: a double's record of its effect at its site, actor the tenant,
  process the App's) for the hand-written `qobs.Record` of `actor` and
  `process`.
- **What remains hand-written**: examples/counters' model 472 lines (329
  of code) → 143 (75), its five bindings (203 lines) → none, its mutant
  and real-tier harnesses 563 → 265 lines; wallet-grist's model 314
  (211) → 124 (64), its three bindings → none, its harnesses 388 → 145.
  Their claims are unchanged and pass in full mode.

### grist §0.10: What the review of GR2 changed

Two reviewers' reading of GR2 (an architect's and a newcomer's, who ran
`grist new` and grew the app it wrote), each fix with a test that fails
without it:

- **The generated models judge what the hand-written ones did.**
  counters' and wallet-grist's skeletons plus their holes were diffed
  against the hand-written models they replaced: on the store's side
  the actions are equivalent (the generated at-least-once checkpoint
  takes the processor's own EffectTries, where the hand-written one
  wrote EFFECT_TRIES), and every mutant is caught by its claim as
  before. The clients' side is weaker in one place: a conflict answers
  any call of a conditioned command there, where wallet's hand-written
  model required a valid transfer (an invalid one is refused before it
  reads, so it cannot conflict). The store's side still refuses it (a
  conflict needs a recorded read the condition failed on, and the
  invalid decision recorded none), and both sides judge every run, so
  no mutant escapes; a generic rule would need the clients to know the
  reads.
- **Regeneration never writes the app's file**, but an app named
  `grist` had its model at `model/grist.qnt`, the vendored library's,
  which every `model` overwrote (with its module named as the
  library's): Generate, Starter and `grist new` refuse the name
  (`TestGenerateRefusesGristsName`, `TestNewRefuses`).
- **A refusal named as one of grist's own outcomes** (`ok`, `conflict`,
  `denied`, `error`) would be judged as that outcome: a "conflict"
  refusal by the failed-condition branch (and committed without its
  condition), a "denied" one at the clients as a 403 that answers
  nothing. `Refuses` refuses them (`TestRefusesDeclared`).
- **Answered** keyed commands by id alone: two tenants' clients using
  one id failed it ("two steps on one side"), and so did a call the API
  denied (no store step, by right), a key answered twice from its
  marker, and a key reused with other arguments (its refusal a step per
  try). It now keys by tenant and id, skips denied calls, and requires
  of the store one outcome per key (key-reuse refusals aside) and each
  outcome a client was told (`TestAnswered`).
- **The generated `step` was outside the Go evaluator's fragment**
  (internal/qeval's `TestAgreesWithQuint`, red since wallet-grist
  landed): grist.qnt's `triplesOf` folded a command's needed reads, a
  set, into a list, an order-dependent fold, and the step drew every
  block's arguments in one product of nondets (wallet's 98304
  successors, past the evaluator's 65536). Read sets are sets end to
  end (`readsOf`, `triplesOf`, `covers`, `conditionHolds`; the action's
  `reads: Set[(str, str, str)]`, rendered by the default template), and
  each branch of `step` draws its own arguments. Nothing is weaker: a
  read set's order never meant anything (`covers` asks for some read,
  `conditionHolds` for every one), and the step's successors are the
  same.
- **The starter's stub of a query whose result has no scalar field** was
  `{  }`, which does not parse; it is `{ unit: true }`, the record type
  and the binding's answer (`TestStarterEmptyResult`).
- **Onboarding**: `<app> model` with no `model/<app>.qnt` listed every
  hole as undefined; it now says the file is missing and that `model
  -init` writes one, and names the files it writes
  (`TestMainModel`, the model command's first test). The overlay is
  `qtest.Mutants.Overlay` (H4: the core's harness).
- **The library trial** (examples/library, TRIAL.md), from `grist new`
  and the docs alone: a query result's `[]string` field was dropped by
  the generator without a word (`MemberLoansResult = { unit: bool }`),
  so the answer compared nothing of it. A field of an event, arguments,
  result or site record that no step records (a list, a struct) is now
  the generator's error with the fix, unless tagged `grist:"-"` (the
  model leaves it out, said where it is declared; library's `Books`).
  `grist new -replace` checks the checkout before writing anything and
  writes the replace relative to the module (`../..` in an example), not
  an absolute path. A claim `Violates("x")` of an eventual val x says to
  write `Violates(validate.EventuallyName("x"))`. docs/grist.md §2.4's `Within` and
  `Also` are marked designed, not built. Documented (skills/grist): a
  site's records are ordered with no commit of the tenant's log, so a
  relation between them and the log is an eventual val (`// grist:
  eventually`); gristsim's `Send` draws uniformly from its list, so a
  name listed twice is sent about twice as often, and there is no
  ordered prologue; `r.Exists(T, Tag(k, v))` records the read `<t>By<K>(v)`
  (`Exists(T)`: `<t>Read`); a `NewDep`'s T is best an interface, whose
  fallback refuses every call.
- **Left**: a `Delivers` processor's Skip or handler Fail (the runtime
  allows both) commits a checkpoint with no effect step and no `ok`,
  which its generated actions cannot take: the binding fails on the
  missing parameter, or the at-least-once checkpoint is rejected. A
  sound model needs the rule for which triggers skip (a hole
  `<p>Skips(prefix, trigger)`) and an action for the skip; the docs say
  so meanwhile (Delivers, skills/grist's model reference). And a
  command that declares no `Refuses` but refuses at run time fails in
  the binding ("outcome has no mapping"), an error that names the
  binding, not `grist.Refuses`. From the trial: a site record carrying
  the position of the commit that triggered its effect (an order edge,
  so a relation to the log is an invariant), an ordered workload in
  gristsim, a claim that every block of the catalog was taken (qtest's
  `Exercised` counts grist's helper actions too, and the summary's
  "never" list mixes them with the app's and truncates), and an outcome
  filter on `Takes`: each a design change of its own, in the ROADMAP.
- **Cost**: counters' `-short` measured 359.5 CPU s here (load 10-25 on
  4 cores; ci/test-times.txt says 354): 90 `quint test` processes, 4
  typechecks and one compile. Each `quint test` of a counters replay
  costs about 3.3 CPU s, of which a typecheck of the replay is 3.1 and of
  `grist.qnt` alone 1.5 (a trivial module's whole test is 0.45): the
  library's typecheck is most of every replay's fixed cost, spread over
  its defs (no one def is slow). Moving the clients' modules out of the
  store binding's files saved 3% per process, under the noise; sharing
  one typecheck between a spec's two bound modules would save one
  quint process per test binary. Neither is the cheap waste the 5% was
  suspected to be: the generated skeleton is larger than the
  hand-written model, and quint typechecks all of it per process.

### grist §0.11: What GR2b changed (the library trial's left items)

The left items of §0.10 and the library trial's ranked list (its
TRIAL.md, items 2-4 and 8), each with a test that fails with it taken
out:

- **A run that reached none of the app's blocks fails.** library's first
  `TestFaithful` never committed a borrow and passed every claim.
  `gristtest.Catalog(App).Exercised(except...)` (H4: qtest's
  `Catalog.Exercised`, the catalog built from the App's) claims every block of the
  catalog taken in some run: each command by `ok` and by each declared
  refusal, each query, a processor's handling (Emits) or its effect and
  checkpoint (Delivers; an at-most-once outcome when it declares a
  success event), each site's record. It names every one missed with
  how to reach it (`Config.Prologue`, `Gen`, `Send`, `Setup`).
  A conflict, a reused key and a skip are not wanted (faults and
  workloads decide them). It is qtest's `TakesEach` of the catalog's
  `Takes` (core: `Take.Outcome`, `Take.Hint`, `TakesEach`), so its
  verdict is any Takes' (`TestCatalogExercised`, qtest's
  `TestTakesOutcomeAndEach`). **Not applied by default**: gristsim's
  `Run` is a `qtest.System`'s run, which knows no claims, and a default
  in qtest would fail every contrast and mutant test, whose workloads
  are narrow by design (library's `TestScarce` never returns a book),
  so each would carry an exception list. That noise is what makes
  exceptions a habit. The faithful test claims it: the starter's
  `TestFaithful` does (`TestNewIsGreen`), as do library's.
  Applied there it found two real gaps. `TestGreedy`, the contrast of the
  `staleLimit` mutant, had never reached the limit refusal it stands for
  (its join and additions were random sends). `TestFaithful`'s runs had
  never refused an invalid addition. Both are now reached through the
  prologue below.
- **The summary's "never" list** names the actions the binding names
  (a grist binding names every block) apart from the model's others
  (grist's `appends`, `readsThrough`, ...), and never cuts the first
  short (`TestSummarySeparatesTheBindingsActions`).
- **`Takes(action).Outcome(o)`**: a run counts only if it took the
  action by a step recorded with outcome o. A contrast test claims the
  refusal its mutant removes (library's `TestGreedy` claims `limit`,
  `TestScarce` `unavailable`).
- **An ordered workload**: `gristsim.Config.Prologue []gristsim.Call`,
  calls made in order, each answered (or pending after MaxAttempts),
  before any client sends its first. They go to each tenant or the one
  named, recorded as clients' calls (caller `prologue`) through the same
  faults (`TestPrologue`, which fails with the clients not waiting).
  library's faithful, greedy and lending shapes open with it, where they
  weighted `Send`.
- **A Delivers processor's Skip has a step**: a Skip or a handler's Fail
  of a Delivers processor commits its checkpoint as the processor's skip
  step (`<p>Skip`, Fail's with its `grist.failed`). The generator writes a
  hole `<p>Skips(prefix, trigger): bool` and an action `<p>Skip(trigger)`
  that requires it, no effect step of the trigger, and nothing appended.
  Its effect actions (and an at-most-once checkpoint) require it false.
  A Fail is then a Mismatch of the skip (its `grist.failed` against the
  skip's empty batch), as an Emits processor's Fail is a Mismatch of its
  emit. `TestDeliversSkip`: the faithful rule conforms and takes both
  the skip and the effect; a rule that skips nothing is
  `Rejects("mailSkip")`, one that skips everything
  `Rejects("mailEffect")`. Before, the skip's step bound to the
  checkpoint action and failed on its missing `ok`. counters',
  wallet-grist's and library's rules say `false` (no handler skips).
- **A command that declares no refusals and refuses** is still refused
  (its key answered: changing that would make every hand-written
  command's refusal an error), but its step carries an error naming the
  command and saying to declare `grist.Refuses`. The binding's "outcome
  has no mapping" prints the step's error, so it now names the fix
  (`TestUndeclaredRefusalNamesRefuses`; the step row gains an optional
  `error`, which an older grist ignores).
- **Left to GR3: a site's record ordered after its trigger's commit**
  (docs/grist-instrumentation.md docs/grist.md §3.3, its slices 1-2). The order GR3
  specifies (`order.pos` at the cause, a thread per key, the cause
  carried with the call) needs the call declared and its cause carried
  to wherever it is served, which is GR3's `NewCall`. A `Site.Record`
  that read the cause from its context would order an in-process double
  only, and GR3's served record replaces it. One finding went into that
  design (its 1b7309e): an order of 2·T+1 would place a record of a
  trigger inside a batch of several events (wallet-grist's transfer
  appends two moves) before the commit holding the trigger, whose step
  is at 2·P, P the batch's end. The record is placed at the poller's
  head, with the rule given the prefix through T. So library's
  `everyReceiptForABorrow` stays an eventual val, and `<s>Admits` still
  sees only the records.

### grist §0.12: What building GR3's slice 1 changed (declared calls, simulated)

docs/grist-instrumentation.md docs/grist.md §4's slice 1: a dependency an effect
calls is a declaration, recorded at both ends and modelled by the
generator, on the simulated tier. Built, each with a test that fails
with it taken out:

- **Declared calls** (grist/calls.go): `grist.NewDependency(App, name)`,
  `grist.NewCall[Req, Resp](dep, name, grist.Keyed(), grist.Refuses(...))`
  (request and response structs of scalars; a field of one name on both
  sides, or named `processor`, `trigger`, `key` or `result`, a refusal
  named `unknown`, a dependency named `api` are `Verify`'s problems), and
  `grist.Uses(calls...)` on a processor, enforced as `Emits` is: `Do`
  from an effect of a processor that does not list the call, or outside
  any effect, is the effect's error naming the fix and records nothing
  (`TestCallDeclared`); `Uses` on a processor that declares no
  `Delivers`, or of another App's call, is a problem
  (`TestCallDeclarations`). The catalog gains `Dependencies` (each call's
  types, `Keyed`, refusals and users) and each processor's `Uses`.
- **Both ends recorded by grist** (`TestCallSteps`). `Call.Do` records
  the caller's view, the step `<dep><Call>` (`bankPay`), as
  `qobs.Invoke` before the request and `Call.Done` with the answer: the
  request's fields, the processor, the trigger, the effect's key, the
  response's fields (zeros without one), the outcome `ok`, a declared
  refusal's name (returned as `NotApplied` of its `*Rejected`: at most
  once, `grist.failed`) or `unknown` (any other error: `grist.unknown`);
  ordered as the effect step it precedes (`order.pos` 2·H+1, the
  effect's `order.p.<processor>`, the poller's thread). The handler's
  wrapper (`Call.Handler`, served by `grist.Local`) records the
  application, `<dep><Call>Served`, at its cause, `grist.CallInfo`
  (App, tenant, processor, trigger, the head the poller read, the key):
  `order.pos` 2·H+1, on a thread of the key, so it follows the commit
  holding the trigger even inside a two-event batch
  (`TestSiteAfterItsCommit`, which fails with 2·T+1: a batch's commit is
  at 2·P). A handler answering a key from its first answer says
  `grist.Replayed(ctx)` and records nothing (`TestReplayedRecordsNothing`);
  one returning an error that is no declared refusal is recorded with
  the fix. `grist.CallKey(ctx)` is the key in an effect and in a handler.
- **gristsim serves each dependency** as a `qenv.Service` named after it
  (faults `rpc.<dependency>.request`, `.delay`, `.response`), on no
  process, from `Config.Doubles` (`func() gristsim.Doubles`, per run);
  a dependency without a double, or a call without a handler, is the
  run's error (`TestDoublesServed`). The default double is slice 2's.
- **The generator** writes per call its request, response and record
  types (`BankPayRequest`, `BankPayResponse`, `BankPayRecord`), the
  caller's steps and the site's (`bankPayCalls`, `bankPayRecords`), the
  caller's action (a user's trigger, before its effect step, under the
  trigger's key, `callKeyStable`; at most once, no earlier call of the
  key; the request the rule's), the site's (a user's trigger in the log,
  `<c>Admits` given `log.slice(0, T)`), a rule hole per (processor,
  call), `<p><Call>` (`payoutBankPay`), and `<c>Admits` itself unless
  the rules define one (a `Keyed` call: no earlier record of the key;
  else any), with `step` branches drawing each (in the Go evaluator's
  fragment: `TestAgreesWithQuint`). grist.qnt gains `CallKey`,
  `callKeyStable` and `keyCalled`, checked in grist/model's tests. The
  binding names both actions with no template but the outcome as
  `result` (and a reserved name's), and the calls' outcomes.
  `gristtest.Catalog(App).Exercised()` takes each call, answered ok and applied.
- **counters' bank** is a declared call (`Bank`, `Pay`: `Keyed`,
  refusing `declined`), its double a handler attached per run in its
  tests (deduplicating by key, `Replayed`); deps.go's `Bank` interface, `BankDep`,
  `noBank`, `payRequest`, `SimBank`, `BankPays` and `Payment` are gone,
  its rules gain `payoutBankPay` and lose `bankPaysAdmits`, and
  `everyRetirementPaidOnce` reads the served records by trigger. The new
  mutant `wrongAmount` (the payout pays one more) is
  `Some(Rejects("bankPay"))` in 4 of 4 runs, where no record the store
  makes differs; `TestPayoutAmount` is its contrast (`None(Rejects("bankPay"))`,
  and conforming through the bank's own lost, duplicated and late
  requests and lost answers). The other payout mutants and claims are
  unchanged and caught as before.

Where the design changed (docs/grist-instrumentation.md, corrected in
place): the site's action does not judge the request, the caller's
does. The site's record is recorded inside the call, before the caller's
completion, at the same 2·H+1: the two are concurrent and reconstructed
in recording order, the site first, so a request judged at the site
would reject `bankPayServed` for the caller's error and the caller's
step would never be judged; grist records the caller wherever it
records the site. The site's records are `<c>Records`, not `<c>Served`
(the site action's name). A response's fields are step arguments, not
observations, so the actions take them as same-named parameters.
`grist.Handler` is a fold's handler, so a call's is `grist.CallHandler`
(`Handlers` as designed). `Transport` and `Local` came with slice 1
(gristsim serves through `Local`), and `Replayed` is new: without it a
deduplicating double's replay is a second application. The call's
timeout is `gristsim.DependencyTimeout` (1 s), not yet an option.

**The slice's review** (2026-10-27), each probed by a counterexample:

- **A replay was unjudged.** `grist.Replayed` recorded nothing, so the
  model could not tell a replay of the first answer from one of another
  answer, or of no application at all (the faithful default double of
  slice 2 could get it wrong unseen). The wrapper now records the step
  `<c>Replayed` with the handler's answer, and the generated action
  admits it only as an earlier served record of the key, request,
  outcome and response alike (`<c>Records.indices().exists(i =>
  <c>Records[i] == rec_)`): counters' bank answering a duplicate with a
  new receipt rejects `bankPayReplayed` in every run (TestPayoutAmount,
  `paying` with its first request duplicated; the faithful bank there
  conforms and replays), where it conformed before; grist's
  `TestReplayedRecorded`. `Replayed` stays the handler's word: a double
  that applies a call again and says `Replayed` with the first answer
  hides it, which no record can show. counters' double now keeps each
  key's first answer, a decline included (a declined key retried was a
  second served record; no counters workload declines).
- **Two records of one key could form a cycle.** The site's record was
  placed at its own cause, 2·H+1, on the key's thread; a request from
  an earlier head served after a later one's (a request delayed past a
  restarted poller's retry, two nodes' pollers) put a higher `order.pos`
  before a lower one on one thread, a cycle (qtrace's `cycle` issue,
  the trace's, not Reconstruct's own list), and would put a replay before
  the application it repeats. `grist.Local` keeps each key's last place
  and records at the later of the two (`TestSiteOrderPerKey`, which
  fails with the cycle without it); still after the trigger's commit.
  `TestSiteAfterItsCommit` checked only Reconstruct's returned issues,
  where a cycle never is: it now checks the trace's too.
- **The site does not judge the request** (left as built): in slice 1
  both ends record the same bytes in grist's own code, and the caller's
  step is recorded for every outcome but a crash during the call, so a
  request that does not follow from its trigger is judged at the caller
  (`wrongAmount`). It becomes a gap where the site is recorded by another
  process or for a request no grist caller sent (slice 3's
  `ServeCalls`): there the site's action should judge its request
  against the rule too, accepting that a caller's wrong request is then
  named at the site first. Keyed admits is enforced at the site as
  built.
- **A response's zeros** without a response (a refusal, an unknown
  outcome) are told apart by `result`, which every record carries; an
  invariant reading a response field conditions on `result == "ok"` (the
  record type's comment says so).
- **API**: `Dependency.Calls` and `Call.Dependency`, used nowhere, are
  removed (the catalog lists a dependency's calls); `Do`'s error for a
  dependency with no transport names the fix (`With(ctx,
  grist.Local(handlers))`).
- **Cost**: `wrongAmount` runs at `paying`'s shape (a prologue creating
  and retiring counter a, so every run pays), `All(Rejects("bankPay"))`
  at one seed per push instead of `Some` at two (the mutant's test 21.6
  to 13.1 CPU s here). TestPayoutAmount's contrasts (without faults, and
  the faithful bank under a duplicate) are full mode: per push
  TestPayoutTwoNodes' runs, which already ran, claim the call answered ok
  and applied without faults, and under faults the bank's lost requests
  and answers, an unknown call and a replay, all conforming. The new
  per-push claim (a new-receipt replay rejected) is one seed. The two
  tests together 41.1 to 37.7 CPU s; counters -short 392 and 402 CPU s
  before and after, in the noise of a shared machine. TestPayoutAmount
  never reached a lost answer (0 of 10 runs at 0.3 among
  the other faults): its comment no longer claims one.

### grist §0.13: What building GR3's slice 2 changed (the default double, every example's dependencies declared)

docs/grist-instrumentation.md docs/grist.md §4's slice 2: no grist example writes a
double, a site or a dedupe rule by hand. Built, each with a test that
fails with it taken out:

- **The default double** (grist/calls.go): `grist.DefaultHandlers(dep)`
  are a dependency's handlers that apply a `Keyed` call once per key and
  answer it with the zero response, answer a later try of the key from
  that first answer as a replay (`grist.Replayed`: recorded
  `<c>Replayed`, judged as one, so a key's first answer is final), and
  apply an unkeyed call every time. gristsim serves them to a dependency
  the run's `Config.Doubles` gives no handlers (a dependency given
  handlers lacking a call is still the run's error); a real tier or a
  binary attaches them in process (`dep.With(ctx,
  grist.Local(grist.DefaultHandlers(dep)))`), their records going to the
  node's recorder. `Call.Handler`'s wrapper is shared, so the default
  records through the same code (`TestDefaultHandlers`; gristsim's
  `TestDefaultDouble`, through the Service's duplicates and lost answers,
  fails with the default's dedupe removed: a key applied twice, no
  replay). The default lives in grist, not gristsim, so slice 3's real
  tier serves it too.
- **library's mailer** (`Mailer`, `Send`: `Keyed`, request `{member,
  book}`) served by the default double: mailer.go 62 to 27 lines (the
  `Mailer` double, `Sends`, `Sender`, `MailerDep` gone), the model's
  `mailerSendsAdmits` (3 lines) gone for the rule `receiptsMailerSend`
  (4). `Receipt.At` goes: the trigger is grist's. `everyReceiptForABorrow`
  is an invariant over the served records' triggers. The mailer is a
  `qenv.Service` now, so TestFaithful claims what it never met:
  `Reaches("rpc.mailer.response").Faulted()`, a send of outcome
  `unknown`, a replay. `noDedupe` is a double sending every try (the
  overlay replaces doubles_test.go, whose faithful `doubles` is nil),
  `Some(Rejects("mailerSendServed"))` by the generated admits.
  `wrongReceipt` cannot name a wrong position any more; it swaps the
  receipt's member and book and is `Some(Rejects("mailerSend"))`, the
  caller's generated rule (below).
- **wallet-grist's ledger** (`Ledger`, `PostEntry`: `Keyed`, request
  `{account, amount}`) served by the default double, in process on the
  real tier and in the binary: deps.go 107 to 28 lines (`Book`,
  `SimLedger`, `LocalLedger`, `LedgerPosts`, `LedgerDep` gone),
  `ledgerPostAdmits` (3 lines) for `postLedgerPost` (4). `ledgerNoDedupe`
  is a double posting every try, `Some(Rejects("ledgerPostServed"))`
  (6 of 6, as `Rejects("ledgerPost")` was). `TestPostAfterTransfer`: a
  transfer commits two moves in one batch, each a post's trigger, and the
  ledger's record of the first follows the batch's commit; with the site
  placed at 2·T+1 every run rejects `ledgerPostServed` (its trigger not
  yet in the log).
- **counters' mailer** (`Mailer`, `Send`: not `Keyed`, as the simulated
  mailer it replaces sent every mail; request `{counter, name}`) served
  by the default double under the same Service name, so `rpc.mailer.*`
  and the scripts' faults keep their kinds: deps.go's mailer half (53
  lines: `Mailer`, `MailerDep`, `noMailer`, `Mail`, `SimMailer`) became
  its declaration (23), the rule `welcomeMailerSend` (5) is new; the real
  tier's and the binary's mailers are handlers served by `grist.Local`.
  `StoreDep` stays a `NewDep`: it is no call. Every claim and mutant
  unchanged.
- **`grist new`'s starter** declares a dependency (`Warehouse`, `Keyed`
  `Receive`, the processor `shelve` at least once, the rule
  `shelveWarehouseReceive`, an eventual val that every addition was
  received or failed every try); `TestNewIsGreen` passes, `Exercised`
  taking the call.

`grist.NewSite` stays, tested in grist (declare_test, model_test,
gristtest's exercised_test), as the escape hatch until GR7; no example
uses it.

Where the design changed (docs/grist-instrumentation.md, corrected in
place):

- **A wrong request is caught at the caller, not by an invariant over
  the site.** A served record carries the bytes its caller sent, and a
  run grist records both ends of rejects at the caller's step; `Violates`
  claims a conforming run's violation, so library's `wrongReceipt` is
  `Rejects("mailerSend")`. The invariant `everyReceiptForABorrow` is
  kept: it states the site's guarantee for a tier where only the site is
  recorded (slice 3's `ServeCalls`). (Superseded by the review below:
  the site judges the request, and the invariant is gone.)
- **A site record before its commit is the site action's rejection**,
  not an invariant's: the generated `<c>Served` requires its trigger in
  the log. wallet-grist's test claims conformance and served posts; an
  invariant restating the guard was tried and dropped.
- **The default double is in grist** (`DefaultHandlers`), where the
  design had it in gristsim alone.

What slice 3 (the real tier over HTTP) must know:

- The real tiers attach in-process transports today (`TierConfig.Deps`:
  `dep.With(ctx, grist.Local(...))`, one `Local` per tier, created
  outside `Deps`, so its key clock is shared by every node of the tier,
  counters' two included). `TierConfig` serving `Handlers` behind
  `ServeCalls` replaces that; the site's records then come from another
  recorder (the site action judges its request since this slice's
  review).
- `DefaultHandlers` is the natural `ServeCalls` default (a test that
  gives no handlers), and its first-answer map is per instance: one per
  served dependency per run.
- library's binary (`cmd/library`) attaches no mailer: its receipts fail
  every try there (`grist.failed`), as before; counters' and
  wallet-grist's binaries serve theirs in process.

Cost (`go test -short ./...` per example, CPU s, user + sys, at load
4-13 on 4 cores): counters 415 before, 438 after (its line in
ci/test-times.txt, 640, unchanged: twice is 1280); wallet-grist 402 to
436; library 225 to 246. The new steps (every mail and post recorded at
both ends) are most of it.

**The slice's review** (2026-10-27), each probed by a counterexample:

- **A wrong request recorded at the site alone was admitted** (the
  slice 1 review's "left as built", found in a simulation, not only in
  slice 3). The caller's step is recorded for every outcome but a crash
  during the call, and that crash is an ordinary fault: the bank's
  answer lost and the node crashed while the payout waits leaves the
  payout's call pending, and an at-most-once effect never calls again,
  so the bank's served record is the payment's only record, and the
  site judged no request. counters' `wrongAmount` (paying one more)
  conformed in 4 of 4 runs of that script (`lostThenCrash`, at
  `payOnce`: the prologue's one payment); under crashes alone (nemesis
  at 0.05, `paying`) 29 of 60 runs did not reject, many of them a
  wrong payment served with its call pending. The generated
  `<c>Served` now requires the request the processor's rule makes for
  the trigger (and a trigger it does not skip), as the caller's action
  does, so a wrong request is named at the site where the dependency
  applied it (its record comes first) and at the caller where nothing
  did. `wrongAmount` claims all three: `All(Rejects("bankPayServed"))`
  without faults and under `lostThenCrash` (the second conforms with
  the site's check taken out), `All(Rejects("bankPay"))` with the request
  dropped (the caller's check, a third party's tier's only one); one
  seed each per push. `TestPayoutAmount`'s full mode claims the faithful
  app conforms under `lostThenCrash`, the bank's record alone.
- **`everyReceiptForABorrow` was dead weight, and now restates the
  guard.** In library (receipts at least once) a crash during the call
  is followed by a retry its caller records, so in every settled run the
  caller's rule caught a wrong receipt first, and the invariant could
  fail only in a run cut before the retry: a mutation of it to `true`
  went unnoticed by every test. With the site judging its request it is
  the generated `mailerSendServed`'s own guard, and is gone (a comment
  in the model says where the property lives). `wrongReceipt` is
  `All(Rejects("mailerSendServed"))`, at one seed per push (lending's
  every run borrows; it was `Some` at three).
- **The default double answered the zero response for any call.** No
  rule judges a response (a rule's request reads the log, and
  `OnSuccess` is fixed before the call), so the zero answer is judged by
  nothing but replays and the app's invariants; what it hides is the
  effect's own handling of an answer (a branch on a reference, a field
  passed to another call) exercised on a value no dependency gives.
  `DefaultHandlers` now panics, naming the call, its fields and the fix,
  for a call whose response has fields, and gristsim's run is the error
  (grist's `TestDefaultHandlers`, gristsim's `TestDefaultDouble`, both
  failing with the check taken out). Every example's defaulted call
  answers `struct{}`; counters' bank has its own double. Generating a
  value was not chosen: a drawn reference would be as unjudged as a zero
  one, and a dependency's answers (a reference per key, stable on
  replay) are its own.
- **counters' mailer, unkeyed, is faithful, not a weakening.** The
  simulated mailer it replaces appended every mail (the old interface's
  "deduplicating by key where it can" was never implemented), and claim
  3 (`TestWelcomeDuplicate`) is that a duplicated welcome is at least
  once's by right, judged by the test-only `welcomedOnce` over effect
  steps, unchanged. Before the port the mailer recorded nothing; now
  every send is a served record the model admits. A `Keyed` mailer would
  be another mailer (the duplicate a replay), not a stronger check.
- **The ported mutants test what their names say.** `noDedupe` and
  `ledgerNoDedupe` reject the generated admits in 6 of 6 runs each (full
  mode), and with the generated `Keyed` admits taken out (`true`) every
  run of both conforms. Every run of their scripts crashes after the
  call, so both are `All(...)` at one seed per push now (`Some` at three
  and two). `noOutbox` is caught by the at-least-once rule
  (`Rejects("post")`, 6 of 6), not by the ledger, as before the port.
- **The site's trigger guard rejects no faithful run** (probed, no
  change): two nodes, three clients transferring (two-event batches),
  commit faults, crashes and the ledger's faults, 24 of 24 runs conform
  with replays taken. A record's place is the head its poller read (at
  or past its trigger's batch, whatever batches commit later), or a
  later record's of the key: never before its trigger's commit.
- **Docs**: the skill's `grist.Local(grist.DefaultHandlers(Bank))`
  example would now panic (the bank answers a reference): it names the
  mailer. The skill, docs/grist-instrumentation.md docs/grist.md §3.4, docs/grist.md §3.5 and docs/grist.md §4, and
  the READMEs say where a wrong request is caught. Left, the core's: a
  crashed caller's pending call is labelled "absent (a defect)" in a
  report's Absent section (validate's `PendingCause`), though the call
  is pending by right; it fails nothing unless `Completes()` is
  claimed.

What slice 3 must know besides the list above: a `ServeCalls` site
judges each request it records against the rule, so a request no grist
caller sent for a trigger (another client of the service) is rejected
there, by right for a dependency only the App calls; a default for
`ServeCalls` refuses a call answering fields, as `DefaultHandlers` does;
and the payout's lost-answer crash is the shape to probe on HTTP too (a
5xx after the bank applied, the node killed), where the site's step file
is the payment's only record.

Cost (`go test -short ./...` per example, CPU s, user + sys, the review's
tree against main's, measured one after the other on 4 cores):
library 238 to 190 (`wrongReceipt` and `noDedupe` at one seed, `All`,
where they were `Some` at three); wallet-grist 439 to 424
(`ledgerNoDedupe` likewise); counters 439 to 458 (`wrongAmount`'s two
new explorations, one seed each at the prologue's one payment: the
price of the crash case and of keeping the caller's check exercised).
Nothing cheaper was found that would not drop a claim: the slice's
growth is the recorded steps of every mail and post, which every
exploration validates.

### grist §0.14: What building GR3's slice 3 changed (the real tier over HTTP)

docs/grist-instrumentation.md docs/grist.md §4's slice 3: a declared call made and
served over real HTTP, its site recorded by the service that applies it.
Built, each with a test that fails with it taken out:

- **`grist.OverHTTP(base, client)`** (grist/overhttp.go): `POST
  base/<call>`, the request's JSON, the key as `Idempotency-Key`, the
  cause as `Grist-Cause` (`grist.CauseHeader`:
  `<app>/<tenant>/<processor>/<trigger>/<head>`, each part
  path-escaped); nil client: one bounded by `callTimeout` (10 s).
  2xx the response, 422 `{"rejected": name}` the refusal, another 4xx
  `NotApplied`, a 5xx, a reset or a timeout `ErrOutcomeUnknown`; one
  attempt (`TestOverHTTPAnswers`, against a server that is not grist's).
  (Superseded by the review below: anything but a 2xx and a declared
  refusal is an unknown outcome, a 3xx not followed.)
- **`grist.ServeCalls(dep, handlers)`**, a dependency the team owns: each
  call by its handler, whose wrapper records `<c>Served` or
  `<c>Replayed` into the request context's recorder (a service wraps it
  in `qobshttp.Handler(h, rec)`: its own recorder and step file), in the
  caller's tenant's trace at the cause's 2·H+1, one site clock per
  `ServeCalls` whichever node calls (`TestServeCallsOrderPerKey`: two
  transports, one server, the delayed first try placed at the retry's
  place). A request without its cause or key, of another App (its
  records join this App's trace), or that does not decode is a 400,
  recorded nowhere (`OverHTTP`: `NotApplied`; the review below: unknown,
  and a processor not using the call or a key not its effect's refused
  too); an undeclared call a 404,
  another method a 405, a handler's error that is no declared refusal a
  500 recorded with it (`TestServeCallsRefuses`). nil handlers are the
  default double, which panics for a call answering fields, as
  `DefaultHandlers` does; handlers lacking a call panic too
  (`TestServeCallsDefault`). End to end, a node's effects over HTTP to a
  bank behind `ServeCalls`, both recorders' steps alike in key, request
  and place (`TestCallOverHTTP`).
- **`grist.Main`**: `grist.ServeDependency(dep, handlers)` serves a
  dependency behind `ServeCalls` on a loopback port beside the node,
  its records in the `-steps` file under a recorder of their own
  (`<app>-<pid>-<dep>`; since §0.15 `<app>/<node>/<incarnation>/<dep>`), the node calling it with `OverHTTP`; `serve
  -dependency NAME=URL` calls URL instead (`TestMainServesDependency`).
  counters' binary serves its bank so (`SERVE=1 ./run-demo.sh` retires a
  counter).
- **`gristtest.TierConfig.Serve`**: a dependency's handlers behind
  `ServeCalls` on a loopback server of the tier's (one per dependency for
  every node), recording into a step file of its own (`Steps` reads it
  with the nodes', `DependencySteps(dep)` alone), the nodes calling it
  with `OverHTTP`; every `DependencyLoseEvery`-th answer (default 5) lost
  after the call was applied, as a 502, `OnLostAnswer` running then
  (where a test kills a node), `Check` requiring some lost. `Local`
  serves handlers in process, one `grist.Local` for the tier, so its
  clock and its handlers' state (a default double's first answers) span
  every node (wallet-grist's and library's tiers use it; they built
  their `Local` outside `Deps` by hand). `Current()` is the node taking
  the traffic.
- **counters' real tier** (`TestRealHTTP`): a third phase of five
  counters created and retired, paid through the bank (`NewBank`, the
  team's own, now in deps.go and every simulation's double too) behind
  `ServeCalls`, the fifth answer lost: that payout is `grist.unknown`,
  each key paid once, the bank's file the only one recording payments.
  The mailer is a third party's over `OverHTTP` (a handler of its own,
  recording nothing). **`payTwice`** (the at-most-once payout calling
  `Do` again after an unknown answer, as a retrying SDK would) is
  `Rejects("bankPay")` from the steps file on that tier: the bank replays
  the retry, nothing is paid twice, the store's records are the faithful
  app's; the third phase is its contrast.
- **The lost answer with the node killed, over HTTP**
  (`TestRealLostThenKilled`, the slice 2 review's ask): one payout, the
  bank's answer lost as `OnLostAnswer` kills the node, a second node
  taking over: the payout's call pending, the bank's file the payment's
  only record, and the run conforms. `wrongAmount` in that shape is
  `Rejects("bankPayServed")`, the site's request judged against
  `payoutBankPay` from the bank's own file.
- **The real variant** (decision 4): `counters.real.binding.yaml`, the
  vals the real tier can judge marked `real`. Two new site claims,
  eventual vals over a call's steps and its served records:
  `everyPaymentAnswerServed` (every answer the payout got, unknown
  aside, is the bank's served record, field for field: on the real tier
  it checks the transport's decoding and the bank's step file), marked
  `default retiring real`, and `everyMailAnswerServed`, marked `default
  retiring` only: judged by the default binding, the real tier violates
  it (the third party's mailer records no site), which
  `TestRealLostThenKilled` claims.

Where the design changed (docs/grist-instrumentation.md, corrected in
place):

- **A killed tier node commits nothing after its kill.** The probe found
  it: the at-most-once outcome append outlives its node's context by
  design (a stopped node must not lose a payment's outcome), so after
  `Node.Kill` killed the sink the outcome still committed, and the next
  node's relay recorded an outcome whose effect step had died with the
  sink: `payoutOutcome` rejected, a run no real process makes (a killed
  process appends nothing). `Kill` now makes the node's store refuse
  every append it begins after the kill (`ErrUnavailable`); one begun
  before may commit, its node's earlier steps written first. Without it
  `TestRealLostThenKilled` rejects `payoutOutcome`.
- **An unknown outcome's error is recorded with its double quotes made
  single**, in its step and its `grist.unknown` event alike (no Quint
  string holds a double quote, and net/http's errors quote the URL:
  every validation of such a run failed rendering the step;
  `TestEffectAtMostOnce` now returns an error with quotes).
- **`ServeCalls` takes no recorder**: it records into the request
  context's, as every handler grist serves does, `qobshttp.Handler`
  giving it one. **A 4xx of no name** is `NotApplied` for the effect
  (grist.failed) while the caller's step records `unknown`, the only
  outcome `Do` records for an answer that is no declared refusal.
  (Superseded by the review below: both unknown.)
- **The lost answer is a 502** (what a proxy that lost the upstream's
  answer gives), counted per served dependency; the design said only
  "loses every fifth answer".
- `grist.Main` gained `ServeDependency` and `-dependency`, which the
  design named only as "serving a dependency's Handlers".

What slice 4 (lints for an app's own code) must know: `OverHTTP` is
grist's transport, attached where the node starts (`Dependencies`, a
tier's config), never called from an effect, so a lint against an
effect's raw `net/http` client must not flag `Call.Do`; a handler given
to `ServeCalls` or `Call.Handler` is the dependency's code, not an
effect's, and may use what it likes (counters' `NewBank` holds a
`sync.Mutex`: a real service is concurrent). The real-tier test files
(`real_test.go`) sleep and start servers with `//qenv:ok` reasons
already; they import no qenv.

What slice 5 (peer calls, actors named after the service) must know:
the site's actor is set in one place, the handler wrapper
(`callInfo.handler`, calls.go: actor the cause's tenant, process the
App's name), which `ServeCalls`, `Local` and gristsim share, so
`<app>/<tenant>` changes there; `Grist-Cause` already carries the App
first, and `ServeCalls` refuses another App's call (400) because its
records join one App's trace: a dependency serving several Apps needs
that revisited with the actors. The key a `ServeCalls` dedupes by is the
caller's (`Idempotency-Key`), so prefixing it with the App changes no
transport. A peer's site is its commit, not `ServeCalls`.

Cost (`go test -short ./...` in examples/counters, CPU s, user + sys,
load 6-13 on 4 cores, this tree and main's one after the other): 717
against 460 (749 with eight retirements in the real tier's phase, cut
to five, the fewest that reach the bank's lost fifth answer). Of it: `TestRealHTTP` grew by its third phase and its real
variant (about 30 s of wall time to 47), `TestRealLostThenKilled` and
`payTwice`'s real tier are new, and so is `wrongAmount`'s real probe
(each a real tier's few seconds of HTTP and SQLite, and its
validations); the two site claims cost every exploration of the default
and retiring bindings about 8% (`TestFaithful` and `TestPayoutTwoNodes`
together, measured alone: 65 against 62). ci/test-times.txt's line (640)
is unchanged: twice it is 1280.

**The slice's review** (2026-10-27), each probed by a counterexample:

- **A 4xx of no declared refusal said "not paid"** (the slice's own
  note). Over HTTP it was `NotApplied`: an at-most-once payout's outcome
  grist.failed, beside a caller step recorded `unknown`. Nothing in the
  model relates the two, so no check caught the contradiction, and a 4xx
  is no dependency's word that nothing happened (a third party's 409 for
  a key in use, a gateway's 429 after forwarding). Only a declared
  refusal is: `OverHTTP` now takes every other answer for an unknown
  outcome, and `Call.Do` turns any transport's `NotApplied` into one, so
  the outcome event always agrees with the step (grist's
  `TestCallAnswerOfNoRefusal`: a transport's own `NotApplied`,
  grist.failed with `Do`'s rule taken out; `TestOverHTTPAnswers` and
  `TestServeCallsRefuses`: every such answer unknown, `NotApplied` with
  `OverHTTP`'s mapping taken out). A 422 naming
  a refusal the call does not declare was already unknown, the step and
  the event alike; a 2xx whose body does not decode too.
- **A redirect was followed.** `OverHTTP` used the client's default
  policy: a 303 became a GET (`ServeCalls` answers 405, so
  grist.failed, of a POST a server may have applied, as
  POST-redirect-GET does), and a 307 re-sent the call, its body, key
  and `Grist-Cause`, to wherever `Location` pointed: a second send, to
  another host. It now uses a copy of the client that follows none, a
  3xx an unknown outcome naming the Location (`TestOverHTTPAnswers`, a
  caller's own client included).
- **OverHTTP's unknown outcomes read as a command's**: their text began
  with `ErrOutcomeUnknown`'s, "retry it with the same id", stored in
  every grist.unknown, advice an at-most-once payment must not take.
  They are a call's now, still `errors.Is(err, ErrOutcomeUnknown)`.
- **`ServeCalls` recorded what no grist caller sends.** It checked the
  cause's App, not its processor or key: a request naming a processor
  that does not use the call was recorded (the model rejects it, so a
  buggy or hostile client failed the caller's validation), and one for a
  real trigger under another key (`payer/t1/2` for trigger 1, another
  tenant's) was a second served record the `Keyed` rule admits (it
  compares keys only). It now refuses both, 400 and unrecorded:
  the processor must use the call, the key must be the trigger's effect
  key (`processor/tenant/trigger`, one function with the poller's).
  The App, tenant, trigger and head remain the caller's word, which no
  server can check: `ServeCalls` authenticates nobody, and says so (serve
  it where only the App's nodes reach it, as `ServeDependency`'s
  loopback port, or behind the service's own authentication). Header
  injection is not open: `Grist-Cause`'s text parts are path-escaped,
  and the key is the effect key, of checked tenant and processor names.
  The body is bounded (`defaultMaxBody`, both ways). It dedupes nothing
  itself: the handlers do, per call (`DefaultHandlers`, counters'
  `NewBank`), its site clock ordering each key's records.
- **`serve -dependency NAME=URL` took any URL**: one with no scheme
  (`bank=localhost:8080`) made every call fail, each payout
  grist.unknown. It is a usage error now (`TestMainServesDependency`).
- **A stopped node's poller spun** (found by profiling counters'
  -short, 88% of its Go CPU, CI's examples job at 1469 CPU s against
  its line of 640): `commitAt` retried a transient append failure
  without looking at its context, and once the node stopped its backoff
  returned at once, so a store failing alike (a killed tier node's,
  which refuses every append; one answering an ended context with a
  transient error) was retried until the process ended, the killed
  node's pollers taking cores from every parallel test. The loop now
  ends with its context (`TestPollerStopsWithItsNode`: 182044 appends
  in 200 ms after the stop without it, none with it); pushed alone,
  before the rest of the review.
- **The killed node's store is faithful, and the harness's**
  (probed, no change): a killed process begins no append, and one in
  flight at the kill may commit (the check precedes the append, which
  then runs to its end), its node's earlier steps already written
  (`PerStep`). The "effect committed, caller never told" window stays
  open: `TestRealLostThenKilled` is that window, the bank's payment made
  and the caller's answer lost with its node. `nodeStore` is gristtest's
  (`Node.Kill`), never grist's runtime, whose at-most-once outcome
  append outlives its context by design.
- **The real variant judges what the real tier can, and only that**
  (probed, no change): every `default` val but the mailer's site claim,
  and `everyRetirementHandled`; the `exactly` vals are test-only claims
  that a crash breaks by right. `everyMailAnswerServed`'s violation
  under the default binding on the real tier is a verdict on the tier
  (a third party's mailer records no site, whatever the code does), not
  on the app; it stays, said so, as the claim's only violation, the
  evidence that it is not vacuous. **`everyPaymentAnswerServed` had
  none**: `TestRealHTTP` now validates its run with the ok payment's
  receipt changed in the caller's step (a transport that decoded the
  bank's answer wrongly) and claims `Violates` it.
- **Double quotes** (probed, no change): `modelText` rewrites only the
  error text grist itself stores in a grist.unknown and its step (the
  model computes the event from the step, so both alike); no check reads
  the quote. itf refuses a string with one (`UnquotableError`, whose
  message advises this very replacement): Quint has no escapes, so
  there is nothing to escape to, and the one rule is itf's refusal,
  grist's text being the one place grist makes text of its own. An
  app's own fields (a counter's name, a bank's reference) holding one
  still fail validation with itf's error, by design.

What slice 4 (lints for an app's own code) must know besides the
slice's own note: `Call.Do`'s error is the effect's to return as it is
(an at-most-once effect's outcome is classified from it: only a
declared refusal is grist.failed now), so a lint might flag an effect
that wraps `Do`'s error in a `NotApplied` of its own (it would say
"not paid" of an unknown outcome; `Do` cannot see that from inside) or
retries `Do` after an unknown outcome in an at-most-once effect (the
payTwice mutant's shape: the model catches it only on a tier where the
unknown outcome happens). A `ServeCalls` reached from outside the App's
nodes is a deployment matter, not a lint's.

Cost (counters, `go test -short ./...`, CPU s, user + sys, on 4 cores
shared with other agents' tests): main with slice 3 1106 (load 17-30;
CI 1030 in run 418, 1469 in run 40596878521, against a line of 640);
the poller's stop alone (7bc208e) 768 (load 27); this review 760 (load
20). The spin was most of slice 3's growth. Per test, the review's
changes against 7bc208e, one after the other at load 5-9:
`TestRealHTTP` 57 to 81 (two retirements for five, and the new
forged-receipt validation of the whole run, the payment claim's one
violation, about 30), `TestRealLostThenKilled` 40 to 23 (the clients'
validation, `TestRealHTTP`'s claim, dropped), `payTwice` 22 to 18 (one
retirement), `wrongAmount` 55 to 43 (its real probe in full mode): 173
to 164. ci/test-times.txt's line is CI's run after the fix: 1038 (run 427), set to 1040.

### grist §0.15: What building GR10's slice 1 changed (identity, probes, the drain)

docs/grist-platform.md docs/grist.md §9's slice 1, the two bugs it found first. Built,
each with a test that fails with it taken out:

- **The node's identity** (`grist.Identity`, `NewIdentity`, named
  `IdentityFromEnv` until the review: it mints an incarnation per call,
  `WithIdentity`; grist/identity.go). The node's recorder was
  `<app>-<pid>`, and the pid is 1 in every container: two pods' merged
  step files numbered one recorder twice, Reconstruct's "two different
  records" and no verdict, and a restarted container collided with its
  earlier self. It is now `<app>/<node>/<incarnation>`: the node
  `GRIST_NODE`, else `POD_NAME`, else the host name; the incarnation 12
  random hex digits per start; the served dependencies'
  `<app>/<node>/<incarnation>/<dep>`. `-steps DIR` writes
  `DIR/<app>.<node>.<incarnation>.jsonl`, a file per start, which a pod
  must give: a restart truncates a `-steps FILE`, losing the earlier
  start's steps (serve warns in a pod: `TestMainStepsFileInPod`). Namespace,
  host (`NODE_NAME`) and zone (`GRIST_ZONE`) are read and printed, not
  yet on the recorder (G8's step location is the core change that puts
  them in reports). `TestMainIdentity` (two starts of one pod merged:
  no duplicate; without the incarnation, the duplicate error);
  counters' `TestRealTwoPods`, two processes of the app's binary on one
  SQLite file, their files and the clients' merged: no duplicate,
  `Conforms`, `Settles`, the clients linearized; its mutant
  `pidIdentity` (both pods `counters/1/1`, the pid's identity) gets the
  duplicate error and a report failing on it alone.
- **The drain is a schedule** (`grist.Drain`, `WithDrain`, `-grace`;
  grist/drain.go). At SIGTERM `/readyz` fails at once and the server
  keeps serving for `Delay` (`drainDelay`, 5 s, on an orchestrator,
  `KUBERNETES_SERVICE_HOST` set; none elsewhere), then Shutdown, Settle
  and Flush, 10 s each as before: 35 s on an orchestrator, more than the
  default grace of 30 s, so a pod is given 45 s (the maintainer's
  choice, docs/grist.md §10.2 of the platform note; slice 8's manifests write it).
  `-grace D` budgets the phases into D: the flush keeps its share first
  (at most half the grace), the rest shared by the delay, the shutdown
  and the settle in proportion, and each phase ends in time to leave
  the later ones theirs, its unused time passed on. The startup line
  says it: `counters: node counters-0, incarnation ...; drain needs a
  grace of 35s (...); -grace 20s leaves delay 2s, shutdown 4s, settle
  4s, flush 10s; GOMEMLIMIT ...`. A shutdown cut by its bound returns
  an error naming it (its clients retry under their keys).
  `TestMainGraceBudgetsTheDrain`: a client trickling a body across the
  signal, phases needing 7.5 s, `-grace 2s`: the file at the kill
  deadline holds the settle and the commits, serve ended; without
  `-grace` the drain is still in its shutdown at the kill, no settle.
- **The side port** (`-health-addr`, default `:8081` on an
  orchestrator, else off; grist/health.go): `/livez` fails only when a
  poller has worked on one trigger past `liveBound` (5 m: a deadlock or
  a stuck handler; the clock restarts at each trigger of a batch and
  after each backoff from a store error, so neither a batch of slow
  effects nor a commit retried through a database outage fails it:
  never the store: `TestMainLivezPerTrigger`, `TestPauseIsProgress`;
  as first built the bound was the whole batch's, and four 400 ms
  effects in one failed a 1 s bound); `/readyz` is the
  node started (the store answered: `App.Start` read its tenants) and
  not draining, never a dependency or processor lag; `/metrics`, in
  Prometheus' text format with the standard library, has
  `grist_ready`, `grist_draining`, `grist_api_requests_in_flight`,
  `grist_effects_in_flight`, `grist_relay_backlog`, per processor and
  tenant `grist_processor_lag` (head minus checkpoint) and
  `grist_processor_oldest_trigger_age_seconds`, and `grist_store_up`.
  qrole's u and v (docs/grist.md §3.7 of the platform note) are not yet among them.
  `TestMainHealth`: an effect hung past a shortened bound fails
  `/livez`, naming the processor, while `/readyz` stays ok; the gauges;
  after SIGTERM `/readyz` 503 while a query is still served in the
  delay.
- **Loopback in a pod is refused**: `-addr` on loopback (the default
  `127.0.0.1:8080` included) with `KUBERNETES_SERVICE_HOST` set is a
  usage error naming `-addr :8080` (`TestMainLoopbackInPod`).
- **`GOMEMLIMIT`**: unset, serve sets the runtime's limit to 90% of the
  cgroup's `memory.max` (`debug.SetMemoryLimit`), so the collector works
  before an OOM kill (no drain at all); `GOMAXPROCS` and `GOGC` are left
  alone (`TestMemoryLimit`).

What the platform note's acceptance got wrong: "a `-grace 20s` run
records its settle and flush where today's schedule is killed in the
flush" cannot be built with the defaults the maintainer chose. A settle
needs the relay caught up, so the flush after one is nearly instant,
and the settle comes as soon as the node quiets after the shutdown: the
unbudgeted drain ends at about the later of delay + shutdown (at most
15 s) and the node's quiet, so with a 20 s kill it is never killed
after a settle that a budget could have saved. What a budget saves at
20 s is the flush when the node never quiets (the unbudgeted settle's
10 s ending past the kill); the settle is saved only when delay and
shutdown alone outrun the grace, which the test shows with its phases
scaled down. Its example line ("-grace 30s leaves the settle 5 s")
assumed the settle shrinks first; the budget shares the shortfall.
counters' `cmd/counters` options moved into the package
(`counters.Serve`), so the mutant's overlay and the pods (the test
binary as the app's binary, `TestMain`) run what the binary runs.

Its review's fixes, each with a test that fails without it: `/livez`'s
bound is per trigger and a store backoff restarts it (above); a second
SIGINT or SIGTERM in the drain ends the process, where
`signal.NotifyContext` swallowed it until the drain's end
(`TestMainSecondSignal`); `/metrics` escapes label values as the text
format does (a backslash, a double quote, a newline), not with Go's
quoting (`TestMetricsLabelEscaping`: a tenant with a tab); `qenv.Signal`'s
`Notify` raced with itself outside a simulation (sqlstore's commits,
found by `-race` on `TestMainGraceBudgetsTheDrain`;
`TestSignalNotifyFromRealGoroutines`). And `/readyz` is ok only once
the API listens (a window of microseconds, no test). The platform
note's three errors are corrected there (docs/grist.md §3.2, docs/grist.md §3.3, docs/grist.md §9's slice 1).

### grist §0.16: What building GR10's slice 2 changed (termination and the router)

docs/grist-platform.md docs/grist.md §9's slice 2. Built, each with a test that fails
with it taken out:

- **Graceful termination in qenv** (qenv/terminate.go).
  `Sim.Terminations(qenv.Termination{Grace, Short, Budget}, procs...)`
  registers `nemesis.terminate.<proc>` (family terminate); the signal
  (`Process.Terminate`, a test's to send too) cancels every context
  `qenv.Terminating(ctx)` gave the process's tasks, the process drains
  and `Process.Exit`s, or is killed at its grace, and either way is
  restarted (a StatefulSet's pod back under its name). Option `short` of
  `terminate.<proc>.grace` is a reduced grace; `Budget` is a
  PodDisruptionBudget, counting the group's unavailable processes
  (terminating, or down).
  `TestTerminateDrainsOrKills`, `TestTerminateShortGrace`,
  `TestTerminationsBudget` (two terminating at once in some run without
  the budget, never with it).
- **The router** (qenv/router.go). `qenv.NewRouter(s, name, lag,
  procs...)`: `Pick` chooses an `Endpoint` of its view (a process's
  incarnation: a pod's address, which a restart replaces) by the
  schedule choice `route.<name>`, `ErrNoEndpoint` on an empty view. Each
  change of a backend's readiness (terminating, down, restarted) reaches
  the view at once or, under `route.<name>.lag`'s option `stale`, `lag`
  later, in the order the changes happened, every backend's in one
  sequence (the view is the readiness of some moment past, as an
  endpoint pipeline's is): a terminating backend still chosen, a
  restarted one not yet (`TestRouterLag`), and never one backend's
  change ahead of another's (`TestRouterKeepsOrderAcrossBackends`; a
  lag per change, each backend's apart, was the first version, and its
  view emptied in a rollout no real one empties). An endpoint that is `Gone`
  refuses the request sent to it.
- **`Service.Call` stops at a cancelled context** (context.Canceled, not
  a deadline, which passes in wall time), returning the context's error,
  the request maybe processed (`TestCallCancelled`). It did not: a
  simulated call ran to its answer whatever its caller's context, so a
  node cancelling its work at SIGTERM lost no outcome in a simulation
  where it does on the real tier. With it taken out, the
  `stopNodeAtSignal` mutant's runs all conform.
- **The drain shared** (grist/drain.go's `runDrain`). Main and
  gristsim's `Cluster` run one schedule: the server's delay and
  shutdown (each tier's: Main's `http.Server`, a simulated node's API),
  the node's settle (on `qenv.Now` and `qenv.Sleep`: real time under
  Main, virtual in a simulation), the node's stop and the relay's flush,
  each within its share of the grace. A simulation's drain records no
  settle (the run's `Sim.Settle` does). `grist.Terminating(ctx)` is
  `qenv.Terminating` in a simulation and, under Main, a context ended by
  the first signal (Main puts it in the node's and the requests'
  contexts): for work that may stop at once, never an effect's.
- **`gristsim.Cluster`** (`Config.Cluster`): every node (3 by default)
  serves the API as a `qenv.Service` of its own (`api.<node>`) behind
  the router `api` (`EndpointLag`); each node is terminable
  (`nemesis.terminate.<node>`, `Grace` default 45 s, `Disruptions` the
  budget) and drains with the app's `Drain`, then exits and restarts;
  `Rolling` restarts each node in turn under load from a point
  `gristsim.rollout` chooses, and `Pace` spaces each client's calls (1 s)
  so a run's calls span its drains. A try no server takes (the endpoint
  gone or none, its API shut down when the try was sent) is refused,
  retried, and counted on the call's step (`refused`, as grist/client
  counts a refused connection and a router's 502 or bare 503); a try
  sent before the shutdown is served (the shutdown waits for it, as an
  `http.Server`'s waits for its connections). Not with `Config.HTTP` yet
  (an error says so). `TestClusterRollingRestart` (refusals without the
  delay, none with it, none without the lag), `TestClusterDisruptions`.
- **`gristtest.NoClientErrors()`** and its negation `ClientErrors()`,
  over the clients' binding (a report with no client actor has no
  verdict for either, `qtest.NoVerdict`, so no claim holds by it and
  the failure names the fix: it first missed both, and
  `None(NoClientErrors())` held of a binding that judged no client),
  built on the core's new `qtest.Satisfies`: no client call refused a
  try, answered an error, or left pending.
- **counters**: `Drain` (5 s of serving, everywhere) and `NodeContext`
  in serve.go, used by the binary and the simulations; the test-only
  `noPayoutUnknown` (`counters.terminating.binding.yaml`); the mutants
  `stopNodeAtSignal` (`NodeContext` is `grist.Terminating`'s) and
  `noDrainDelay` (`Drain.Delay` none). `TestRollingRestart`: two nodes
  rolled under load, the router stale at half its changes, a slow bank;
  `Conforms`, `All(Settles)`, at the clients `All(Completes)` and
  `NoClientErrors`, `None(Violates("noPayoutUnknown"))`; the mutants
  `Some(Violates("noPayoutUnknown"))` (6 of 20 runs) and
  `Some(ClientErrors())`, and with the lag off `NoClientErrors` holds of
  the mutant too. `TestRealRollingRestart` (full mode, about 35 s): three
  pods of the binary on one SQLite file, `-steps DIR` and
  `-health-addr` each, behind the test's router polling `/readyz` every
  200 ms, three clients through it, each pod in turn sent SIGTERM and
  started again under its `POD_NAME`: no duplicate, `Conforms` and
  `Settles` at the store, `Conforms` linearized and `NoClientErrors` at
  the clients, and each drain's `/metrics` shows every processor's lag
  at 0 before its settle; `noDrainDelay`, three rounds of it, has tries
  refused in the router's lag (14 calls in 9 drains).

Two slice-1 defects found on the way, fixed:

- `grist_processor_lag` was the head minus the checkpoint, and a
  processor's checkpoint stays at the last trigger it handled while the
  head moves on with every other event, so a processor that had caught
  up never showed 0 while other events came: the real rolling restart
  saw no 0 in any drain. It is now that difference only while a trigger
  waits, else 0 (`TestProcessorLag`).
- A poller stopped by its context's end still counted as running, so a
  node whose work was cancelled was never idle, and its drain waited out
  the settle's bound (10 s), keeping every other termination (a budget
  of one) out of the run's clients' phase; the poller now leaves the
  count when its loop ends (`TestStoppedPollersAreIdle`).

What the platform note's acceptance got wrong:

- "`noDrainDelay`, which with lag fails `NoClientErrors`": no client
  call fails. grist/client and gristsim's clients retry a refused try
  under its key, so every answer is right and every call completes; what
  the router's lag costs is the tries, so `NoClientErrors` is defined
  over them (the step's `refused` count), and the mutant's claim is
  `Some(ClientErrors())`, qtest having no negation of an expectation.
  The review kept it so: a general `qtest.Not` has no one meaning for
  most expectations (`Not(Conforms())` is a rejection, a mismatch or a
  violation, each named already), and `Some(ClientErrors())` reads as
  the claim it is. The name `NoClientErrors` stays too: a refused try is
  an error the client saw and hid by retrying, and the doc says so.
- "a router without lag (ablation) makes the endpoint-lag test pass the
  mutant": only once a try sent before the shutdown is served. As first
  built a simulated server refused what reached it after its close, and
  2 of 6 runs without any lag refused tries in flight at the signal;
  a real `http.Server`'s shutdown serves the connections it has.
- "`stopNodeAtSignal` leaves an at-most-once outcome `grist.unknown` in
  some run": only once the simulated transport stops at a cancelled
  context (above), and only when a signal lands while a payment is in
  flight on that node: with three nodes 5 runs in 40, with two 12, so
  the claim's cluster has two nodes and a bank taking 900 ms.
- `gristsim.Cluster`'s slice-2 fields are `Grace`, `EndpointLag` and
  `Disruptions`, as listed, and three the note did not name: `Drain`
  (the app's schedule, which the binary passes `grist.WithDrain`),
  `Rolling` and `Pace`. A simulated node is never killed at its grace:
  its drain is budgeted into it and each phase ends in time; the kill is
  qenv's, tested there. Option `short` is qenv's, not the Cluster's.
  "Down by them" in the budget is not moot: between a drained process's
  exit and its replacement's start, and while a crash nothing restarted
  keeps one down, it is unavailable, and the budget counts it, as a PDB
  counts an unhealthy pod (`TestTerminationsBudgetCountsDown`; the first
  version counted only those terminating and up).
- A rolling restart that terminates the next node as soon as the last
  is back up emptied a two-node router's view when the next one's
  removal reached it before the last one's addition: 3 runs in 10
  refused 26 tries of a call, the app blameless. `Rolling` first waited
  for the restarted node in the view, which no rollout does
  (`minReadySeconds` counts from the pod's readiness, not from its
  endpoints'). The review found the cause in the router: its lag let one
  backend's change overtake another's, which a load balancer acting on
  the latest state cannot. The view now takes changes in order, and
  `Rolling` waits only for the node to be back, as a StatefulSet does.

### grist §0.17: What building GR10's slice 6 changed (sizing)

docs/grist-platform.md §9's slice 6 and §8.4's corrections. Built, each
with a test that fails with it taken out:

- **Sizing against a burst** (qmeta/size.go). `RecoveryAfter(p, Burst,
  healthy, within)`: the chain at the burst's rate from the empty
  system for its duration (`Chain.Distribution`, uniformization
  forward, which `Mean` now shares), then each end state's
  `RecoveryWithin` at the base rate, weighed. `Size` doubles then
  bisects the workers, `LimitFor` bisects the rate to 1%, `TimeoutFor`
  bisects a ladder of `floor × 2^(k/2)`; `TestSizeAgainstBruteForce`,
  `TestLimitForAgainstBruteForce`, `TestTimeoutForAgainstBruteForce`
  compare each with every value on random small chains (and check the
  monotonicity the bisection rests on); with the bisections taken out
  (the first rung, the doubling's count) they fail. The floor is the
  measured p99.9 plus Erlang C's p99.9 wait (`TestErlangFloor`: the
  M/M/1 formula at ρ 0.5).
- **Units** (`Params.Unit`). Every chain state is a unit of g requests:
  λ, capacity, queue and orbit divided, ceil(W/g) workers sharing the
  capacity; `Loss` compares in requests, `Rules` multiply their
  thresholds back. `TestUnitsKeepTheDrift` (the coarse drift times g is
  the fine one), `TestRulesInUnits`. Without it counters' tenant chain,
  82/s behind 2 s, was 500 × 1000 states per setting.
- **Retry layers** (`qload.Layered`): a proxy's tries at once, the
  client asked at every G-th; `TestLayeredMultipliesAttempts`. grist's
  client decides through one `retry` method that `Do` and `Policy`
  share, so the simulation runs the library's decision, not a copy.
- **grist/gristload**: `Measure` pairs every invocation record with its
  answer (client calls by block, declared calls at their caller),
  fits a command's duration against its commit's head, and finds the
  busiest tenant, the most calls in flight and each processor's
  triggers per call (`TestMeasure` on a synthetic recording with known
  means and growth). `Report` ranks the Gateway's tries on commands at
  the busiest tenant (one commit at a time) and on queries at the API,
  confirms the recommended setting, the one in force and the next
  failing one over the Load's seeds, bisects the smallest failing burst
  as the margin, then the timeout, the replicas (a zone lost), the
  tenant's limit, each processor's lag under its dependency 10x slower
  (fluid arithmetic, at every replica count alike), the pool (Little),
  the grace (the drain's sum: 45 s, decision 2's), CPU and memory from
  given measurements, and a command's growth. A hop whose burst stays
  under half its capacity is not solved (no queue to tip) and says so.
  `TestReport`, `TestRun` (the command line, its evidence and its usage
  error); `TestMainSizingRunsItsBinary` (grist.Main runs
  `<app>-sizing` with its arguments and exit status, and without one
  names `gristload.Main`).
- **counters**: `cmd/counters-sizing`, `testdata/sizing/steps.jsonl`
  (400 calls on the real tier, one at a time: t1's commands 12.2 ms
  mean, μ 82/s), `TestSizing` (the chain alone: `postRetries3` 3.0e-06
  against 1.000; with the multiplication ablated the mutant is
  recommended), `TestSizingAcceptance` (6 of 6 and 0 of 6),
  `TestSizingStaysOutOfTheBinary`; `SIZING=1 ./run-demo.sh`.
  retrystorm's `TestSizedFromTheChain`: the limits rank budget >
  immediate ≥ backoff, retrying at once needs two servers where the
  budget needs one, and the simulation ranks the ends alike at 9/s; the
  chain's limit for immediate retries (7.2/s) is below the rate the
  simulation still recovers at, so the claim is the order.

Left: the HPA rows and `qload.Autoscaler` (slice 7), the lease row
(slice 11), reading CPU and heap from a profile (an input today),
gristsim prologues for growth at 10k and 100k events, and manifests
taking sizing.yaml (`-sizing`, slice 8).

## Why AGENTS.md's gotchas say what they say

The stories behind some of AGENTS.md's gotchas, moved here so the file
every agent loads keeps only the rules. One bullet per gotcha, starting
with its subject.

- **actionmap stops at functions outside the loaded packages:** going
  through such a library made cancel "reach" PlaceOrder.
- **`qenv.Store`'s commit waits for every key at once:** waiting key by
  key let it write under another's lock.
- **`qenv.Wait` and `Signal.Wait` outside a simulation:** they panicked
  once.
- **`Sim.Run` returns `ErrStuck`:** a stalled run once looked quiet.
- **A Script's faults are in a partial order:** it was a strict order,
  where a fault of a phase that had passed (checkout's API faults happen
  only while orders are placed) blocked every later one.
- **A double takes its process at construction:** a field set later was
  forgotten and silently kept partitions out.
- **A prefixed fault kind panics with the fix:** it once matched nothing,
  silently.
- **`qhegel.Choices` draws 512-byte chunks:** per-choice draws made a
  shrinking test 20x slower.
- **Explore and Experiment runs are strict:** a step from a goroutine
  started with `go` once panicked, on a goroutine nothing could recover,
  killing the test binary.
- **qenv finds the task of a goroutine when a context carries none:**
  without that, a woven background goroutine's simulated latency slept in
  real time.
- **Libraries that read the real clock:** untranslated, crossdb's grace
  period never expired and blocked every deletion.
- **A `qenv.Signal` waiter is tagged with the task's wait generation:**
  a timed-out wait woken by a later `Notify` once shortened the next
  sleep.
