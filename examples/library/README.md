# library

A lending library on grist, scaffolded by `grist new library` and grown
one block at a time: members `join` (refused `already-member`), books are
catalogued with a number of copies (`add-book`, refused `invalid`), a
member `borrow`s a copy (refused `not-member`, `unavailable` with no
copy free, and `limit` at three loans by the `loan-limit` rule, which
guards every append of a borrow) and returns one they hold (`return-book`,
refused `not-held`); `member-loans` shows a member's loans; the `receipts`
processor mails each borrow's receipt at least once through the mailer, a
declared dependency (`Mailer`, its `Keyed` call `Send`): grist records the
call where the effect makes it (`mailerSend`) and where the mailer sends it
(`mailerSendServed`, placed after the borrow's commit), and in a
simulation serves the mailer its default double, which sends each key once
and is lossy (`rpc.mailer.*`). It is checked against its Quint
model with nothing annotated and no binding written by hand: grist records
every block's step, and the model's skeleton and both bindings are
generated from the App's catalog. [TRIAL.md](TRIAL.md) is the friction log
of building it as a newcomer to grist.

| file | what it is |
|---|---|
| [app.go](app.go), [commands.go](commands.go), [rules.go](rules.go), [queries.go](queries.go), [processors.go](processors.go), [mailer.go](mailer.go) | the events, the read models (`books` keyed by book, `loans` keyed by member), the commands, the rule (`loan-limit`, on `book-borrowed`: `grist.NewRule`), the query, the processor, the mailer's declaration (no double: gristsim serves its default) |
| [model/library_rules.qnt](model/library_rules.qnt) | written by hand: the rules the skeleton calls (the loan limit's verdict and reads, `loanLimitRefusal` and `loanLimitNeeds`, which guard the generated `borrow`; the receipt a borrow asks the mailer for, `receiptsMailerSend`) |
| [model/library.qnt](model/library.qnt) | written by hand: the invariants (copies on loan within the book's copies, at most three loans, at every step; every borrow receipted, after a settle: the mailer's served record ok, stronger than the generated `receiptsEventuallyHandled`, an ok effect step or a `grist.failed`, which it keeps beside it). That every receipt is for a borrow that happened is the generated `mailerSendServed`'s own guard, not an invariant |
| [doubles_test.go](doubles_test.go) | the runs' doubles: none (the `noDedupe` mutant replaces it) |
| [model/library_gen.qnt](model/library_gen.qnt), [model/library_steps.qnt](model/library_steps.qnt), `*.binding.yaml`, [model/grist.qnt](model/grist.qnt) | generated: `go run ./cmd/library model` (never edit them; `TestGenerated` fails when they are stale), the catalog's obligations among them (`receiptsEventuallyHandled`, `mailerSendEventuallyServed`: eventual vals the store's binding judges at a settle, docs/grist.md §3.3.1) |
| [cmd/library](cmd/library/main.go) | the binary (`grist.Main`): `model`, `catalog`, `verify`, `serve -db sqlite:PATH` |
| [testdata/mutants](testdata/mutants) | `noCopyCheck`, `staleLimit`, `noDedupe`, `wrongReceipt`; `drop-loan-limit` is generated (`gristtest.RuleMutants(App)`: the app with its rule dropped); `qtest.Mutants` finds and runs them all |

## Which check catches which mutant

| test | configuration | catches |
|---|---|---|
| `TestFaithful` | one tenant, a prologue (two members, a copy of two books, an invalid addition), three clients, commit faults, node crashes, a lossy API and mailer | nothing: `Conforms`, `All(Settles)`, commit faults reached, the mailer's answers lost (`Reaches("rpc.mailer.response").Faulted()`, a send of outcome `unknown`, a replay), every block of the catalog taken by each outcome but `borrow.limit` (`gristtest.Catalog(App).Exercised`) |
| `TestFaithfulAtClients` | the same runs at the clients, linearized | nothing: `Conforms` |
| `TestScarce` | members borrowing one book added a copy at a time, no faults | nothing: `None(Rejects("borrow"))` (`noCopyCheck`'s contrast), a borrow refused `unavailable` (`Takes("borrow").Outcome("unavailable")`) |
| `TestMutant` (`noCopyCheck`) | the same | `Some(Rejects("borrow"))`: a borrow with no copy free commits where the model's rule says `unavailable` (and its read set misses the shelf) |
| `TestGreedy` | one member and three books opened by a prologue, three clients borrowing at ReadCommitted, no faults | nothing: `None(Rejects("borrow"))` (`staleLimit`'s and `drop-loan-limit`'s contrast), a borrow refused at the `limit` by the rule |
| `TestMutant` (`staleLimit`) | the same, the borrow `Unconditioned` | `Some(Rejects("borrow"))`: a fourth loan commits on a stale read of the member's loans (the rule's reads advisory with the borrow's), where the model's rule says `limit` |
| `TestMutant` (`drop-loan-limit`, generated) | the same, the `loan-limit` rule dropped | `gristtest.DroppedRule`'s claim, `Some(Rejects("borrow"))`: a fourth loan commits where the generated guard (`loanLimitRefusal`) says `limit` |
| `TestAtTheLimitPossible` | the model on its own (`qtest.Possible`) | a model too strict for the scenario `a member at the limit` (`gristtest.Scenario`: a join, a book of three copies and one of one, three borrows, then a fourth refused `limit`) to happen; the generated binding's `borrow.limit` possible says the same of any limit refusal (`gotogether simulate`) |
| `TestAtTheLimit` | the scenario as a workload: its first six calls a prologue, one client asking the fourth loan, no faults | nothing: `Conforms`, `All(atTheLimit)` (every run follows the scenario; a miss would name the longest prefix taken and where it stopped) |
| `TestReceiptOnce` | a scripted crash just after a receipt's mail, before its checkpoint | nothing: `Conforms` (the mail sent again, the mailer replaying its first answer), `All(Settles)`, the crash reached |
| `TestMutant` (`noDedupe`) | the same, a mailer double sending every try | `All(Rejects("mailerSendServed"))`: a second served record of one key, which the generated `mailerSendAdmits` refuses (`Send` is `Keyed`; no rule written by hand) |
| `TestMutant` (`wrongReceipt`) | borrows, no faults | `All(Rejects("mailerSendServed"))`: a receipt naming no borrow (member and book swapped), refused where the mailer sends it (its request not `receiptsMailerSend`'s for its trigger), the first record of it |
| `TestRealHTTP` | the real tier: SQLite, real HTTP, the mailer's default double in process (`TierConfig.Local`: one `grist.Local` for the tier), every fifth answer lost (gristtest.Tier) | nothing: both bindings conform, every answer has its store-side step |
| `TestGenerated` | the generated files against the catalog | a block changed without regenerating |

```sh
go test -short ./...           # every claim (needs quint on PATH)
./run-demo.sh
MUTANT=staleLimit ./run-demo.sh
```
