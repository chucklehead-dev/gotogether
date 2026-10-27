# library

A lending library on grist, scaffolded by `grist new library` and grown
one block at a time: members `join` (refused `already-member`), books are
catalogued with a number of copies (`add-book`, refused `invalid`), a
member `borrow`s a copy (refused `not-member`, `limit` at three loans,
`unavailable` with no copy free) and returns one they hold (`return-book`,
refused `not-held`); `member-loans` shows a member's loans; the `receipts`
processor mails each borrow's receipt at least once through a mailer
double that deduplicates by the effect's key and records each send where
it makes it (the `mailer-sends` site). It is checked against its Quint
model with nothing annotated and no binding written by hand: grist records
every block's step, and the model's skeleton and both bindings are
generated from the App's catalog. [TRIAL.md](TRIAL.md) is the friction log
of building it as a newcomer to grist.

| file | what it is |
|---|---|
| [app.go](app.go), [commands.go](commands.go), [queries.go](queries.go), [processors.go](processors.go), [mailer.go](mailer.go) | the events, the read models (`books` keyed by book, `loans` keyed by member), the commands, the query, the processor, the mailer double and its site |
| [model/library.qnt](model/library.qnt) | written by hand: the rules the skeleton calls and the invariants (copies on loan within the book's copies, at most three loans, every receipt for a borrow that happened, every borrow receipted) |
| [model/library_gen.qnt](model/library_gen.qnt), `*.binding.yaml`, [model/grist.qnt](model/grist.qnt) | generated: `go run ./cmd/library model` (never edit them; `TestGenerated` fails when they are stale) |
| [cmd/library](cmd/library/main.go) | the binary (`grist.Main`): `model`, `catalog`, `verify`, `serve -db sqlite:PATH` |
| [testdata/mutants](testdata/mutants) | `noCopyCheck`, `staleLimit`, `noDedupe`, `wrongReceipt` |

## Which check catches which mutant

| test | configuration | catches |
|---|---|---|
| `TestFaithful` | one tenant, a prologue (two members, a copy of two books, an invalid addition), three clients, commit faults, node crashes, a lossy API | nothing: `Conforms`, `All(Settles)`, commit faults reached, every block of the catalog taken by each outcome but `borrow.limit` (`gristtest.Exercised`) |
| `TestFaithfulAtClients` | the same runs at the clients, linearized | nothing: `Conforms` |
| `TestScarce` | members borrowing one book added a copy at a time, no faults | nothing: `None(Rejects("borrow"))` (`noCopyCheck`'s contrast), a borrow refused `unavailable` (`Takes("borrow").Outcome("unavailable")`) |
| `TestMutant` (`noCopyCheck`) | the same | `Some(Rejects("borrow"))`: a borrow with no copy free commits where the model's rule says `unavailable` (and its read set misses the shelf) |
| `TestGreedy` | one member and three books opened by a prologue, three clients borrowing at ReadCommitted, no faults | nothing: `None(Rejects("borrow"))` (`staleLimit`'s contrast), a borrow refused at the `limit` |
| `TestMutant` (`staleLimit`) | the same, the borrow `Unconditioned` | `Some(Rejects("borrow"))`: a fourth loan commits on a stale read of the member's loans, where the model's rule says `limit` |
| `TestReceiptOnce` | a scripted crash just after a receipt's mail, before its checkpoint | nothing: `Conforms` (the mail sent again, the mailer dropping it), `All(Settles)`, the crash reached |
| `TestMutant` (`noDedupe`) | the same, the mailer without its dedupe | `Some(Rejects("mailerSends"))`: a second record of one key, which the mailer's rule (`mailerSendsAdmits`) refuses |
| `TestMutant` (`wrongReceipt`) | borrows, no faults | `Some(Violates("eventually everyReceiptForABorrow"))`: a receipt naming no borrow |
| `TestRealHTTP` | the real tier: SQLite, real HTTP, a mailer, every fifth answer lost (gristtest.Tier) | nothing: both bindings conform, every answer has its store-side step |
| `TestGenerated` | the generated files against the catalog | a block changed without regenerating |

```sh
go test -short ./...           # every claim (needs quint on PATH)
./run-demo.sh
MUTATION=stale-limit ./run-demo.sh
```
