# Possibles and scenarios: acceptance criteria the model must meet

*About 20 minutes. You will: write acceptance criteria as `possible:`
entries and as Go scenarios; ask the model on its own whether it can
follow each one; make a model too strict and watch every run still
conform while the scenario fails; claim the scenario over simulated
runs (`Some`, `None`); and read the scenarios grist generates for every
command's outcome.*

Invariants say what must never happen. A model that forbids too much
passes all of them, and judges faithful a system that never does what
it was built for: a model in which a refund can never be refused has no
quarrel with code that never refuses one. So the model also states what
it must be able to do, and the toolkit checks that, before any run.

Outputs are pasted as they came, but for paths shortened and long lines
cut (`[...]`). It assumes the [first tutorial](prove-it.md)'s setup.

## 1. From a sentence to a `possible:`

hello's product owner says: "a request the client retries is
recognised, not counted again", and "the client finishes". In
[examples/hello](../../examples/hello)'s binding:

```yaml
simulate:
  holds: [exactlyOnce]
  possible:
    - name: a retry recognised
      steps: [apply, replay]
    - done
```

An entry is one of:
- an action some trace takes (`done`);
- a predicate some trace reaches (`'count > 2'`);
- a call, an action with given values (`'withdraw(result = "ok")'`);
- `steps:`, several of those in turn, others allowed between them.

`gotogether simulate` searches for each (the Go evaluator first, quint's
sampling for what it does not find) and prints the witness:

```console
$ cd examples/hello
$ gotogether simulate -binding hello.binding.yaml
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), 1000 traces of 30 steps, seed 1
  holds   exactlyOnce: none of 1000 sampled traces of up to 30 steps violates it (a sample, no proof: a violation that 1 trace in 334 shows is missed at most 5% of the time by 1000 traces, a rarer one more often; -graph searches every state)
  possible a retry recognised: reached by apply(id = 1), replay(id = 1) (the Go evaluator's search)
  possible done: reached by apply(id = 1), apply(id = 2), apply(id = 3), done (the Go evaluator's search)
```

The witness is documentation: the shortest way the model does it, with
its arguments. [examples/migrate-payments](../../examples/migrate-payments)'s
binding is a longer list, one entry per requirement, each in call form
(`'refund(id = "ch_1", result = "exceeds")'`).

## 2. The same scenario in Go

A test can name the scenario, check it against the model, and claim it
over runs. In `hello_test.go`:

```go
var retried = qtest.NamedScenario("a retry recognised",
	qtest.Takes("apply").With("id", "1"), qtest.Takes("replay").With("id", "1"), qtest.Takes("done"))

func TestRetryPossible(t *testing.T) {
	qtest.Model(t, "hello.binding.yaml").Expect(t, qtest.Possible(retried))
}
```

and, among `TestFaithfulConforms`' claims over 30 simulated runs,
`qtest.Some(retried)`: some run met a retry of its first request and
recognised it. Run both:

```console
$ go test -count=1 -run 'TestRetryPossible|TestFaithfulConforms' -v .
=== RUN   TestRetryPossible
    hello_test.go:36: the model able to follow the scenario "a retry recognised" (apply(id = 1) -> replay(id = 1) -> done): reached by apply(id = 1), replay(id = 1), apply(id = 2), apply(id = 3), done (the Go evaluator's search)
--- PASS: TestRetryPossible (0.58s)
=== RUN   TestFaithfulConforms
    hello_test.go:53: gotogether: TestFaithfulConforms#1: 30 runs, all conform (quint confirmed 2, the Go evaluator alone the other 28); model actions taken 3 of 3; fault kinds faulted 3 of 3 reached; a sample, no proof: a failure 1 run in 10 shows is missed at most 5% of the time, a rarer one more often, one the runs cannot reach always
--- PASS: TestFaithfulConforms (1.43s)
PASS
ok  	github.com/chucklehead-dev/gotogether/examples/hello	2.063s
```

The two checks answer different questions. `qtest.Possible`, a claim
of `qtest.Model` (the model alone, as `gotogether simulate` judges it:
the Go evaluator's search, then quint's sampling): *can* the
model follow it (no run needed)? `qtest.Some(retried)`: *did* some run
follow it (the runs reached the case the scenario names)? A scenario
needs both: the first keeps the model honest, the second keeps the
workload honest.

The mutant `noDedupe` (a counter that counts a retry again) turns the
second around: its test claims `qtest.None(retried)` (no run recognises
a retry) next to `qtest.Some(qtest.Rejects("apply"))`:

