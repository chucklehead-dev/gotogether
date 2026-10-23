---
name: gotogether-faults
description: Test Go services against a Quint model under deterministic fault injection with gotogether's qenv package - write the app against seams (qenv.DB/Txn, broker and service interfaces, qenv.Go/Now/Sleep), deploy it into a simulated environment (crashable processes, a store at Serializable/SnapshotIsolation/ReadCommitted, an at-least-once queue, lossy RPC, fake Kubernetes API servers), explore seeds with System.Explore or shrink with Hegel (qhegel.Choices + qhegel.FindViolation), and carry the same model to a real environment (kwok, Maelstrom-style). Use when a system's correctness depends on retries, idempotency, outboxes, isolation levels, crashes, status lag or lost responses, or when a bug only shows up in a real environment.
---

# Fault injection with qenv

A fault is a choice the environment's contract allows:
- a response lost after the request took effect;
- a message redelivered;
- a read from a snapshot;
- a crash between two statements.

`qenv` runs the system deterministically and makes every such choice
through one `Chooser`. Option 0 of every choice is nominal, so Hegel's
shrinking removes faults.

## 1. Write the app against seams

- **Data:** `qenv.DB` and `qenv.Txn` (Get, GetForUpdate, Put, Delete,
  Scan, Commit, Rollback, OnCommit). In production, an adapter over
  `database/sql` implements them.
- **Other services:** small interfaces of your own (a broker, a payment
  client), implemented in the deployment over `qenv.Queue` and
  `qenv.Service`.
- **Concurrency and time:** `qenv.Go`, `qenv.Now` and `qenv.Sleep`, not
  `go`, `time.Now` or `time.Sleep`. Outside a simulation they fall through
  to the real thing.
- **Iteration:** iterate maps in sorted order wherever the order affects
  behaviour.
- **Kubernetes controllers:** take readers and writers per cluster
  through an interface (`Clusters`). In simulation, give them
  controller-runtime fake clients behind `interceptor.Funcs`; that's the
  seam where generation, conflicts, lag and lost responses happen
  (examples/fleetrollout `kubesim`).

## 2. Record steps where effects happen

- **A commit:** `tx.OnCommit(func() { qobs.Record(ctx, "place", nil, "order", id, "qty", n) })`.
  The hook runs when the commit is applied, even if the caller is told
  the outcome is unknown.
- **A remote service** records its own operations (a payment provider's
  charges and refunds).
- **A fake API server** records patches as it applies them. With real
  clusters the controller records them when the update succeeds.
- **Evidence:** record every read the code decides on, with its version
  (`resourceVersion`), and let the model order evidence by it.
- **On return, from the caller** (`//quint:action`): the step is what
  the caller saw, and a crash before the return means no step. A crashed
  task's deferred recording at `Close` goes to `Recorder.Interrupted`,
  reported with a failing run but not checked. If the model must see an
  effect whether or not its caller survives (a write that landed, then a
  crash), record it at the effect's site instead.
- **Crash tests in a real environment** need a durable step log:
  `qobs.FileSink` is (one write per step). `BufferedFileSink` loses the
  last steps of a killed process, undetectably.

## 3. Deploy into a simulation

```go
s := qenv.New(chooser)
defer s.Close()
db := qenv.NewStore(s, "shop", cfg.Isolation)
q := qenv.NewQueue(s, "payments", nil)                           // its broker of no process: no partition reaches it
provider := qenv.NewService(s, "provider", nil, p.Handle)          // its own process, never crashes
api := s.Process("api", true, func(context.Context) {})              // crashable; handlers run in it
s.Process("payments", true, func(ctx context.Context) { app.RunPayments(ctx, inbox{q}) })
s.Spawn("client", func(ctx context.Context) { /* calls that retry on qenv.ErrTimeout */ })
s.Run(20 * time.Second)       // with faults
s.Settle(60 * time.Second)    // faults off, canonical schedule, until quiet
qobs.Record(s.Context(), "settle", nil)
```

- **Restarts:** a process's start function runs on boot and after every
  restart. It must rebuild from durable doubles only; in-memory state is
  lost, which is the point.
- **Stuck runs:** `Run` and `Settle` return `qenv.ErrStuck`, naming the
  tasks, when a task waits with nothing left to wake it (a lost message, a
  missed Notify). Return the error from your `Run`; a task that may wait
  forever by design (a server loop) calls `qenv.Daemon(ctx)`, and a
  double's own receive waits with `qenv.WaitIdle`.
- **Disks:** `qenv.NewDisk(s, p.Name, p)` is process p's machine's disk
  (name each double uniquely in a simulation: two of one name share their
  fault kind, so neither can be faulted alone):
  writes reach the OS at once and the disk on `File.Sync`. When p crashes
  the fault kind "disk.<name>.crash" decides: nominal (the process died,
  the OS kept every write), "powerLoss" (only synced writes) or
  "tornWrite" (power loss mid-write: part of the last one). Write a journal or a
  step log through it to see what an fsync buys.
