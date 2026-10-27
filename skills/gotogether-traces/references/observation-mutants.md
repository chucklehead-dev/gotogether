# Observation mutants: what the binding and the model constrain

Read this when a conforming run's check must be shown to read what it
should: after writing a binding, after adding a recorder's fields, or
when a run passes and you doubt the check looked.

A code mutant changes the program and asks whether a run of it fails
(`System.Mutate`, overlays). An observation mutant changes the *log*: one
step dropped, recorded twice, swapped with its recorder's next, one field
corrupted, retimed, moved to another actor, and asks whether the check of
the log still passes. A survivor is a field the binding and the model do
not constrain: the recording could be wrong there and nothing would say.

```go
steps := readSteps() // a recorded run that conforms
rep := qtest.MutateSteps(t, "bank.binding.yaml", steps)
rep.Expect(t,
	qtest.Killed(qtest.StepOp("corrupt").Action("deposit").Field("obs.balance").Change("+1")),
	qtest.Survived(qtest.StepOp("drop").Action("probe")), // a trailing probe the model does not need
)
t.Log(rep) // the table by op, and every survivor
```

From a file: `gotogether validate -binding b.yaml -mutate-steps
[-mutate-ops drop,corrupt] [-mutate-limit 20] run.jsonl` prints the same
table after a pass. In code, `qtest.JudgeStepMutants(checker, steps,
search)`; the mutants alone, `qtrace.Mutations(steps)`.

## The ops

Each keeps the recorder's own contract (dense seqs, a completion after
its invocation record, one incarnation at a time, order keys that agree
with program order), so the check that judges a mutant is the model's
and the binding's, not Reconstruct's. A candidate Reconstruct would
refuse with an error issue the original lacks (a step moved into an
actor whose Lamport keys contradict its thread's order: a cycle) is
left out. Watermark, settle, invocation and loss records are never
mutated; they are renumbered with their recorder's steps.

| op | what changes | a survivor says |
|---|---|---|
| `drop` | one step out; its recorder's later seqs move down (a gap would be a lost step, which every check reports) | no later step depends on it, or observes its effect |
| `dup` | one step recorded twice, the copy at the next seq (a copy at the same seq is an exporter's retry, deduped) | the action is idempotent in the model, or its copy's observation is not compared |
| `swap` | two adjacent steps of one recorder and incarnation exchange seqs and times | the two commute in the model, or nothing observes the order |
| `corrupt` | one argument or observation: ints and floats ±1, bools flipped, strings emptied and changed, the field removed | the binding does not read it (marked `Unread`, from `Binding.UsedFields`), or the model accepts both values |
| `retime` | one step moved later, never past its actor's next step (a within's clock would go back) nor a settle or watermark: past the other actors' steps before it, else later within its gap; its actor's last, past another actor's next step | no `within` and no real-time order (`Linearize`) reads its time |
| `reactor` | one step moved to another actor, in that actor's process | the actors' models do not tell the step apart |
| `gap` (only when named: `-mutate-ops gap`, `MutateOps("gap")`) | one step out, its recorder's later seqs kept: a hole, as a sink that dropped it writes | nothing: its mutants are undecided by design (a failure the dropped step may explain is no verdict); it tests the absence path, not the run |

## Fates

- **killed**: the mutant fails where the original conformed (a
  rejection, a mismatch, a violation), or, if the original
  finished (`Finishes`), the mutant does not (a completion dropped
  leaves its call pending).
- **survived**: the same verdict as the original's.
- **undecided**: no verdict (a search out of its budget, a lost tail),
  or a survivor whose bounds went unjudged where the original's were
  judged: neither. Raise the budget.
- **malformed**: refused before a verdict, as a real log like it is (a
  tool error, exit 2): Reconstruct refused the mutant's log (an overlap,
  a cycle), or the binding could not plan it (a field it reads removed:
  `refused by the binding`, which says the binding needs the field, not
  that a run without it fails). No check of the model's stopped it.
  Mutations leaves out what it can see would be; one that is anyway is
  reported with its issue, in no score, and meets no claim.

The original must conform: a failing run's mutants fail too, and say
nothing. Verdicts are shared with every check of the test binary (the
cache by instance and steps), so mutants that repeat a log are judged
once.

## Reading a survivor

Each survivor is a question: either add the check that reads the field
(an `expect` comparing it, an invariant over what it records), or accept
it and say so with `Survived(...)`. Survivors that are equivalent by
right:

- a trailing probe or read the model does not need, dropped: nothing
  after it can disagree;
- an idempotent read duplicated, or a write the model makes idempotent
  (`put` of the same value);
- an observation the binding does not compare corrupted (`Unread`): a
  field kept for people, not for the check;
- any mutant of a step the binding skips (`outcomes: { error: skip }`,
  marked `Skipped` with the outcome, and said in the report): a skipped
  step is no model call, so nothing reads it. If the refusal matters,
  model it (an action enabled only when the call must fail) instead;
- a swap of steps that commute (deposits to two accounts);
- a retime, when nothing in the binding reads time.

A survivor that is none of these is a gap: the bank's transfer
observes no balance, so a withdrawal before it dropped, or its amount
one more, survives (`qtest/stepmutate_test.go`).

## Cost and narrowing

Every mutant is one more check of the whole log. A long log has hundreds
(webjobs' journey: 662): narrow with `qtest.StepMutations(qtrace.MutateOps(...),
qtrace.MutateLimit(n))`, which keeps n per op spread over the log, and
keep the full run out of `-short`.

## Which mutants when

| mutants | change | answer | use when |
|---|---|---|---|
| code (`Mutation` options, `System.Mutate` with `qweave -mutate`) | the program | does a run of the buggy code fail? | the code is yours to plant bugs in |
| overlays (`go test -overlay`) | files of a package left as it is | the same, without editing the code | code you must not edit (payd, webjobs) |
| observation (`MutateSteps`, `validate -mutate-steps`) | the recorded log | does the check read what was recorded? | after a binding is written; for a recorder another team or tool writes; to find fields no check reads |

Code and overlay mutants test the model and the binding through the
program; observation mutants test the recording/binding boundary alone,
with no program to run, so they apply to any log, a production one
included.
