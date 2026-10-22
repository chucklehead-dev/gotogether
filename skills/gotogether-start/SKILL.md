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
| Is it correct under crashes, lost responses, redelivery, isolation levels? | `qenv` simulation, `qtest.System` (`Explore`, `Sweep`, `Fuzz`), `qhegel.Shrink` | gotogether-faults |
| Which code did no run reach, and does the model constrain the code that ran? | `qweave -cover`, `qweave -mutate`, `System.Mutate`, `x.Evidence()` | gotogether-faults (its coverage reference) |
| Does it recover after a load spike or a slow dependency? | `qmeta` chain, `qload` simulation, `qhegel.FindTrigger`, alert rules | gotogether-metastability |
| My system is an HTTP server, a gRPC service, or Kafka/Kinesis consumers and publishers | the seams and examples: kvstore, grpcledger (`simgrpc`), streams (a simulated log, kfake) | gotogether-faults (its services reference) |
| My code is a whole program (goroutines, sockets, timers), not written against qenv's seams | run it unchanged on gosim (`qgosim`): the scheduler, network and clock simulated, choices from qenv | gotogether-faults (its gosim reference) |
| I cannot change the code to record steps or use seams | weave it: `//quint:action` and qenv's sync/goroutine aspects, with `qweave go test` or Orchestrion | gotogether-faults (its weaving reference), gotogether-traces |
| My system is a Kubernetes controller or a Crossplane provider | simulated API servers (fleetrollout's kubesim, crossdb's interceptors), then real kwok or kind clusters, one model | gotogether-faults (examples/fleetrollout, examples/crossdb) |
| Seeds and Hegel found nothing; I want coverage-guided search | `System.Fuzz` (the run's choices from the fuzzer's bytes, the model the oracle) | gotogether-faults (its fuzzing reference) |
| My model is written in Choreo | name each transition (`choreo::step_with`), then any workflow | gotogether-model (Choreo section) |
| I am starting a component or an example, or setting up CI | the end-to-end workflow and triage | gotogether-workflow |
| A test failed: whose bug is it? | the verdict table and triage | gotogether-workflow (its verdicts reference) |

## First commands

```sh
quint typecheck model.qnt
gotogether scaffold -spec model.qnt -module M      # a binding skeleton
gotogether scaffold -system -binding m.binding.yaml -pkg app > system_test.go   # a qtest.System test to fill in
gotogether lint -binding m.binding.yaml -src .     # binding vs model, annotations vs binding
gotogether lint -sim .                             # code that escapes a simulation
gotogether validate -noitf -binding m.binding.yaml run.jsonl
```

The smallest simulated example is `examples/hello` (a counter, a lossy
network, a retrying client, one mutant): read it before the bigger ones.
Its shape is every simulated test's:

```go
func system(m Mutation) qtest.System {
	return qtest.System{Binding: "hello.binding.yaml", Run: func(c qenv.Chooser) error { return Run(c, m) }}
}

func TestNoDedupe(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoDedupe).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Some(qtest.Rejects("apply")))
	qhegel.Shrink(t, system(NoDedupe), lossy).Expect(t, qtest.Rejects("apply")) // Hegel, shrunk to one fault
	system(Faithful).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
}
```

In Go tests, `qtest.RequireQuint(t)` skips without quint; state what
each test expects with `qtest.Expect` and its claims, never by matching
report text.
