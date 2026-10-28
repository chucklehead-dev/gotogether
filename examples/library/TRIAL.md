# TRIAL: a newcomer builds examples/library on grist

A friction log. Minutes are from the start of the trial. I read only what a
user has: README.md, skills/grist (SKILL.md and references), docs/grist.md,
`grist new`, `go doc`. Anything else I looked at is named where I looked.

## Log

- **0** Worktree off origin/main (bf91adf). Read skills/grist/SKILL.md and
  both references (~400 lines): dense, but it reads as a reference, not a
  walk-through; the "first hour" paragraph is the one I needed.
- **1** `go run ./grist/cmd/grist new -dir examples/library -module
  github.com/chucklehead-dev/gotogether/examples/library -replace ../.. library`
  from the repository root. I guessed `-replace` takes the path the go.mod's
  `replace` should say (as every example's `replace => ../..`). It does not:
  it is resolved against the current directory and written into go.mod as an
  absolute path. The error was go's (`replacement directory .../grist does not
  exist`), not grist's, and it left a half-written directory behind. Rerun with
  `-replace .` worked; go.mod then had absolute paths to my scratch worktree,
  which I rewrote to `../..` by hand. **Docs silent** on what `-replace`
  writes; the skill says `-replace <checkout>`, nothing about examples/.
- **2** `go test ./...` on the starter: **green on its first run** (73 s wall,
  one package). The starter is good: it has every file AGENTS.md asks of an
  example (model, bindings, mutant, run-demo.sh, README with the table).
- **3** Read the starter's files (app, commands, query, processor, model,
  sim/mutant/real tests). They are the best documentation grist has: short,
  each block commented, and the model shows every hole filled. Planned the
  library: four events (`member-joined`, `book-added`, `book-borrowed`,
  `book-returned`), two keyed read models (`books` by book, `loans` by
  member), four commands, one query, an at-least-once processor and a
  mailer double with a site.
- **4** **Guess**: references/entities.md says "one event per entity
  touched" because a keyed read model's key "must be a tag of every
  handler's type". My borrow touches two entities of *different* kinds, so I
  guessed one `book-borrowed` event tagged with both `member` and `book` can
  feed both keyed read models. It can (the generator wrote
  `bookBorrowedByMember` and `bookBorrowedByBook`, and every run conforms).
  **Docs silent**: entities.md's walk-through is two entities of one kind
  (a transfer); it never says whether an event may carry two tags for two
  read models.
- **5** **Docs wrong**: docs/grist.md §2.4 names `grist.Within(Totals)` and
  `grist.Also(q)` as options; `go doc grist Within` says no such symbol, and
  `go doc grist Option` lists neither. I wanted `Within(Books)` for the
  stale-limit mutant (the loans read advisory) and used `Unconditioned` instead.
- **5** Read `go doc` of `NewSite`, `NewDep`, `Effect`, `EffectKey`,
  `Delivers`, `gristsim.Config`, `gristtest.TierConfig`. Clear. **Guess**:
  `NewDep`'s fallback "refuses every call with the fix", but I write that
  stand-in myself; with a struct type the natural fallback is nil, which
  would panic. I made the dependency an interface (`Sender`) with a refusing
  stand-in. The docs could say "make T an interface".
- **6** First `go run ./cmd/library model`: `query "loans": the name is taken
  by a read model: give every block its own name`. **Told me what to do**;
  renamed the query `member-loans`.
- **6** The missing-holes report printed a stub for every hole, each with a
  comment saying what it is. Excellent: I pasted from it.
- **7** **Silent loss**: my query's result was `{Books []string}`. The
  generator made `MemberLoansResult = { unit: bool }` and the stub's answer
  `{  }`: a list field of a result is dropped without a word, so the query's
  answer would never have been compared. references/model.md does say "the
  result's scalar fields", which I read as "fields that are scalars are
  compared", not "lists are silently ignored". I added `Count int` (compared)
  beside `Books` (not compared). A member's loans as a list is exactly what a
  user asks of this query; the model cannot check it. grist should either
  compare lists of scalars (the obs already carry lists: `obs.strs`) or
  refuse the result type with the fix.
- **8** Wrote model/library.qnt: folds (`copies`, `onLoan`, `held`,
  `loanCount`, `isMember`), the rules, three invariants and an eventual
  val. `quint typecheck` passed on the first try; the generated accessors
  (`bookBorrowedBook(e)`) and selections (`booksEvents(log, b)`,
  `loansReads(m)`) made the rules short. **Guess**: what a read of
  `r.Exists(Joined, Tag("member", m))` records in the read set; I guessed
  `memberJoinedByMember(m)` for `joinNeeds`/`borrowNeeds`, and it was right
  (no `covers` rejection), but nothing says which generated `Read` an
  `Exists` corresponds to.
- **9** `gotogether simulate -binding library.binding.yaml`: **my invariant
  "every receipt is for a borrow that happened" violated by the model on its
  own** (a state with no log and one mailer record). The site's generated
  action is guarded only by `mailerSendsAdmits(records, r)`, which cannot see
  the log, so the model lets a mailer record anything at any time. Marked it
  `violable`. **Docs silent**: model.md says `<s>Admits` is "the dependency's
  own rule (a ledger's dedupe by key)"; nothing says a site's records are
  unconstrained by the log, or how to state "a site's record follows a
  commit".
- **10** First exploration (`TestGreedy`): `everyReceiptForABorrow: FAIL,
  violated after step 1`, the mailer's record reconstructed *before* every
  commit (log empty). The generated comment on the site action says "in any
  order with the steps that asked for them", so a site record is ordered with
  nothing in the tenant's log, and no per-step invariant relating site records
  to the log can hold. Moved it to `// grist: eventually`. The report was good
  (state after the step, the quint step, the observed step, reproduce line);
  the conclusion (sites are unordered, use eventually) I had to reach myself.
  This is the second time the same fact bit me: model.md should say it.
- **10** `TestReceiptOnce` (scripted `crashAfter` of `grist.receipts.effect`):
  `in 2 of 3 runs scripted faults never happened ...; lower After, or script
  a fault the run reaches (Run.Unscripted lists them)`. **Told me what to
  do**, roughly: the cause was my workload (runs that never borrowed). There is
  no way to give gristsim's clients a fixed prologue (join, then add, then
  borrow): `Send` picks names at random. **Guess**: listing a name several
  times in `Send` weights it. The runs then borrowed; whether that is the
  mechanism or luck, the docs do not say.
