---
name: gotogether-start
description: Start here for checking Go code against a Quint model with gotogether - a question-to-tool table that picks the workflow (model-based tests with connect, Hegel with qhegel, trace validation with qobs/binding/validate, simulated fault injection with qenv and qtest.System, metastability with qload/qmeta, coverage, mutants, sweeps, fuzzing, Choreo models) and the skill to load next. Use when a task mentions Quint and Go together and it is not yet clear which part of the toolkit applies, or when asked what the toolkit can do.
---

# gotogether: which tool for which question

Find the question, then load the skill it names; each skill loads its own
references when the task needs them.

| Question | Tool | Skill |
|---|---|---|
| I have Go code and no model yet | write the model first, simulate its invariants | gotogether-model, gotogether-patterns |
| What should the model allow? Why did a buggy system pass? | patterns: requirement vs solution, the environment's real guarantees | gotogether-patterns |
| Does my code do what the model's traces do? | `connect.Run` (model drives the code, state compared) | gotogether-mbt |
| Does the code accept inputs the model forbids? | Hegel drives, the model judges (`qhegel.Test`, `FindViolation`) | gotogether-hegel |
| Do runs of the real system (tests, staging, production) conform? | record steps (`qobs`, `//quint:action`, OTel spans, `convert`), bind, `gotogether validate` | gotogether-traces |
| Is it correct under crashes, lost responses, redelivery, isolation levels? | `qenv` simulation, `qtest.System` (`Explore`, `Exhaust`, `Sweep`, `Fuzz`), `qhegel.Shrink`; `qhistory.Check` for a store's or a real database's history | gotogether-faults, examples/wallet |
| Is my store or client library linearizable (every call takes effect at one point between its invocation and its answer)? | record each call with `qobs.Invoke` and `Call.Done`, check with `qtest.Linearize()` or `LinearizeByKey("key")` (`gotogether validate -linearize` for a step log); `Pending` for calls never answered | gotogether-traces (its emitting reference), examples/quorumkv |
| Does the system finish its work and converge after the faults stop (liveness)? | `Sim.Settle` (or `SettleCore` around a quorum), `eventually:` vals in the binding, `qtest.Settles()`, `Completes()` | gotogether-faults, gotogether-patterns (eventual properties) |
| Did a run (a driver's session, an end-to-end test) finish its journey, or only conform up to where it stopped? | the journey's end as an eventual val on a ghost flag, `qobs.Settled`, `qtest.Finishes()`; `gotogether validate -require-settle` (or `complete: true`) | gotogether-patterns (eventual properties), gotogether-workflow (its verdicts reference) |
| Does what a browser explorer or an end-to-end suite did (Bombadil, Playwright) conform, and did each journey finish? | an adapter from the driver's trace: each request a click sent as an invocation record, completed only by its own answer, its own times and refs; the server's effects recorded where it makes them; `qtest.Finishes()`, `validate -manifest -run-info` | gotogether-traces (its external-drivers reference), examples/webjobs |
| Which of a driver's actions (a failing Bombadil journey, a Go client's session) does the failure need? | `qtest.ShrinkActions`: reruns on the real system after a `qtest.Resetter`, calibrated for a failure that shows only sometimes, the original's failure kept (`SameFailure`), `ErrDiverged` never a pass; outside go test, `gotogether shrink` reruns any driver command per candidate | gotogether-traces (its external-drivers reference) |
| Does the whole system survive the faults my model's traces have? | `System.FollowTraces`: the model's fault actions (`qtest.FaultMap`) become the simulation's faults, at the trace's points; everything else is the system's | gotogether-faults |
| Which code did no run reach, and does the model constrain the code that ran? | `qweave -cover`, `qweave -mutate`, `System.Mutate`, `x.Evidence()` | gotogether-faults (its coverage reference) |
| Does it recover after a load spike or a slow dependency? | `qmeta` chain, `qload` simulation, `qhegel.FindTrigger`, alert rules | gotogether-metastability |
| My system is an HTTP server, a gRPC service, or Kafka/Kinesis consumers and publishers | the seams and examples: kvstore, grpcledger (`simgrpc`), streams (a simulated log, kfake) | gotogether-faults (its services reference) |
| My code is a whole program (goroutines, sockets, timers), not written against qenv's seams | run it unchanged on gosim (`qgosim`): the scheduler, network and clock simulated, choices from qenv | gotogether-faults (its gosim reference) |
| I cannot change the code to record steps or use seams | weave it: `//quint:action` and qenv's sync/goroutine aspects, with `qweave go test` or Orchestrion | gotogether-faults (its weaving reference), gotogether-traces |
| My system is a Kubernetes controller or a Crossplane provider | simulated API servers (fleetrollout's kubesim, crossdb's interceptors), then real kwok or kind clusters, one model | gotogether-faults (examples/fleetrollout, examples/crossdb) |
| A bug needs one task far ahead of another, and seeds miss it | `qenv.NewPCT` as the exploration's chooser (priorities, change points), or `ExploreConfig.Steer` toward rare states | gotogether-faults |
| Seeds and Hegel found nothing; I want coverage-guided search | `System.Fuzz` (the run's choices from the fuzzer's bytes, the model the oracle) | gotogether-faults (its fuzzing reference) |
| My model is written in Choreo | name each transition (`choreo::step_with`), then any workflow | gotogether-model (Choreo section) |
| I am starting a component or an example, or setting up CI | the end-to-end workflow and triage | gotogether-workflow |
| A test failed: whose bug is it? | the verdict table and triage | gotogether-workflow (its verdicts reference) |

