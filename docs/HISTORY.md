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
- **W8** moved to ROADMAP.md, extended after the fourth review.

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