- **11** **Guess**: a claim "some borrow was refused `limit`" (to show the
  contrast test reaches the refusal its mutant breaks). `qtest.Takes` has no
  outcome filter and is not an Expectation, so `Some(Takes(...))` does not
  compile; I claim `Takes("borrow")` only, which a refusal satisfies. A
  contrast test cannot state that it reached the refusal it contrasts.
- **12** `wrongReceipt`: `Violates("everyReceiptForABorrow")` failed with
  `everyReceiptForABorrow is not an invariant of the runs (checked: copiesHeld,
  eventually everyBorrowReceipted, eventually everyReceiptForABorrow, ...)`.
  The list showed the fix (`Violates("eventually everyReceiptForABorrow")`),
  but the message did not say it; an eventual val's claim name with the
  `eventually ` prefix is nowhere in the skill.
- **13** Whole -short suite **green**, every mutant caught as claimed:
  `noCopyCheck` 3/3 `Rejects(borrow)`, `staleLimit` 5/6 `Rejects(borrow)` at
  ReadCommitted, `noDedupe` 3/3 `Rejects(mailerSends)`, `wrongReceipt` 2/3
  `Violates(eventually everyReceiptForABorrow)`; `TestRealHTTP` green on its
  first run with a mailer attached through `TierConfig.Deps`.
