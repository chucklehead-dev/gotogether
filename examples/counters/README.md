# counters: grain's counter example on grist

grist's slice 1 application ([docs/grist.md](../../docs/grist.md) §8.1):
ObneyAI/grain's counter example, without float averages, written as
grist's blocks and checked against a Quint model with no annotation of
its own. grist records every block's step (each commit at the store,
each refusal and query on return, each effect), so what the application
writes is its decisions and folds.

| file | what it is |
|---|---|
| [counters.go](counters.go) | the events (`counter-created` tagged by counter and name, `counter-incremented`, `average-computed` with integer sum and count, `notified`) and read models (`counter-names`, plain; `totals`, keyed by counter) |
| [commands.go](commands.go) | `create-counter` (refused when `Exists(counter-created, name)`), `increment-counter` (refused for an unknown counter, read through `Totals.For`), the query `show-counter` |
| [processors.go](processors.go) | `average` (`Emit`: the tenant's sum and count as of each increment), `welcome` (`Effect` at least once: a mail through the `mailer` service, then `notified`) |
| [deps.go](deps.go) | the dependencies a processor reaches through its context: a simulated mailer (`qenv.Service`), the store (for the `dispatchDirect` mutant only) |
| [model/counters.qnt](model/counters.qnt) | one tenant's log: `counters` (the commit side), `countersCalls` (the client side), the read models as defs over the log |
| [model/grist.qnt](model/grist.qnt) | grist's guarantees (conditions, triggers, checkpoints, effects), vendored from [grist/model](../../grist/model/grist.qnt) |
| [counters.binding.yaml](counters.binding.yaml) | the commit-side steps: every parameter the step's field, the outcome as `result`, each commit's head and batch compared with the model's |
| [counters.once.binding.yaml](counters.once.binding.yaml) | the same steps judged by the test-only `welcomedOnce` |
| [counters.calls.binding.yaml](counters.calls.binding.yaml) | the clients' calls (`gristsim.AtClients`), linearized |
| [testdata/mutants](testdata/mutants) | grain's two bugs, each a file replacing the app's of its name (a `go test -overlay`) |

## The model

The model's state is one tenant's log (each tenant's trace is its own
instance), the processors' checkpoints, the batch the last step appended
and the effect steps. A committed create's precondition is that no
counter has the name, that its read set's condition held
(`conditionHolds(log, readPos, reads)`, the read set from the step) and
that its command id appended nothing before; a refusal's is that the
state through its position refuses it. `average(trigger)` appends the
sum and count of the increments in `log.slice(0, trigger)`;
`welcomeEffect` may repeat before `welcome` checkpoints the trigger.
`uniqueNames` and `averageAsOfTrigger` are the design's (`simulate:
holds`); `everyCounterWelcomedOrFailed` is judged after the run's last
settle.

## Which check catches which mutant

| test | configuration | catches | rate (2026-10-27) |
|---|---|---|---|
| `TestFaithful` | the app at Serializable, Snapshot and ReadCommitted; commit faults (`store.counters.commit`), node crashes, lost and late requests | nothing: `Conforms`, `All(Settles)`, both faults reached | 60 of 60 conform |
| `TestFaithfulAtClients` | the same, recorded at the clients (`Invoke`/`Done`), `qtest.Linearize()` | nothing: a retry's duplicate would be a later read's `Mismatch` | 20 of 20 |
| `TestWelcomeDuplicate` | a `qenv.Script` of `grist.welcome.effect`'s `crashAfter`; `Exhaust` within one fault | the duplicate welcome: admitted by `counters.binding.yaml`, `All(Violates("welcomedOnce"))` under the script, `None` without faults, `Some` within one fault | 10 of 10 scripted; 10 of 99 exhausted |
| `TestMutant` (`blind`: create `Unconditioned`, grain's create-counter) | two clients creating in one tenant, no faults | `Some(Rejects("createCounter"))`: a second create of a name commits where the model's precondition is false | 25 of 40 at ReadCommitted; 2 of 40 at Serializable, where the race's loser gets `ErrConflict` and its transient retry decides again |
| `TestMutant` (`dispatchDirect`: average appends through the store and returns `Skip`, grain's processor) | creates and increments in one tenant, without and with faults | `Some(Mismatch("average"))`: the step records no event where the model appends one, and the raw append shifts every later head | 19 of 30 without faults, 22 of 30 with |
| `grist/storetest` (grist's own tests) | memstore, simstore at three levels, a mutant store whose `Read` ignores the tenant | the mutant fails `TenantIsolation`, the rest pass | every run |

`TestMutants` builds the test binary with each mutant's overlay and runs
`TestMutant` in it. Under `-short` each claim runs at fewer seeds, each
with a miss chance under 5%.

## Run it

```sh
go test -short ./...                 # every claim, at CI's seeds
./run-demo.sh                        # claims 2 and 3
MUTATION=blind ./run-demo.sh         # claim 1
MUTATION=dispatch-direct ./run-demo.sh
COUNTERS_COST=740 go test -run TestValidationCost -v .   # a run of about 1000 steps, judged
```

Judging one tenant's run (one seed, the explorer's determinism rerun
included, a 4-core machine): 203 steps in 7 s, 1003 steps in 35 s; the
simulation itself takes 0.03 s and 0.35 s.
