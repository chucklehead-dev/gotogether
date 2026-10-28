# Exploring, then shrinking

From the gotogether-faults skill (its SKILL.md says when to read this).

## 4. Explore, then shrink

```go
sys := qtest.System{Binding: "app.binding.yaml", Run: func(c qenv.Chooser) error { return app.Run(c, cfg) }}
sys.Rates = rates // the environment's fault rates, for every workflow (sys.WithRates(r): a copy at others)
sys.Explore(t, qtest.ExploreConfig{Seeds: 100}).Expect(t, qtest.Violates("chargedAtMostOnce")) // or Conforms(), Some/All/None(...)
qhegel.Shrink(t, sys, hegel.WithTestCases(50)).Expect(t, qtest.Violates("chargedAtMostOnce"))
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

- **This fault, at this point:** a `qenv.Script` makes exactly the faults
  it names, once the run has taken `After` steps, and no others: crash
  process payments after its third step, or lose the provider's response
  to the second charge.

  ```go
  sys.Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser {
      return &qenv.Script{Faults: []qenv.Scripted{
          {Kind: "nemesis.crash.payments", After: 3},
          {Kind: "rpc.provider.response", Name: "lost", After: 5},
      }}
  }}).Expect(t, qtest.Conforms())
  ```

  Kinds are as fault coverage prints them, matched exactly; a crash is
  `nemesis.crash.<process>`. A misnamed one fails the exploration with
  the fix (`Script.Misnamed`). A fault a run never made fails it too,
  whatever the claims ("scripted faults never happened"; `Run.Unscripted`
  lists them): the run never reached a choice of its kind after `After`
  steps. For random faults, a kind never faulted is listed as "never
  faulted" when a claim fails; `x.Evidence().Faults` and its `Unreached`
  say which kinds the runs reached.
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
  - Run `gotogether lint -sim .` to find `go` statements, wall-clock calls
    (`time.Now`, `time.Sleep`), `math/rand`'s package functions (draw from
    `Sim.Choose` instead), either as a value too (`now := time.Now`,
    called later), and selects of two or more cases (Go picks among
    ready ones at random) in files that import qenv, tests included
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
  usual), or `:schedule=...` (Exhaust), `:corpus=<file>`, `:choices=<file>`
  (a steered run, `ExploreConfig.Steer`). A run alone is
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
  - Turn whole kinds off with `qenv.DefaultRates.Set("kube", 0)`, or per
    run with `qenv.Swarm`.
  - A path behind several rare faults in a row (three failed writes, then
    a restart) is one run in hundreds. `ExploreConfig{Seeds: 20, Steer:
    180}` runs 180 more, each replaying a run that found something new (a
    step's action, outcome or observed state, a fault, a code block) up to
    a random choice: runs one fault deep breed runs that start there
    (journal's restart: 1 run in 200 seeds, 8 steered). Record the depth
    as an observation (`obs.fails`) and steering can climb it.
  - A bug that needs one task to run far ahead of another (a reader that
    must finish before a writer's single step) is out of reach of
    uniform random schedules. Schedule by priorities instead:
    `ExploreConfig{Chooser: func(seed uint64) qenv.Chooser { return
    qenv.NewPCT(base, seed, 3, 500) }}` with `base` a `qenv.NewSeeded(seed)`
    (seeds are `uint64`: `qenv.NewSeeded(uint64(i))` for an `int` or
    `int64` one)
    at `sys.FaultRates()` (PCT: depth 3 finds bugs needing three
    orderings; 500, about a run's scheduling steps: set it to your run's
    length, since change points past the run's end are lost and too large
    a value makes depth 2 act as depth 1). Not on the native runtime
    (gosim), which refuses it; over Hegel, only faults shrink.
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
  one option list: `qenv.DeclareFaults` panics if a second declaration differs.
  `qenv.FaultPoint` declares its own (its errors' names, `crash`).
- **Extending qenv** (a new double, a scheduler change): run its tests
  with `GOTOGETHER_CHECK=1` (or `s.Check = true`). The scheduler then verifies
  its own invariants at every step, and each `Store` checks every commit
  against its isolation level (a cycle of dependencies the level forbids
  panics, naming it). `TestSchedulerSemantics` states its
  semantics as a Hegel property over random programs.
- **Families added after the suite:** clock faults, stalls and
  partitions are opt-in, so a suite written before them never meets
  them. `GOTOGETHER_ALL_FAULTS=1` turns them all on in every simulation
  (Clocks, Stalls, a partition nemesis between every two processes): run
  a suite so now and then, and a verdict that changes is a fix, or a
  `qenv.Without` with its reason. A failure's reproduce line carries the
  variable.
- **Profiles:** run each property under every contract you might run
  on. For each mutant, a row per isolation level (or store, or broker
  mode) says where the bug is real (see examples/checkout's table).

## More, when the task needs it

Read the reference before using what it covers:
- [`references/services.md`](services.md): HTTP servers and clients (`qenv/qhttp`: an unmodified `http.Handler` and `http.Client` in a simulation), gRPC services (`simgrpc`, otelgrpc spans) and Kafka/Kinesis consumers and publishers (a simulated partitioned log, kfake, a fake Kinesis): the seams and examples.
- [`references/sql.md`](sql.md): a database/sql service over a Store (`qenv/qsql`): the SQL subset, the anomalies of each level, SQLSTATE errors for retry loops, connection faults, database/sql's goroutines.
- [`references/fuzzing.md`](fuzzing.md): coverage-guided exploration over the chooser's bytes (`System.Fuzz`), a shrunk failure as a corpus entry.
- [`references/fault-points.md`](fault-points.md): your own functions as fault points (`//quint:fault`, `qenv.FaultPoint`), and which faults runs reached.
- [`references/coverage-and-mutants.md`](coverage-and-mutants.md): which code each step ran (`qweave -cover`), the one-report `Evidence`, and mutants (`qweave -mutate`, `System.Mutate`).
- [`references/sweeps.md`](sweeps.md): isolation levels, sizes and fault kinds swept by pairwise rows (`qsweep`, `System.Sweep`), factors as model constants.
- [`references/weaving.md`](weaving.md): third-party or legacy code with sync locks and goroutines, simulated by weaving (qweave, Orchestrion).
- [`references/gosim.md`](gosim.md): a whole program, its network and channels, simulated by gosim (qgosim).
- [`references/workloads.md`](workloads.md): a production workload (SimKube traces, `qload.Piecewise`) replayed against the simulation.
- [`references/real.md`](real.md): faults in a real deployment (`qnemesis`: kill, pause and restart processes and containers, toxiproxy partitions, latency and loss, a Random or a trace's schedule), recorded as steps the same binding judges; claims over real runs (`qtest.Runs`); a failed run's faults re-made in a simulation (its journal's `Script`).
- [`../gotogether-workflow/references/verdicts.md`](../../gotogether-workflow/references/verdicts.md): every verdict and expectation (`Rejects`, `Mismatch`, `Violates`, `Some`/`All`/`None`) and what each means.
