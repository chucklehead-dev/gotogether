# AGENTS.md: working on gotogether

Guidance for coding agents (and humans) changing this repository. If you
are *using* the toolkit in another project, the skills in `skills/` are
for you (`gotogether skills install`).

## What this is

A Go toolkit for checking Go code against Quint models in four ways, and
its load behaviour in a fifth:
- **MBT** (`connect`): Quint traces drive the code.
- **Hegel integration** (`qhegel`): Hegel drives and the model
  judges, or the model drives and Hegel varies.
- **Trace validation** (`qobs`, `qtrace`, `otelio`, `convert`, `binding`,
  `validate`, `qtest`): recorded runs are checked by the model.
- **Simulated environments** (`qenv`, `System.Explore`): the system runs
  deterministically against fault-injecting doubles, every choice made by
  Hegel or a seed, every run checked by the model.
- **Metastability** (`qload`, `qmeta`): whether a system recovers after a
  load spike or capacity dip. A CTMC predicts it, a load simulation on qenv
  confirms it, and Hegel shrinks the smallest trigger.

The design record is `docs/DESIGN.md`. Read §9 before changing `connect`
or `qhegel`, §10 before changing `qenv`, and §11 before changing
`qload` or `qmeta`.

## Layout

```
connect/      MBT runtime (quint-connect parity); testdata/bank.qnt
qhegel/       separate module: Hegel integration; testdata/bank.yaml
internal/hegelprops/  unpublished module (replace => ../..): the core's Hegel-checked properties, on the current core
itf/          ITF decode/encode, Go <-> Quint values (Decode/Encode), Quint literal parser
qobs/ qtrace/ otelio/ convert/ input/  recording, schema/reconstruction, OTLP, span conversion, reading any trace input
binding/ validate/ qtest/              binding YAML, quint-test-based validator, test helpers (Expect, Explore)
qenv/         deterministic simulation: scheduler, virtual time, processes; Store, Queue, Service doubles;
              internal/choice (choosers, byte rule, rates) and internal/profile (virtual-time pprof), qenv's API
qenv/weave/   Orchestrion aspects: sync locks, wait groups and //qenv:go goroutines simulated inside qenv, real outside
qenv/fault/   the //quint:fault aspect: fault points (qenv.FaultPoint) with the function's own errors, declared for coverage
qgosim/       separate module: qenv and whole programs in gosim (on the fork, module github.com/chucklehead-dev/gosim)
qweave/       separate module: weaves Orchestrion aspects with types into a go -overlay (cmd/qweave); also gosim's
qload/ qmeta/ load simulation (open/closed-loop clients, servers, triggers, the recovery property); CTMC, alert rules (qmeta imports nothing of the module)
qcover/ qrun/ code coverage by model step (woven by qweave -cover); a run's state found from its context or goroutine
qlint/        simulation and weaving lints (gotogether lint -sim, -weave); per file for qanalysis
qanalysis/    separate module (x/tools): analyzers (cmd/quintvet, golangci plugin), static action map (cmd/actionmap)
qmut/         mutant schemata (woven by qweave -mutate): one build, a mutant per run; qtest's System.Mutate explores each
qhistory/     a real database's isolation level from a list-append workload's history (Elle's; Adya's anomalies)
qsweep/       covering arrays (pairwise and t-way rows with constraints, sequence covering); System.Sweep runs them
qrole/        load roles (//quint:serve, //quint:operation, //quint:call; aspects in orchestrion.yml): u and v as gauges
cmd/gotogether/  CLI
skills/       agent skills, the source of truth (embedded by package skills;
              symlinked into .agents/skills and .claude/skills, checked by skills_test.go)
examples/*            each example is a module of its own (unpublished, replace => ../..); the root module is the toolkit
examples/sktrace      unpublished library module: SimKube traces (msgpack v3, own decoder), production workloads
                      as load shapes and replayed writes; fleetrollout and retrystorm use it
examples/glomers      Gossip Glomers on a Maelstrom runtime + simulator (maelstrom/); per-challenge model, impl, binding, mutants;
                      cmd/* run under real Maelstrom (maelstrom.sh)
examples/kvstore      app: HTTP KV service with CAS (kvserver, kvload); concurrency, span conversion
examples/jobqueue     app: at-least-once queue with leases and restarts; design-level verdicts
examples/grpcledger   separate module: gRPC ledger; simgrpc (any generated gRPC service in qenv: ServiceRegistrar + ClientConnInterface),
                      real tier over bufconn with otelgrpc spans converted to steps (ledger.convert.yaml); cmd/ledger-real
examples/streams      separate module: outbox publisher + consumers on Kafka (franz-go, kfake), Kinesis (AWS SDK,
                      kinesisfake) and a simulated log (simlog: partitions, idempotent producer, group, fencing); one binding
examples/hello        app: the smallest simulated system (a counter, a lossy network, a retrying client); start here
examples/wallet       app: transfers on a Store with request-id dedupe, an outbox, a ledger; Exhaust, isolation levels and qhistory,
                      ResolvePending, Linearize and LinearizeByKey, an eventual val, GOTOGETHER_ALL_FAULTS
examples/quorumkv     app: a quorum-replicated store (ABD, hinted handoff); liveness (SettleCore), pending calls, LinearizeByKey, PCT
examples/raftkv       app: etcd's raft library on qenv (WAL on a Store, election entropy from the run); two models (clients linearized
                      whole, leadership with within), pending calls, lease reads under drift (Exhaust), votes, rolling upgrades
examples/lease        app: a lease lock and the store it guards under clock faults (Sim.Clocks); fencing vs clocks, wall vs monotonic
examples/twophase     app: two-phase commit on qenv against a Choreo model (vendored choreo.qnt); named transitions, connect and Explore
examples/checkout     app: checkout saga on qenv (isolation levels, outbox, idempotency, crashes); cmd/checkout-sim; under load (load.go)
examples/fleetrollout separate module: multicluster-runtime controller, simulated (kubesim) and on kwok (kwok-demo.sh;
                      TRACE= times pods by a SimKube trace through kwok Stages, cmd/kwok-stages)
examples/crossdb      separate module: Crossplane provider (crossplane-runtime) for a dbaas, simulated and on kind with Crossplane (kind/)
examples/retrystorm   library: retry policies (backoff, budget); budget checked by a model, policies by qmeta/qload/FindTrigger
examples/bank         library example; every workflow on one small system
examples/edgepublish  separate module (Orchestrion); the spike's real-world case, model vendored in model/;
                      sim/: the publisher, unchanged, in qenv by weaving (qweave go test ./sim/, as CI does)
examples/gosim        separate module; on gosim via qgosim: broadcast/ (Glomers node over TCP), checkout/ (the saga on
                      qenv's native runtime); simulated tests //go:build sim, host tests make the choices (DESIGN §14, §15.1)
docs/         DESIGN.md (why), ROADMAP.md (what is open), HISTORY.md (what is done, and the reviews), instrumentation.md
              agents/: commands by module, gotchas by area
ci/           install-quint.sh (quint + its Rust evaluator, avoiding GitHub API rate limits)
```

