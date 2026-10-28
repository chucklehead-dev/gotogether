# Liveness: a system that can stay stuck

*About 25 minutes. You will: state "eventually" as an eventual val;
prove a controller's model live and refute a retry storm's with a lasso;
change the verdict with fairness (`-unfair`, `-stops`) and see why the
two differ; then find the same storm in simulated runs, as their hot
cycle.*

An invariant says nothing bad happens. A liveness property says
something good eventually does: the object gets reconciled, the queue
drains. No finite trace refutes it, so no sample can. What can refute it
is a cycle: a way for the system to go round for ever with the good
thing never true. `gotogether simulate -graph` searches the model's
state graph for such cycles; a run's report shows the cycle a run was
going round when it ran out of time.

The tutorial uses [examples/requeue](../../examples/requeue): a
controller that applies a cloud resource's spec and requeues until the
resource is ready. The cloud provisions in two steps (pending, warming,
ready), and treats an apply of a changed spec as a replacement. A
controller that puts the reconcile's time in the spec restarts the
provisioning it waits for, at every requeue.

Outputs are pasted as they came, but for paths shortened and long
lines cut (`[...]`). It assumes the [first tutorial](prove-it.md)'s
setup (`gotogether` and `quint` on PATH).

## 1. The property: an eventual val

```quint
  // the controller saw the resource not ready yet and looks again later:
  // a requeue changes nothing (it is a stutter, which fairness counts as
  // no step)
  action requeue = all { not(ready), phase' = phase, ready' = ready }

  // the controller saw the resource ready and marked the object ready
  action finish = all { phase == "ready", not(ready), phase' = phase, ready' = true }

  action step = any { create, advance, requeue, finish }

  val reconciled = ready
```

The binding lists it as eventual, and says the model must drive it
(`simulate.holds`), with the marked object the end the model means:

```yaml
eventually: [reconciled]
simulate:
  holds: [reconciled]
  terminal: ready
```

`eventually:` is judged in two places: in a run, in its last state, if
the run recorded a settle (the system quiet: `Sim.Settle` in a
simulation, `qobs.Settled` in a real one); in the model's graph, as
cycles.

## 2. The faithful model is live

```console
$ cd examples/requeue
$ gotogether simulate -graph -binding requeue.binding.yaml
simulate requeue.binding.yaml: requeue(RESEND = false), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 5 states, 8 transitions, depth 4, 0.0s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  ends    1 reachable state the step cannot leave, each terminal (simulate.terminal)
  covered create 1 in 1 state, advance 2 in 2 states, requeue 4 in 4 states, finish 1 in 1 state
  cycles  1 eventual val judged as true infinitely often (at quiescence, for a run that settles): a fair cycle of states where one is false refutes it; every action weakly fair, fairness weak and per action (per-call or strong fairness would rule out more cycles, so a lasso may be no behaviour of a system that has them, and live is so only for a system at least this fair); a stutter is no step
  live    reconciled: no fair cycle keeps it false (the whole graph, 5 states); fairness rules out 4 hot states stuttering (the nearest leaves create enabled throughout, never taken: if it is an input that may stop, simulate.unfair; a fault, simulate.stops)
```

The `cycles` line is the assumption the verdict rests on, stated every
time:
- **every action weakly fair**: an action enabled throughout a cycle is
  taken in it. A cycle that leaves one out is no behaviour of the
  system, so it refutes nothing.
- **a stutter is no step**: `requeue` changes no variable. Requeueing
  for ever is not a cycle the search counts: it satisfies nothing and
  obliges nothing. (Without that rule every controller would be "stuck"
  in its own polling.)

`live`: no fair cycle keeps `reconciled` false, in the whole graph. The
tail says what fairness ruled out: four states where the system could
stutter for ever, each with an action enabled throughout and never
taken (`create`, nearest). If that action is one a client may never
send, fairness was too generous, which section 4 is about.

## 3. The storm's model: a lasso

