# Which tool for which question: the full table

From the gotogether-start skill (its SKILL.md says when to read this).

## gotogether: which tool for which question

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
| Did a run (a driver's session, an end-to-end test) finish its journey, or only conform up to where it stopped? | the journey's end as an eventual val on a ghost flag, `qobs.Settled`, `qtest.Finishes()`; `gotogether validate -require-finish` (or `complete: true`) | gotogether-patterns (eventual properties), gotogether-workflow (its verdicts reference) |
| Does what a browser explorer or an end-to-end suite did (Bombadil, Playwright) conform, and did each journey finish? | a request log (package qdriver: adapters/playwright writes it, `qdriver/bombadil` reads Bombadil's trace): each request an invocation, completed only by its own answer, its own times and refs; the server's effects recorded where it makes them; `gotogether validate -binding b.yaml server.jsonl requests.jsonl`, `qtest.Finishes()`, `validate -manifest -run-info` | gotogether-traces (its external-drivers reference), examples/webjobs |
| Which of a driver's actions (a failing Bombadil journey, a Go client's session) does the failure need? | `qtest.ShrinkActions`: reruns on the real system after a `qtest.Resetter`, calibrated for a failure that shows only sometimes, the original's failure kept (`SameFailure`), `ErrDiverged` never a pass; outside go test, `gotogether shrink` reruns any driver command per candidate | gotogether-traces (its external-drivers reference) |
| Does the whole system survive the faults my model's traces have? | `System.FollowTraces`: the model's fault actions (`qtest.FaultMap`) become the simulation's faults, at the trace's points; everything else is the system's | gotogether-faults |
| Which code did no run reach, and does the model constrain the code that ran? | `qweave -cover`, `qweave -mutate`, `System.Mutate`, `x.Evidence()` | gotogether-faults (its coverage reference) |
| Does it recover after a load spike or a slow dependency? | `qmeta` chain, `qload` simulation, `qhegel.FindTrigger`, alert rules | gotogether-metastability |
| My system is an HTTP server, a gRPC service, or Kafka/Kinesis consumers and publishers | the seams and examples: kvstore, grpcledger (`qenv/simgrpc`), streams (`qenv/simlog`, its Kafka and Kinesis modules) | gotogether-faults (its services reference) |
| My code is a whole program (goroutines, sockets, timers), not written against qenv's seams | run it unchanged on gosim (`qgosim`): the scheduler, network and clock simulated, choices from qenv | gotogether-faults (its gosim reference) |
| I cannot change the code to record steps or use seams | weave it: `//quint:action` and qenv's sync/goroutine aspects, with `qweave go test` or Orchestrion | gotogether-faults (its weaving reference), gotogether-traces |
| My system is a Kubernetes controller or a Crossplane provider | simulated API servers (`qenv/kubesim`, as fleetrollout uses it; crossdb's interceptors), then real kwok or kind clusters, one model | gotogether-faults (examples/fleetrollout, examples/crossdb) |
| A bug needs one task far ahead of another, and seeds miss it | `qenv.NewPCT` as the exploration's chooser (priorities, change points), or `ExploreConfig.Steer` toward rare states | gotogether-faults |
| Seeds and Hegel found nothing; I want coverage-guided search | `System.Fuzz` (the run's choices from the fuzzer's bytes, the model the oracle) | gotogether-faults (its fuzzing reference) |
| I am writing an event-sourced application and want it checked without annotations | `grist new <name>` (an app green on its first run), grist's blocks (commands conditioned on their reads, processors with checkpoints), `gristsim.Run` as a `qtest.System`, the model's skeleton and bindings generated from the catalog (`<app> model`), the app writing its rules | grist (examples/counters) |
| My model is written in Choreo | name each transition (`choreo::step_with`), then any workflow | gotogether-model (Choreo section) |
| I am starting a component or an example, or setting up CI | the end-to-end workflow and triage | gotogether-workflow |
| A test failed: whose bug is it? | the verdict table and triage | gotogether-workflow (its verdicts reference) |