- **Partitions:** `s.Net().PartitionNemesis(a, b, ...)` makes every link
  between those processes a nemesis (cut, later healed): a `Service` call
  across a cut loses its request and its response, for as long as it
  lasts. A `Queue` whose broker runs in a process (`qenv.NewQueue(s, name,
  broker)`) is partitioned too: a client cut off waits, and a consumer's
  cut redelivers what it holds. A nemesis's cut heals by itself after
  `s.Net().CutLength` (default 1 s): a run whose every task waits on a cut
  would otherwise be stuck. `s.Net().Partition(a, b)` and `Heal` script
  one, `OnCut` hooks a double's dropped connections; `Settle` heals them
  all. `p.Spawn(name, fn)` starts a task in process p from outside it.
- **Fault kinds per instance:** a double's kinds carry its name, as
  `rpc.<name>.request` does: `queue.<name>.ack`, `store.<name>.commit`,
  `disk.<name>.crash`. `"queue"` (in Rates, Without, a sweep) still covers
  every queue's.
- **Clocks:** a process has two, as a Go program does. `qenv.Now(ctx)`
  is its wall clock; `qenv.Mono(ctx)` and `qenv.Since(ctx, m)` its
  monotonic clock, what `time.Since` measures (a `time.Time` from Now
  carries no monotonic reading, so `Now().Sub` is wall arithmetic). Set
  `s.Clocks = qenv.Clocks{Skew: time.Second, Jump: 5 * time.Second,
  Drift: 0.05}` before creating processes; a zero field leaves its fault
  out. A skew ("clock.skew") and a drift ("clock.drift") are chosen per
  process at their rates (raise them: `Rates.Set("clock.drift", 1)`); a
  jump is a nemesis (`jump.forward.<p>`, `jump.back.<p>`) of its own
  family, at its own rate: raise it alone with `Rates.Set("nemesis.jump",
  0.05)` (at DefaultRates a lease's wall-clock bug showed in 7 runs of
  100), and crashes come as often as before. Skew and jumps
  move only the wall clock; a drift moves both, with the process's sleeps,
  call timeouts and `qenv.WaitFor`. Deadlines given to `WaitUntil` and
  `Signal.Wait` are true time. examples/lease shows each fault breaking
  the code that trusts it: a cross-machine comparison by a skew, a
  wall-clock interval by a jump, a monotonic one by a drift past its
  margin.
- **Workload choices** (cancel or not, when) are schedule choices:
  `s.Choose("sched.client.cancel", 10)`.
- **Start from a scaffold:** `gotogether scaffold -system -binding
  app.binding.yaml -pkg app -out system_test.go` writes a System test whose Run deploys
  nothing yet, a commented `qobs.Record` per action with the fields the
  binding reads, a conformance test that fails until steps are recorded,
  and a mutant test to fill in. `examples/hello` is the smallest one
  filled in.
- **Write a `Run(chooser, Config)`** for the whole deployment, so tests,
  Hegel and a CLI that writes step logs all share it.

## 4. Explore, then shrink

```go
sys := qtest.System{Binding: "app.binding.yaml", Run: func(c qenv.Chooser) error { return app.Run(c, cfg) }}
sys.Explore(t, qtest.ExploreConfig{Seeds: 100, Rates: rates}).Expect(t, qtest.Violates("chargedAtMostOnce")) // or Conforms(), Some/All/None(...)
qhegel.Shrink(t, sys, rates, hegel.WithTestCases(50)).Expect(t, qtest.Violates("chargedAtMostOnce"))
```

- **One System, every workflow:** `sys.Explore` (seeds), `sys.Exhaust` (every run within a bound), `sys.Sweep`
  (covering rows), `sys.FollowTraces` (a model's traces' faults),
  `qhegel.Shrink` (Hegel), all from the same `Run`. `Run` returns an
  error when the system could not run, which fails the test and names
  the seed: never `t.Fatal` inside it, since a seed's goroutine is not
  the test's. `ExploreConfig.Chooser` gives each seed its own chooser (a
  Script, `qenv.Replay(v.Choices)` of a shrunk failure). A run that
  panics (a `GOTOGETHER_CHECK` self-check, a bug) fails as that run,
  with the panicking task's stack, kept in the corpus if there is one.

- **Batching:** every seeded run is checked in one quint batch. On a
  miss, the report names the seed, its faults and its step log.