- **14** Read the summaries again: `TestFaithful`'s 4 runs had **never taken
  `receipts`, `receiptsEffect` or `mailerSends`**: no borrow ever committed
  ok under the faulty random workload (a borrow needs a join and an addition
  before it). The claims passed anyway. The summary told me ("model actions
  taken 5 of 14 (never: ...)") but buried the processor among helpers
  (`appends`, `refuses`, `readsThrough` are always listed), and truncated
  ("and 1 more"). Weighted `Send` (borrow twice) and member `a`; now every
  block is reached. A claim like `Exercised` for the app's own blocks (all
  commands, the processor and the site taken) would have failed loudly.
- **15** `gotogether lint -sim .` clean with a `sync.Mutex` in the mailer.
  **Guess**: a site's `Record` may be a scheduling point, so I record outside
  the lock; nothing says whether a double may hold a real lock across
  `Site.Record` in a simulation.
- **16** README, run-demo.sh (one MUTANT per mutant), the root README's
  row. gofmt flagged a mutant file I wrote by script; vet, `go mod tidy
  -diff` (GOWORK=off) and the docs tests clean.
- **22** Final `GOWORK=off go test -short ./...`: green, 240 s wall on 4
  cores, **227 s CPU** (215.8 s user + 11.7 s sys, mutant builds included).

## No binding written by hand

None. Both bindings are generated, and nothing needed a template. The two
model-side workarounds (a compared `Count` beside the list, the receipt
invariant moved to `eventually`) are in model/library.qnt and queries.go,
each commented.

## What I looked at beyond the allowed sources

Nothing of grist's implementation, examples/counters or examples/wallet-grist.
I read go.work and the first line of examples/hello/go.mod and
examples/counters/go.mod (module paths, to name mine), and ci.yml's example
loop (to be sure the library is not in it).

## What grist should add or change, ranked

1. **A query result's lists are dropped silently** (`{ unit: bool }`). Compare
   lists of scalars, or make `Verify`/the generator refuse the field with the
   fix. A user's natural "show me my loans" is unchecked otherwise.
2. **Say how a site's records are ordered, and give `<s>Admits` the log.**
   "Every effect is for something that happened" is the first invariant one
   writes about a dependency; it cannot be an invariant today (the site
   record is placed anywhere, and Admits sees only the records), and the
   docs never say so. Either order a site's record after the effect step
   that caused it (it runs inside the effect: the effect key names the
   trigger) and pass `log` to Admits, or document "site invariants are
   eventual" in references/model.md.
3. **A claim that the app's own blocks were exercised.** A faithful
   exploration whose runs never committed a borrow passed every claim; the
   summary's "never" list mixes the app's blocks with grist's helpers and
   truncates. `qtest.Exercised`-like for grist (every command, ok and each
   declared refusal, every processor and site) by default in the starter.
4. **A workload prologue for gristsim** (`Config.Before: []Call`, or
   per-client scripts): an app whose commands depend on earlier ones (join,
   then catalogue, then borrow) gets most of its random operations refused.
   And say whether repeating a name in `Send` weights it.
5. **docs/grist.md §2.4 documents `grist.Within` and `grist.Also`, which do
   not exist.** Mark them proposed or remove them.
6. **`grist new -replace`** should accept a relative path and write it as
   given (examples need `../..`), and not leave a half-written module when go
   mod tidy fails.
7. **The `eventually ` prefix in `Violates`**: accept the bare val name, or
   say "write Violates(\"eventually X\")" in the error and in the skill.
8. **`Takes(action).Outcome(o)`**, so a contrast test can claim it reached
   the refusal its mutant removes.
9. **Say which generated `Read` each Go read records** (`Exists(T, Tag(k,
   v))` is `<t>By<K>(v)`; `X.For(r, k)` is `<x>Reads(k)`) in model.md, for
   writing `Needs`.
10. **`NewDep`'s fallback**: suggest an interface type, and show the
    stand-in.
11. **CPU**: four mutants are four extra test binaries; the library's -short
    suite costs ~227 CPU s, much of it builds. `FullOnly` exists; the starter
    could say when to use it.