`requeue.storm.binding.yaml` is the same model with `RESEND` true: an
apply after the first replaces the resource.

```console
$ gotogether simulate -graph -binding requeue.storm.binding.yaml -out /tmp/requeue/storm.qnt
simulate requeue.storm.binding.yaml: requeue(RESEND = true), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 7 states, 16 transitions, depth 6, 0.0s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  covered create 7 in 7 states, advance 4 in 4 states, requeue 4 in 4 states, finish 1 in 1 state
  cycles  [...]
  FAIL    reconciled: the model should drive it, and a fair cycle keeps it false forever: fix the model if it must, move it to simulate.allows if it is the code's to reach, or name the actions a run stops before it settles (simulate.stops) or need not take (simulate.unfair). The lasso:
    0. init
         phase = "none", ready = false
    1. create
         phase = "pending", ready = false
    then this cycle, forever, reconciled false in each of its states (2 steps of a component of 3; taking advance, create, every weakly fair action enabled throughout it taken: advance):
    2. advance
         phase = "warming", ready = false
    3. create
         phase = "pending", ready = false (step 1's state again)
    replay: run r = init.then(create).then(advance).expect(not(reconciled)).then(create).expect(and { not(reconciled), phase == "pending", ready == false })
    (quint replays the path and one turn of the cycle back to its state, the val false throughout; no quint run can check that it repeats forever, nor its fairness: the Go evaluator's search does)
    fairness rules out 1 hot state stuttering (the nearest leaves create enabled throughout, never taken: if it is an input that may stop, simulate.unfair; a fault, simulate.stops)
  wrote   /tmp/requeue/storm.qnt: quint test --main simulate --match 'gtg_' /tmp/requeue/storm.qnt replays each counterexample
$ quint test --main simulate --match gtg_ /tmp/requeue/storm.qnt

  simulate
    ok gtg_hot_reconciled passed 1 test(s)

  1 passing (23ms)
```

A **lasso**: a path from init (the stem, step 1) to a cycle (steps 2-3)
gone round for ever. Why does weak fairness not save it? `advance` is
enabled throughout the cycle and is taken. Its step to `ready` is
enabled only in `warming`, and every `create` takes the resource back to
`pending`, so no action that would reach `ready` is enabled
*throughout*. Weak fairness obliges only those. This is the shape of a
retry storm: the retry keeps disabling the progress it waits for.

Quint replays the stem and one turn back to the cycle's first state,
with the val false throughout. It cannot check that the cycle repeats
for ever, or its fairness; the Go evaluator's search does, and the
report says so.

## 4. Fairness changes the verdict: `-unfair` and `-stops`

Every action is weakly fair unless the binding (or the command line)
says otherwise. Two ways to say otherwise, for two kinds of action:

- **`unfair`**: an input a client may send for ever or never. It is
  obliged to nothing: a cycle may leave it out.
- **`stops`**: a fault, or an input the driver stops before it
  settles. It is taken in no cycle.

Take the faithful model and make the cloud's `create` unfair, as if the
apply were a client's input that might never come:

```console
$ gotogether simulate -graph -binding requeue.binding.yaml -unfair create | grep -v '^         '
[...]
  cycles  1 eventual val judged as true infinitely often (at quiescence, for a run that settles): a fair cycle of states where one is false refutes it; every action weakly fair but unfair: create (simulate.unfair, simulate.stops), [...]
  FAIL    reconciled: the model should drive it, and a fair cycle keeps it false forever: [...] The lasso:
    0. init
    then nothing, forever: no weakly fair action is enabled in that state (reconciled false there; only stutters, unfair actions or those that stop leave it)
    replay: run r = init.expect(and { not(reconciled), phase == "none", ready == false })
    [...]
```

The live model now has a lasso of length zero: nothing ever applies, so
nothing is ever ready. The verdict is only as good as the fairness you
declare; the `cycles` line names it every time.

