# twophase: two-phase commit against a Choreo model

A coordinator and three participants run two-phase commit in a qenv
simulation: crashable processes, each with its own durable store, over a
network that loses and duplicates requests and loses responses. The model
is written in [Choreo](https://github.com/informalsystems/choreo)
(`model/twophase.qnt`, after Choreo's own two_phase_commit example; the
framework is vendored in `model/`), and every workflow of the toolkit
checks the code against it.

**Choreo and recorded steps.** Choreo's `step` picks a node and one of
its transitions, so neither a recorded step nor `quint run --mbt` could
say which transition a node took. The model gives each transition a
named action of the node that takes it, with `choreo::step_with`:

```quint
action prepare(node: Node): bool = all { PARTICIPANTS.contains(node), step_with(node, spontaneously_prepares) }
action decideCommit: bool = step_with(COORDINATOR, decides_on_commit)
action step = { nondet node = PARTICIPANTS.oneOf()  any { prepare(node), ..., decideCommit, decideAbort } }
```

Then:
- the binding names those actions, and its expectations read the
  model's state through a helper (`stage(n)`, since `choreo::s` is not
  visible outside the module): `expect: 'stage({{.node}}) == Prepared'`;
- `quint run --mbt` reports the named action and its `node`, and connect
  compares the driver's state with `choreo::s.system` (`StatePath: s,
  system`: module qualifiers are stripped).

No change to the toolkit was needed.

**The model alone, every state.** For three participants the model has
99 reachable states, so `gotogether simulate -graph` checks both
invariants in each with the Go evaluator (about 0.1 s, no quint process
but the compile) and proves them for the instance:

```
$ gotogether simulate -graph -binding twophase.binding.yaml
  graph   complete: 99 states, 286 transitions, depth 7, 0.0s
  holds   consistency: no reachable state violates it (the whole graph, 99 states)
  holds   commitIsDecided: no reachable state violates it (the whole graph, 99 states)
  ends    9 reachable states the step cannot leave, each terminal (simulate.terminal)
```

Its 9 states with no transition enabled are the protocol's end, every
node decided; the binding says so (`simulate.terminal: 'NODES.forall(n
=> stage(n) == Committed or stage(n) == Aborted)'`), or they would fail
as deadlocks. With the coordinator's decision made early in the model
(it commits once any participant has prepared: `MODEL=early-decision
./run-demo.sh`), the graph refutes consistency with its shortest
counterexample, `init.then(prepare("p1")).then(decideCommit)
.then(commitAsInstructed("p1")).then(abort("p2"))`, which `-out` writes
as a Quint run and quint test replays.

**The code.** `tpc/` has the state machines: each transition is a
transaction on the node's store that records its model step when it
commits. `twophase.go` deploys them: participants vote (mostly to commit,
by a schedule choice) and resend their vote until they hear the
decision; the coordinator aborts if every vote has not arrived in 3 s, and
resends its decision until each participant answers.

## Tests

| Test | Workflow | Shows |
|---|---|---|
| `TestGraphProvesInvariants` | `simulate -graph` (`validate.Checker.Graph`) | the model's whole graph, 99 states: both invariants proved for three participants, its 9 ends terminal, every action enabled somewhere |
| `TestGraphFindsEarlyDecision` | `simulate -graph` on a model mutant | the coordinator deciding on one vote: consistency violated four steps from init, the counterexample replayed by quint test |
| `TestChoreoBinding` | validation of recorded steps | a choreo model binds like any other: a run conforms, an early commit is rejected |
| `TestFaithfulConforms` | `System.Explore`, 60 seeds, crashes and message faults | every run conforms; `Evidence` lists the faults reached |
| `TestMutants` | `System.Explore` with and without faults | `EarlyCommit` is rejected at `decideCommit` without faults; `ImpatientParticipant` (rejected at `abort`) and `VolatileDecision` (rejected at `decideAbort`) conform without faults and fail with them |
| `TestHegelShrinksImpatience` | `qhegel.Shrink` | the impatient participant's failure, shrunk to a few steps and the crashes it needs |
| `TestModelDrivesTransitions` | `connect.Run` | the model's traces drive `tpc`'s transitions, every node's stage compared with `choreo::s.system` |
| `TestModelDrivenMutants` | `connect.Replay` | the model's traces catch `EarlyCommit` at its first prepare, and never ask for the impatient abort |

`./run-demo.sh` explores the model's graph, then simulates 30 seeds and
validates their step logs with `gotogether validate`; `MUTANT=earlyCommit
| impatient | volatileDecision` injects a bug into the code,
`MODEL=early-decision` into the model. Under `earlyCommit` most runs never
settle: the coordinator resends its commit forever to a participant
that voted to abort (and refuses it), which is the bug's consequence,
not the harness's. twophase-sim says which seeds did not settle and
writes their logs without a settle record, so validation rejects each
at its `decideCommit` (the demo exits 1 with the report).