## Requirements and commands

- The published modules (the core, qhegel, qweave, qanalysis, qgosim)
  have no `replace`: `go install …@version` refuses one, and a
  dependency's is ignored. They require each other at pushed commits
  (pseudo-versions, until tags). A change to the core that another
  published module needs: push it, then `go get
  github.com/chucklehead-dev/gotogether@<commit>` in that module (GOWORK=off)
  and push again; until then CI tests the module against the older core.
  Examples are not published and keep `replace => ../..`.
  Tags, when the maintainer says: gosim (its own repository) first, then
  the core, qweave, qhegel, qanalysis and qgosim, each module's tag
  prefixed with its directory (`qhegel/v0.1.0`), and each module's
  requirements moved from pseudo-versions to the tags.
- Each published module's exported API is recorded in api/<module>.txt
  (api/core.txt, api/qhegel.txt, ...): every exported function, type,
  field, method, interface method, constant and variable, as declared,
  read with go/ast by the core's internal/apisurface (an alias of an
  internal type, qenv.Rates, carries that type's fields and methods). A
  deliberate change to any of them regenerates it (`GOTOGETHER_UPDATE_API=1
  go test ./internal/apisurface/`, from the root) and adds a CHANGELOG
  line.
- The repository is a Go workspace (`go.work`, checked in): every module
  sees the others' code as it is, without a release. CI sets `GOWORK=off`
  and tests each module on its own go.mod, as a user would build it.
