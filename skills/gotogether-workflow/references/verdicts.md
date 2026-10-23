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
| an invariant violated | `conformance: PASS`, `invariant I: FAIL` | `qtest.Violates("I")` | the model allows the behaviour and the property rules it out: a design bug, or a property stronger than the requirement |
| an issue | `issue: ...` (gaps, cycles in the recorded order, an order real time contradicts) | none: fix the recording | the steps could not be put in one order; a pass would mean little |
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
| `qtest.Conforms()` | every run conforms |
| `qtest.Some(a, b)` | one run shows both |
| `qtest.All(a)` | every run shows it |
| `qtest.None(a)` | no run shows it |
| `qtest.Reaches("kind").Faulted().InAtLeast(p)` | a share p of the runs reach a fault point of the kind (and fault it) |
| `qtest.Takes("action").InAtLeast(p)` | a share p of the runs take the model action (up to a failing step) |
| `qtest.NotVacuous()` | every invariant's antecedent (`A` of `A implies B`, or the binding's `antecedents`) held in some run where it passed; `Evidence` lists the vacuous ones |
| `qtest.Complete()` | an exhaustive search (`System.Exhaust`) ran every run within its bound: `MaxRuns` did not cut it short |
| `qtest.Exercised(except...)` | every model action is taken in some run and every reached fault kind faulted in some run, but those named |

`Takes` and `Exercised` say the runs exercised what the model and the
environment offer: an exploration whose runs never take `cancel` conforms
about cancel vacuously. `Evidence` lists the actions never taken.

Several claims are judged one by one. A bug that needs faults is "some
run", not "every run": the faults are chance. State the run's count by
seeds, not by hand.

## Where each workflow states it

- recorded steps: `qtest.Validate(t, binding, steps).Expect(t, ...)`;
- an exploration or sweep row: `x.Expect(t, claims...)`, `SweepConfig.Expect(row)`;
- Hegel: `qhegel.FindViolation(...).Expect(t, ...)`,
  `qhegel.Shrink(t, sys, rates).Expect(t, ...)` (fails when nothing is found);
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
log, and `reproduce: GOTOGETHER_SEED=n go test -run '^TestX$'` (an
exhaustive search's: its schedule, `GOTOGETHER_SCHEDULE="17:1"`, and the
choices it made otherwise). Two traps
have their own message: every run failing at step 1 (the binding's
constants do not match the scenario's: `qtest.Constants`, or a sweep
factor's `.Constant`), and "all runs conform" listing the fault kinds
never faulted and the scripted faults that never happened.

After a green test, `t.Log(x.Evidence())`: what the runs reached
(faults, pairs, code, mutants) is what "conforms" leaves unsaid.
