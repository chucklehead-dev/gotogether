---
name: gotogether-mbt
description: Write, extend or debug a gotogether model-based test in Go, where Quint traces drive the implementation (connect.Run, connect.Test, connect.Switch, StateDriver, Pick), or a whole program follows a trace's faults (System.FollowTraces). Use when asked to test Go code against a Quint spec "like quint-connect", when a connect test reports an unimplemented action or a state divergence, or when adding a new action to a driver.
---

# Model-based testing: Quint drives Go (`connect`)

`quint run --mbt` samples traces of the model; each is replayed on a
fresh driver, and after every step the implementation's state is
compared with the model's. The Go counterpart of quint-connect.

## Choose your path

- The skeleton, decoding model values (Go types generated from the model's: `gotogether types`), the TDD loop, `CheckStep`, `Step.Eval`, concurrency gates, fault scripts and the limits: [references/driver.md](references/driver.md).
- Inputs the model forbids: let Hegel drive (the gotogether-hegel skill).
- The model itself fails to typecheck or names nothing: the gotogether-model skill.

## Steps

1. A driver: bind each action to its method (`connect.Bind(newImpl).
   Action("deposit", (*Bank).Deposit, "account", "amount")`, picks named
   in parameter order), or a `Step` dispatching with `connect.Switch`
   (`"init"` first) and `connect.MustPick[T](s, "param")`.
2. A projection: `State() (any, error)`, or a bound `.State(func(T) S)`:
   return a concrete type S from State to have it checked before any
   trace. Exported fields named after the model's. One the model defines (`def balanceOf(a)`): compare in
   `CheckStep` with `connect.MustEval[int](s, "balanceOf", "alice")`, not a
   copy of the def in Go.
3. `connect.Run(t, connect.Config{Spec: ..., Main: ..., MaxSamples: 50,
   MaxSteps: 30}, newDriver)`, or `b.Run(t, cfg)` / `b.Test(t, cfg, test)`
   for a binding: checked before any trace is generated, where
   `connect.Run(t, cfg, b.Driver)` checks only at the first trace. The
   factory takes the trace's `testing.TB`; use its `Context` and `Cleanup`
   for resources, not the outer test's.
4. One action at a time, until a run of 50 samples passes; then raise
   `MaxSteps`.
5. A state a bug hides at, rare in the samples: name its boolean val in
   `Config.Require`, the state after the step under test (a witness ends
   there); the run replays the shortest path to it first
   (`witness_<name>`), logs how often the samples reached it, and fails
   when nothing did.
6. A program that runs by itself: `sys.FollowTraces(t, cfg,
   qtest.FaultMap{...})`, the trace choosing only its faults.

## Checklist

- [ ] Every action of the model has a case, or `connect.Skip` on purpose.
- [ ] `init` resets whatever the driver holds outside itself.
- [ ] `State()` compares something: every field maps to the model's.
- [ ] A divergence is fixed in the code unless the requirements say the model is wrong.

## Results

| Report says | Means | Next |
|---|---|---|
| `unimplemented action "X" (picks: a, b)` | the next action to write | add its case, decode those picks |
| `states diverge` with `.Field: model M, implementation I` | the code computed another state, or the projection maps wrongly | fix the code, or `State()` |
| `an anonymous action was taken` | an inline `all {}` in `step` | name it (gotogether-model) |
| `unknown nondet pick "x"` | the pick is named otherwise | rename the `nondet` after the parameter |
| `the trace has no mbt::actionTaken` | a `quint test` trace | `connect.Run`, or `NondetPath` |
| `QUINT_MAX_SAMPLES="many"` or another numeric configuration error | invalid selected environment value or explicit field; generation did not start | correct the named value; check the error from `Config.FromEnv()` |
| a fault left in `Script.Remaining()` | the program could not follow its trace | where it stopped short |
| `never reached the required x in N traces, and no witness trace reached it` | no state had the val true: the run proved nothing there | raise `MaxSteps`, or check it is reachable (`gotogether simulate`) |
| `the witness trace of required x` on a divergence | the shortest path to the required state failed | fix the code at that state; the trace is the whole reproduction |
| `required x: no witness trace (...)` | the evaluator found no path within its bounds | the samples still count; raise `MaxSteps` |
| `required x: reached in k of N traces` | how often the run reached it | a small k: raise the samples before trusting a pass |
| `Eval("x") at step N (a): ...` | the def is misspelt, takes other arguments, is undefined there, or is outside the Go evaluator | the fix the message names (driver.md, `Step.Eval`) |
| `connect.Bind: action x: ... takes N parameters ... but M picks are named` | the binding names too few or too many picks | one pick per parameter, in order (a leading `context.Context` is not named) |
| `connect.Bind: action x: ... returns T besides an error, and nothing checks it` | a result the run would drop | `.Returns("x", "def", picks...)` against the model's def, `.Observes("x", picks...)` when it is a choice the model picks, or bind a func that drops it |
| `the binding does not fit spec (N mismatches, found before any trace ran)` | the bound driver was checked against the model's actions, picks and types; every mismatch is a line below it | fix each line as it says |
| `connect.Bind: action x: the model has no action x` or `(pick p): the model has no nondet or parameter p` | a name the model lacks: a typo, or a rename in the model | the model's name (the line lists them) |
| `connect.Bind: action x, parameter i (pick p): p is a pick of y, not x (x's picks: ...)` | p is another action's choice: quint records every nondet at every step, so it would decode y's | a nondet that feeds x's call in `step` (the line lists x's own) |
| `connect.Bind: State: the projection, Go type S, does not fit the model's state (...)` | a field the model's state lacks, an unexported one, or one of a type that does not decode | name each field after a state variable (or tag it `itf:"-"`), of a type that decodes from it |
| `Go type T does not fit the model's Q`, or `Returns' def d is no val or def` | a parameter, an observed result or a Returns' result `itf.Decode` cannot fill from the model's type, or a def the model lacks | a Go type that decodes from Q (driver.md, Bind) |
| `FAIL f.go: stale: type X changed` or `missing` (`gotogether types -check`) | the model's types changed since the file was generated, or it was never written | the `run gotogether types ...` it names, then commit the file |
| `the implementations disagree` | a `connect.Differential`'s implementations differ, where the model may say nothing | decide which is right, fix the other |
| `reproduce with QUINT_SEED=0x...` | the failing trace | rerun with it, `QUINT_VERBOSE=2` |

## Run it, do not infer it

Let the test name the next action and the diverging field; reproduce
with the seed it prints rather than reconstructing the trace by hand.