```console
$ go test -short -count=1 -run 'TestMutants' -v . | grep -v '^=== '
    hello_test.go:87: gotogether: as expected, some run showing an apply step the model cannot take: 23 of 30 runs (failing: 23 reject apply); the first, seed 1, faults [rpc.counter.request=duplicate rpc.counter.response=lost rpc.counter.delay=slow rpc.counter.response=lost]:
        step 2, apply(id=1): the model's apply(id = 1) is disabled: in apply: not(applied.contains(id)) is false (where applied = Set(1), id = 1)
        navigate: GOTOGETHER_MUTANT=noDedupe gotogether nav -run 'TestMutant#1:seed=1' -at fail (in the test's package directory; -json for agents); GOTOGETHER_VERBOSE=1 prints the whole report
    [...]
    --- PASS: TestMutant (6.59s)
--- PASS: TestMutants (0.00s)
    --- PASS: TestMutants/noDedupe (8.25s)
```

A claim that holds as expected prints nothing of its own; the summary
line names the catch.

## 3. A model too strict

Now break the model the quiet way: let `replay` happen only when the
count is already ahead of the requests applied, which never happens.
One line, added to `replay`'s guard:

```console
$ sed -i 's/^    applied.contains(id),$/&\n    count > applied.size(),/' model/hello.qnt && git diff --stat . && gotogether simulate -binding hello.binding.yaml
 examples/hello/model/hello.qnt | 1 +
 1 file changed, 1 insertion(+)
simulate hello.binding.yaml: hello(REQUESTS = 1.to(3)), 1000 traces of 30 steps, seed 1
  holds   exactlyOnce: none of 1000 sampled traces of up to 30 steps violates it (a sample, no proof: a violation that 1 trace in 334 shows is missed at most 5% of the time by 1000 traces, a rarer one more often; -graph searches every state)
  FAIL    possible a retry recognised (steps apply, replay): the Go evaluator: impossible: no trace reaches it (the search saw the whole state graph, 8 states); quint: not reached in 1000 sampled traces of up to 30 steps (a state that 1 trace in 334 shows is missed at most 5% of the time by 1000 traces, a rarer one more often). The model forbids it: if a conforming run must be able to show it, find the guard that forbids it (gotogether nav -binding hello.binding.yaml shows each action disabled in a state, with why) and loosen it; if it must be impossible, drop it from simulate.possible
  possible done: reached by apply(id = 1), apply(id = 2), apply(id = 3), done (the Go evaluator's search)
```

`exactlyOnce` still holds: a model that forbids more breaks fewer
invariants. Only the possible fails, and "impossible" is a proof here:
the search saw the whole graph. The test says the same:

```console
$ go test -count=1 -run 'TestRetryPossible' .
--- FAIL: TestRetryPossible (0.35s)
    hello_test.go:33: the scenario "a retry recognised" (apply(id = 1) -> replay(id = 1) -> done): the model on its own cannot follow it, impossible: no trace reaches it (the search saw the whole state graph, 8 states): a step names what the model forbids, or the model is too strict for any conforming run to show it (gotogether nav -binding hello.binding.yaml shows each disabled action with why)
FAIL
FAIL	github.com/chucklehead-dev/gotogether/examples/hello	0.357s
FAIL
```

Follow its advice. `gotogether nav` opens the model at a state and says
why each action is disabled there, naming the first false conjunct:

```console
$ gotogether nav -binding hello.binding.yaml -follow 'apply(1)' -text
node "apply(1)" (from init)
  path: apply(1)
  state:
   *applied = Set(1)
   *count = 1
  invariant exactlyOnce: true
  branches (2):
    #1 apply(2)
    #2 apply(3)
  disabled: replay
    why: the model's replay is disabled:
      in replay (id = 1, the one closest to enabled of the 3 values of REQUESTS, each disabling it): count > applied.size() is false
        where applied.size() = 1, count = 1, applied = Set(1)
  disabled: done
    why: the model's done is disabled:
      in done: applied == REQUESTS is false
        where applied = Set(1), REQUESTS = Set(1, 2, 3)
```

The guard you added, with its values. Without the possible, the runs
under this model would be rejected only when a retry happens, and a run
with no faults (no retries) would conform. Put it back:

```sh
git checkout -- model/hello.qnt
```

## 4. Scenarios of a grist app: in its own values

[examples/library](../../examples/library) is a lending library on grist.
Its acceptance criterion "a member holding three loans is refused a
fourth, though a copy is free" is a scenario in the app's own commands
and errors (`sim_test.go`):

```go
var atTheLimit = gristtest.Scenario("a member at the limit",
	JoinCmd.With(Join{Member: "a"}),
	AddCmd.With(AddBook{Book: "a", Copies: 3}), AddCmd.With(AddBook{Book: "b", Copies: 1}),
	BorrowCmd.With(Borrow{Member: "a", Book: "a"}), BorrowCmd.With(Borrow{Member: "a", Book: "a"}), BorrowCmd.With(Borrow{Member: "a", Book: "a"}),
	BorrowCmd.With(Borrow{Member: "a", Book: "b"}).Refused(ErrLimit))
```

