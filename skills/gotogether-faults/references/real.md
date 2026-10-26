# Faults in a real deployment: `qnemesis`

Read this before injecting faults into real processes or containers
(kill, pause, partitions, latency, loss) and judging the run with the
binding the simulation uses. A real run finds what a simulation cannot:
the real network stack, the real disk, the real library under real
timing. Its faults are recorded as model steps, so the same binding
judges it.

## The pieces

```go
p1 := &qnemesis.Process{Name: "n1", Cmd: func(inc int) *exec.Cmd {
	// one step log per incarnation, recorder id n1-<inc>: a killed one's ends where it died
	return exec.Command("./node", "-id", "n1", "-log", fmt.Sprintf("logs/node-n1.%d.jsonl", inc))
},
	Recorders: func(inc int) []string { return []string{fmt.Sprintf("n1-%d", inc)} }, // a crash declares their tails lost
	Logs:      func(inc int) []string { return []string{fmt.Sprintf("logs/node-n1.%d.jsonl", inc)} },
	PIDFile:   "logs/n1.pgid", // swept at the next start if a killed test left the group
}
t.Cleanup(func() { _ = p1.Close() }) // kills the process group: nothing left running
proxy := &qnemesis.Toxiproxy{URL: "http://127.0.0.1:8474"}
t.Cleanup(func() { _ = proxy.Close() })
addr, err := proxy.Link("n1", "n2", n2Addr) // n1 dials addr to reach n2; one proxy per direction
rec, sink, err := qnemesis.NewRecorder("logs/nemesis.jsonl") // id "nemesis", PerStep
n := &qnemesis.Nemesis{
	Targets: map[string]qnemesis.Target{"n1": p1, "n2": p2, "n3": p3},
	Net:     proxy,
	Rec:     rec,
	Record:  recording,                              // fault family -> model action
	Steps:   qnemesis.StepsIn("logs/node-*.jsonl"), // the system's logs, which the nemesis's cannot match
	Journal: journal,                                // faults.jsonl: every fault's start and end
}
err = n.Run(ctx, schedule)
err = n.Stop(ctx) // heal, clear, resume, start the down: the real settle
```

- **Targets**: `Process` (started with its own process group, SIGKILL
  to the group and reaped on a crash, SIGSTOP/SIGCONT for a pause, which
  returns once every process of the group has stopped), `Container` (the
  docker CLI: kill + wait, start, pause, unpause), or your own behind
  `Target` (Kill, Start, Pause, Resume, Alive; `Recorded` to name what an
  incarnation records).
