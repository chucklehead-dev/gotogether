# counters: grain's counter example on grist

grist's slice 1 application ([docs/grist.md](../../docs/grist.md) §8.1):
ObneyAI/grain's counter example, without float averages, written as
grist's blocks and checked against a Quint model with no annotation of
its own. grist records every block's step (each commit and committed refusal at
the store, each query on return, each effect), so what the application
writes is its decisions and folds. It runs on three tiers: grist's
simulated store (simstore), the SQL tier in simulation (grist/sqlstore
over qsql: the real tier's statements), and SQLite (a real file), each
judged by the same bindings.

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
| [counters.calls.binding.yaml](counters.calls.binding.yaml) | the clients' calls (actor `<tenant>/clients`, recorded in every run beside the store's steps), linearized |
| [sql_test.go](sql_test.go), [real_test.go](real_test.go) | the claims on the SQL tier (`gristsim.Config.SQL`), and the real tier on SQLite |
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
| `TestFaithful` | the app at Serializable and ReadCommitted (Snapshot's runs are Serializable's: every append writes the head), one tenant, names from two letters; commit faults (`store.counters.commit`), node crashes, lost and late requests | nothing: `Conforms`, `All(Settles)`, both faults reached | 40 of 40 conform at each level |
| `TestFaithfulAtClients` | the same runs, judged at the clients (`<tenant>/clients`: `Invoke`/`Done`) by `counters.calls.binding.yaml`, `qtest.Linearize()` | nothing: a retry's duplicate would be a later read's `Mismatch` | 40 of 40 |
| `TestFaithfulAtClaim1` | claim 1's configuration (two clients creating in one tenant, no faults, ReadCommitted), the app as it is | `None(Rejects("createCounter"))`: claim 1's contrast; with grist's condition taken out of the runtime, 13 of 20 runs reject | 40 of 40 conform |
| `TestDuplicateRequests` | the API duplicating and delaying 3 requests in 10 (`rpc.api.request`, `rpc.api.delay`), creates, increments and shows in one tenant; at the commits and at the clients | nothing: a key's first answer is final; without grist's markers 19 of 20 runs reject at the commits, without refusal markers 18 of 20 (a refused command's late duplicate committing) | 40 of 40 on each side |
| `TestWelcomeDuplicate` | a `qenv.Script` of `grist.welcome.effect`'s `crashAfter`; the same then three lost sends; `Exhaust` within one fault | the duplicate welcome: admitted by `counters.binding.yaml`, `All(Violates("welcomedOnce"))` under the script, `None` without faults, `Some` within one fault; an ok send then three failures checkpoints `grist.failed`, admitted (`atLeastOnceDone`) | 10 of 10 scripted; 13 of 117 exhausted |
| `TestMutant` (`blind`: create `Unconditioned`, grain's create-counter) | two clients creating in one tenant, no faults, ReadCommitted | `Some(Rejects("createCounter"))`: a second create of a name commits where the model's precondition is false | 25 of 40, on the SQL tier 25 of 40; under Serializable 0 of 120 (slice 1), the race's loser retrying a transient `ErrConflict` and deciding again |
| `TestMutant` (`dispatchDirect`: average appends through the store and returns `Skip`, grain's processor) | creates and increments in one tenant, without and with faults; on the SQL tier without | `Some(Mismatch("average"))`: the step records no event where the model appends one, and the raw append shifts every later head | 15 of 30 without faults, 22 of 30 with; 18 of 30 on the SQL tier |
| `TestSQLTier` | claim 2 on the SQL tier (sqlstore over qsql) at Serializable and ReadCommitted, commit faults at 15 in 100, node crashes; both bindings over the same runs | nothing: `Conforms`, `All(Settles)` at the store and at the clients, both faults reached | 40 of 40 on each side, at each level |
| `TestSQLDuplicateRequests` | `TestDuplicateRequests` on the SQL tier | nothing; without grist's markers 20 of 20 runs reject, without refusal markers 16 of 20 | 40 of 40 on each side |
| `TestSQLFaithfulAtClaim1` | claim 1's contrast on the SQL tier | `None(Rejects("createCounter"))`; with grist's condition taken out, 11 of 20 runs reject | 40 of 40 |
| `TestRealSQLite` | the real tier: the node and four clients as goroutines on a SQLite file, the clients recording their calls (`gristsim.Do`), steps to a `PerStep` file sink; the first node killed mid-phase (its sink dead at once, its relay behind), a second with a relay of its own, new counters in the second phase, a mailer taking 20 ms, a settle once the processors and the relay caught up | nothing: the file conforms to `counters.binding.yaml` (and settles) and, linearized, to `counters.calls.binding.yaml`; every client's answer has its store-side step; the second relay records what the first never did, its copies of the first's records dropped as duplicates. A refusal's step dropped fails the answers' check; the settle recorded without waiting fails `everyCounterWelcomedOrFailed` | every run |
| `grist/storetest` (grist's own tests) | memstore, simstore and sqlstore (on qsql at three levels, SQLite, PostgreSQL with `GRIST_POSTGRES`, in CI); mutant stores whose `Read` ignores the tenant, whose conditions are checked outside the lock, whose `Wait` returns at once, whose `WaitSteps` waits on the head, whose rows' times are not in commit order, whose `WaitTenants` returns at once, that honour one tag of two | each mutant fails its check (`TenantIsolation`, `ConcurrentAgainstBruteForce`, `WaitWakes`, `StepLog`, `TenantFeed`, `ReadsAgainstBruteForce` and `ConditionsAgainstBruteForce`), the rest pass | every run |

`TestMutants` builds the test binary with each mutant's overlay and runs
`TestMutant` in it. Under `-short` (every push) each claim runs at fewer
seeds, each with a miss chance under 5%, and the SQL tier's claims that
repeat a mechanism a per-push test covers run in full mode only:
`TestSQLDuplicateRequests`, `TestSQLFaithfulAtClaim1`, `blind`'s and
`dispatchDirect`'s SQL-tier halves, `TestSQLTier`'s Serializable half and
`TestFaithfulAtClients` (the clients' side is judged per push by
`TestDuplicateRequests` and `TestSQLTier`).

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
