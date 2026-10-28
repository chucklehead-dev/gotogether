# Prove it for an instance: `simulate -graph`

*About 20 minutes. You will: check a model by sampling, then over every
reachable state; plant a bug in a model and get its shortest
counterexample, replayed by quint; tell a deadlock from an intended end
(`terminal:`); refute a two-phase commit design; and see what a bound
does to a verdict.*

Before any code is checked against a model, the model should be right
on its own. `gotogether simulate` runs the model alone with the
binding's invariants. Without `-graph` quint samples it: a thousand
random traces, and "holds" means no sample broke it. With `-graph` the
Go evaluator visits every reachable state of the binding's instance
(its constants fixed), so "holds" means *no reachable state breaks
it*: a proof, for that instance.

Every command below was run on this repository, and its output is
pasted as it came, but for paths shortened and long state lines cut
(`[...]`).

## Before you start

```sh
git clone https://github.com/chucklehead-dev/gotogether && cd gotogether
npm install -g @informalsystems/quint     # quint ≥ 0.32
go install ./cmd/gotogether               # the CLI, from this checkout
```

## 1. Sampled, then every state (hello)

[examples/hello](../../examples/hello) is a counter that must apply each
request once. Its model has one invariant, `exactlyOnce`
(`count == applied.size()`), and its binding says the model guarantees
it (`simulate.holds`). Sample it first:

```console
$ cd examples/hello
$ gotogether simulate -binding hello.binding.yaml
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), 1000 traces of 30 steps, seed 1
  holds   exactlyOnce: none of 1000 sampled traces of up to 30 steps violates it (a sample, no proof: a violation that 1 trace in 334 shows is missed at most 5% of the time by 1000 traces, a rarer one more often; -graph searches every state)
  possible a retry recognised: reached by apply(id = 1), replay(id = 1) (the Go evaluator's search)
  possible done: reached by apply(id = 1), apply(id = 2), apply(id = 3), done (the Go evaluator's search)
```

"A sample, no proof". Now every state:

```console
$ gotogether simulate -graph -binding hello.binding.yaml
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 8 states, 25 transitions, depth 3, 0.0s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  holds   exactlyOnce: no reachable state violates it (the whole graph, 8 states)
  quiet   1 reachable state only a stutter leaves (every edge back to itself), so no deadlock there
  covered apply 12 in 7 states, replay 12 in 7 states, done 1 in 1 state
  possible a retry recognised: reached by apply(id = 1), replay(id = 1) (the Go evaluator's search)
  possible done: reached by apply(id = 1), apply(id = 2), apply(id = 3), done (the Go evaluator's search)
```

Read it line by line:
- **`graph complete`**: the search ran out of new states before any
  bound (100000 states by default): the 8 states are all there are for
  `REQUESTS = 1.to(3)`.
- **`sampled`**: quint still runs, a little, to cross-check the Go
  evaluator: every state quint's own traces reach must be in the graph.
  A state missing from it is an evaluator bug, and the verdicts are
  void.
- **`holds`** with "the whole graph": proved, for this instance.
- **`quiet`**: the state where every request is applied can only
  stutter (`replay` and `done` change nothing). That is not a deadlock:
  something can still happen there.
- **`covered`**: each action's transitions, and in how many states it is
  enabled. An action enabled nowhere would be listed as `never`.