- **Orphans**: `Close` (a `t.Cleanup`) kills the group. If the test
  binary itself is killed (kill -9, a CI timeout) no cleanup runs: on
  Linux the kernel kills the group's leader (Pdeathsig), not what the
  leader started, which lives on, reparented. Set `PIDFile`: the next
  start of a Process with that file (the next test run's) kills what an
  earlier incarnation left of its group (by group id, a reused id left
  alone). Nothing the kernel offers an unprivileged process kills a whole
  group when its parent dies (a PID namespace would, with privileges).
- **Network**: `Toxiproxy`, a small client of toxiproxy's HTTP API
  (`toxiproxy-server` on PATH; `go install
  github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0`, renamed
  `toxiproxy-server`, as CI does). A cut
  disables the proxies (connections closed, new ones refused), so a
  framed protocol never reads a stream with bytes missing from its
  middle. Latency and loss are toxics on the responses; toxiproxy draws a
  loss's toxicity per connection, not per message.
- A pair's faults act on both directions; names cannot contain dots.
- Name the system's logs so no glob of them matches the nemesis's
  (`logs/node-*.jsonl` beside `logs/nemesis.jsonl`, not `logs/n*.jsonl`):
  `StepsIn` leaves the nemesis's steps out by recorder id, but the same
  glob reading the logs to validate them, with the nemesis's log added,
  would read its steps twice.

## Faults and schedules

The names are qenv Script's: `nemesis.crash.<p>` (restart at once, or
after `For`), `nemesis.pause.<p>`/`resume.<p>`,
`nemesis.partition.<a>.<b>`/`heal.<a>.<b>`, `nemesis.isolate.<p>` (all
of p's links), `heal.<p>`, `nemesis.latency.<a>.<b>` (Arg),
`nemesis.loss.<a>.<b>` (Toxicity, each connection closed after Arg).
No clock skew (Go reads the clock through the vDSO, which libfaketime
does not reach), no failpoints.

- `qnemesis.Random(seed, mix, interval, dur)`: Jepsen's: one fault per
  interval, undone after dur, before the next. `qnemesis.Kinds("crash",
  names...)` names a family's faults for the Mix.
- `qnemesis.FromScript(sc)`: a Quint trace's faults (`qtest.FaultScript`)
  on the real system. `After` counts lines of the watched step logs: it
  means the trace's step count only when the trace's steps are the
  program's own; otherwise time the faults with `At`.
- A fault is due when `At` has passed, the logs have `After` lines, its
  kind's earlier faults have happened, and what it `Follows` has happened
  and ended. `Run` refuses a schedule with a misnamed fault
  (`Misnamed()`: no such target or link, a double's kind, a family
  without a name, a cycle, `After` without `Steps`), each with the fix.

## Recording, so the check stays fair

- A crash is recorded after the process is reaped: every step it
  recorded precedes the crash. A cut, a pause, latency: after the call
  returns (in effect). A heal, a resume, a restart: before the call (the
  restarted incarnation's steps follow; a binding's `within` bound covers
  the API's latency).
- `Recording` maps a family (crash, restart, pause, resume, partition,
  isolate, heal, latency, loss, clear) to the model's action and
  arguments; nil is `DefaultRecording()` (crash(node), partition(a, b),
  isolate(node), heal(a, b) or heal(node)); pause, latency and loss
  record nothing unless mapped.
- The nemesis's steps are ordered against the system's by wall time:
  one host, one clock, comparable within `qtrace.RealTimeSlack`. Across
  hosts give the steps order keys.
- A pause is not a partition: requests sent to a paused process wait in
  the kernel's queue and are served after the resume.
- A killed incarnation's torn last line is its tail declared lost
  (`qtrace.ReadJSONL`): failures after the cut are no verdict, never a
  FAIL.
- A kill can land after an effect and before its step: a reply sent, the
  step recorded on return (`//quint:action`'s), never written. The log
  ends cleanly, and another recorder's step that saw the effect (a
  client's) is rejected: a false FAIL. Name the incarnation's recorders
  (`Process.Recorders`, and `Logs` for where each was cut): the crash
  declares their tails lost (loss records on the nemesis's recorder,
  before the crash's step, cut at each recorder's last step, or the
  incarnation's start), so such a failure is no verdict. The price: a
  failure after the killed incarnation's last step is no verdict either.
  The loss is bounded by the reaping (`LostBefore`): a settle recorded
  after the crash settles the run (`Settles()` holds), one before it does
  not. A torn tail with no crash stays unbounded and voids every settle.
  A program that records each step before its effect is seen (at the
  effect's site, before the reply) has no such window and can leave
  Recorders unset.
- The nemesis's steps are of its own process (`nemesis`): a binding that
  judges them in one actor with the system's needs its `restart:` or
  steps of one process.

## Judging real runs

Validate each run's logs (the system's and the nemesis's together) with
`qtest.Validate`, then state claims over them:

```go
qtest.Runs(runs...).Expect(t, qtest.Some(qtest.Rejects("put")), qtest.None(qtest.Violates("agreement")))
```

`qtest.Runs` checks its claims as an exploration does: names the model
or the binding lack, eventual vals no run judged, no runs, and runs of
two instances fail with the fix.

A failed run's journal re-makes its faults in a simulation:
`j, _ := qnemesis.ReadJournal(f); sc := j.Script()` is a `qenv.Script`
(crashes, pauses, partitions and heals by qenv's names, in the order
they happened, each at its first chance). It keeps the faults' order,
not their timing against the workload: a real run's step counts (real
clients, the nodes' own logs) are not a simulated run's, and faults
timed by them were never reached. `j.StepScript()` keeps the steps
(`After`), for a journal whose steps are the simulated program's own.
Nothing waits for a
heal: qenv heals a cut by itself after `CutLength` (1 virtual second),
so a longer real partition's heal stays in `Remaining`; set
`sim.Net().CutLength` to `j.LongestCut()` to keep it.
`j.Unreproducible(sim)` says what the script leaves out or cannot make
as it happened (latency and loss, a heal of links never cut, pauses of
another length than `sim.Stalls.Pause`, cuts longer than its
`CutLength`, within `PauseTolerance`), with the fix. The script keeps the run's order, so a fault the simulation
cannot make (a pause without `Sim.Stalls.Pause`, a partition without
`PartitionNemesis`) holds up every fault after it: after the run,
`qnemesis.Unmade(sc)` names the cause (the configuration qenv needs
for the nemesis: `Sim.Stalls.Pause`, `PartitionNemesis`, a crashable
process) and what waited for it (a qtest
exploration with the script as its chooser fails on them too). Run the script as the chooser of the simulated system and
explore from there.
