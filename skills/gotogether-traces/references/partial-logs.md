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
anywhere but first is skipped, with `issue: info snapshot`.

Lint (and `validate.NewChecker`) refuses a snapshot action the model
lacks, one without parameters, or one that leaves a variable unassigned
(`snapshot restore leaves ops unassigned`), with the fix. A log whose
steps carry no seqs cannot show a lost prefix: declare it
(`qtrace.MarkLost(steps, qtrace.Lost{Recorder: r, Prefix: true})`).

## A lost tail: what it leaves unjudged

A recorder that lost its last steps (a torn last line, which `ReadJSONL`
reads as a loss record after each of the log's recorders' last seq; a
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

`-lost-prefix r` declares a lost head a log without seqs cannot show.
Both flags take comma-separated recorder ids.
