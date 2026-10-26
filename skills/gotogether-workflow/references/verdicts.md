# Verdicts and expectations

One reference for what a check can say and how a test states what it
expects. Every workflow (validation, exploration, sweeps, fuzzing, Hegel,
mutants) ends in one of these verdicts.

## A run's verdict

| Verdict | Report says | Expectation | Means |
|---|---|---|---|
| conforms | `conformance: PASS`, every invariant `PASS` | `qtest.Conforms()` | the run is a behaviour of the model and its properties hold |
| a step the model cannot take | `step i is not a transition of the model from the state reached` | `qtest.Rejects("action")` (`""`: any) | the code did something the model forbids from that state |
| a state mismatch | `does not match` with `expect:` and `obs:` | `qtest.Mismatch("action")` | the step was allowed, but the observed state (`obs.*`) differs from the model's after it |
| an eventual val false at the end | `invariant eventually V: FAIL` | `qtest.Violates(validate.EventuallyName("V"))` | the run ended (settled, in a simulation) with work the model says is pending: a livelock or lost work |
| an invariant violated | `conformance: PASS`, `invariant I: FAIL` | `qtest.Violates("I")` | the model allows the behaviour and the property rules it out: a design bug, or a property stronger than the requirement |
| an issue | `issue: ...` (gaps, cycles in the recorded order, an order real time contradicts) | none: fix the recording | the steps could not be put in one order; a pass would mean little |
| a lost prefix | `issue: error lost-prefix` | none: record a snapshot | a log starts after seq 1 (rotated, truncated): its first state is not `init`; a trace that starts with the binding's `snapshot` step is judged from its state instead (`issue: info lost-prefix: judged from the state recorded by A at seq N`) |
| no verdict (a lost tail) | `issue: warn lost-suffix: ... may depend on r's lost steps after seq n`, `conformance: N/A` (or the invariant's), Reason `no verdict (lost steps)` | none: not a `Rejects`, `Mismatch` or `Violates`, and not OK | a recorder lost its tail (a torn log, `MarkLost`, `-lost-suffix`) and the failure is at a step recorded after the cut; a failure before it stands, a pass says `the recorded steps conform; recorder r lost its tail after seq n`, and eventual vals are not judged |
| no verdict (a search's budget) | `issue: warn linearize: ... no verdict (conformance n/a)`, invariants `n/a` | none: not a `Rejects`, `Mismatch` or `Violates`; a `None(...)` claim fails over it (what the run did is not known), and a mutant with no failing run but such runs is `Undecided`, not killed | Linearize (or ResolvePending) ran out of its budget, or hidden actions past `hiddenDepth` (raise it) before finding an order that conforms, or one in which every invariant holds: raise `qtest.LinearizeBudget`, partition keys (`LinearizeByKey`), or record steps where they take effect |
| eventual vals not judged | `issue: info eventually: eventual vals not judged (V)` | `qtest.Settles()` states whether runs settled | the trace's last record is no settle (no `Sim.Settle`, a step after it, its error dropped) or another recorder recorded after it (`issue: warn settle`): an exploration claiming `Conforms()` of such runs fails, unless it also claims whether they settle |
| real time bounds nothing | `issue: warn linearize: ... real time bounds none of its N steps` | none: record calls with `qobs.Invoke` and `Call.Done` | no step has an invocation time, so every order of its threads is allowed: sequential consistency, not linearizability |
| a tool failure | an error, exit 2, no verdict | none: fix the binding or model | a template, a type, a missing field, or quint itself |

`qtest.AnyOf(a, b)` is either. A report's summary shows the model's state
around the failing step: read it before deciding whose bug it is (the
workflow skill's triage table).

## Over many runs: claims

An exploration, a sweep row, a fuzz corpus or a mutant's runs are judged
together (`Exploration.Expect`):

| Claim | Holds when |
|---|---|
| an expectation alone, e.g. `qtest.Rejects("ship")` | some run shows it (the run is logged) |
| `qtest.Conforms()` | every run conforms, and its system settled if the run had it settle |
| `qtest.Pending("action")` | some run left a call of the action invoked and never completed: a caller given up on, or that crashed |
| `qtest.Completes()` | every operation recorded with `qobs.Invoke` completed; a report names each other as `invoked, never completed: <call>`. A crashed caller's call is pending by right: claim it of callers that do not crash |
| `qtest.Settles()` | every run's system settled: it recorded a settle (`Sim.Settle` found it quiet, or `qobs.Settled`) with nothing after it, on its recorder or (by time, give or take a millisecond) on any other. A run with no Settle, or on the native runtime, does not; `None(qtest.Settles())` states a livelock. Across machines the times are their clocks': one behind can hide a step after the settle, one ahead unsettle a quiet run |
| `qtest.Some(a, b)` | one run shows both |
| `qtest.All(a)` | every run shows it |
| `qtest.None(a)` | no run shows it |
| `qtest.Reaches("kind").Faulted().InAtLeast(p)` | a share p of the runs reach a fault point of the kind (and fault it) |
| `qtest.Takes("action").InAtLeast(p)` | a share p of the runs take the model action (up to a failing step) |
| `qtest.NotVacuous()` | every invariant's antecedent (`A` of `A implies B`, or the binding's `antecedents`) held in some run where it passed; `Evidence` lists the vacuous ones. It fails when it cannot judge: no invariant with an antecedent, or one quint cannot evaluate |
| `qtest.Exercised(except...)` | every model action is taken, every option of every reached fault kind taken (by name, among those its choices offered: `drop` does not stand for `duplicate`), and every nemesis a run enabled fired (by name: `crash.a` does not stand for `crash.b`; heals aside, since a cut heals by itself), each in some run, but those named, which may be any declared kind or option, reached or not; a name covers those below it (`"nemesis.partition"`, `"rpc.payments"`) |

`Takes` and `Exercised` say the runs exercised what the model and the
environment offer: an exploration whose runs never take `cancel` conforms
about cancel vacuously. `Evidence` lists the actions never taken.

Several claims are judged one by one, by one judgment: `System.Mutate`
kills a mutant by exactly what would fail the test (NotVacuous included). A bug that needs faults is "some
run", not "every run": the faults are chance. State the run's count by
seeds, not by hand.

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
- connect: a replay error (`StepError`: `step`, `state`, `check`), no
  expectation type: assert `err != nil` for a mutant (examples/bank's
  `TestModelDrivenCatchesOnlyDivergence`).

## When it fails

The failure names the run: its seed, its faults, the report, the step
log, and `reproduce: GOTOGETHER_RUN='TestX#2:seed=n' go test -run '^TestX$'`:
the run in its exploration (`#2`, the test's second), or an exhaustive
search's schedule (`:schedule=17:1`, the choices it made otherwise). The
paths a failing claim prints outlive the test. A passing claim satisfied
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