The two words differ, and the storm shows how. In the storm's model one
action, `create`, is both the first apply and the replacement. Split it,
so the replacement is an action of its own (`replace`), in
`model/requeue.qnt`:

```quint
  action create = all {
    phase == "none",
    phase' = "pending",
    ready' = ready,
  }

  // a changed spec resent: the cloud replaces the resource
  action replace = all {
    RESEND,
    phase != "none",
    phase' = "pending",
    ready' = ready,
  }
```

add it to `step` (`any { create, replace, advance, requeue, finish }`)
and to `requeue.storm.binding.yaml`'s actions (`replace: {}`). The lasso
is the same storm, now through `replace`:

```console
$ gotogether simulate -graph -binding requeue.storm.binding.yaml | grep -v '^         '
[...]
  FAIL    reconciled: the model should drive it, and a fair cycle keeps it false forever: [...] The lasso:
    0. init
    1. create
    then this cycle, forever, reconciled false in each of its states (2 steps of a component of 3; taking advance, replace, every weakly fair action enabled throughout it taken: advance):
    2. advance
    3. replace
    [...]
```

Declare the replacement unfair: still a lasso. An unfair action need not
be taken, but it *may* be, for ever, so the storm is still a behaviour:

```console
$ gotogether simulate -graph -binding requeue.storm.binding.yaml -unfair replace | grep -E '^  (FAIL|live)|^    [0-9]'
  FAIL    reconciled: the model should drive it, and a fair cycle keeps it false forever: fix the model if it must, move it to simulate.allows if it is the code's to reach, or name the actions a run stops before it settles (simulate.stops) or need not take (simulate.unfair). The lasso:
    0. init
    1. create
    2. advance
    3. replace
```

Declare that replacements stop (a fault that ceases before the system
settles): live.

```console
$ gotogether simulate -graph -binding requeue.storm.binding.yaml -stops replace | grep -E '^  (cycles|FAIL|live)'
  cycles  1 eventual val judged as true infinitely often (at quiescence, for a run that settles): a fair cycle of states where one is false refutes it; every action weakly fair but stopping: replace (simulate.unfair, simulate.stops), [...]
  live    reconciled: no fair cycle keeps it false (the whole graph, 7 states); fairness rules out 4 hot states stuttering (the nearest leaves create enabled throughout, never taken: if it is an input that may stop, simulate.unfair; a fault, simulate.stops)
```

So the design recovers once the replacements stop, and is stuck while
they go on. Before the split, `-stops create` would have stopped the
first apply too, and reported the trivial lasso at init: give the
action you mean to qualify a name of its own. In a binding these are
`simulate.unfair: [...]` and `simulate.stops: [...]`.

```sh
git checkout -- model/requeue.qnt requeue.storm.binding.yaml
```

## 5. The same storm in the runs: the hot cycle

The code has the storm as a mutant, `stampSpec` (the reconcile's time
in the spec). Its demo simulates 20 seeds and validates their step
logs against both models:

```console
$ MUTANT=stampSpec ./run-demo.sh 2>&1 | sed -n "/== the controller/,\$p"
== the controller (mutant stampSpec)
requeue-sim: 20 of 20 runs did not settle: the controller was still requeueing a minute into the settle
-- against the storm's model: conforms, never settles, a hot cycle
  conformance: PASS
  hot cycle (evidence, no verdict: the run did not settle): reconciled false throughout steps 106-107, which return the model to its state after step 105 (in it 23 times with it false): advance; create
actor seed1: 110 steps
  issue: info eventually: eventual vals not judged (reconciled): the trace's last record is no settle (Sim.Settle, or qobs.Settled after a probe), or another recorder recorded after it
  conformance: FAIL
    record /tmp/tmp.3Gr5zLKpEA/runs/seed1.jsonl:5 is not a transition of the model from the state reached
      record:   /tmp/tmp.3Gr5zLKpEA/runs/seed1.jsonl:5, 2026-01-01T00:00:01.5Z, process 1c0e2c9f3dda
      observed: create()
      quint:    create
      why: the model's create is disabled:
        in create: RESEND or (phase == "none") is false
          where phase == "none" = false, RESEND = false, phase = "warming"
        enabled instead (the model's moves from this state, not records): advance, requeue
      model state before the step:
        phase = "warming"
        ready = false
  instance: RESEND = false
step logs and report in /tmp/tmp.3Gr5zLKpEA
mutant stampSpec caught, as expected
```

