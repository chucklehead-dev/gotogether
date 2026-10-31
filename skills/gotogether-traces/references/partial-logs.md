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
error (its lost steps may have come after the snapshot). `gotogether itf`
writes such a trace with state 0 the snapshot's, and validating the ITF
starts there too.

A later snapshot is a checkpoint of the whole replayed state. Every state
variable must agree with the state described by its recorded parameters; a
difference is a conformance mismatch at that snapshot record. It cannot reset
the model or erase an earlier mismatch. For example, after a deposit of 5,
`restore(5, 1)` agrees with balance 5 and one operation; `restore(5, 0)`
disagrees even though the balance matches. Record these checkpoints atomically
with the operations they describe. A checkpoint does not repair a missing
prefix or gap earlier in the log, and does not establish settlement.
History-order and pending-effect searches refuse traces with later checkpoints
in this first slice: moving a checkpoint to the head must not turn it into a
free initial state. Partitioned validation also refuses them because a checkpoint
describes the whole state. Finite model-choice witness search retains its
existing snapshot exclusion. Validate a deterministic, ordinary ordered capture
instead.


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
for them. The starts are the Go evaluator's list of what the resume
admits (its agreement with quint is tested generally, not per run): a
pass's witness and the failure's "from every start" are over that list.
A run that only a start the invariants ruled out explains is a
mismatch, and the report says which start and invariant ("explained
only by a start that violates nonNegative"): narrow the resume.
Conformance, invariants and eventual vals are judged on one path, as a
replay judges them, with hidden actions too. Lint refuses a resume that takes parameters, leaves a variable
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
nor `None(Pending(...))` holds of the run, and it shows no `Pending`.
Wherever a claim is read (`Holds`, `Run.Expect`, `Some`, `All`, `None`),
a run without a verdict for it neither meets nor misses it; `AnyOf`
holds if a part holds, whatever another's verdict. A cached verdict of a run with
a loss record is keyed with its steps' times, which the cut is judged
by.

`-lost-prefix r` declares a lost head a log without seqs cannot show.
Both flags take comma-separated recorder ids.

## Absent steps: dropped, uncollected, defect

A step the run was expected to show and does not (a bounded response
not met in time, an eventual val not judged or false at the settle, a
call never completed) gets one cause in the report's `absent:` section
(`validate.Result.Absent`, `Report.Absent()`), so a production trace's
missing observation is not read as an application bug, nor the bug as
missing telemetry:

- **dropped** (no verdict): a recorder that would hold the step lost
  steps while it was due: a gap in its seqs (`Trace.Gaps`, with the
  times of the steps either side), a lost tail, a loss record. For a
  pending call, only a gap on its own recorder after its seq (where its
  completion would be). A
  within's or an eventual val's failure there is no verdict (`issue:
  warn gap`, Reason `no verdict (lost steps)`); the fix is telemetry
  that keeps its steps.
- **uncollected** (no verdict): the window ends past the watermark of
  those recorders, or the run recorded no settle and ended before the
  window did; a call whose recorders state a watermark and no settle
  reaches it. Wait for the collector and state a watermark past the
  window, or record a settle.
- **absent (a defect)**: the recorders are complete past the window (a
  settle, a watermark, or, with no watermark, the trace's own steps
  going on past it) and lost nothing: the only FAIL.

Which recorders would hold a step: those of its actor's steps in the
input. An actor is one instance of the model, and any of its recorders
may record the step that meets an obligation; a call's completion is
on its invocation's recorder, one of them. A recorder that has
recorded nothing of the actor yet (a restarted process's new recorder)
is in no trace, and a per-recorder watermark cannot cover it: state
`*` (every recorder) where recorders can appear later.

**A watermark** says a recorder's steps recorded at or before T are all
in the input: a record (`quint.complete_through_unix_nano`, recorder `*`
for every one) written by `qobs.Recorder.CompleteThrough(ctx, t)` where
delivery is known (after a flush, a collector's acknowledgement), by
`qtrace.MarkComplete(steps, r, t)`, or by `gotogether validate
-complete-through r=T` (repeatable; T in RFC3339 or Unix nanoseconds;
`*=T`) for an external collector that knows what it ingested (a
ClickHouse poll). Readers (JSONL, OTel) keep it, and `Filter` keeps it,
no later than its `Until`. A log with no watermark is taken whole, as
before: its trace's own end bounds what it shows, and nothing changes.
With one:

- a recorder whose steps carry seqs is complete through its last step
  with no seq missing below it, whatever its watermark says
  (`qtrace.CollectedThrough`): a lagging watermark costs no verdict
  where the seqs are dense. Steps without seqs have the watermark alone;
- a within never met is judged where the watermark reaches its window's
  end (give or take `qtrace.RealTimeSlack`), though the run never
  settled: what lets a production trace, which never settles, be judged
  absent. A response held late in what was collected is no verdict
  where the watermark falls short of its window (an earlier one may not
  be collected yet);
- a within whose window it does not reach is no verdict (Reason `no
  verdict (uncollected steps)`), even where the trace went on past the
  window, which without a watermark is a FAIL;
- a settle it does not reach (by more than the slack) settles nothing (`issue: warn settle:
  recorder r's steps are complete only through T`): a step after the
  settle may not be collected yet, so eventual vals are not judged, and
  `Finished` (`qtest.Finishes()`, `-require-finish`) is `no verdict
  (uncollected)`;
- a pending call with no settle is uncollected: neither `Completes()`
  nor `Pending(...)` holds.

A search (Linearize, ResolvePending) re-sequences an actor onto a
recorder of its own and carries the actor's collection onto it as it
is (`qtrace.CarryWatermark`), so its order is judged as the steps as
recorded are.

Claims: `qtest.Absent(name, validate.Uncollected)` (a within's
`binding.Within.Name`, `validate.EventuallyName(v)`, or a call's
action), over runs `None(qtest.Absent(w, validate.Defect))`; and
`qtest.Undecided(qtest.Violates(w), "uncollected")`,
`qtest.Undecided(qtest.Completes(), "not be collected yet")`.