- **Parallel seeds:** seeds run in parallel (`ExploreConfig.Parallel`,
  default GOMAXPROCS), each with its own recorder in the chooser's
  context (`qenv.WithContext`). So:
  - build the simulation from the chooser you are given;
  - keep no state across runs;
  - start goroutines with `qenv.Go`, never `go`. A step lands in its run
    from any of the simulation's goroutines (its tasks, the one that
    created it), whatever context it was recorded with. From a `go`
    goroutine it has no run, and the exploration fails with the fix.
  - Run `gotogether lint -sim .` to find `go` statements and wall-clock calls
    (`time.Now`, `time.Sleep`) in files that import qenv, tests included
    (a System's `Run` closure lives there). Mark a deliberate one
    `//qenv:ok <why>`.
- **Other instances:** `qtest.Constants(map[string]string{"STOCK":
  "1000"})` checks against the model with other constants, so one
  binding serves runs of every size.
- **Every run, not a sample:** `sys.Exhaust(t, qtest.Bound{Delays: 2,
  Faults: 1})` runs every run that strays from the nominal one in at most
  that many choices (a delay: a task other than the oldest; a fault, a
  crash among them), fewest first, checked as an exploration's runs are.
  A search `MaxRuns` (default 1000) cuts short fails the test, naming
  how many runs it left; `Bound.Partial` accepts a sample instead. One fault is often enough: checkout's DualWrite window, which
  seeds find only at ten times the crash rate, and streams' reorder show
  in a complete one-fault search. Runs with the same steps are checked
  once, so thousands of runs cost a few quint checks; a failing run's
  report gives `GOTOGETHER_RUN='TestX#1:schedule=17:1'`, which runs it alone. No
  partial-order reduction: two delays on a system with many tasks is
  tens of thousands of runs, so start at one fault.
- **A corpus of failures:** `ExploreConfig{Corpus: "testdata/corpus/TestX"}`
  replays the saved runs there before the seeds, and a failed claim saves
  the run it shows there (its choices, as JSON): commit it, and the bug
  stays found whatever the seeds become. An entry names the exploration
  that saved it (`TestX#2`), which alone replays it, so a test's
  explorations can share the directory. An entry that no longer replays
  (the code or its faults changed) fails as stale, with the seed that
  finds its run again: delete it once the bug is fixed.
  `GOTOGETHER_CORPUS=dir` gives every exploration `dir/<test>` (the
  nightly run uploads it).
- **Reproducing a run:** a failing run's report ends with
  `reproduce: GOTOGETHER_RUN='TestX#2:seed=17' go test -run '^TestX$'`:
  that run alone in the test's second exploration (its others run as
  usual), or `:schedule=...` (Exhaust), `:corpus=<file>`. A run alone is
  judged only by the claims about every run (Conforms, All, None); the
  rest are listed as not judged. The numbers restart in every run of the
  test (`-count`); a line that names no exploration of the test fails
  it. A fuzz input's report points at Go's saved input instead. A failure's files (step log, module) are
  copied out of the test's temporary directory, so its report's paths
  are there after the test. A run whose Script never made
  all its faults lists them ("scripted faults that never happened").
- **Rates:**
  - A nemesis is considered at every scheduling step, so its rate is per
    step (default 1 in 200). Each family is a choice of its own at its
    own rate (`nemesis.crash`, `nemesis.partition` with its heals,
    `nemesis.jump`, a double's own such as simlog's `nemesis.rebalance`):
    `"nemesis"` sets them all, a family's key one alone, and adding
    partitions no longer makes crashes rarer. Under load, steps per second grow a
    hundredfold, and so would crashes. Set `s.NemesisEvery = time.Second`
    to make the rate per virtual second (`qload.Experiment` does this
    when it injects faults).
  - A bug with a one-step window needs a higher rate:
    `qenv.DefaultRates.Set("nemesis", 0.05)` (a kind as reports print
    it; `Set` returns a copy). `qenv.NoFaults` runs the schedule's
    choices only (a zero `Rates` is the default rates).
  - Turn whole kinds off with `{"kube": 0}`, or per run with
    `qenv.Swarm`.
- **Shrinking:**
  `qhegel.FindViolation(t, cfg, func(tc, rec) { app.Run(rec.Env(qhegel.Choices(tc)), cfg) })`
  shrinks a failure to the interleaving and faults it needs; faults
  appear as notes. Skip it under `testing.Short()`.
  `rec.Env` records the run's choices too: `qenv.Replay(v.Choices)`
  repeats the shrunk failure outside Hegel, a regression test with no
  Hegel in it. State the failure with `v.Expect(t, qtest.Rejects(...))`.