The proof is for three requests. Try twelve with `-const` (the
binding's constants overridden, nothing edited):

```console
$ gotogether simulate -graph -binding hello.binding.yaml -const 'REQUESTS=1.to(12)'
simulate hello.binding.yaml: hello(REQUESTS = 1.to(12)), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 4096 states, 49153 transitions, depth 12, 0.8s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  holds   exactlyOnce: no reachable state violates it (the whole graph, 4096 states)
  [...]
```

2¹² states, still complete in under a second. The size of an instance
is what you trade for a proof: keep the constants small enough to
finish, and large enough to exercise every guard.

## 2. A bug in the model: the shortest counterexample

Plant a bug: let `apply` take a request it has already applied, by
deleting its guard `not(applied.contains(id))`. Sampling catches it,
with quint's trace:

```console
$ sed -i '/not(applied.contains(id)),/d' model/hello.qnt
$ gotogether simulate -binding hello.binding.yaml
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), 1000 traces of 30 steps, seed 1
  FAIL    exactlyOnce: the model should guarantee it, and a trace violates it: fix the model if it must hold, or move it to simulate.allows if it is a requirement on the code (the model lets it fail, the code must prevent it). The trace:
An example execution:

[State 0] { simulate::hello::applied: Set(), simulate::hello::count: 0 }

[State 1] { simulate::hello::applied: Set(2), simulate::hello::count: 1 }

[State 2] { simulate::hello::applied: Set(2, 3), simulate::hello::count: 2 }

[State 3] { simulate::hello::applied: Set(2, 3), simulate::hello::count: 3 }

[violation] Found an issue (32ms at 31 traces/second).
[...]
```

The graph finds the *shortest* one, and `-out` writes it as a Quint run
(`-out` makes its directory):

```console
$ gotogether simulate -graph -binding hello.binding.yaml -out /tmp/hello/cex.qnt
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), its state graph by the Go evaluator (at most 100000 states)
  graph   truncated by the state bound (100000): 100000 states, 485668 transitions, depth 14287, 6.7s
  FAIL    exactlyOnce: the model should guarantee it, and a reachable state violates it: fix the model if it must hold, or move it to simulate.allows if it is a requirement on the code. The shortest counterexample:
    0. init
         applied = Set(), count = 0
    1. apply(id = 1)
         applied = Set(1), count = 1
    2. apply(id = 1)
         applied = Set(1), count = 2
    replay: run r = init.then(apply(1)).then(apply(1)).expect(not(exactlyOnce))
  [...]
  wrote   /tmp/hello/cex.qnt: quint test --main simulate --match 'gtg_' /tmp/hello/cex.qnt replays each counterexample
$ quint test --main simulate --match gtg_ /tmp/hello/cex.qnt

  simulate
    ok gtg_violates_exactlyOnce passed 1 test(s)

  1 passing (164ms)
```

Two things to notice. The graph is now **truncated**: without the guard
`count` grows for ever, so the state space is infinite and the search
stopped at its bound. A violation found in a truncated graph is still a
real one (the path is there, and quint replays it); what a truncated
graph cannot give is a proof: an invariant it never saw broken is
reported `bounded`, never `holds` (section 5). And the counterexample is
two steps, where quint's sample took three and wandered: the graph is
searched breadth first, so the first violation found is a shortest.

Put the model back:

```sh
git checkout -- model/hello.qnt
```

## 3. A deadlock, or the end the model means (twophase)

[examples/twophase](../../examples/twophase) is two-phase commit written
in Choreo: a coordinator and three participants. Its binding classifies
both invariants as guaranteed:

```console
$ cd ../twophase
$ gotogether simulate -graph -binding twophase.binding.yaml
simulate twophase.binding.yaml: twophase(PARTICIPANTS = Set("p1", "p2", "p3")), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 99 states, 286 transitions, depth 7, 0.0s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  holds   consistency: no reachable state violates it (the whole graph, 99 states)
  holds   commitIsDecided: no reachable state violates it (the whole graph, 99 states)
  ends    9 reachable states the step cannot leave, each terminal (simulate.terminal)
  covered prepare 75 in 56 states, abort 75 in 56 states, abortAsInstructed 96 in 56 states, commitAsInstructed 12 in 7 states, decideCommit 1 in 1 state, decideAbort 27 in 27 states
```

`ends`: 9 states have no transition enabled at all. Unlike hello's quiet
state, nothing can happen there. The graph checks deadlocks as TLC
does, so a state with no way out fails, unless the binding says it is
the end the model means:

```yaml
simulate:
  holds: [consistency, commitIsDecided]
  terminal: 'NODES.forall(n => stage(n) == Committed or stage(n) == Aborted)'
```

Delete that line and run it again:

```console
$ sed -i '/terminal:/d' twophase.binding.yaml
$ gotogether simulate -graph -binding twophase.binding.yaml | cut -c1-160
[...]
  FAIL    deadlock: 9 reachable states the step cannot leave. If the model means to stop there, say where with simulate.terminal (a predicate over the state); e
    0. init
         s = { events: Map("c" -> Set(), "p1" -> Set(), "p2" -> Set(), "p3" -> Set()), extensions: (), messages: Map("c" -> Set(), "p1" -> Set(), "p2" -> Set(),
    1. abort(node = "p1") choosing transition = { effects: Set(), post_state: { process_id: "p1", role: Participant, stage: Aborted } }
    [...]
    4. abort(node = "p3") choosing transition = { effects: Set(), post_state: { process_id: "p3", role: Participant, stage: Aborted } }
    [...]
    replay: run r = init.then(abort("p1")).then(decideAbort).then(abort("p2")).then(abort("p3")).then(step.fail())
```

The nearest deadlock is every node aborted: the protocol's end. Saying
so with `terminal:` is the fix; a deadlock somewhere the predicate does
not hold would still fail. When a deadlock is not meant, `gotogether
nav -binding twophase.binding.yaml` walks the states and shows, for each
action, the first conjunct of its guard that is false there.

```sh
git checkout -- twophase.binding.yaml
```

## 4. A refutation: the coordinator decides early

A classic design error: the coordinator commits once *any* participant
has prepared, not all of them. Make it in the model (one guard), and ask
the graph:

```console
$ sed -i 's/ctx.messages.preparedIn() == PARTICIPANTS/ctx.messages.preparedIn().size() > 0/' model/twophase.qnt
$ gotogether simulate -graph -binding twophase.binding.yaml -out /tmp/twophase/cex.qnt | grep -v '^         '
simulate twophase.binding.yaml: twophase(PARTICIPANTS = Set("p1", "p2", "p3")), its state graph by the Go evaluator (at most 100000 states)
  graph   complete: 147 states, 412 transitions, depth 7, 0.0s
  sampled quint's 50 traces of up to 30 steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)
  FAIL    consistency: the model should guarantee it, and a reachable state violates it: fix the model if it must hold, or move it to simulate.allows if it is a requirement on the code. The shortest counterexample:
    0. init
    1. prepare(node = "p1") choosing transition = { effects: Set(Broadcast(ParticipantPrepared("p1"))), post_state: { process_id: "p1", role: Participant, stage: Prepared } }
    2. decideCommit choosing transition = { effects: Set(Broadcast(CoordinatorCommit)), post_state: { process_id: "c", role: Coordinator, stage: Committed } }
    3. commitAsInstructed(node = "p1") choosing transition = { effects: Set(), post_state: { process_id: "p1", role: Participant, stage: Committed } }
    4. abort(node = "p2") choosing transition = { effects: Set(), post_state: { process_id: "p2", role: Participant, stage: Aborted } }
    replay: run r = init.then(prepare("p1")).then(decideCommit).then(commitAsInstructed("p1")).then(abort("p2")).expect(not(consistency))
  holds   commitIsDecided: no reachable state violates it (the whole graph, 147 states)
  ends    15 reachable states the step cannot leave, each terminal (simulate.terminal)
  covered prepare 111 in 86 states, abort 111 in 86 states, abortAsInstructed 96 in 56 states, commitAsInstructed 48 in 37 states, decideCommit 19 in 19 states, decideAbort 27 in 27 states
  wrote   /tmp/twophase/cex.qnt: quint test --main simulate --match 'gtg_' /tmp/twophase/cex.qnt replays each counterexample
$ quint test --main simulate --match gtg_ /tmp/twophase/cex.qnt

  simulate
    ok gtg_violates_consistency passed 10000 test(s)

  1 passing (1697ms)
```

Four steps: p1 prepares, the coordinator commits, p1 commits, p2 aborts.
Each step names the Choreo transition it chose. The graph is complete,
so this is a refutation of the design for three participants; the
`grep -v` only hid the state after each step. The same check runs in
Go (`TestGraphFindsEarlyDecision`, through `validate.NewChecker` and
`model.Analyzer.Graph`), and `MODEL=early-decision ./run-demo.sh` runs it as a
demo.

```sh
git checkout -- model/twophase.qnt
```

## 5. What a bound does to a verdict (quorumkv)

Most models of real systems do not finish. [examples/quorumkv](../../examples/quorumkv)
(three replicas, two keys, values written and observed) is one. Bound
the search by depth:

```console
$ cd ../quorumkv
$ gotogether simulate -graph -depth 3 -binding quorumkv.binding.yaml | cut -c1-200
simulate quorumkv.binding.yaml: quorumkv(KEYS = Set("a", "b"), REPLICAS = Set("r1", "r2", "r3")), its state graph by the Go evaluator (at most 100000 states, depth 3)
  graph   truncated by the depth bound (3): 3360 states, 14416 transitions, depth 3, states at the bound not expanded, 0.4s
  [...]
$ gotogether simulate -graph -depth 6 -binding quorumkv.binding.yaml | cut -c1-200
simulate quorumkv.binding.yaml: quorumkv(KEYS = Set("a", "b"), REPLICAS = Set("r1", "r2", "r3")), its state graph by the Go evaluator (at most 100000 states, depth 6)
  graph   truncated by the state bound (100000): 100000 states, 1227975 transitions, depth 6, 35.1s
  [...]
```

Depth 3 is 3360 states in under a second; depth 6 hits the state bound
after 35 s. Truncated, an invariant no explored state breaks is
`bounded`, never `holds`. quorumkv's only property is an eventual one
(the next tutorial's), so see it on hello with a tiny bound:

```console
$ cd ../hello
$ gotogether simulate -graph -states 5 -binding hello.binding.yaml
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), its state graph by the Go evaluator (at most 5 states)
  graph   truncated by the state bound (5): 5 states, 6 transitions, depth 2, 0.0s
  bounded exactlyOnce: none of the 5 states explored violates it, which proves nothing past them (the graph is truncated: raise -states or -depth (simulate.search.states, simulate.search.depth) for a proof)
  never   done: no state explored enables it (within the bound)
  covered apply 5 in 2 states, replay 1 in 1 state
  sampling 1 possible the Go evaluator did not find with quint (1000 traces of 30 steps): not part of the graph's verdicts
  possible a retry recognised: reached by apply(id = 1), replay(id = 1) (the Go evaluator's search)
  possible done: reached (quint's sampled trace: apply, replay, apply, apply, replay, replay, done; the Go evaluator: not found within the bound (5 states, depth 2; the states bound cut the search))
```

The bound is printed with the verdict, and so is what it hid (`never
done ... within the bound`). For a model the size of quorumkv, prove the logic on a smaller instance (fewer replicas
or keys, by `-const`), and keep sampling, and the runs' own checks, for
the full one. A binding can set the bound it wants for CI, one for the
graph and the possibles alike: `simulate.search: {states, depth}`
(`format: gotogether.binding/2`). In `go test` the same proof is a
claim, failing where the graph is truncated:

```go
func TestModelProves(t *testing.T) {
	qtest.Model(t, "hello.binding.yaml").Expect(t, qtest.Proves("exactlyOnce"), qtest.NoDeadlocks())
}
```

## What you saw

| line | meaning |
|---|---|
| `graph complete` | every reachable state of this instance was visited |
| `graph truncated by ...` | a bound cut the search: violations are real, passes prove nothing past it |
| `holds` | no reachable state violates it (only in a complete graph) |
| `bounded` | no explored state violates it (truncated) |
| `FAIL ... The shortest counterexample` | a path from init, breadth first; `replay:` is the Quint run, `-out` writes them all |
| `ends` / `FAIL deadlock` | states with no transition: meant (`simulate.terminal`), or a deadlock |
| `quiet` | states only a stutter leaves: not deadlocks |
| `sampled` | quint's cross-check of the evaluator |

## Read next

- [Liveness: a system that can stay stuck](liveness.md): the same graph,
  searched for cycles.
- The gotogether-workflow skill, step 1 ("Model and simulate before
  writing code"): [skills/gotogether-workflow/references/workflow.md](../../skills/gotogether-workflow/references/workflow.md).
- [docs/DESIGN.md](../DESIGN.md) §61, the state-graph explorer, for how
  states are keyed, how edges are labelled, and what the cross-check
  compares.
- [examples/twophase](../../examples/twophase): the same model bound to
  code, explored under faults, and driving the code (connect).