Two checks, two answers:
- Against the faithful model, the run is **rejected** at its second
  `create`: that model has no replacement. This is the safety check
  catching the code doing something the design does not allow.
- Against the storm's model the run **conforms** and never settles, so
  `reconciled` is not judged ("eventual vals not judged"). The report's
  `hot cycle` line is the evidence: the run's model states went round
  `advance; create`, the graph's lasso, 23 times with the val false. It
  is evidence, not a verdict: a run cut short has work pending by
  right.

The other mutant, `giveUp`, stops requeueing after one try. Its runs
settle, and the settle judges the val false:

```console
$ MUTANT=giveUp ./run-demo.sh 2>&1 | sed -n "/== the controller/,\$p"
== the controller (mutant giveUp)
actor seed1: 5 steps
  conformance: PASS
  invariant eventually reconciled: FAIL
    violated after record /tmp/tmp.QqtsaiCdKW/runs/seed1.jsonl:6
      record:   /tmp/tmp.QqtsaiCdKW/runs/seed1.jsonl:6, 2026-01-01T00:00:02Z, process e79a095f9b54
      observed: advance()
      quint:    advance
      the step changed, in the model's state:
        phase: "warming" -> "ready"
      model state after the step: 2 variables (-v prints it)
  absent:
    eventually reconciled: absent (a defect): false where the run settled, and its recorders lost nothing
  [...]
mutant giveUp caught, as expected
```

The graph answers "can the design stay unreconciled for ever?"; a
settle answers "did this run get there?". `giveUp`'s design is live
(the faithful model); its code is not.

## 6. In Go: claims over runs

`requeue_test.go` states the same verdicts as claims. The storm's runs:

```go
system(true).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 3}).
	Expect(t, qtest.None(qtest.Settles()), qtest.All(stormCycle))
```

`stormCycle` is a `qtest.Satisfies` over each run's report, checking
that its hot cycle is `advance, create`. A ready-made claim over hot
cycles, `qtest.NoHotCycle()`, is designed (ROADMAP I51), not built.
<!-- not built: qtest.NoHotCycle -->
`giveUp`'s runs: `qtest.All(qtest.Violates(validate.EventuallyName("reconciled")))`.
A faithful run that must finish says `qtest.Finishes()` (conforms,
settled, every eventual val true, no call pending).

## What is not built

- Strong fairness and per-call weak fairness (`simulate.fair`, ROADMAP
  I43): today fairness is weak and per action. A lasso the system's real,
  stronger fairness rules out can be a false alarm; the `cycles` line
  says which fairness was assumed.
- `qtest.NoHotCycle()` (I51), above.

## Read next

- [Possibles and scenarios](scenarios.md): what the model must be able
  to do, the other half of "is the model right?".
- The gotogether-patterns skill, pattern 5 (eventual properties
  conditioned on a settle): [skills/gotogether-patterns/references/patterns.md](../../skills/gotogether-patterns/references/patterns.md).
- The workflow's liveness paragraph, and `simulate.unfair` and
  `simulate.stops`: [skills/gotogether-workflow/references/workflow.md](../../skills/gotogether-workflow/references/workflow.md).
- [docs/DESIGN.md](../DESIGN.md) §62, liveness as cycles: the
  construction, and why the default is weak fairness per action.
- [examples/quorumkv](../../examples/quorumkv): an eventual val the
  model *allows* to stay false (`simulate.allows`): a probe may observe
  anything, so convergence is the code's to reach.
