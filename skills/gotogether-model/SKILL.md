---
name: gotogether-model
description: Write or adapt a Quint specification so Go code can be tested against it with gotogether (model-based tests, Hegel state machines, trace validation) - named actions, parameters picked under their own names, an instance module, boolean vals, restarts and outcomes as values, Choreo's transitions named. Use when creating a .qnt model of a Go component, when quint run/test/typecheck fails, or when a connect/qhegel/validate error points at the model (anonymous action, missing nondet pick, constants, restart action).
---

# Quint models that Go code can be checked against

One model serves three ways: its traces drive the code (`connect`), it
judges Hegel's runs (`qhegel`), and it replays recorded runs
(`validate`). A few conventions make that possible.

## Choose your path

- The nine conventions in full, cheap replays, Choreo models, the commands, the errors' table: [references/conventions.md](references/conventions.md).
- What the model should allow (requirement vs solution, the environment, ghosts, settles): the gotogether-patterns skill.
- The order of work (scenarios, safety, liveness, then code): the gotogether-workflow skill.

## Steps

1. Name every action the code performs; `step` is an `any { }` of them.
2. Parameters the code observes, each picked in `step` as `nondet
   <param> = S.oneOf()`.
3. `const`s for sizes and sets, an instance module for the design.
4. Properties as boolean `val`s; a restart a parameterless action; an
   outcome a value; observable choices as parameters. Finite internal choices
   have bounded ordinary-trace support (see conventions, item 9).
5. `quint typecheck`, `quint run --invariant`, then `gotogether simulate
   -binding` (`-graph`) with `holds`, `allows` and `possible`.
6. `quint run --mbt` once: read `mbt::actionTaken` and `mbt::nondetPicks`.
7. `gotogether scaffold -spec model.qnt -module M -out m.binding.yaml`.

## Checklist

- [ ] No inline `all { }` in `step`; every action named.
- [ ] Every `nondet` named like the parameter it feeds.
- [ ] No identifier Quint reserves (`to`, `of`, `get`, `set`, `size`, ...).
- [ ] Each acceptance scenario is a `possible`, each property a `val` in `holds` or `allows`.
- [ ] A crashable process has a restart action, a failing operation an outcome value.
- [ ] History properties are checked on arrival, with a ghost.

## Results

| Error | Means | Next |
|---|---|---|
| `an anonymous action was taken` | an inline `all {}` in `step` | name it |
| `unknown nondet pick "x"` | the pick is named otherwise, or missing | rename it after the parameter |
| `QNT500 Uninitialized const` | the parameterised module was run | `--main` the instance |
| `QNT101` (or `QNT008`) on a name: `item is a Quint built-in's name, which no parameter may take` | a reserved name (`item`, `to`, `int`) | rename it `item_` and map the field: `item_: '{{.item}}'` (scaffold writes it) |
| `possible ...: FAIL` (the model forbids it) | too strict for a scenario | `gotogether nav -binding` to the guard |
| a `holds` invariant violated in `simulate` | a design bug, or the property is wrong | the counterexample (`-graph -out cex.qnt`) |
| an `allows` invariant never violated | its check of the code would be vacuous | the model is too strict, or the property moved |
| `Cannot continue in "then"` (QNT513) in validation | a recorded step not enabled | the gotogether-traces skill |

## Run it, do not infer it

Do not decide by reading the spec what it allows: run `gotogether
simulate`, and walk states with `gotogether nav -binding m.binding.yaml
-json` (`-follow` takes branches).