- Go ≥ 1.26.1 (`go.mod`; hegel-go needs it). quint ≥ 0.32 on PATH
  (`npm i -g @informalsystems/quint`, then `QUINT_VERSION=0.33.0
  ci/install-quint.sh` for the Rust evaluator). Tests that need quint
  skip without it.
- `go vet ./... && gofmt -l .`: both must be clean.
- `go test -short ./...` takes a few minutes (validate's and qtest's
  quint processes most of it). `go test ./...` adds the
  shrinking-against-the-model tests, about ten on CI; each core package
  over ten seconds has its time in ci/test-times.txt, and CI fails one
  twice as slow. Both cover the root
  module only: every example is a module, tested in its directory (`cd
  examples/hello && go test ./...`; CI loops over them).
- Each module's own commands (edgepublish woven, gosim, qanalysis,
  grpcledger, streams, fleetrollout, crossdb), quint's backends and the
  `GOTOGETHER_*` variables (keep, run one run alone, evidence, corpus,
  Hegel seeds, verbose): docs/agents/commands.md.

## Conventions

- The spec is ground truth. A failing model test means fixing the code
  or, deliberately and visibly, the model. Never loosen a check to get
  green.
- Error messages say what to do next: name the step, the action and its
  picks, the field, and the fix (see `connect`'s "anonymous action" and
  "no mbt::actionTaken" errors). Tests assert on these messages, so keep
  them stable.
- Keep the core free of heavy dependencies: OTel and yaml.v3 only. Hegel
  (and purego, which it loads) is qhegel's, a module of its own, so a
  program that records steps does not depend on it; a core test that
  needs Hegel lives in internal/hegelprops, a module of its own that
  replaces the core with the checkout. Orchestrion stays in examples.
- Any `//quint:action` expression may only use parameters, the receiver
  and named results. Methods called only from annotations (like
  `Publisher.actor`) look unused to linters: mark them, don't delete them.
- Comments explain why, at the density of the surrounding code. Doc
  comments on every exported identifier.
- New user-facing behaviour gets:
  - a test (against `connect/testdata/bank.qnt` or
    `examples/bank/model/bank.qnt`, or a model in `testdata/`);
  - a line in README.md if it is a new entry point;
  - an update to the relevant skill in `skills/` if agents should use it.
  `skills/skills_test.go` checks the skills' front matter.
- quint invocations go through argv (never a shell). Pass absolute spec
  paths and set `cmd.Dir` to the spec's directory.
- Timing-sensitive or slow tests (shrinking against the model) skip under
  `testing.Short()`.
