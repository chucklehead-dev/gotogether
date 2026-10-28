# payd: an unmodified net/http + database/sql service, simulated

payd ([payd/](payd)) is a small payments service written as any Go
service is: `net/http` handlers over `database/sql`, a processor called
over HTTP, PostgreSQL's retry loop for serialization failures, and an
SDK ([payd/client](payd/client)) that retries a charge with its
idempotency key. It imports the standard library alone
(`TestPaydImportsNoToolkit`), and knows nothing of how it is tested.

Its tests run it, unchanged, in a qenv simulation through two adapters:
`qenv/qsql`, a database/sql driver over a `qenv.Store` at each isolation
level, and `qenv/qhttp`, a `Server` for its handler and a `Transport`
for its clients and its processor client. They run it for real too, on
SQLite (modernc.org/sqlite, pure Go) behind `httptest` servers. Both are
checked against one model. The planted bugs are Go build overlays
(`go test -overlay`), so payd's own files stay as they are.

| file | what it is |
|---|---|
| [payd/payd.go](payd/payd.go), [charge.go](payd/charge.go), [schema.go](payd/schema.go), [processor.go](payd/processor.go) | the service: `Migrate(ctx, db)`, `New(db, proc, procURL)`; POST /v1/charges with an `Idempotency-Key`, GET /v1/charges/{id} |
| [payd/client](payd/client/client.go) | the SDK: retries a charge with its key on a timeout, a reset, a refused connection or a 5xx |
| [model/payd.qnt](model/payd.qnt) | requests, responses, charges, debits, captures and the balances after the settle; the properties below |
| [payd.binding.yaml](payd.binding.yaml) | the steps as the model's actions, every parameter the step's field |
| [record.go](record.go) | `Recording`, the RoundTripper around the SDK's transport that records each attempt's request and response |
| [sim.go](sim.go) | the simulated deployment: payd's process (`qsql.Open`, `Migrate`, `srv.Handle(payd.New(...))` at every incarnation), its Store, the processor double, the clients |
| [real.go](real.go) | the real one: SQLite, `httptest`, clients on goroutines meeting at each key |
| [testdata/mutants](testdata/mutants) | each mutant's files, replacing payd's of the same name |
| [cmd/payd](cmd/payd/main.go) | payd on SQLite, with the processor double in process |

## The design

A charge is three steps, and no database transaction spans the call to
the processor:

1. **claim**: one transaction checks the account and inserts the key's
   row as pending (`INSERT INTO idempotency ... ON CONFLICT (key) DO
   NOTHING`). A key already done answers with its stored response
   (`Idempotent-Replayed: true`), and a key that came with another request
   answers 409.
2. **capture** at the processor, with the same `Idempotency-Key`.
3. **complete**: one transaction marks the key done if it is still
   pending (`UPDATE idempotency SET status = 'done' WHERE key = ? AND
   status = 'pending'`, a compare-and-set that only one of two attempts
   wins), debits the account, inserts the charge and stores the response.

Holding a transaction across the processor call would hold row locks and
a pooled connection across the network. A key found pending is driven
on, not refused: its attempt may have died between the steps, and the
processor's dedupe by key makes capturing again safe. Refusing it (the
`pendingConflict` mutant) strands the key, and the money captured with
it, for good. Charge ids come from the account's own counter
(`ch_<account>_<n>`), not from the key, so the claim is the one dedupe
that the precheck mutant takes away. `charges.idem_key` is UNIQUE too, as
a second guard. The service has no overdraft check: a charge's account
must exist, and its balance may go negative.

## Where the steps come from

Nothing is recorded in payd. Every step is recorded at an adapter or in
test code:

- `request` and `response` come from `Recording`, wrapped around the SDK's
  transport. A response carries what the attempt asked and, for a 2xx,
  the charge its body names (id, account, amount). An attempt that got no
  response (a timeout, a reset, a refusal, a body that never arrived) is
  status 0, and the client gets the same error.
- `charge`, `chargeChanged` and `debit` come from qsql's `Options.OnCommit`:
  each row a commit inserted into charges, each charge it updated or
  deleted, and each account balance it lowered. The commit is their
  linearization point (change data capture is the production analogue).
  In the real tier, the charges are read from the table after every
  handler has finished (`httptest.Server.Close` waits for them).
- `capture` comes from the processor double, where it commits.
- `balance` is each account's balance, read once the run settled, in both
  tiers.

A charge may be recorded before or after the response that names it, so
the model's invariants are about what was seen, in any order:

| property | what it says |
|---|---|
| `noDoubleCharge`, `noDoubleCapture` | a key is charged at most once, and captured at most once |
| `responseMatchesCharge` | every 2xx for a key names one charge, the key's: its id, account and amount |
| `chargeMatchesRequest` | a charge is of what its key first asked |
| `noRefusalOfCharged` | a key refused (4xx) when it asked what it first asked is never charged or captured |
| `chargesImmutable` | no charge is updated or deleted |
| `noDebitWithoutCharge` | no account loses more than its charges add up to |
| `balanceMatchesCharges` | a balance read after the settle is the initial one less the account's charges |
| `answeredCharged` (eventual) | every key answered 2xx has exactly one charge, the one named, and exactly one capture |
| `capturedCharged` (eventual) | every captured key was charged |
| `allAnswered` (eventual) | every key asked for validly was answered 2xx: the SDK retries until it is, and the faults stop at the settle |

The real tier injects no faults (no crash, no lost response, no lost
commit answer). It shows what concurrency alone does: each key is sent
by three clients that wait for each other at the key, so they send it
together.

## What each test catches