## The first hour

The `examples/...` paths in these skills are in the gotogether repository
(https://github.com/chucklehead-dev/gotogether/tree/main/examples), not in
your project: clone it to read and run them. In your own module, `go get
github.com/chucklehead-dev/gotogether@latest` (and `.../qhegel@latest`
for Hegel) and start from `gotogether scaffold`; the examples' `replace`
lines are only for working inside the repository. To run the CLI without
installing it, add it to the module first (`go get
github.com/chucklehead-dev/gotogether/cmd/gotogether`), then `go run
github.com/chucklehead-dev/gotogether/cmd/gotogether ...`; in a module
that replaces gotogether with a checkout, `go mod tidy` after the
`go get`.

1. Run the smallest example: `git clone
   https://github.com/chucklehead-dev/gotogether && cd
   gotogether/examples/hello && go test -v .` (with quint on PATH).
2. Read it, in this order: `model/hello.qnt` (what may happen),
   `hello.binding.yaml` (recorded steps to the model's actions),
   `hello.go` (the system, written against qenv's seams), `hello_test.go`
   (the system explored by seeds, a mutant caught, Hegel shrinking it).
3. For your component, write the model first and simulate its
   invariants (`gotogether simulate -binding`, `quint run --invariant`
   before a binding exists): gotogether-model.
4. Scaffold a binding and a System test, then record each model step
   where it takes effect, until the faithful system conforms.
5. Add a mutant and state how it is caught (`Rejects`, `Mismatch`,
   `Violates`). A check no mutant can fail proves nothing.
6. Let Hegel shrink the mutant's failure (`qhegel.Shrink`), and carry the
   model to the real environment (`gotogether validate`).

## Glossary

- **Model**: the Quint spec, the ground truth. A failing check means
  fixing the code or, deliberately, the model.
- **Step**: one model action the code took, recorded with its arguments,
  its outcome and observations (`obs.<name>`) where it took effect.
- **Binding**: YAML mapping recorded steps to the model's actions, their
  arguments and expected post-states.
- **Run**: one execution's steps; with qenv, also the choices that made
  it (replayable).
- **System**: an application bound to its model (`qtest.System`): every
  workflow (Explore, Sweep, Fuzz, Mutate, Shrink) runs its `Run`.
- **Chooser**: what makes every choice of a simulated run: schedules,
  faults, delays. Seeded, Hegel's, a replay, a script.
- **Fault kind**: a named fault a double declares (`rpc.counter.request`:
  `drop`, `duplicate`). Option 0 of every choice is nominal: no fault, so
  shrinking removes faults.
- **Seam**: an interface the code uses for its environment (`qenv.DB`, a
  service, `qenv.Now`), so a double can stand in for it.
- **Claim**: what a test expects of its runs: `Conforms`, `Rejects`
  (a step the model cannot take), `Mismatch` (a state the model did not
  expect), `Violates` (an invariant), over runs `Some`, `All`, `None`.
- **Mutant**: the system with a bug on purpose, to show a check can fail.
- **Weaving**: rewriting code at build time (`qweave`, Orchestrion) to
  record steps or simulate `sync` without changing the source.

## First commands

```sh
quint typecheck model.qnt
gotogether scaffold -spec model.qnt -module M -out m.binding.yaml      # a binding skeleton
gotogether scaffold -system -binding m.binding.yaml -pkg app -out system_test.go   # a qtest.System test to fill in
gotogether lint -binding m.binding.yaml -src .     # binding vs model, annotations vs binding
gotogether lint -sim .                             # code that escapes a simulation
gotogether validate -binding m.binding.yaml run.jsonl
```

The smallest simulated example is `examples/hello` (a counter, a lossy
network, a retrying client, one mutant): read it before the bigger ones.
Its shape is every simulated test's:

<!-- from examples/hello/hello_test.go -->
```go
func system(m Mutation) qtest.System {
	s := qtest.System{Binding: "hello.binding.yaml", Run: func(c qenv.Chooser) error { return Run(c, m) }}
	if m == NoDedupe {
		s.Mutant = "NoDedupe" // its runs are not the counter's: the suite's evidence leaves them out
	}
	return s
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system(Faithful).WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms(),
		// ...
		qtest.Reaches("rpc.counter.request").Faulted().InAtLeast(0.3),
		// ...
		qtest.Exercised())
}

func TestNoDedupeCountsRetries(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoDedupe).WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
		Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()))
	// ...
}

func TestHegelShrinks(t *testing.T) {
	// ...
	qhegel.Shrink(t, system(NoDedupe), hegel.WithTestCases(50), hegel.WithSeed(1)).
		Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
	// ...
}
```

In Go tests, `qtest.RequireQuint(t)` skips without quint; state what
each test expects with `.Expect(t, claims...)` on its run or
exploration, never by matching report text.
