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
| [model/payd.qnt](model/payd.qnt) | requests, responses, charges and captures by key; `noDoubleCharge`, `noDoubleCapture`, `responseMatchesCharge`, `chargeMatchesRequest` invariants, `answeredCharged` and `capturedCharged` eventual vals |
| [payd.binding.yaml](payd.binding.yaml) | the steps as the model's actions, every parameter the step's field |
| [record.go](record.go) | `Recording`, the RoundTripper around the SDK's transport that records each attempt's request and response |
| [sim.go](sim.go) | the simulated deployment: payd's process (`qsql.Open`, `Migrate`, `srv.Handle(payd.New(...))` at every incarnation), its Store, the processor double, the clients |
| [real.go](real.go) | the real one: SQLite, `httptest`, clients on goroutines |
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
  transport. A response with no answer (a timeout, a reset, a refusal) is
  status 0.
- `charge` comes from qsql's `Options.OnCommit`, for each row a commit
  inserted into charges. The commit is the charge's linearization point
  (change data capture is the production analogue). In the real tier, the
  charges are read from the table after the run.
- `capture` comes from the processor double, where it commits.

A charge may be recorded before or after the response that names it, so
the model's invariants are about what was seen, in any order. The
eventual vals are judged after the run settled.

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestFaithful` | none | 20 seeds (4 under `-short`) at read committed, snapshot isolation and serializable, then with a third of payd's requests duplicated or late. Every fault is on: lost, duplicated and late requests and responses to payd and to the processor, refused connections, broken database connections, commits applied with their answer lost (08006), and payd crashing (its crash window included). `Check` (the Store's history checker) is on | every run conforms and settles; in the duplicated runs, at least half duplicate a request and half crash payd (measured: all of them, and 59 of 60) |
| `TestOneFault` | none | `Exhaust` within one fault: one client's one charge | all 78 runs conform |
| `TestReal` | none | SQLite behind real HTTP: 30 keys, each sent by three clients at once | conforms; 30 charges |
| `TestKeyReuse` | none | a key sent again, again with another amount, no key, no account | the same 201 replayed, 409, 400, 404; one debit |
| `TestMutants/precheck` | `precheck`: the key checked by a lookup of its charge, in its own autocommit outside the transaction that makes it, and no UNIQUE on `charges.idem_key` | `Exhaust` within one fault (one charge); two clients sending one key at once with no faults; for real, 10 keys each sent by three clients at once | one run of 66 within one fault charges twice (the duplicated request); every run with two tabs (10 of 10, 2 under `-short`; 40 of 40 measured) charges twice; for real, 14 to 23 charges for 10 keys (10 of 10 runs); never a double capture (`None(Violates("noDoubleCapture"))`: the key still reaches the processor) |
| `TestMutants/noForwardKey` | `noForwardKey`: the capture sent without the `Idempotency-Key` | `Exhaust` within one fault (one charge); for real, as above | 30 of 76 runs within one fault capture twice (a capture's answer lost, a crash between capture and completion, a duplicate); for real, with no fault, two tabs' requests both drive the key and both capture (5 of 5 runs); never a double charge |
| `TestMutants/pendingConflict` | `pendingConflict`: a key found pending answers 409 "in progress, retry later" | `Exhaust` within one fault (one charge) | 29 of 78 runs violate `eventually capturedCharged`: the processor captured, payd answered 502, the retry was refused, and after the settle the key has no charge. No invariant fails: only the eventual check sees it. The real tier's concurrent load does not catch it (the second tab's 409 is not retried, and the first completes) |
| `TestPaydImportsNoToolkit` | none | `go list -deps ./payd/...` | the standard library alone |

Each mutant fails the faithful tests too: `precheck` fails `TestFaithful`
(every level), `TestOneFault` and `TestReal`; `noForwardKey` fails all
three; `pendingConflict` fails `TestFaithful` and `TestOneFault`. Each
mutant's claims fail on payd as it is: built with only the mutant's name
and not its files, every `Some` claim finds no run.

The two guards back each other up. With the compare-and-set removed, the
charges' UNIQUE refuses the second insert (23505, a 500, and the retry
replays). With the UNIQUE removed, the compare-and-set lets one attempt
through. Either alone conforms. With both removed, `TestFaithful` (read
committed, serializable), `TestOneFault` and `TestReal` charge twice.

## How the mutants run

A mutant is a directory of `testdata/mutants/` whose files replace
payd's files of the same names. `TestMutants` writes a go build overlay
for each mutant: the mutant's files, plus a test file that sets
`mutant`. It then runs `go test -overlay <it> -run '^TestMutant$'`, and
`TestMutant` states the mutant's claims with `Expect`. payd's sources
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
go test -short ./...                 # about 45 s
./run-demo.sh                        # the faithful service, then cmd/payd answering one key twice
MUTATION=precheck ./run-demo.sh      # or no-forward-key, pending-conflict
go run ./cmd/payd -db /tmp/payd.db   # then: curl -X POST localhost:8080/v1/charges -H 'Idempotency-Key: k1' -d '{"account": "acct_1", "amount": 250}'
```
