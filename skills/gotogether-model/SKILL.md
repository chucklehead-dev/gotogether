---
name: gotogether-model
description: Write or adapt a Quint specification so Go code can be tested against it with gotogether (model-based tests, Hegel state machines, trace validation) - named actions, parameters picked under their own names, an instance module, boolean vals, restarts and outcomes as values, Choreo's transitions named. Use when creating a .qnt model of a Go component, when quint run/test/typecheck fails, or when a connect/qhegel/validate error points at the model (anonymous action, missing nondet pick, constants, restart action).
---

# Quint models that Go code can be checked against

One model serves three ways: its traces drive the code (`connect`), it
judges Hegel's runs (`qhegel`), and it replays recorded runs
(`validate`). A few conventions make that possible.

## Steps

1. Name every action the code performs; `step` is an `any { ... }` of
   named actions, never an inline `all { }`.
2. Give actions the parameters the code observes, and pick each in
   `step` with `nondet <param> = S.oneOf()` under the parameter's name.
3. Put sizes and sets in `const`s, with an instance module for the
   deployed design (`import bank(ACCOUNTS = ...).*`).
4. State properties as boolean `val`s; model a restart as a parameterless
   action and an outcome as a value; lift hidden choices into parameters.
5. `quint typecheck`, then `quint run --invariant`, then, with a binding,
   `gotogether simulate -binding` (`-graph` for every reachable state):
   `holds`, `allows` and `possible` each checked.
6. Check the traces name what the code needs: `quint run --mbt` and
   read `mbt::actionTaken` and `mbt::nondetPicks`.
7. Scaffold the binding: `gotogether scaffold -spec model.qnt -module M`.

## References (when to read each)

- [references/conventions.md](references/conventions.md): the nine
  conventions in full, cheap replays (history properties, growing
  state), Choreo models, the workflow's commands and the common errors'
  table (`anonymous action`, `unknown nondet pick`, QNT500, QNT101,
  QNT513).
- The gotogether-patterns skill: what the model should allow.
