---
name: gotogether-mbt
description: Write, extend or debug a gotogether model-based test in Go, where Quint traces drive the implementation (connect.Run, connect.Test, connect.Switch, StateDriver, Pick), or a whole program follows a trace's faults (System.FollowTraces). Use when asked to test Go code against a Quint spec "like quint-connect", when a connect test reports an unimplemented action or a state divergence, or when adding a new action to a driver.
---

# Model-based testing: Quint drives Go (`connect`)

`quint run --mbt` samples traces of the model; each is replayed on a
fresh driver, and after every step the implementation's state is
compared with the model's. The Go counterpart of quint-connect.

## Steps

1. Write the driver: `Step` dispatching with `connect.Switch` (one case
   per action, `"init"` first), picks decoded with
   `connect.MustPick[T](s, "param")`.
2. Project the state the implementation has: `State() (any, error)`
   returning a struct whose exported fields are named after the model's.
3. Run it: `connect.Run(t, connect.Config{Spec: ..., Main: ...,
   MaxSamples: 50, MaxSteps: 30}, newDriver)`.
4. Implement one action at a time: `unimplemented action "X" (picks:
   a, b)` names the next; `states diverge` prints the field, the model's
   value and the implementation's.
5. Reproduce a failure with its `QUINT_SEED=0x...` (`QUINT_VERBOSE=2` logs
   every state).
6. For a program that runs by itself, let the trace choose its faults
   only: `sys.FollowTraces(t, cfg, qtest.FaultMap{...})`.

## References (when to read each)

- [references/driver.md](references/driver.md): the skeleton with code,
  how model values decode (`itf.Decode`), the TDD loop, `CheckStep`,
  concurrency gates, fault scripts, and the limits (a model-driven test
  never asks for what the model forbids: let Hegel drive for that, the
  gotogether-hegel skill).
