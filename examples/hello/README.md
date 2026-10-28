# hello: the smallest simulated system

A counter service behind a network that loses requests, loses responses
and delivers some requests twice; a client that retries until it hears
back; a model that says every request counts once. The whole system is
[hello.go](hello.go) (73 lines). Start here before the bigger examples.

| file | what it is |
|---|---|
| [model/hello.qnt](model/hello.qnt) | the requirement: a request is applied once, and seen again only as a replay; the client is done when every request was applied |
| [hello.binding.yaml](hello.binding.yaml) | the recorded steps as calls of the model's actions (`id` is the step's field of that name); what the model must be able to do (`simulate.possible`: a retry recognised, the client done) |
| [hello.go](hello.go) | the service (`Counter.Add`, which records `apply` or `replay`), the retrying client, `Run` on a qenv simulation |
| [hello_test.go](hello_test.go) | one `qtest.System`, explored by seeds; `TestMutant` says what catches each mutant |
| [testdata/mutants](testdata/mutants) | `noDedupe`: a `hello.go` whose `Add` counts a retried or duplicated request again, the bug planted by hand (a `go test -overlay`, built and run by `qtest.Mutants`) |

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestRetryPossible` | none: the model alone | the scenario `a retry recognised` (`qtest.NamedScenario`: `apply` of request 1, its `replay`, then `done`) searched in the model on its own (`qtest.Model(...).Expect(t, qtest.Possible(...))`); the binding's `simulate.possible` says the same of any retry (`gotogether simulate`) | possible, with the witness `apply(id = 1), replay(id = 1), ...`; a model too strict to replay (a guard no run meets) fails here, "impossible", where every run would still conform |
| `TestModelProves` | none: the model alone | `qtest.Proves("exactlyOnce")` and `qtest.NoDeadlocks()` over the model's whole state graph, then a model mutant whose replay counts again, `qtest.Refutes("exactlyOnce").InAtMost(2)` | proved for `REQUESTS = 1.to(3)` (8 states); the mutant refuted by `apply(id = 1), replay(id = 1)`, replayed by quint test |
| `TestFaithfulConforms` | none | 30 seeds, one message fault in five | every run conforms, and some run follows the scenario (`Some(retried)`) |
| `TestExhaustive` | none | every run within 2 delays and 2 faults (`System.Exhaust`) | all 4430 conform, the search complete |
| `TestMutationOfTheCounter` | every mutant `qweave -mutate` weaves (10), when woven (CI does) | 30 seeds each, and the client done in 90% of runs (`Takes("done")`) | `NoSurvivors`, three marked equivalent with their reasons; a client that sent too few requests is killed by `done`, an action that needs every request applied |
| `TestHegelFindsNothing` | none | Hegel makes every choice (`qhegel.Shrink`), full mode | nothing found (`Expect(t, qtest.Conforms())`) |
| `TestMutant` (`noDedupe`), run by `TestMutants` | `noDedupe`: a retried or duplicated request is counted again | `TestFaithfulConforms`' exploration; the same with no faults; every run within 1 fault; in full mode Hegel | some run's `apply` is rejected (the model has no second apply of a request), and no run follows the scenario (`None(retried)`: a retry is never recognised); with no faults every run conforms: no retry, no bug; one fault is enough for a rejected `apply`; Hegel shrinks it to that one fault (`ShrunkTo(1)`) |

A mutant is a directory of [testdata/mutants](testdata/mutants) whose
files replace the package's of the same names: `qtest.Mutants{}.Run(t)`
(`TestMutants`) finds each, builds the tests with its overlay and runs
`TestMutant` there, `qtest.Mutant()` naming it. The code under test
carries no switch for its bugs. `qweave -mutate` is the generated way
(`TestMutationOfTheCounter`).

The bug needs a fault to show: a lost response makes the client retry,
and a duplicate delivers the request twice. That is why the faults are
the environment's, chosen by the simulation, and every run is checked by
the model rather than by assertions written for the cases someone
thought of.

## Run it

```sh
go test -v .                    # needs quint on PATH: each test's summary, the mutant's catch in three lines
GOTOGETHER_VERBOSE=1 go test -v .   # each catch's whole report
./run-demo.sh                   # simulate 20 seeds, check the step logs with gotogether
MUTANT=noDedupe ./run-demo.sh   # the same, built with the mutant's overlay
```

Next: the same System runs under sweeps (`System.Sweep`) and the fuzzer
(`System.Fuzz`); [examples/checkout](../checkout) is a saga with
crashes, isolation levels and an outbox.