Rates were measured on 2026-10-26. A claim's seeds are chosen so that it
misses with a chance under 5% at its measured rate. Under `-short` each
mutant states its cheapest claim; the rest run in the full tests.

| test | mutant | check | verdict |
|---|---|---|---|
| `TestFaithful` | none | 20 seeds at read committed, snapshot isolation and serializable (2 under `-short`), then 20 (4) with a third of payd's requests duplicated or late. Every fault is on: lost, duplicated and late requests and responses to payd and to the processor, refused connections, broken database connections, commits applied with their answer lost (08006), and payd crashing (its crash window included). The Store's history checker is on | every run conforms and settles; at least half the duplicated runs duplicate a request and crash payd (measured: 60 of 60 and 59 of 60) |
| `TestOneFault` | none | `Exhaust` within one fault: one client's one charge | all 78 runs conform |
| `TestReal` | none | SQLite behind real HTTP: 30 keys (10 under `-short`), each sent by three clients at once | conforms; one charge per key |
| `TestKeyReuse` | none | a key sent again, again with another amount, no key, no account | the same 201 replayed, 409, 400, 404; one debit |
| `TestChargeEdited` | none: an operator edits a charge after the run | one run | violates `chargesImmutable` |
| `TestMutants/precheck` | `precheck`: the key looked up in its own autocommit, outside the transaction that charges; no UNIQUE on `charges.idem_key` | two tabs, no faults; one client with half its requests duplicated; for real (16 keys) | two tabs: 40 of 40 runs charge twice; duplicates: 22 of 60; for real: 15 of 15 runs (14 to 23 charges for 10 keys). Never a double capture: the processor got the key |
| `TestMutants/noForwardKey` | `noForwardKey`: the capture sent without the key | `Exhaust` within one fault; for real (16 keys) | 30 of 76 runs capture twice; for real, without a fault: 15 of 15 runs. Never a double charge |
| `TestMutants/pendingConflict` | `pendingConflict`: a key found pending answers 409 "in progress" | two tabs, no faults; `Exhaust` within one fault; for real | two tabs: 40 of 40 runs refuse a key that is charged (`noRefusalOfCharged`); within one fault 29 of 78 runs refuse a captured key, and 5 more never answer it 2xx (`eventually allAnswered`); for real: 15 of 15 |
| `TestMutants/debitFirst` | `debitFirst`: the debit ahead of the compare-and-set, so the losing attempt debits without a charge | two tabs, no faults; for real | `noDebitWithoutCharge` and `balanceMatchesCharges` in 40 of 40 runs; for real (no debit is seen there) the balances: 15 of 15 |
| `TestMutants/lostRace` | `lostRace`: the losing attempt answers 409 "already processed" instead of the stored response | two tabs, no faults; for real | `noRefusalOfCharged` in 40 of 40 runs; for real 15 of 15 |
| `TestMutants/centsInBody` | `centsInBody`: the response's amount in cents | one charge | `responseMatchesCharge` in every run |
| `TestPaydImportsNoToolkit` | none | `go list -deps ./payd/...` | the standard library alone |

The real runs of 15 of 15 were measured with 10 keys (pendingConflict's
and noForwardKey's also with 4); the tests use 16 keys. A key's tabs race
in some runs only (in lostRace's, about one key in four), so a run of
many keys rarely misses.

**Measured once on 2026-10-26, not asserted.** Built with each mutant's
overlay, the faithful tests fail:
- `precheck` fails `TestFaithful` (every level), `TestOneFault` and `TestReal`;
- `noForwardKey` fails all three;
- `pendingConflict` fails `TestFaithful` and `TestOneFault`.

Built with only the mutant's name and none of its files, each mutant's
`Some` claim found no run. Before the model saw money leave accounts and
refusals, `debitFirst` and `lostRace` passed every test (a review found
them).

The two guards back each other up (measured the same day). With the
compare-and-set removed, the charges' UNIQUE refuses the second insert
(23505, a 500, and the retry replays). With the UNIQUE removed, the
compare-and-set lets one attempt through. Either alone conforms; with
both removed, `TestFaithful` (read committed, serializable),
`TestOneFault` and `TestReal` charge twice.

## How the mutants run

A mutant is a directory of `testdata/mutants/` whose files replace
payd's files of the same names. `TestMutants` is the core's harness,
`qtest.Mutants{Package: "payd"}.Run(t)`: it finds the mutants there,
writes a go build overlay for each, and runs `go test -overlay <it> -run
'^TestMutant$'` with `GOTOGETHER_MUTANT` naming the mutant, its own
`-short` and `-timeout`, and without `GOTOGETHER_SEEDS` (a mutant's
claims have their stated miss chance at their own seeds). `TestMutant`
switches on `qtest.Mutant()` and states the mutant's claims with
`Expect`. payd's sources
are never touched, and each mutant is a whole file to diff against
payd's.

qweave's `-mutate` was the other way to do this. It makes mechanical
mutants (a dropped call, a negated condition) in one build, which fits
`noForwardKey` (dropping one `Header.Set`) but not a dedupe rewritten as
check-then-insert. It also weaves the toolkit into the service's build.
An overlay is plain `go test`, costs one link per mutant (about a
second, cached after), and leaves the binary the service's own.

## Running it

```sh
go test -short ./...                 # about 50 s
./run-demo.sh                        # the faithful service, then cmd/payd answering one key twice
MUTANT=precheck ./run-demo.sh      # or noForwardKey, pendingConflict, debitFirst, lostRace, centsInBody
go run ./cmd/payd -db /tmp/payd.db   # then: curl -X POST localhost:8080/v1/charges -H 'Idempotency-Key: k1' -d '{"account": "acct_1", "amount": 250}'
```
