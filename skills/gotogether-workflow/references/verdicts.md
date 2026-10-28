# Verdicts and expectations

One reference for what a check can say and how a test states what it
expects. Every workflow (validation, exploration, sweeps, fuzzing, Hegel,
mutants) ends in one of these verdicts.

## A run's verdict

| Verdict | Report says | Expectation | Means |
|---|---|---|---|
| conforms | `conformance: PASS`, every invariant `PASS` | `qtest.Conforms()` | the run is a behaviour of the model and its properties hold |
| conforms, decided in Go | as above, and `judged by the Go evaluator, with no quint replay` | as above | the Go evaluator (internal/qeval) passed it; every failing verdict below is quint's, from its replay (`GOTOGETHER_EVAL=quint` replays passes with quint too, `=shadow` with both, failing on any difference) |
| a step the model cannot take | `record <id> is not a transition of the model from the state reached` (`step i` for an inferred step), with `the model expects: <call>, which changes:` when the model answers that request otherwise | `qtest.Rejects("action")` (`""`: any) | the code did something the model forbids from that state |
| a state mismatch | `does not match` with `expect:` and `obs:` | `qtest.Mismatch("action")` | the step was allowed, but the observed state (`obs.*`) differs from the model's after it |
| an eventual val false at the end | `invariant eventually V: FAIL` | `qtest.Violates(validate.EventuallyName("V"))` | the run ended (settled, in a simulation) with work the model says is pending: a livelock or lost work |
| an invariant violated | `conformance: PASS`, `invariant I: FAIL`, `violated after record <id>`, `the step changed, in the model's state:` | `qtest.Violates("I")` | the model allows the behaviour and the property rules it out: a design bug, or a property stronger than the requirement |
| an issue | `issue: ...` (cycles in the recorded order, an order real time contradicts; `issue: error duplicate: recorder r seq n is two different records`, two logs numbering one recorder) | `qtest.HasIssue(kind)` (`"duplicate"`, `"cycle"`, ...) where a mutant's defect is the input's, the report failing on it alone; otherwise fix the recording | the steps could not be put in one order; a pass would mean little |
| no verdict (dropped steps) | `issue: error gap: recorder r is missing seq [n]`, a failure after it `issue: warn gap: conformance not evaluated: the failure at step i ... comes after recorder r lost seq n`, Reason `no verdict (dropped steps)`, `RESULT: NO VERDICT` | none: not a `Rejects`, `Mismatch` or `Violates`, not OK, `Finishes()` and `SameFailure` unknown | a recorder lost a step mid-log (a sampled or dropped export): a failure at or after the gap (a later seq of its recorder, a later time on another; any failure but the initial state's when the order is linearized or the actor's steps carry a domain clock, which may put the dropped step first) may be the dropped step's doing; a failure before every gap stands, a FAIL, whatever another check's after it says; recorded steps that pass beside it are no verdict too; repair the telemetry |
| a lost prefix | `issue: error lost-prefix` | none: record a snapshot | a log starts after seq 1 (rotated, truncated): its first state is not `init`; a trace that starts with the binding's `snapshot` step is judged from its state instead (`issue: info lost-prefix: judged from the state recorded by A at seq N`) |
| no verdict (a lost tail) | `issue: warn lost-suffix: ... may depend on r's lost steps after seq n`, `conformance: N/A` (or the invariant's), Reason `no verdict (lost steps)` | none: not a `Rejects`, `Mismatch` or `Violates`, and not OK | a recorder lost its tail (a torn log, `MarkLost`, `-lost-suffix`) and the failure is at a step recorded after the cut; a failure before it stands, a pass says `the recorded steps conform; recorder r lost its tail after seq n`, and eventual vals are not judged |
| no verdict (a sampled choice) | `issue: warn choice: judged by quint, the Go evaluator having handed it over: A at step i makes a choice of its own (nondet v ...)`, Reason `no verdict (the model's choice, sampled)` | none, as for a lost tail | the action chooses inside and its expect reads the result: quint's replay drew one choice, so the failure is a sample; expose the choice as a parameter of the action, or record it as an observation (`lint -binding` warns of it) |
| no verdict (a snapshot overlap) | `issue: warn snapshot: no verdict: ... overlapped the snapshot`, Reason `no verdict (snapshot overlap)` | none, as for a lost tail | a linearized step invoked before the snapshot completed may be inside its state; record the snapshot when no operation is in flight |
| judged from a resume | `issue: info lost-prefix: ... judged from the states R admits (K of them)`, and on a pass `the steps pass from <state>, a state R admits (quint confirms)` | the usual ones | a log that lost its head with no snapshot, judged from every state the binding's `resume` admits: a pass means some admitted state explains the rest; past 20000 states no verdict (`narrow resume, or record a snapshot step`) |
| no verdict (a search's budget) | `issue: warn linearize: ... no verdict (conformance n/a)`, invariants `n/a` | none: not a `Rejects`, `Mismatch` or `Violates`; a `None(...)` claim fails over it (what the run did is not known), and a mutant with no failing run but such runs is `Undecided`, not killed | Linearize (or ResolvePending) ran out of its budget, or hidden actions past `hiddenDepth` (raise it) before finding an order that conforms, or one in which every invariant holds: raise `qtest.LinearizeBudget`, partition keys (`LinearizeByKey`), or record steps where they take effect |
| absent: dropped | `absent:` section, `NAME: dropped (no verdict): ... recorder r lost seq n while the response was due`; a within's or eventual val's failure there `issue: warn gap`, Reason `no verdict (dropped steps)` (a lost tail's: `no verdict (lost steps)`) | `qtest.Absent(name, validate.Dropped)` | a step due (a within's response, the step that makes an eventual val hold, a call's completion) may be among steps a recorder of its actor lost: a gap, a lost tail, a loss record; repair the telemetry |
| absent: uncollected | `NAME: uncollected (no verdict)`; a within the trace went past but its watermark does not reach: Reason `no verdict (uncollected steps)`; `issue: warn settle: recorder r's steps are complete only through T` | `qtest.Absent(name, validate.Uncollected)`, `qtest.Undecided(qtest.Violates(w), "uncollected")` | the window ends past the recorders' watermark (`-complete-through`, `qobs.Recorder.CompleteThrough`), or the run recorded no settle and ended first: wait for collection and raise the watermark, or record a settle |
| absent (a defect) | `NAME: absent (a defect)`, with the within's or eventual val's `FAIL`, or a call never completed | `qtest.Absent(name, validate.Defect)` with `Violates`, `Pending` | the recorders are complete past where the step was due (a settle, a watermark past the window, the trace going on past it) and lost nothing: the system did not take the step |
| eventual vals not judged | `issue: info eventually: eventual vals not judged (V)` | `qtest.Settles()` states whether runs settled | the trace's last record is no settle (no `Sim.Settle`, a step after it, its error dropped) or another recorder recorded after it (`issue: warn settle`): an exploration claiming `Conforms()` of such runs fails, unless it also claims whether they settle |
| incomplete (a prefix) | `validate -require-finish` (or `complete: true`): `the run: incomplete (no settle)`, `actor A: incomplete (pending calls)`, exit 1; `no verdict (lost tail)` and `no verdict (ITF trace)`, exit 1 too | `qtest.Finishes()` | the run passes and is no finished one: it never settled, so its eventual vals were not judged, or a call was left pending (one `-resolve-pending` placed included). A lost tail may hold what is missing (no verdict: check a log that kept its steps); an ITF trace records no settle (see "A prefix vs a finished run") |
| real time bounds nothing | `issue: warn linearize: ... real time bounds none of its N steps` | none: record calls with `qobs.Invoke` and `Call.Done` | no step has an invocation time, so every order of its threads is allowed: sequential consistency, not linearizability |
| a tool failure | an error, exit 2, no verdict | none: fix the binding or model | a template, a type, a missing field, or quint itself |

`qtest.AnyOf(a, b)` is either: it holds if one does, and has no verdict
if neither does and one has none. A run with no verdict for an
expectation neither meets nor misses it, wherever it is read: `Holds`
is false, `Run.Expect` fails saying so, `Some` is not shown by it and
`All` and `None` do not hold over it. A report's summary shows what the failing
step changed in the model's state (or would have, for the model's
answer to a rejected request), and `-v` the whole state: read it before deciding whose bug it is (the
workflow skill's triage table).

## Reading a failure at 3 a.m. (validate's report)

A report names a failing record once, by where it was read: `record
day.jsonl:12` (a step log's file and line), `record span 5b8a…` (a span,
its trace on the `record:` line), else `record <recorder>#<seq>`. Grep
that. The `record:` line adds the step's wall time (UTC), its process,
and its recorder when that says more (grist's
`<app>/<node>/<incarnation>`: the pod and its start). `-v` adds the
step's place in the replay and its recorder's seq, every example of the
actions enabled instead (the model's moves, never records: an action
enabled many ways shows its count without `-v`), and the model's state
whole. The invariants a failure leaves unchecked are one line, `invariants
not checked (N: ...)`: the model's states after a rejected step are not
the run's, so nothing after it is judged.

`validate -json` (and `simulate -json`) prints the report as data,
`gotogether.report/1`: each failure's `record` (`id`, `time`, `process`,
`recorder`, `traceId`, `spanId`, `step`, `seq`), `brief` (one line for an
alert), `why.expected`, `changes`, the `state` whole. `-report
junit=FILE`, `-report sarif=FILE` write the CI renderings beside the
text; `GOTOGETHER_REPORT_DIR` makes every validate and simulate write
its JSON there (the Action's job summary reads it).

| Verdict (`reason`) | Severity | First look | Then |
|---|---|---|---|
| `rejects <action>` | page: the code did what the model forbids | the record's line in the log, its time and pod; `the model expects` is the answer the model would have given that request | the code path that answered otherwise (fix the code), or the binding's mapping of the outcome; `gotogether nav ... -at N` opens the model before the step |
| `mismatch after <action>` | page: the step was allowed, the state observed differs | `expect:` against `obs:`; the step's `changed` lines | a read that saw stale or partial state, or an expect that reads the wrong field |
| `violates <I>` | page when `I` is a requirement on the code (`simulate.allows`), else the model's bug | `the step changed` lines: the field that broke `I` | the decision that let the step happen (a check missing, a race); a property stronger than the requirement is the model's to fix |
| `no verdict (...)` | ticket, not a page: the telemetry lost steps | the `issue:` line naming the recorder and the seqs lost | repair the export; a failure before every gap is a FAIL and pages |
| `incomplete (...)` | ticket: a run that never finished | the finish line (`no settle`, `pending calls`) | the run's end: a settle not recorded, a call never answered |
| tool error (exit 2, `"verdict": "error"`) | the pipeline's, not the service's | the error's text: it names the fix | the binding, the model or quint |

## A prefix vs a finished run

A run cut short conforms: three steps of a five-step workflow are a
behaviour of the model, and `Conforms()` passes them. That is right of a
run that stopped by right (a load test's horizon, a run with work
pending) and wrong of a journey that had to finish (a browser driver's
session, an end-to-end test). Say which:

- the model latches the journey's end in a ghost flag and the binding
  lists it under `eventually:` (gotogether-patterns, section 5);
- the run records `qobs.Settled` once the driver's calls are answered
  and the collector drained (`Sim.Settle` in a simulation), with nothing
  after it;
- a test claims `qtest.Finishes()`: conforms, `Settles()`, every eventual
  val judged and true, `Completes()`. Its failure says which is missing:
  `the run recorded no settle last`, `eventually V is false where actor A
  settled: the run stopped short of the journey's end`, `call c (process p)
  was invoked and never completed`, `the binding states no eventual val`;
- the CLI says it with `gotogether validate -require-finish`, or the
  binding's `complete: true`: one judgment with `Finishes()`
  (`validate.Report.Finished`), so the two agree. A passing run that
  never settled, or left a call pending, exits 1 as `incomplete (...)`,
  where without the flag it passes with an info issue. A call
  `-resolve-pending` placed is still unfinished: its caller never got
  the answer. A lost tail exits 1 as `no verdict (lost tail)`, an ITF
  input as `no verdict (ITF trace)`, never a pass. A binding with no
  eventual val is refused (exit 2). `complete` changes only the CLI's
  exit; a test states `Finishes()` itself.

The three claims of a run's end, side by side: each is a part of the
next.

| Claim | States | The CLI |
|---|---|---|
| `qtest.Settles()` | the system settled: a settle recorded last (`Sim.Settle`, `qobs.Settled`) | (none alone) |
| `qtest.Completes()` | every call recorded with `qobs.Invoke` completed | (none alone) |
| `qtest.Finishes()` | it conforms, `Settles()`, every eventual val holds at the settle, `Completes()` | `validate -require-finish`, or `complete: true` |

## Over many runs: claims

An exploration, a sweep row, a fuzz corpus or a mutant's runs are judged
together (`Exploration.Expect`):

| Claim | Holds when |
|---|---|
| `qtest.Some(qtest.Rejects("ship"))` | some run shows it (the run is logged). An expectation alone over runs panics, naming `Some`, `All` and `None`, but the four whose names say every run: `Conforms`, `Settles`, `Completes`, `Finishes` |
| `qtest.Conforms()` | every run conforms, and its system settled if the run had it settle; a run with no verdict (a search's budget, a lost tail, dropped steps) neither conforms nor fails, so the claim fails as `N of M runs have no verdict` |
| `qtest.Some(qtest.Pending("action"))` | some run left a call of the action invoked and never completed: a caller given up on, or that crashed |
| `qtest.Settles()` | every run's system settled: it recorded a settle (`Sim.Settle` found it quiet, or `qobs.Settled`) with nothing after it, on its recorder or (by time, give or take a millisecond) on any other. A run with no Settle, or on the native runtime, does not; one with none that lost its tail (or whose log is still being collected) may have settled out of sight, and has no verdict; `None(qtest.Settles())` states a livelock. Across machines the times are their clocks': one behind can hide a step after the settle, one ahead unsettle a quiet run |
| `qtest.Completes()` | every operation recorded with `qobs.Invoke` completed; a report names each other as `invoked, never completed: <call>`. A crashed caller's call is pending by right: claim it of callers that do not crash |
| `qtest.Finishes()` | every run is a finished one: it conforms, settled, every eventual val was judged and holds (a binding with none finishes no run), and every invoked call completed; a run that may have finished in a lost tail has no verdict. A miss names the part missing and the fix |
| `qtest.Some(a, b)` | one run shows both |
| `qtest.Some(qtest.Not(e))` | some run misses e (`gristtest.NoClientErrors`' negation: a client call refused, erring or unanswered); a run with no verdict for e has none for `Not(e)`; `Not(Conforms())` panics: name the failure |
| `qtest.All(a)` | every run shows it |
| `qtest.None(a)` | no run shows it, and every run has a verdict for it: one with no verdict on its conformance, or on an invariant (or eventual val) `a` names (a lost tail's, hidden actions past their cap), fails it |
| `qtest.Reaches("kind").Faulted().InAtLeast(p)` | a share p of the runs reach a fault point of the kind (and fault it) |
| `qtest.Takes("action").InAtLeast(p)` | a share p of the runs take the model action (up to a failing step) |
| `qtest.NotVacuous()` | every invariant's antecedent (`A` of `A implies B`, or the binding's `antecedents`) held in some run where it passed; `Evidence` lists the vacuous ones. It fails when it cannot judge: no invariant with an antecedent, or one quint cannot evaluate |
| `qtest.Exercised(except...)` | every model action is taken, every option of every reached fault kind taken (by name, among those its choices offered: `drop` does not stand for `duplicate`), and every nemesis a run enabled fired (by name: `crash.a` does not stand for `crash.b`; heals aside, since a cut heals by itself), each in some run, but those named, which may be any declared kind or option, reached or not; a name covers those below it (`"nemesis.partition"`, `"rpc.payments"`) |
| `qtest.Scenario(qtest.Takes("submit"), qtest.Takes("verify").Outcome("ok"), qtest.Takes("open"))` | a run's steps take each in turn, others between (a Take's `With(param, literal)` wants an argument too); under `Some` one run, `All` every run, `None` no run. Missed: per run the longest prefix taken, each at its step, where it stopped, the next step's near misses, and whether the model on its own can follow it (`NamedScenario` names it; `gristtest.Scenario` from the catalog's `Command.With(...).Refused(err)`) |

Every claim has three values: it holds, it fails, or it has no verdict,
which fails the test too. Over no runs at all (`Seeds` below 1, a filter
that left none) no claim has one: `All` and `None` would hold of
nothing, so each fails with `want ...; TestX#1 has no runs, so the claim
has no verdict`, as `qtest.Runs()` with none does. `TakesEach()` of no
takes, or a catalog's `Exercised` that excepts every block, panics: it
would hold of any runs. A `Satisfies` function returns an error wrapping
`qtest.NoVerdict` for a run it cannot judge.

`Takes` and `Exercised` say the runs exercised what the model and the
environment offer: an exploration whose runs never take `cancel` conforms
about cancel vacuously. `Evidence` lists the actions never taken.

Several claims are judged one by one, by one judgment: `System.Mutate`
kills a mutant by exactly what would fail the test (NotVacuous included). A bug that needs faults is "some
run", not "every run": the faults are chance. State the run's count by
seeds, not by hand.

## What bounds a pass

A claim about every run or no run (`All`, `None`, `Conforms`) is a
proof only of the runs made. A passing exploration's summary line, and
its `Evidence`, say what bounds it:

| The summary says | Evidence | Means |
|---|---|---|
| `30 runs, all conform (quint confirmed 2, the Go evaluator alone the other 28)` | `judges: {evaluator: 28, confirmed: 2}` | the Go evaluator passed 28 runs with no quint replay; quint judged 2 too (`GOTOGETHER_EVAL_SAMPLE`, 1 in 50 by default, at least 1 in 5 for a plan whose actions reach a set fold of no commuting shape or an operator new since the last shadow-clean nightly). A failure is always quint's. `(the Go evaluator's, none confirmed by quint)`: no pass was sampled |
| `; a sample, no proof: a failure 1 run in 10 shows is missed at most 5% of the time, a rarer one more often, one the runs cannot reach always` | `sample: {runs: 30, rate: 0.095, oneIn: 10}` | seeded runs: a universal claim that held bounds the rate of a failure it forbids below `rate` at 95% confidence (about 3/runs), and says nothing of a fault never on, a workload that never asks, a run too short. Said when a universal claim passed; a claim some run shows has its witness |
| `; complete within 2 delays and 1 fault: every run within that bound, none beyond it` | `search: {delays: 2, faults: 1, complete: true}` | `System.Exhaust`: every run within the bound ran; a run of a third delay was never tried |
| `; truncated within 1 delay and 0 faults: at least 7 runs within it left untried (Bound.Partial), ...` | `search: {complete: false, left: 7}` | MaxRuns cut it: the claims are about the runs it got to (without `Partial` the test fails) |
| `; PCT depth 3, Steps 500: a bug of depth up to 3 is found in a run with probability at least 1/(4 tasks·500^2) = 1e-06, ...` | `pct: {depth: 3, steps: 500, maxSteps: 400, maxTasks: 4, probability: 1e-06}` | `qenv.NewPCT`'s guarantee per run, n the most tasks a run created; over r runs a bug is missed at most (1-p)^r |
| `; PCT depth 3, Steps 500: runs took up to 900 scheduling steps, past Steps, so its guarantee holds of none of them ...` | `probability: 0` | the change points fall only in the first Steps: set Steps to the run's length |
| `as expected, taking put in at least 50% of the runs: 16 of 30 runs (want 15); at that rate 30 runs fall short of it 36.2% of the time` | | a share claim (`InAtLeast`) that held by a margin as many seeds may miss another day: more seeds, or a lower share (said when above one in a thousand) |

`gotogether evidence` adds each test's bound to its line: `(complete
within 2 delays and 1 fault)`, `(search truncated)`, `(a sample: a
failure in under 9.5% of runs may be missed)`, `(28 judged by the Go
evaluator alone)`. A manifest's actors say `judgedBy: "evaluator"` (or
`"both"`) for a pass quint did not judge alone. `simulate`'s sampled
`holds` says `(a sample, no proof: a violation that 1 trace in 334 shows
is missed at most 5% of the time by 1000 traces, a rarer one more often;
-graph searches every state)`; `-graph`'s `bounded` is a truncated
graph's, which proves nothing past the states explored.

## Where each workflow states it

- recorded steps: `qtest.Validate(t, binding, steps).Expect(t, ...)`;
- an exploration or sweep row: `x.Expect(t, claims...)`, `SweepConfig.Expect(row)`;
- Hegel: `qhegel.FindViolation(...).Expect(t, ...)`,
  `qhegel.Shrink(t, sys).Expect(t, ...)` (fails when nothing is found);
  with `qtest.ShrunkTo(n)` for how far it must shrink (at most n faults),
  never a hand count of `v.Faults()`;
- where an invariant must first fail: `qtest.Violates("inv").At("action")`,
  never a walk of the report's actors;
- a trigger search: `qhegel.FindTrigger(t, sys, space).Expect(t, qload.SomeTrigger())`
  (or `NoTrigger()`);
- observation mutants of a conforming log: `qtest.MutateSteps(t, binding,
  steps).Expect(t, qtest.Killed(sel), qtest.Survived(sel))`, a selector
  `qtest.StepOp("corrupt").Action("deposit").Field("obs.balance").Change("+1")`
  (`.Index(i)` for one step). A claim whose selector names no mutant
  fails (a misspelled action or field), as does one any named mutant
  contradicts; an undecided mutant (no verdict) meets neither. A mutant
  is killed when it fails where the original conformed, or when it
  does not finish where the original finished; it survived with the
  original's verdict; one refused before a verdict, by Reconstruct (an
  overlap, a cycle) or by the binding (a field it reads removed), is
  malformed, never killed, and in no score;
- a shrink of a driver's actions: `qtest.ShrinkActions(t, actions,
  qtest.ActionShrink[A]{...}).Expect(t, qtest.Violates("inv"),
  qtest.AtMostActions(n))`, its Expectations judged on the minimal
  run as `Run.Expect` judges them;
- the model alone: the binding's `simulate` section (`holds`, `allows`,
  `possible`, `terminal`), judged by `gotogether simulate`. With
  `-graph`, `holds` is printed only for a complete graph (a proof for
  the instance); a truncated one says `bounded` (no state explored
  violates it, which proves nothing past them), an invariant the
  evaluator cannot evaluate somewhere `unchecked` (exit 2), a state the
  step cannot leave `deadlock` (exit 1) unless `terminal` holds there
  (`ends`), and states only a stutter leaves `quiet` (never a failure:
  a model that can always read, a grist model, has no deadlock). An
  eventual val is judged as cycles, as true infinitely often (at
  quiescence, for a run that settles) under weak fairness per action:
  `live` (no fair cycle keeps it false: proved in a complete graph for
  a system at least that fair, `bounded` in a truncated one),
  `hot` (a fair cycle keeps it false forever, unclassified: a report),
  `allows` (the same, the val in `simulate.allows`), `FAIL` (the same,
  the val in `simulate.holds`: the model should drive it; fix it, or
  name the actions that stop at a settle, `simulate.stops`, or need not
  run, `simulate.unfair`). "fairness rules out N hot cycles and M hot
  states stuttering (the nearest leaves X enabled throughout, never
  taken)" says which weakly fair actions made the difference: if X is
  an input or a fault, declare it, and look again (a model whose state
  only grows, a grist model, has only the stuttering states). A lasso
  that ends ("the step cannot leave that state": a deadlock, or an end
  `simulate.terminal` declares) is a run that stops with the val false:
  in `holds`, the model's bug. A run's `hot cycle (evidence, no verdict
  ...)` line is a run that did not settle going round a cycle of model
  states with the val false: read its steps as the storm or livelock
  that kept it from settling, or as progress the model does not see (a
  counter that wraps repeats a state too); it is not a verdict that the
  cycle goes on forever (examples/requeue's storm does, and its model's
  lasso says so). A
  counterexample's replay marked as drawing among successors may not
  reproduce in quint test; the path printed is the evaluator's;
- connect: a replay error (`StepError`: `step`, `state`, `check`), no
  expectation type: assert `err != nil` for a mutant (examples/bank's
  `TestModelDrivenCatchesOnlyDivergence`).

## Not reproduced, never a pass

A rerun of a shrunk sequence (`qtest.ShrinkActions`) that does not show
the failure kept is "not reproduced", and that is all it says: the
failure was not seen in it. It is not a pass of the sequence, and the
shrink treats it so:

- a rerun with no verdict for the failure kept (a lost tail, which may
  hold it; an invariant of a conforming actor left unjudged, which may
  have failed first; a search out of its budget) is undecided
  (`ShrunkActions.Undecided`): not among a candidate's reruns, and a
  one-removal decided by such reruns alone leaves the result not
  minimal;
- a rerun whose Rerun returned `qtest.ErrDiverged` (the driver could not
  do the actions) is neither reproduced nor not: counted apart, not
  among a candidate's reruns, and a sequence whose one-removal only ever
  diverged leaves the result not minimal (`ShrunkActions.NotMinimal`);
- a candidate is taken as passing only after the calibrated reruns
  (`Calibration`: n from the original's rate) all missed, wrongly with
  chance at most α (so `Minimal` holds with chance at least 1−α for each
  removal); a rate below 1 at the end (`ShrunkActions.Rate`)
  says the failure needs more than the actions (timing, data), not
  that the minimal sequence is wrong;
- another failure is not a reproduction: `qtest.SameFailure` (the
  default kept) wants each of the original's failing actors' first
  failure, kind and name.

## When it fails

The failure names the run: its seed, its faults, the report, the step
log, and `reproduce: GOTOGETHER_RUN='TestX#2:seed=n' go test -run '^TestX$'`:
the run in its exploration (`#2`, the test's second), or an exhaustive
search's schedule (`:schedule=17:1`, the choices it made otherwise). The
paths a failing claim prints outlive the test: the files were in the
test's `t.TempDir()`, so a failure copies them out (to
`$TMPDIR/gotogether-*`, and says so), as `Run.Expect` does for Record's
and Validate's runs. A passing claim satisfied
by failing runs (`Some(Rejects(...))`) logs its first example headed `as
expected, <claim>:`, without paths: its files are not kept
(`GOTOGETHER_KEEP=dir` keeps them). When the rate it measured would let
as many seeds miss the claim more than one time in a thousand, the line
says how often, and the seeds that would not (`at that rate 20 runs
miss it 35.8% of the time: 135 seeds ... (356 at the rate's lower
bound)`): a claim held by luck fails another day, so raise `Seeds` to
it; a rate measured from a few failing runs is loose, and the lower
bound's count is the safer one. Run alone, only the claims about every
run are judged. Two traps
have their own message: every run failing at step 1 (the binding's
constants do not match the scenario's: `qtest.Constants`, or a sweep
factor's `.Constant`), and "all runs conform" listing the fault kinds
never faulted and the scripted faults that never happened.

After a green test, `t.Log(x.Evidence())`: what the runs reached
(faults, pairs, code, mutants) is what "conforms" leaves unsaid.

With `qtest.ResolvePending()`, an issue `resolve` says what the pending
calls were taken to have done: info, a resolution conformed, and which (a
call had no effect, or took effect after step i, before the first settle
that followed it); warn, none did, the one shown got furthest.
