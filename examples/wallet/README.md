# wallet: transfers applied once, posted always, under every fault

A wallet service on qenv ([wallet.go](wallet.go)): accounts in a
transactional store, transfers and deposits applied at most once by
their request id, an outbox that a relay drains to a queue, and a ledger
that posts every applied request. Clients retry a request until it is
answered, with the same id, over a network that loses, delays and
duplicates, and the bank's store may lose a commit's answer (applied,
reported lost); the bank, the ledger and the clients crash. Two accounts
share a credit line: a transfer from either is allowed while their sum
covers it. A request the bank must refuse (a transfer to its own source,
to an account it does not hold, of an amount that is not positive) is
answered false and moves nothing.

It is the example that uses, on one small system, the checks for what
can go wrong in a service that moves money: lost responses and retries
(`Exhaust`), isolation levels (the store's, and qhistory's reading of
its history), linearizability, calls left pending by a crash
(`ResolvePending`), an eventual property after a settle, and every fault
family at once.

| file | what it is |
|---|---|
| [model/wallet.qnt](model/wallet.qnt) | balances, each request's outcome and moves by id (a retry answers as the first time, an invalid request is refused), the credit line's rule, `post` (a request's move, as the ledger posted it) and the probe's `observe`; `noOverdraft` an invariant, `allPosted` and `postedApplied` eventual vals |
| [wallet.binding.yaml](wallet.binding.yaml) | a request's `ok` and a read's balance against the model's; `eventually: [allPosted, postedApplied]` |
| [wallet.go](wallet.go) | the bank (`Handle`, `Relay`), the ledger (`Consume`), the clients (`Do`), the workloads, `Run` on a qenv simulation, then the settle and the probe |
| [history.go](history.go) | the bank's store's transactions as a list-append history, for qhistory |
| [wallet_test.go](wallet_test.go) | one `qtest.System` per configuration, explored, searched or checked by qhistory |

Each account is an append-only list of entries (a request's id, its
amount, and the append's own tag), so the store's history is the
list-append workload Elle reads, and `qhistory.Check` judges it against
an isolation level as it would a real database's.

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestFaithful` | none | 10 seeds, one message fault in ten, commits answered lost, crashes of the bank and the ledger; transfers, a deposit, reads; linearized | every run conforms, settles and completes every call |
| `TestNoDedupeWithinOneFault` | `NoDedupe`: a retried request applied again | `Exhaust` within one fault: one client, one transfer | the faithful bank conforms in all 83 runs; with the mutant, the runs that lose a response or a commit's answer move the money twice (24 of 81 reject `observe`) |
| `TestUnlockedRecord` | `UnlockedRecord`: the request's record read without its lock | two transfers from one account at read committed, half the requests duplicated | a duplicate delivered while the first is in flight is applied twice (8 of 20 runs: a transfer's `ok` or a balance the model cannot have); the faithful bank conforms. Its first version did this |
| `TestInvalidRequests` | `NoValidation`: requests it must refuse applied | a transfer to its own source, to an account it does not hold, of a negative amount; a deposit of nothing | the faithful bank refuses each and conforms; the mutant answers ok where the model refuses (every run) |
| `TestReadThenWrite` | `ReadThenWrite`: the source account read without its lock | two transfers from one account, at read committed, then at snapshot isolation | at read committed both spend what one saw, a lost update (14 of 20 runs reject `observe`); at snapshot isolation the first committer wins and every run conforms |
| `TestWriteSkew` | `WriteSkew`: the credit line's other account read without its lock | one transfer from each account of the line, at snapshot isolation, no faults; linearized | the line goes negative: some transfer's `ok` no order explains (every run); the faithful bank conforms |
| `TestWriteSkewHistory` | `WriteSkew` | the bank's store's history, `qhistory.Check` | serializability forbids it: G2, two anti-dependencies (snapshot isolation allows it); the faithful bank's history, under every fault (commits of unknown outcome included), is serializable |
| `TestNoOutbox` | `NoOutbox`: the post published after the commit, not through the outbox | `Exhaust` within one fault: one transfer | no transfer's `ok` mismatches, and some run violates `eventually allPosted` (3 of 62): a crash between the commit and the publish lost the post |
| `TestReplicaRead` | `ReplicaRead`: a balance answered from the ledger's copy | deposits and reads, each on one account; linearized per account | the faithful bank conforms (some runs need the order: a read concurrent with a deposit); a read after a deposit misses it (`Mismatch("read")`) |
| `TestPendingTransfers` | none | the clients crash too; transfers only, the probe's balances after the settle | with `ResolvePending` every run conforms though some leave transfers pending; without it, the runs whose pending transfer took effect reject `observe` (9 of 12) |
| `TestAllFaults` | none | `GOTOGETHER_ALL_FAULTS`: clock skews and jumps, stalls, partitions between every pair of processes | every run conforms, settles and completes every call |

Each check is needed: without `Linearize`, some of `TestFaithful`'s runs
fail on the order the clients' answers came in, not the one their requests
took effect in; without `ResolvePending`, 9 of 12 crashed-client runs; without the eventual vals, `NoOutbox` passes;
without the ledger's own dedupe, a post is recorded twice (`Rejects("post")`).

## How the checks are written

**Retries and dedupe, searched.** A client retries a request with its id
until it is answered; the bank reads the request's record, locked, and
answers a retry as it answered the first time. A lost response (or a
commit whose answer is lost) is the whole bug of a bank without it, so a
search of every run within one fault finds it, and proves the faithful
bank applies every request once there:

```go
system(one).Exhaust(t, qtest.Bound{Faults: 1}).Expect(t, qtest.Conforms())
```

An exploration's first seed, and an exhaustive search's nominal run,
are made twice and must be the same: the wallet keeps every choice in
qenv's (request ids from a counter, accounts in a fixed order, the
outbox drained in id order).

**Isolation, two ways.** The model sees the effect of a skewed pair of
transfers (an `ok` no order explains); qhistory sees the cause, in the
store's history, named as Adya names it:

```go
d, _ := RunDeployment(chooser, Config{Mutation: WriteSkew, Workload: Skew})
rep, _ := qhistory.Check(qhistory.Serializable, d.History.OnAccounts())
rep.Has("G2") // true; at qhistory.SnapshotIsolation, rep.OK()
```

The skew's window is wide: uniform seeds interleave the two transfers in
39 runs of 40. PCT (which runs one task long before switching) found it
in 1 of 40 with `qenv.NewPCT(base, seed, 2, 400)`, whose 400 is the run's
expected number of scheduling choices: this run makes about 60, so the
change point fell past its end. Sized to the run, depth 2 finds it in 16
to 18 of 40, still below uniform: PCT is for a narrow window, one task far
ahead of another (quorumkv's `BlindRepair`), and its `steps` is the
nominal run's choice count.

**Pending calls, resolved.** A client that crashes mid-call never hears
back: its call stays pending (`Pending("transfer")`). Whether it took
effect, only the system knows; `qtest.ResolvePending()` searches the
resolutions (it had no effect, or took effect after some step), and the
probe's balances after the settle decide.

**Posted, eventually.** The ledger records a post (a request's move on an
account) where it commits; the post commutes with every request in the
model (it may be recorded before its request's caller heard back), and
`allPosted` (every applied request was posted) and `postedApplied` (every
post is one of its request's moves) are judged in the last state of a run
that settled.

**The settle, then a probe.** After the clients are done, `Run` settles
the wallet (the relay drains the outbox, the ledger posts) and the probe
records every account's balance as an `observe` step. `observe` is a
guard, not an expect: its action is enabled only when the model holds
that balance, so a probe that saw anything else is a step the model
cannot take (`Rejects("observe")`), which is how a request applied twice
shows when no client read the account.

**What `noOverdraft` checks.** The model never overdraws: a transfer the
funds do not cover is refused. So in a run the invariant never fails on
its own; a bank that overdraws shows first as an `ok` the model refuses,
or a balance it cannot have. It is there for `gotogether simulate`, which
checks the model's own design.

## Run it

```sh
go test -short .                     # needs quint on PATH; -short skips the slowest searches
./run-demo.sh                        # the faithful wallet, and under every fault family
MUTATION=write-skew ./run-demo.sh
```
