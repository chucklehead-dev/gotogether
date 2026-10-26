# Partial logs: a lost head, a lost tail

A log from production rarely starts at the run's beginning: it was
rotated, truncated, or its recorder started after the system did. Nor
does it always end where the run did: a torn last line, a killed
process's buffered steps. Both are said in the trace, and judged.

## A lost prefix: start from a snapshot

A recorder whose first seq is k > 1 lost seqs 1..k-1: `issue: error
lost-prefix` (not a `gap`, which is a hole after the first seq). Its first
step's state is not the model's `init`, so a replay from `init` judges
nothing. Record the whole state at the head of each log, as one step of
a model action that takes it as parameters and assigns every variable:

```quint
action restore(b: int, n: int): bool = all { b >= 0, balance' = b, ops' = n }
```

and name it in the binding:

```yaml
snapshot: restore     # a trace whose first step is restore starts from its state
```

A trace whose first step is the snapshot action's starts there, not at
`init`, in every check (Check, CheckBatch, Judge's searches, which keep
it first in every order, hidden actions' search; a key partition starts
each key from it). The lost prefix of its own recorder becomes `issue:
info lost-prefix: judged from the state recorded by restore at seq 3
(recorder r lost seqs 1..2)`; another recorder's lost prefix stays an
error (its lost steps may have come after the snapshot). A snapshot step
anywhere but first is skipped, with `issue: info snapshot`. `gotogether
itf` writes such a trace with state 0 the snapshot's, and validating the
ITF starts there too.

Record the snapshot when no operation is in flight (under the lock that
rotates the log). A linearization keeps it first, but a step invoked
before it completed may have taken effect before it, its effect inside
the state recorded: an order that conforms with the step after the
snapshot stands, a failure is no verdict (`issue: warn snapshot: no
verdict: ... overlapped the snapshot`, Reason `no verdict (snapshot
overlap)`).

Lint (and `validate.NewChecker`) refuses a snapshot action the model
lacks, one without parameters, or one that leaves a variable unassigned
(`snapshot restore leaves ops unassigned`), with the fix. A log whose
steps carry no seqs cannot show a lost prefix: declare it
(`qtrace.MarkLost(steps, qtrace.Lost{Recorder: r, Prefix: true})`).

## A lost prefix without a snapshot: resume

When a log cannot record a snapshot, name a nullary model action whose
nondet picks give the states a log may start in, bounded by the model's
constants:

```quint
action resumeAny = { nondet b = 0.to(LIMIT).oneOf() all { balance' = b, ops' = 0 } }
```

```yaml
resume: resumeAny     # a trace whose first recorder lost its head starts from any of these
```

A trace whose first step's recorder lost its prefix, with no snapshot
step at its head, is then judged from every admitted state that keeps
the invariants checked (a start that violates one is not the trace's to
blame): `issue: info lost-prefix: recorder r lost seqs 1..2: judged from
the states resumeAny admits (11 of them); a pass means some admitted
state explains the rest`. The Go evaluator searches them (with any
`hidden:` actions); quint confirms a pass by replaying the steps from the
witness (`the steps pass from balance = 3, ops = 0, a state resumeAny
admits (quint confirms)`) and a failure by replaying them from every
start, when there are at most 200 (more: the evaluator's verdict alone,
said so). Hidden actions leave the verdict the evaluator's, as theirs
is. More states than the search keeps (20000) is no verdict: narrow the
resume, or record a snapshot. A bounded response already open in the
start state opened in the lost prefix and is not judged; one that fails
from the witness while other starts are admitted is no verdict.
Linearize and ResolvePending refuse such a trace: record a snapshot
for them. Lint refuses a resume that takes parameters, leaves a variable
unassigned, or is a step of the binding. A snapshot step, where there is
one, is used instead.

## A lost tail: what it leaves unjudged

A recorder that lost its last steps (a torn last line, which `ReadJSONL`
reads as a loss record after each of the log's recorders' last seq,
every recorder writing the log, since the cut line may not name its
own; a
killed process's `Buffered` sink, which no log shows: declare it with
`qtrace.MarkLost(steps, qtrace.Lost{Recorder: r, After: n})` or
`gotogether validate -lost-suffix r`) leaves unknown what it recorded
after its last kept step. So, in every check:

- the run is unsettled (its settle may have come before a lost step):
  `issue: info eventually: eventual vals not judged (V): recorder r lost
  its tail after seq n`. A lost enqueue cannot make a queue's emptiness a
  false PASS;
- a failure (conformance, an invariant, a within) at a step recorded at
  or after the cut, by another recorder that went on, may depend on the
  lost steps: no verdict (`conformance: N/A`, `issue: warn lost-suffix:
  ... may depend on r's lost steps after seq n`, Reason `no verdict (lost
  steps)`), never a FAIL. It is no PASS either: the run is not OK;
- a failure before the cut stands, and a pass says `issue: info
  lost-suffix: the recorded steps conform; recorder r lost its tail after
  seq n`.

A step near the cut (within `qtrace.RealTimeSlack` of it, two machines'
clocks apart) counts as after it, and a within obligation whose window
(its trigger plus the bound) reaches the cut is no verdict: the response
may be among the lost steps. A loss is the trace's of each actor its
recorder recorded, as a gap is: actors are separate instances of the
model. Claims over runs know it: `None(Violates("I"))` does not hold of
a run whose `I` (or an eventual val, unjudged for the lost tail) has no
verdict, and such a run kills no mutant. A call invoked before its
recorder's cut may have completed in the lost tail: neither `Completes()`
nor `None(Pending(...))` holds of the run, and it shows no `Pending`. A cached verdict of a run with
a loss record is keyed with its steps' times, which the cut is judged
by.

`-lost-prefix r` declares a lost head a log without seqs cannot show.
Both flags take comma-separated recorder ids.
