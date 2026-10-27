# counters: grain's counter example on grist

grist's slice 1 application ([docs/grist.md](../../docs/grist.md) §8.1):
ObneyAI/grain's counter example, without float averages, written as
grist's blocks and checked against a Quint model with no annotation of
its own. grist records every block's step (each commit and committed refusal at
the store, each query on return, each effect), so what the application
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
| [counters.calls.binding.yaml](counters.calls.binding.yaml) | the clients' calls (actor `<tenant>.clients`, recorded in every run beside the store's steps), linearized |
| [testdata/mutants](testdata/mutants) | grain's two bugs, each a file replacing the app's of its name (a `go test -overlay`) |

## The model

The model's state is one tenant's log (each tenant's trace is its own
instance), the processors' checkpoints, the batch the last step appended,
the effect steps and the command ids answered. A committed create's
precondition is that no counter has the name, that its read set covers
the name's read (`covers(reads, nameRead(name))`: a runtime that loses
the read is caught at every commit), that its condition held
(`conditionHolds(log, readPos, reads)`, the read set from the step) and
that its id was never answered (`freshKey(answered, id)`); a refusal
commits its marker too, so its id is answered and a late duplicate
cannot commit after it. `average(trigger)` appends the sum and count of
the increments in `log.slice(0, trigger)`; `welcomeEffect` may repeat,
under the trigger's key (`keyStable`), before `welcome(trigger, ok)`
checkpoints it with what its run reached (`atLeastOnceDone`: notified
after an ok send, `grist.failed` after three failed ones).
`uniqueNames` and `averageAsOfTrigger` are the design's (`simulate:
holds`); `everyCounterWelcomedOrFailed` is judged after the run's last
settle.

## Which check catches which mutant

| test | configuration | catches | rate (2026-10-27) |
|---|---|---|---|
| `TestFaithful` | the app at Serializable and ReadCommitted (Snapshot's runs are Serializable's: every append writes the head), one tenant, names from two letters; commit faults (`store.counters.commit`), node crashes, lost and late requests | nothing: `Conforms`, `All(Settles)`, both faults reached | 40 of 40 conform |
| `TestFaithfulAtClients` | the same, recorded at the clients (`Invoke`/`Done`), `qtest.Linearize()` | nothing: a retry's duplicate would be a later read's `Mismatch` | 20 of 20 |
| `TestFaithfulAtClaim1` | claim 1's configuration (two clients creating in one tenant, no faults, ReadCommitted), the app as it is | `None(Rejects("createCounter"))`: claim 1's contrast; with grist's condition taken out of the runtime, 13 of 20 runs reject | 40 of 40 conform |
| `TestDuplicateRequests` | the API duplicating and delaying 3 requests in 10 (`rpc.api.request`, `rpc.api.delay`), creates, increments and shows in one tenant; at the commits and at the clients | nothing: a key's first answer is final; without grist's markers 40 of 40 runs reject at the commits (33 of 40 fail at the clients), without refusal markers 17 of 20 (a refused command's late duplicate committing) | 40 of 40 on each side |
| `TestWelcomeDuplicate` | a `qenv.Script` of `grist.welcome.effect`'s `crashAfter`; the same then three lost sends; `Exhaust` within one fault | the duplicate welcome: admitted by `counters.binding.yaml`, `All(Violates("welcomedOnce"))` under the script, `None` without faults, `Some` within one fault; an ok send then three failures checkpoints `grist.failed`, admitted (`atLeastOnceDone`) | 10 of 10 scripted; 13 of 105 exhausted |
| `TestMutant` (`blind`: create `Unconditioned`, grain's create-counter) | two clients creating in one tenant, no faults, ReadCommitted | `Some(Rejects("createCounter"))`: a second create of a name commits where the model's precondition is false | 30 of 40; under Serializable 0 of 120, the race's loser retrying a transient `ErrConflict` and deciding again |
| `TestMutant` (`dispatchDirect`: average appends through the store and returns `Skip`, grain's processor) | creates and increments in one tenant, without and with faults | `Some(Mismatch("average"))`: the step records no event where the model appends one, and the raw append shifts every later head | 24 of 30 without faults, 25 of 30 with |
| `grist/storetest` (grist's own tests) | memstore, simstore at three levels; mutant stores whose `Read` ignores the tenant, whose conditions are checked outside the lock, whose `Wait` returns at once | each mutant fails its check (`TenantIsolation`, `ConcurrentAgainstBruteForce`, `WaitWakes`), the rest pass | every run |

`TestMutants` builds the test binary with each mutant's overlay and runs
`TestMutant` in it. Under `-short` each claim runs at fewer seeds, each
with a miss chance under 5%.

## Run it

```sh
go test -short ./...                 # every claim, at CI's seeds
./run-demo.sh                        # claims 2 and 3
MUTATION=blind ./run-demo.sh         # claim 1
MUTATION=dispatch-direct ./run-demo.sh
COUNTERS_COST=752 go test -run TestValidationCost -v .   # a run of about 1000 steps, judged
```

Judging one tenant's run (one seed, the explorer's determinism rerun
included, a loaded 4-core machine): 214 steps in 6.0 s, 999 steps in
25.3 s (`COUNTERS_COST=752`), of which the eventual val's own replay is
0.7 s and 7.1 s; the simulation itself takes well under a second.