checked by `qtest.Model(t, commits).Expect(t, qtest.Possible(atTheLimit))`, and claimed of every
run of a workload shaped like it, `qtest.All(atTheLimit)`:

```console
$ cd ../library
$ go test -count=1 -run 'TestAtTheLimitPossible$|TestAtTheLimit$' -v . | grep -v '^=== '
    sim_test.go:166: gotogether: TestAtTheLimit#1: 3 runs, all conform (the Go evaluator's, none confirmed by quint); 3 settled; [...]
--- PASS: TestAtTheLimit (11.30s)
    sim_test.go:149: the scenario "a member at the limit" (join(member = "a") with outcome ok -> addBook(book = "a", copies = 3) with outcome ok -> addBook(book = "b", copies = 1) with outcome ok -> borrow(member = "a", book = "a") with outcome ok -> borrow(member = "a", book = "a") with outcome ok -> borrow(member = "a", book = "a") with outcome ok -> borrow(member = "a", book = "b") with outcome limit): possible in the model, reached by join(member = "a", id = "i1", caller = "a", readPos = 0, reads = Set(("member-joined", "member", "a")), result = "ok"), [...]
--- PASS: TestAtTheLimitPossible (11.33s)
PASS
```

A grist app also gets possibles it did not write: its generated binding
has one per command outcome and per processor step, so a rule that can
never refuse, or a processor that can never run, fails before any run:

```console
$ gotogether simulate -binding library.binding.yaml | cut -c1-240
simulate library.binding.yaml: library(), 1000 traces of 30 steps, seed 1
  holds   copiesHeld: [...]
  holds   withinLimit: [...]
  possible addBook.ok: reached in the initial state (the Go evaluator's search)
  possible addBook.invalid: reached in the initial state (the Go evaluator's search)
  possible borrow.ok: reached by addBook(book = "a", copies = 1, id = "i1", caller = "a", readPos = 0, reads = Set(), result = "ok"), join(member = "a", id = "i2", caller = "a", readPos = 1, reads = Set(("member-joined", "member", "a")), re
  possible borrow.not-member: reached in the initial state (the Go evaluator's search)
  possible borrow.unavailable: reached by join(member = "a", id = "i1", caller = "a", readPos = 0, reads = Set(("member-joined", "member", "a")), result = "ok") (the Go evaluator's search)
  possible borrow.limit: reached (quint: it held in 58 of 1000 sampled traces of up to 30 steps; the Go evaluator: not found within the bound (2000 states, depth 4; the states bound cut the search))
  [...]
  possible receipts: reached (quint's sampled trace: borrow, memberLoans, addBook, addBook, join, memberLoans, join, addBook, join, borrow, mailerSend, mailerSend, join, join, returnBook, borrow, receiptsEffect, mailerSendServed, borrow, re
  [...]
```

Each `<command>.<outcome>` entry is a predicate: "this command, sent
now, would answer this outcome". So `addBook.ok` holds already in the
initial state (an addition is accepted from the start), and
`borrow.unavailable` after one join (a member, no book). `borrow.limit`
lies deeper than the Go evaluator's search bound (2000 states), and
quint's sampling found it: still reached, and the line says by whom.

## What you saw

| you write | checked by | fails when |
|---|---|---|
| `simulate.possible:` in the binding | `gotogether simulate` (and `-graph`) | the model cannot reach it: "impossible" over a complete search, "not reached" within a bound |
| `qtest.NamedScenario` / `gristtest.Scenario` | `qtest.Model(t, binding).Expect(t, qtest.Possible(s))` | the same, in `go test` |
| the scenario as a claim | `qtest.Some(s)`, `qtest.All(s)`, `qtest.None(s)` | no run (or not every run, or some run) followed it |

## Read next

- [Shipping a grist app to Kubernetes](grist-on-kubernetes.md).
- The workflow skill on scenarios first: [skills/gotogether-workflow/references/workflow.md](../../skills/gotogether-workflow/references/workflow.md),
  section 1; verdicts and claims: [skills/gotogether-workflow/references/verdicts.md](../../skills/gotogether-workflow/references/verdicts.md).
- The grist guide, §6.4 "Scenarios and possibles": [docs/grist-guide.md](../grist-guide.md#64-scenarios-and-possibles).
- The migration skill, which turns requirements found in old code,
  tests and logs into `possible:` entries with their provenance:
  [skills/gotogether-migration/SKILL.md](../../skills/gotogether-migration/SKILL.md)
  and [examples/migrate-payments](../../examples/migrate-payments).
