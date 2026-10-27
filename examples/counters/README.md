# counters: grain's counter example on grist

grist's slice 1 application ([docs/grist.md](../../docs/grist.md) §8.1):
ObneyAI/grain's counter example, without float averages, written as
grist's blocks and checked against a Quint model with no annotation of
its own. grist records every block's step (each commit and committed refusal at
the store, each query on return, each effect), so what the application
writes is its decisions and folds. It runs on three tiers: grist's
simulated store (simstore), the SQL tier in simulation (grist/sqlstore
over qsql: the real tier's statements), and SQLite (a real file), each
judged by the same bindings; its clients call the app in process, over
a simulated network, or over its HTTP API (simulated on qhttp, and real
in `TestRealHTTP` and `cmd/counters`).

| file | what it is |
|---|---|
| [counters.go](counters.go) | the events (`counter-created` tagged by counter and name, `counter-incremented`, `average-computed` with integer sum and count, `notified`) and read models (`counter-names`, plain; `totals`, keyed by counter) |
| [commands.go](commands.go) | `create-counter` (refused when `Exists(counter-created, name)`; the event records its creator, `Reads.Actor`), `increment-counter` (refused for an unknown counter, read through `Totals.For`), `retire-counter` (its creator's only: `not-creator` for another actor), the query `show-counter` |
| [processors.go](processors.go) | `average` (`Emit`: the tenant's sum and count as of each increment), `welcome` (`Effect` at least once: a mail through the `mailer` service, then `notified`), `payout` (`Effect` at most once: a retired counter's total paid through the `bank` service, then `paid-out`) |
| [deps.go](deps.go) | the dependencies a processor reaches through its context: a simulated mailer and a simulated bank (`qenv.Service`s; the bank records each payment it makes, `bankPays`), the store (for the `dispatchDirect` mutant only) |
| [model/counters.qnt](model/counters.qnt) | one tenant's log: `counters` (the commit side), `countersCalls` (the client side), the read models as defs over the log |
| [model/grist.qnt](model/grist.qnt) | grist's guarantees (conditions, triggers, checkpoints, effects), vendored from [grist/model](../../grist/model/grist.qnt) |
| [counters.binding.yaml](counters.binding.yaml) | the commit-side steps: every parameter the step's field, the outcome as `result`, each commit's head and batch compared with the model's |
| [counters.once.binding.yaml](counters.once.binding.yaml) | the same steps judged by the test-only `welcomedOnce` |
| [counters.retiring.binding.yaml](counters.retiring.binding.yaml), [counters.exactly.binding.yaml](counters.exactly.binding.yaml) | runs that retire counters: the commit side with `retire-counter`'s and the payout's steps (eventually every retirement checkpointed), and the test-only exactly-once `everyRetirementPaidOnce` |
| [counters.calls.binding.yaml](counters.calls.binding.yaml) | the clients' calls (actor `<tenant>/clients`, recorded in every run beside the store's steps), linearized |
| [sql_test.go](sql_test.go), [http_test.go](http_test.go), [real_test.go](real_test.go) | the claims on the SQL tier (`gristsim.Config.SQL`) and the HTTP tier (`gristsim.Config.HTTP`), and the real tier over HTTP on SQLite |
| [cmd/counters](cmd/counters/main.go) | the app's binary (`grist.Main`): `catalog`, `verify`, `serve -db sqlite:PATH` |
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
`payout` is at most once: its checkpoint step comes first
(`atMostOnceFirst`), its one payment after it (`atMostOnceEffect`),
and its outcome, if present, agrees with the payment
(`payoutOutcome`, `atMostOnceDone`); a lost payment or outcome is
admitted.
A retirement's ok requires its `principal` (the step's actor) to have
created the counter (`createdBy`), `not-creator` the negation; the
bank's own record of each payment (`bankPays`) is in `payments`.
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
| `TestWelcomeDuplicate` | a `qenv.Script` of `grist.welcome.effect`'s `crashAfter`; the same then three lost sends; `Exhaust` within one fault | the duplicate welcome: admitted by `counters.binding.yaml`, `All(Violates("welcomedOnce"))` under the script, `None` without faults, `Some` within one fault; an ok send then three failures checkpoints `grist.failed`, admitted (`atLeastOnceDone`) | 10 of 10 scripted; 13 of 142 exhausted |
| `TestMutant` (`blind`: create `Unconditioned`, grain's create-counter) | two clients creating in one tenant, no faults, ReadCommitted | `Some(Rejects("createCounter"))`: a second create of a name commits where the model's precondition is false | 25 of 40, on the SQL tier 25 of 40; under Serializable 0 of 120 (slice 1), the race's loser retrying a transient `ErrConflict` and deciding again |
| `TestMutant` (`dispatchDirect`: average appends through the store and returns `Skip`, grain's processor) | creates and increments in one tenant, without and with faults; on the SQL tier without | `Some(Mismatch("average"))`: the step records no event where the model appends one, and the raw append shifts every later head | 15 of 30 without faults, 22 of 30 with; 18 of 30 on the SQL tier |
| `TestSQLTier` | claim 2 on the SQL tier (sqlstore over qsql) at Serializable and ReadCommitted, commit faults at 15 in 100, node crashes; both bindings over the same runs | nothing: `Conforms`, `All(Settles)` at the store and at the clients, both faults reached | 40 of 40 on each side, at each level |
| `TestSQLDuplicateRequests` | `TestDuplicateRequests` on the SQL tier | nothing; without grist's markers 20 of 20 runs reject, without refusal markers 16 of 20 | 40 of 40 on each side |
| `TestSQLFaithfulAtClaim1` | claim 1's contrast on the SQL tier | `None(Rejects("createCounter"))`; with grist's condition taken out, 11 of 20 runs reject | 40 of 40 |
| `TestPayoutAtMostOnce` | one client creating and retiring counter `a`; a `qenv.Script` of `grist.payout.effect`'s `crashBefore` (after the checkpoint, before the payment), then `crashAfter`, then no faults | the lost payment: admitted by `counters.retiring.binding.yaml` (`Conforms`, `All(Settles)`), `All(Violates(eventually everyRetirementPaidOnce))` under `counters.exactly.binding.yaml` (the bank's own record of its payments: `bankPays`), `everyPayoutOutcomeLogged` holding; after the payment, `All(Violates(eventually everyPayoutOutcomeLogged))` (its paid-out lost) and nothing paid twice; without faults, or with the bank's answer lost (`grist.unknown`), both hold. With grist skipping an ok effect's outcome, every run violates `everyPayoutOutcomeLogged`. With grist's `AtMostOnce` run as at least once, 10 of 10 runs reject `payoutEffect` | 10 of 10 each |
| `TestMutant` (`checkpointFirst`: welcome declared `AtMostOnce` while the model says at least once) | one client creating in one tenant, no faults (full mode) | `Some(Rejects("welcome"))`: its checkpoint step comes before any effect step (`atLeastOnceDone`), which the faithful app never records: a declaration contradicting the model, not a lost mail | 10 of 10 |
| `TestMutant` (`silentPayout`: the payout without `OnSuccess`) | one client creating and retiring, no faults (full mode) | admitted by `counters.retiring.binding.yaml` (the outcome is optional); `Some(Violates(eventually everyPayoutOutcomeLogged))` under `counters.exactly.binding.yaml` | 6 of 6 |
| `TestPayoutTwoNodes` | `retirements` on two nodes (`gristsim.Config.Nodes`), their payout pollers racing; without faults, then with commit faults and crashes | nothing: `Conforms`, `All(Settles)`, exactly once without faults; a checkpoint left to the other poller after a failed append is reached (counted from the run's log); with grist taking the moved checkpoint for its own, the loser pays too: 10 of 10 runs reject `payoutEffect` | 10 of 10 each |
| `TestRetireByCreatorOnly` | two clients creating and retiring counter `a`, no faults | nothing: `Conforms`, `All(Settles)`, some retire refused `not-creator` at the store (the model's `retireCounter` judges it by the step's `principal`) | 6 of 6 |
| `TestMutant` (`anyoneRetires`: retire-counter without its creator check) | the same (full mode) | `Some(Rejects("retireCounter"))`: a retire by a non-creator commits | 6 of 6 |
| `TestFaithfulRetiring` | claim 2 with every command (retirements and the at-most-once payout among them), commit faults and crashes (full mode) | nothing: `Conforms`, `All(Settles)` under `counters.retiring.binding.yaml` | 20 of 20 |
| `TestHTTPTier` | claim 2 on the HTTP tier: the node's API (`grist.NewHandler`) on a `qhttp.Server`, the clients calling it with `grist/client` over qhttp's Transport (the API's 503s and refusals, requests dropped, duplicated and late, answers lost, at 0.15, crashes with requests in flight), commit faults; both bindings over the same runs (the store's in full mode) | nothing: `Conforms`, `All(Settles)` at the store and at the clients, answers lost and crashes reached; with the client sending each request under a new key, 12 of 20 runs fail at the clients (the store's side admits each key as a new command) | 40 of 40 on each side |
| `TestHTTPDuplicateRequests` | `TestDuplicateRequests` on the HTTP tier | nothing; with a new key per retry 9 of 20 runs fail at the clients | 40 of 40 on each side |
| `TestRealHTTP` | the real tier: `grist.NewHandler` on a real `http.Server` (a loopback port) over a SQLite file, four clients calling it with `grist/client` (each call recorded where the client sees it), steps to a `PerStep` file sink; the first node killed mid-phase (its sink dead at once, its relay behind), a second, with a relay of its own, taking the server's traffic at once; new counters in the second phase, a mailer taking 20 ms, a settle once the processors and the relay caught up | nothing: the file conforms to `counters.binding.yaml` (and settles) and, linearized, to `counters.calls.binding.yaml`; every client's answer has its store-side step; every fifth command's answer is lost after it was served, and its client's retry under the same key is answered from the marker (counted); the second relay records what the first never did, its copies of the first's records dropped as duplicates. A refusal's step dropped fails the answers' check, and so does a client retrying under another key; the settle recorded without waiting fails `everyCounterWelcomedOrFailed` | every run |
| `grist/storetest` (grist's own tests) | memstore, simstore and sqlstore (on qsql at three levels, SQLite, PostgreSQL with `GRIST_POSTGRES`, in CI); mutant stores whose `Read` ignores the tenant, whose conditions are checked outside the lock, whose `Wait` returns at once, whose `WaitSteps` waits on the head, whose rows' times are not in commit order, whose `WaitTenants` returns at once, that honour one tag of two | each mutant fails its check (`TenantIsolation`, `ConcurrentAgainstBruteForce`, `WaitWakes`, `StepLog`, `TenantFeed`, `ReadsAgainstBruteForce` and `ConditionsAgainstBruteForce`), the rest pass | every run |

`TestMutants` builds the test binary with each mutant's overlay and runs
`TestMutant` in it. Under `-short` (every push) each claim runs at fewer
seeds, each with a miss chance under 5%, and the SQL tier's claims that
repeat a mechanism a per-push test covers run in full mode only:
`TestSQLDuplicateRequests`, `TestHTTPDuplicateRequests`, `TestHTTPTier`'s store side, `TestPayoutTwoNodes`' exactly-once half, the `silentPayout` and `anyoneRetires` mutants, `TestSQLFaithfulAtClaim1`, `TestFaithfulRetiring`, `TestPayoutAtMostOnce`'s exactly-once judgments, the `checkpointFirst` mutant, `blind`'s and
`dispatchDirect`'s SQL-tier halves, `TestSQLTier`'s Serializable half and
`TestFaithfulAtClients` (the clients' side is judged per push by
`TestDuplicateRequests` and `TestSQLTier`).

## Run it

```sh
go test -short ./...                 # every claim, at CI's seeds
./run-demo.sh                        # claims 2 and 3
MUTATION=blind ./run-demo.sh         # claim 1
MUTATION=dispatch-direct ./run-demo.sh
SERVE=1 ./run-demo.sh                # cmd/counters served on SQLite, driven with curl, its steps validated
COUNTERS_COST=752 go test -run TestValidationCost -v .   # a run of about 1000 steps, judged
```

Judging one tenant's run (one seed, the explorer's determinism rerun
included, a loaded 4-core machine): 214 steps in 6.0 s, 999 steps in
25.3 s (`COUNTERS_COST=752`), of which the eventual val's own replay is
0.7 s and 7.1 s; the simulation itself takes well under a second.