- A search or a heuristic (Linearize, ResolvePending's placements,
  Exhaust's schedules, the isolation classifier, Cover) is checked
  against brute force on inputs small enough to enumerate (a
  `...AgainstBruteForce` test), and its tests fail with it turned off
  (ablate it before committing: a test that passes without the feature
  proves nothing of it). The nightly mutates the core's packages
  (`ci/mutate-core.sh <pkg>`, `JOBS=n`); a survivor is a test to write,
  or an equivalent mutant the matrix's `max` names.
- A skill's mechanics (how to weave, gosim, sweeps, coverage) go in
  `skills/<name>/references/*.md`, linked from its SKILL.md with when to
  read each; verdicts and expectations are in
  `gotogether-workflow/references/verdicts.md`. `skills_test.go`
  checks every link resolves, and fails on removed identifiers and old
  names in the skills, README.md, AGENTS.md and the examples' READMEs
  (`TestSkillsAvoidDeprecated` lists them): when removing an API, add it
  there. Every fenced go block of the docs (HISTORY and ROADMAP aside),
  and every Go block of a doc comment, parses, and names only what the
  toolkit's packages export (`internal/docsnippets`): write elided code
  as `/* ... */`.
- Skills live in `skills/<name>/SKILL.md`, never directly in `.agents/` or
  `.claude/`, which only hold symlinks to them. Add a symlink in both when
  adding a skill (`skills_test.go` checks it). A skill's front-matter
  `name` equals its directory.
- An example app has a model in `model/`, a binding, at least one
  mutant, a `run-demo.sh`, tests showing which check catches which
  mutant, and a README with the table of tests. Its row goes in the
  README's example table. State expected verdicts with `.Expect(t,
  claims...)` (`Rejects`, `Mismatch`, `Violates`; over runs, `Some`,
  `All`, `None`)
  and expected Hegel failures with `qhegel.FindViolation(...).Expect`,
  not by hand: no string matching on summaries, no walks of reports.
  Bindings give templates only where a parameter is not the same-named
  step field.

## Gotchas

The gotchas of one area are in docs/agents/; read its page before
working there:
- qload, qmeta, qrole, a load test: docs/agents/metastability.md.
- qgosim, qweave, qenv/weave, the native runtime, coverage, mutants:
  docs/agents/gosim-weaving.md.
- qanalysis, the binding lint: docs/agents/analysis.md.
- fleetrollout, crossdb: docs/agents/kube.md.

- `quint run --mbt` records every `nondet` evaluated in a step, not only
  the action's own: picks of other actions appear as `Some(..)`.
- `quint test` ITF has no `mbt::` variables.
- Generated replay modules import the model by relative path from their
  own directory. Moving a generated `.qnt` breaks its import.
- A model with no constants must be imported as `import M.*`, not `M()`
  (`binding.RenderModule` handles this).
- Hegel's `TestCase.Errorf` aborts the test case (unlike `testing.T`).
  `hegel.Test` cannot express "expected to fail": use `hegel.Run`.
- Choreo models (examples/twophase): Choreo's `step` picks a node and a
  transition, which no recorded step or `--mbt` trace can name. Give each
  transition a named action of its node (`choreo::step_with(node, f, ...)`)
  and choose among them in `step`. `choreo::s` is not visible outside the
  model's module: bindings and runs read state through a def
  (`stage(n)`). connect's `StatePath: [s, system]` reaches the nodes (the
  `m::choreo::` qualifiers are stripped).
- A step for a refusal recorded with `Txn.OnCommit` needs the transaction
  to commit: return nil from it and the error after `qenv.Retry`
  (checkout's `PlaceOrder`). An error rolls back and drops the hook.
- `qenv.Store` detects lock-wait cycles: the transaction whose
  `GetForUpdate` or `Commit` would close one aborts with `ErrDeadlock`
  (an ErrConflict). A commit waits until every key it writes is unlocked
  at once, and the check follows each waiter's keys as they are now, not
  a key it saw. `qenv.Service` lets a crash fall between the handler's
  return and the response's delivery (the effect committed, the caller
  never told) unless `NoCrashWindow` is set; the window is a scheduling
  point, so choices recorded before it was the default do not replay.
  A request may also arrive late (`rpc.<name>.delay`: just after its
  caller's timeout, a timeout after it, after a retry that backed off,
  or slow but in time; in true time), which breaks a lock without
  fencing whatever the clocks do: a test of other faults turns it off by
  its kind (lease's `punctual`).
- An expectation naming an action the model lacks, or an invariant the
  runs did not check, fails the test (a misspelled `None(Violates(x))`
  would otherwise hold for nothing). Verdicts are cached per instance
  (binding, constants, invariants), not per steps alone.
- A binding template's `.seq` and `.action` are the step's own unless the
  step has an argument of that name, which wins; the step's are always
  `.step.seq`, `.step.action`. `.process`, `.outcome`, `.obs`, `.step` and
  `.args` are always the step's: every argument is also `.args.X`, and a
  parameter of a reserved name defaults to it (`{{.args.process}}`).
- A step's observed values may be negative (an overdraft's balance): never
  use -1 as "not observed". Pass the observation or nothing.
- `quint` reserves `to` (and other built-in names): a parameter named `to`
  fails to parse (QNT101). grpcledger's model says `src`/`dest`.
- A crashable process whose restart redoes its whole job (a publisher that
  republishes everything) livelocks a run under per-step crash rates:
  every restart adds work at the same virtual instant. Give it durable
  progress (an outbox it clears on acknowledgement), as a real one has.
- Outside a simulation `qenv.Wait` polls its predicate in real time and
  `Signal.Wait` waits on a channel `Notify` closes: a seam's code runs
  unchanged with a real client, whose goroutines notify. The predicate
  must then be safe to call concurrently.
- `Sim.Run` returns `ErrStuck` (naming the tasks) when nothing can run, no
  deadline is set and a task still waits, so a stalled run is not
  mistaken for a quiet one. A task that may wait forever (a server's
  loop, a sender waiting for records) calls `qenv.Daemon(ctx)`; a
  double's receive uses `qenv.WaitIdle`. The native runtime runs in real
  time and cannot tell. `Settle` returns a `*Unsettled` (`ErrUnsettled`)
  when its time runs out with such a task at work or waiting, polling
  loops included: mark them Daemon. qtest judges it, not fails it
  (`Run.Unsettled`, a report not OK, `qtest.Settles()`).
  A settle takes runnable tasks in turn (fair), not choice 0;
  `SettleCore` goes on faulting outside its core (a double's fault
  kinds carry its process, a nemesis the processes it touches).
  A settle is a record in the trace (`Sim.Settle`, `qobs.Settled`): a
  binding's `eventually` vals are judged in the last state of a trace
  whose recorder recorded nothing after one, nor any other recorder later
  in time (`Trace.Settled`), by every
  check, never otherwise (a run cut short has work pending by right).
  `Run(d)` ending at its horizon judges nothing; only Settle says.
  Faults stay off after a settle (a probe runs on the settled system)
  until `Sim.ResumeFaults`.
- A step recorded inside a simulation (by a task, or the goroutine that
  created it) carries the simulation's true virtual time (`hooks.Now`,
  set by qenv), never a process's skewed clock nor the wall clock's, so a
  simulated client history has intervals and explorations can run
  Linearize.
- `qobs.Invoke` writes an invocation record (a step with `Invocation`,
  its own seq) and `Call.Done` the step, whose `Call` names it:
  `qtrace.Reconstruct` drops the records, keeping those never completed
  as `Trace.Pending`, so every reader must go through it. Pending is no
  step and not in OK (a crashed caller's call is pending by right):
  `qtest.Completes()` judges it. A completion's `Invoked` is its
  record's time, and Linearize orders one recorder's steps by seq (a step
  recorded before another's invocation record precedes it): a
  simulation's calls take no virtual time unless something sleeps, and
  times with `RealTimeSlack` once let a client's read miss its own write.
- `qrun.Start(ctx)` makes ctx the goroutine's run without a simulation
  (`qtest.Record`, `qhegel.Case.Install`), and `qenv.New` inherits
  it. Goroutines the run starts are not in it: their steps fail Record
  (the run is strict) unless it is given `qtest.Global()` (in
  qhegel's `Config.Opts` too), which installs the global recorder for
  them (`qobs.InstallGlobal`). A second install while one is in place fails,
  so such tests must not run in parallel; a restore puts back the
  previous recorder only if its own is still installed.
- A model that learns a log from its reads checks Kafka, Kinesis and a
  simulated log with one binding (examples/streams): the consumer is the
  one place every tier can record.
- Long replays are split into `.then` segments of 20 (`binding.segmentSteps`):
  Quint's Rust evaluator rejects chains nested much past 50. A disabled
  last step is reported by quint as QNT511 at the run's own line, and a
  segment ending in one at the `.then(segK)` line. The validator maps both,
  and `validate/segments_test.go` covers them.
- A failed quint test with no error position is a tool failure (e.g. the
  evaluator crashed). The validator returns an error, never a verdict.
- Distributed examples order steps across nodes with a Lamport clock
  (`order.lamport`). Domain-clock steps with equal values are concurrent,
  and every one of them precedes every step with a larger value
  (`qtrace` links whole groups, not neighbours). A service's operations
  are recorded at the service, where they take effect.
- qenv runs are deterministic only if all nondeterminism goes through the
  Chooser: code under test must use `qenv.Go`, `qenv.Now`, `qenv.Sleep` and
  the doubles, and iterate maps in sorted order where the order matters.
  An exploration runs its first seed twice and fails on the first
  choice or step that differs (identities compared up to renaming: a
  process's default name is its recorder's random id).
  `qenv.Replay` stops at the first divergence and reports it (`Err`, the
  run's error in an exploration). `EncodeChoices` writes the byte
  rule's version (`qenv.BytesVersion`): bump it when the rule changes,
  and old corpus entries fail with the fix instead of replaying another
  run.
- A fault point's options are nominal, then its errors in the order
  named, then the crash (`qenv.FaultPoint`, which declares them as its
  kind: the errors' FaultName or text, and `crash`, nominal outside a
  process). A Script's or a trace's option numbers depend on that order:
  keep it. One kind has one option list: two sites of a kind with other
  errors panic in `qenv.DeclareFaults`; give them their own kinds. A scripted crash is a
  nemesis by name (`nemesis.crash.<process>`): the scheduler asks a
  chooser with `ChooseNemesis` instead of choosing among enabled
  nemeses by index, which no trace can name.
- A run's state (its recorder, role registry, coverage counts) is found
  through `qrun`: the context, else the goroutine's run (qenv's
  resolver). A new kind of per-run state is a `qrun.Key`, never a
  package-level default that runs in parallel would share: every
  package-level var of the core says in its comment whether it is
  read-only or process-wide (`internal/pkgstate` fails otherwise, and
  on a write to a read-only one outside init in its package).
- A Script's faults are in a partial order: each kind's in order, other
  kinds independent unless a fault `Follows` them (qtest.FaultScript
  chains a trace's faults; an ordered pair says b follows a).
- A chooser that wraps another implements `Unwrap() qenv.Chooser`
  (`qenv.Wrapper`): `New`, `ContextOf`, `EntropyOf` and nemeses by name
  look through the chain (`qenv.As[T]`). A wrapper without it hides the
  run's context and entropy.
- A double's fault kinds carry its instance's name (`rpc.<name>.request`,
  `queue.<name>.ack`, `store.<name>.commit`, `disk.<name>.crash`), and a
  double that a partition reaches takes its process at construction
  (`NewService`, `NewQueue`, `NewDisk`), not a field set later. A name
  that covers nothing declared (the run's `hooks.FaultNames`: what its
  simulations and doubles declared and chose, and what was declared
  outside any run, a woven fault point's at init) matches nothing: an
  exploration fails on such a Rates key, a sweep on such a FaultKind,
  Exercised on such an exception. A name only another test's runs
  declared is unknown, whatever ran first. Two doubles of one kind and name in a
  simulation would share their fault kinds: the second panics (a
  Service and a Queue may share a name).
- A double declares its fault kinds by name (`qenv.DeclareFaults("kube.request",
  "unavailable", "lostResponse")`) and chooses with `Sim.FaultOf`, which
  returns the option's name. A call that cannot fail in some options takes
  a prefix (`k.Only("unavailable")`), so an option's number means one
  thing at every call. Coverage and scripts name options from the
  declaration.
- Users name fault kinds as reports print them, without the chooser's
  `fault.` prefix: Rates keys (`"nemesis"`, `"rpc.payments"`), `Without`,
  `Swarm`, `Reaches`, Script kinds. A prefixed one panics with the fix; a
  Script's is reported after the run by `Script.Misnamed`, with every
  other fault it cannot make (a Script matches kinds exactly, and nemeses
  by their full name), and fails the exploration. The prefix stays in the
  Chooser protocol (`Choose("fault.<kind>", n)`, a recorded
  `Choice.Kind`).
- Option 0 of every qenv choice is nominal (no fault, canonical order).
  Keep that convention in new doubles: it is what makes Hegel's shrinking
  remove faults.
- `qhegel.Choices` draws 512-byte chunks (`hegel.Binary(n, n)`), not
  one draw per choice (20x slower when shrinking). A task's panic is
  re-raised by `Sim.Run` with its original value, because Hegel aborts
  a test case by panicking from whichever goroutine draws. qtest's
  explorations recover it (`runOne`) into the run's `*RunPanic` with the
  task's stack (`internal/hooks.WithPanicStack`): a self-check's panic is
  a failing run, named and kept, not the test binary's end. qhegel's own
  runs do not go through `runOne`, so Hegel's aborts reach Hegel.
- A nemesis (crash) is considered at every scheduling step: its rate is
  per step (`DefaultRates` 1 in 200), not per operation. Under load that
  means far more crashes per second: set `Sim.NemesisEvery`
  (`qload.Experiment` does when it injects faults). Each family (the
  name's first segment, a heal a partition's) is its own choice,
  `fault.nemesis.<family>`, at its own rate, considered in turn until one
  acts: one family's nemeses do not dilute another's. A Script's pick by
  name is recorded as those choices, so its run replays; it bypasses a
  Without or Swarm around it, which turns its nemeses off only for other
  choosers. A family a wrapper answers reaches no ChoiceLog inside it:
  `ChoiceLog.Nemeses` lists only what a log was offered.
- Simulated runs record into their own recorder. `System.Explore` runs
  seeds in parallel, each with a recorder in the chooser's context
  (`qenv.WithContext`, `qobs.WithRecorder`). A step without it in its
  context finds its run through its goroutine: qenv registers each
  task's goroutine and the simulation's creator (`qrun`,
  `qenv/goroutine.go`). Explore and Experiment runs are strict
  (`internal/hooks.Strict`), so a step from a goroutine started with `go`
  fails the exploration (`hooks.Escaped`; `Series.Escaped` for an
  Experiment). A step from a goroutine one of a run's tasks started (its
  stack's "created by ... in goroutine N", a task's id kept until its
  simulation closes) fails that run alone (`hooks.WithEscapes`); any
  other escape (a goroutine's goroutine, an escape under gosim, whose
  goroutines gosim's scheduler starts, or one after its run ended) is
  logged process-wide, where every strict run running sees it. So qtest's tests run in parallel, except those that touch
  process-wide state (Setenv, a global recorder, the process-wide log):
  keep `t.Parallel()` out of those.
  `gotogether lint -sim .` (in CI) flags `go` statements, wall-clock
  calls, math/rand's and crypto/rand's package functions and selects of
  two or more cases in files that import qenv, tests included; `//qenv:ok` exempts a
  line (give the reason after it). The core's own simulated code (qenv, its
  internals, qload, qrole, qobs, qrun, qcover, qmut) is held to it too
  (qlint's TestCoreStaysInTheSimulation), every exemption with its reason.
- `//quint:action` arguments split on spaces outside quotes and brackets
  (`obs.balance:b.Balance(ctx, acct)` is one): binding's lint and qweave
  share the rule, tested on `binding/testdata/directives.json`. Change
  both with the table.
- qenv finds the task of a goroutine when a context carries none: Sleep,
  Wait, Go and recording with `context.Background()` from a task stay in
  its simulation (`taskOf`, `qenv/goroutine.go`), so a woven background
  goroutine's simulated latency does not sleep in real time.
- A task killed by a crash stays frozen while the simulation runs (no
  deferred code runs), and unwinds at `Close`. Steps recorded while it
  unwinds (`//quint:action` records on return) are not in the run: they
  go to `Recorder.Interrupted`, and `System.Explore` reports them with a
  failing run (`hooks.SetDiscard`). A task that crashes its own process
  stops in `Crash`; restart from an `OnCrash` hook.
- A step recorded by the caller on return describes what the caller saw.
  An effect the model must see whether or not its caller survives (a
  write that landed before the crash) belongs at the effect's site: the
  double or service records it.
- Run qenv's tests, and tests of new doubles, with `GOTOGETHER_CHECK=1`: the
  scheduler checks its invariants at every step, and every `Store` checks
  each commit's dependency cycles against its isolation level
  (`qenv/internal/history`: G1a, G0, G1c, G-single, G-nonadjacent, G2;
  one classifier with qhistory, `internal/depgraph`). CI does, and runs
  the store's examples so too. A change to the store's validation that
  lets a forbidden cycle commit panics there, naming the cycle. Every
  `Queue` checks its contract after each operation: no message lost, none
  delivered after its acknowledgement, an Ordered queue's first
  deliveries in order.
- One thread per goroutine when recording with a domain clock: a thread
  asserts program order, and goroutines sharing one contradict it
  (`issue: error cycle`).
- A sink's durability (`qobs.FileSink(path, d)`, `NewJSONLSink(w, d)`):
  `PerStep` survives a killed process (one write per step, no buffer),
  not a power loss: the OS may not have written the last steps back, and
  the line being written can be torn (`qenv.Disk` shows both). `Synced`
  fsyncs each step. `Buffered` buffers, and a killed process loses its
  last steps, undetectably: use it only where nothing crashes. `qtrace.ReadJSONL` keeps the steps before a torn last
  line and returns a `*TornTail` (input.File.Torn; validate warns).
- A `qenv.Signal` waiter is tagged with the task's wait generation; a
  timed-out wait must not be woken by a later `Notify`. Keep the tag when
  touching the scheduler.
- qhegel's case checker (`caseChecker`): prefix reuse is only sound for one actor with
  no `order.*` keys, where the recording order is the reconstructed order,
  and with equal instance constants. Keep those guards.