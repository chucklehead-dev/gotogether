---
name: gotogether-mbt
description: Write, extend or debug a gotogether model-based test in Go, where Quint traces drive the implementation (connect.Run, connect.Test, connect.Switch, StateDriver, Pick), or a whole program follows a trace's faults (System.FollowTraces). Use when asked to test Go code against a Quint spec "like quint-connect", when a connect test reports an unimplemented action or a state divergence, or when adding a new action to a driver.
---

# Model-based testing: Quint drives Go (`connect`)

`quint run --mbt` samples traces of the model; each is replayed on a
fresh driver, and after every step the implementation's state is
compared with the model's. The Go counterpart of quint-connect.

## Choose your path

- The skeleton, decoding model values, the TDD loop, `CheckStep`, concurrency gates, fault scripts and the limits: [references/driver.md](references/driver.md).
- Inputs the model forbids: let Hegel drive (the gotogether-hegel skill).
- The model itself fails to typecheck or names nothing: the gotogether-model skill.

## Steps

1. A driver: `Step` dispatching with `connect.Switch` (`"init"` first),
   picks decoded with `connect.MustPick[T](s, "param")`.
2. A projection: `State() (any, error)`, exported fields named after the
   model's.
3. `connect.Run(t, connect.Config{Spec: ..., Main: ..., MaxSamples: 50,
   MaxSteps: 30}, newDriver)`.
4. One action at a time, until a run of 50 samples passes; then raise
   `MaxSteps`.
5. A program that runs by itself: `sys.FollowTraces(t, cfg,
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
| a fault left in `Script.Remaining()` | the program could not follow its trace | where it stopped short |
| `reproduce with QUINT_SEED=0x...` | the failing trace | rerun with it, `QUINT_VERBOSE=2` |

## Run it, do not infer it

Let the test name the next action and the diverging field; reproduce
with the seed it prints rather than reconstructing the trace by hand.
