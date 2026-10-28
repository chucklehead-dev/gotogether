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

docs/grist-platform.md §9's slice 1, docs/grist.md §0.15. The node
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
