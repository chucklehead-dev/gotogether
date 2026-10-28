# Deploying into a simulation

From the gotogether-faults skill (its SKILL.md says when to read this).

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
s.Run(20 * time.Second)       // with faults for 20s of virtual time; Run(0) lets no time pass
return s.Settle(60 * time.Second) // faults off, tasks in turn, until quiet; records the settle
```

- **Restarts:** a process's start function runs on boot and after every
  restart. It must rebuild from durable doubles only. State the start
  function makes is lost, which is the point; state a handler reaches
  that was bound at construction (a `Service`'s handler, a struct the
  test holds) is not. A crash runs `s.OnCrash` hooks, then kills the
  process's tasks, and the restart's start function runs later, so a
  request can reach the handler before the rebuild: clear such state in
  an `s.OnCrash(func(p *qenv.Process) { ... })` hook for its process
  (a node that served as leader after its crash, in a newcomer's
  counter).
- **Settles:** `Settle` records a settle in the trace when it finds the
  system quiet, and only then are the binding's `eventually:` vals judged
  (a step recorded after it, by a probe, needs `qobs.Settled` again).
  Faults stay off after it, so a probe runs on the settled system; a run
  of phases calls `s.ResumeFaults()` to fault again.
- **Stuck runs:** `Run` returns `qenv.ErrStuck`, naming the tasks, and
  `Settle` a `*qenv.Unsettled` (its `Stuck`), when a task waits with
  nothing left to wake it (a lost message, a
  missed Notify). Return the error from your `Run`; a task that may wait
  forever by design (a server loop, a controller's or a relay's polling
  loop) calls `qenv.Daemon(ctx)`, and a double's own receive waits with
  `qenv.WaitIdle`. `Settle` also returns a `*qenv.Unsettled` when its
  time runs out with a task that is no daemon still at work or waiting (a
  retry loop that never ends, a wait a daemon's timer hid); in an
  exploration that is no error but an outcome: such a run does not
  conform, and `qtest.Settles()` states it alone.
- **Quorum liveness:** `s.SettleCore(d, a, b)` heals the network and
  leaves processes a and b alone while every other process goes on
  crashing, pausing and losing requests (VOPR's liveness mode): a system
  that needs only a quorum must still finish its work. Name every process
  the core needs, a service's own included; the work of the others is no
  pending work.
- **Disks:** `qenv.NewDisk(s, p.Name, p)` is process p's machine's disk
  (name each double uniquely: a second of one name panics):
  writes reach the OS at once and the disk on `File.Sync`. When p crashes
  the fault kind "disk.<name>.crash" decides: nominal (the process died,
  the OS kept every write), "powerLoss" (only synced writes) or
  "tornWrite" (power loss mid-write: part of the last one). Its I/O
  faults: "disk.<name>.write" (`ErrNoSpace`, nothing written),
  "disk.<name>.sync" (`ErrIO`: the writes are dropped, and a later
  successful fsync does not bring them back, as on Linux) and
  "disk.<name>.read" (a byte flipped: checksum what you read back).
  Write a journal or a step log through it to see what an fsync buys.
- **Upgrades:** `s.Upgrades("v2", startV2, procs...)` makes a rolling
  upgrade a nemesis per process (`nemesis.upgrade.<name>`): each crashes
  its process and restarts it running startV2, with `Process.Version`
  "v2", at any step, so runs pass through every mix of versions (a v2
  writer and a v1 reader, say); `Exhaust(t, qtest.Bound{Faults: 1})`
  tries one upgrade at every point. Code that differs by version reads
  `qenv.ProcessOf(ctx).Version`. `p.Upgrade(v, start)` makes one where a
  test wants it, and `p.RestartAs` restarts a crashed process as another
  version. The same model judges every version: never give one slack.
  Test a rollout paused partway too: a finished one restarts every node
  on the new code, whose replay can repair what the old code got wrong
  (examples/raftkv: a log format the v1 node skipped, caught in 8 of 20
  paused rollouts and almost never in full ones).
- **Graceful termination and a lagging router:**
  `s.Terminations(qenv.Termination{Grace: 30 * time.Second, Budget: 1},
  procs...)` makes SIGTERM a nemesis per process
  (`nemesis.terminate.<name>`; `p.Terminate()` sends one where a test
  wants it): every context `qenv.Terminating(ctx)` gave its tasks is
  cancelled, the process drains and calls `p.Exit()`, or is killed at
  the grace (a crash); either way it restarts. `Short` adds option
  `short` of `terminate.<name>.grace` (an eviction's reduced grace);
  `Budget` is a PodDisruptionBudget. `qenv.NewRouter(s, name, lag,
  procs...)` is a load balancer: `Pick` returns an `Endpoint` (a
  process's incarnation) of its view, by the schedule choice
  `route.<name>`, and the view learns of each backend's change of
  readiness (terminating, down, restarted) `lag` late under
  `route.<name>.lag`'s option `stale`: a request to an endpoint that is
  `Gone`, or to a server that shut its listener, is refused. A drain must
  keep serving longer than the lag. `Service.Call` stops at a cancelled
  context (the outcome unknown), as a real client does: code that cancels
  its work at SIGTERM loses at-most-once outcomes (grist's
  `gristsim.Cluster` runs all of it for an app).
- **Clock drift and leaders:** a lease is timed on each node's own
  clock, and under drift an election is won by the fastest clock, the
  one whose lease ends first. The unsafe case (a slow leader) needs the
  leadership moved to it (raftkv's `TransferLeader`): enumerate drifts
  with `Exhaust` over "clock.drift" after a transfer, not after an
  election.
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
- **Pauses and gray failures:** `s.Stalls = qenv.Stalls{Pause: 3 *
  time.Second, Slow: time.Second, SlowFor: 10 * time.Second}`, before
  creating processes, adds nemeses to every process: `pause.<p>` freezes
  it for Pause at its tasks' next scheduling point (a GC, a SIGSTOP: a
  lease holder wakes still trusting its lease), and `slow.<p>` keeps it
  alive but makes each scheduling point cost Slow more for SlowFor (its
  health checks pass while its work times out). Families
  `nemesis.pause` and `nemesis.slow`; raise one alone with
  `Rates.Set("nemesis.pause", 0.02)`. Not on the native runtime.
  A request can also be late (`rpc.<name>.delay`), on by default: a
  lock without fencing breaks on either, whatever the clocks do. Its
  options: `late` (delivered just after the caller's timeout: processed
  after it gave up), `lateDuplicate` (delivered, and a copy a timeout
  after that, as a retransmission), `veryLate` (a timeout after the
  caller's, past its retry, even one that backed off) and `slow` (at half
  the timeout: late, but in time). `rpc.<name>.request` also
  `duplicate`s a request: a handler that is not idempotent applies it
  twice, so faithful code dedupes by a request id.
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
  call timeouts and `qenv.WaitFor`. A `Signal.Wait`'s limit is
  true time. examples/lease shows each fault breaking
  the code that trusts it: a cross-machine comparison by a skew, a
  wall-clock interval by a jump, a monotonic one by a drift past its
  margin.
- **Workload choices** (cancel or not, when) are schedule choices:
  `s.Choose("sched.client.cancel", 10)`.
- **Start from a scaffold:** `gotogether scaffold -system -binding
  app.binding.yaml -pkg app -out system_test.go` writes a System test whose Run deploys
  nothing yet, a commented `qobs.Record` per action with the fields the
  binding reads, a conformance test that fails until steps are recorded,
  and the overlay mutants' harness (`qtest.Mutants`) with a claim to fill in. `examples/hello` is the smallest one
  filled in.
- **Write a `Run(chooser, Config)`** for the whole deployment, so tests,
  Hegel and a CLI that writes step logs all share it.
- **Two models of one system** (leadership, and the clients' history):
  a System has one binding, so give the deployment a recording switch
  (examples/raftkv's `Config.Record`, KV or Cluster) and a System per
  model, or one model holding both. Lint each binding's sources with all
  the bindings at once: `gotogether lint -binding a.yaml,b.yaml -src .`
  checks each recorded action against their union.