- **A new double's faults:** declare the kind's options by name,
  `var request = qenv.DeclareFaults("db.request", "unavailable", "lostResponse")`,
  and choose with `s.FaultOf(request)` (it returns "" or the option's
  name). A read that cannot lose a response takes
  `request.Only("unavailable")`, a prefix. Scripts name the option
  (`qenv.Scripted{Kind: "db.request", Name: "lostResponse"}`), which
  waits for a choice that offers it, and coverage lists options by name,
  as do recorded runs (`db.request=lostResponse`). A kind has
  one option list: `qenv.Faults` panics if a second declaration differs.
  `qenv.FaultPoint` declares its own (its errors' names, `crash`).
- **Extending qenv** (a new double, a scheduler change): run its tests
  with `GOTOGETHER_CHECK=1` (or `s.Check = true`). The scheduler then verifies
  its own invariants at every step, and each `Store` checks every commit
  against its isolation level (a cycle of dependencies the level forbids
  panics, naming it). `TestSchedulerSemantics` states its
  semantics as a Hegel property over random programs.
- **Profiles:** run each property under every contract you might run
  on. For each mutant, a row per isolation level (or store, or broker
  mode) says where the bug is real (see examples/checkout's table).

## More, when the task needs it

Read the reference before using what it covers:
- [`references/services.md`](references/services.md): HTTP servers, gRPC services (`simgrpc`, otelgrpc spans) and Kafka/Kinesis consumers and publishers (a simulated partitioned log, kfake, a fake Kinesis): the seams and examples.
- [`references/fuzzing.md`](references/fuzzing.md): coverage-guided exploration over the chooser's bytes (`System.Fuzz`), a shrunk failure as a corpus entry.
- [`references/fault-points.md`](references/fault-points.md): your own functions as fault points (`//quint:fault`, `qenv.FaultPoint`), and which faults runs reached.
- [`references/coverage-and-mutants.md`](references/coverage-and-mutants.md): which code each step ran (`qweave -cover`), the one-report `Evidence`, and mutants (`qweave -mutate`, `System.Mutate`).
- [`references/sweeps.md`](references/sweeps.md): isolation levels, sizes and fault kinds swept by pairwise rows (`qsweep`, `System.Sweep`), factors as model constants.
- [`references/weaving.md`](references/weaving.md): third-party or legacy code with sync locks and goroutines, simulated by weaving (qweave, Orchestrion).
- [`references/gosim.md`](references/gosim.md): a whole program, its network and channels, simulated by gosim (qgosim).
- [`references/workloads.md`](references/workloads.md): a production workload (SimKube traces, `qload.Piecewise`) replayed against the simulation.
- [`../gotogether-workflow/references/verdicts.md`](../gotogether-workflow/references/verdicts.md): every verdict and expectation (`Rejects`, `Mismatch`, `Violates`, `Some`/`All`/`None`) and what each means.

## 5. Take the same model to the real environment

Run the real binary against real dependencies, and inject the faults you
can. For Kubernetes: kwok clusters with the binary runtime (no Docker),
`kill -9` the controller, `kubectl` to reset. To give a kwok cluster the
timing of production, generate pod Stages from a SimKube trace (`go run
./cmd/kwok-stages -trace f.sktrace -speed 10 > stages.yaml`, then
`kwokctl create cluster --config stages.yaml`; fleetrollout's `TRACE=`),
from the same quantiles as the simulated `PodTimes`. Write step logs and
check them:
`gotogether validate -binding app.binding.yaml logs/*.jsonl`.
- A new process is a restart (`restart:` in the binding).
- A final fresh read of every component, then `settle`, makes eventual
  properties meaningful there too.

## Third-party controller logic (Crossplane, controller-runtime)

Run the library's real reconciler in the simulation. Don't copy its
logic.
- Give it a manager stub (`GetClient`, `GetScheme`) whose client is a
  fake behind `interceptor.Funcs`.
- Libraries that read the real clock need their timestamps translated at
  that seam: `Sim.ToVirtual` on writes, `Sim.ToReal` on reads and
  responses, for Get, List, Create, Update and Patch. Otherwise their
  timeouts and grace periods never expire.
- Record cluster state with an audit after quiescence (phase, Secrets).
  The same audit works against a real cluster (examples/crossdb).

## What the examples found

- **Locking.** A lock on a parent row doesn't stop write skew under
  snapshot isolation, if the conflicting writes go to other tables. Make
  the conflict a write.
- **Retries.** A client's retry can mask a dual-write bug. The realistic
  form is "commit, respond, publish in the background".
- **In-memory tracking.** It breaks under lost responses, not just
  restarts: an update applied with its response lost is never tracked.
- **External names.** A Crossplane provider whose external name is the
  cloud's generated ID leaks a database after one lost response and a
  delete, with no crash. Make the name deterministic (a client token) and
  the create idempotent; keep the creation grace period for eventually
  consistent clouds.
- **Status.** Trusting a status without `observedGeneration >=
  generation` overruns a rollout budget, both simulated and on real kwok.
