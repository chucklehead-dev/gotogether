# Conventions a checked model follows, cheap replays, Choreo, the workflow, common errors

From the gotogether-model skill (its SKILL.md says when to read this).

## Quint models that Go code can be checked against

gotogether uses one Quint model three ways: Quint traces drive the Go
code (`connect`), Hegel-driven Go runs are judged by the model
(`qhegel`), and recorded production telemetry is replayed through it
(`validate`). The model must follow a few conventions for all three.

## Conventions (check every one)

1. **Name every action the implementation performs.** `step` must be an
   `any { ... }` of *named* actions. An inline `all { a, b }` inside `step`
   is an anonymous action: quint-connect cannot tell which code to run.
   Fix: `action aThenB = all { a, b }` and use `aThenB` in `step`.
2. **Parameters the code can observe.** Actions take as parameters what the
   implementation knows at that point (account, amount, batch id). Values
   the model chooses internally (a fresh id it allocates) are not
   parameters: the binding checks them with `expect` against what the code
   reported.
3. **`nondet` names = parameter names.** In `step`, pick each parameter with
   `nondet <param> = S.oneOf()` named exactly like the action parameter it
   feeds. `quint run --mbt` records picks by the `nondet` name, and both
   `connect.Pick[T](s, "<param>")` and `validate.ValidateITF` match them by
   name. Picks of other actions in the same step also appear (Quint records
   every `nondet` evaluated); ignore them.
4. **Parameterised module + instance module.** Put sizes and sets in
   `const`s and add an instance module for the deployed design:
   ```quint
   module bank { const ACCOUNTS: Set[str] ... }
   module bankDesign { import bank(ACCOUNTS = Set("alice", "bob")).* }
   ```
   `connect.Config{Spec: "bank.qnt", Main: "bankDesign"}` generates traces
   of the instance; a binding instantiates `bank` with constants sized to
   each recorded run (`constants:` templates such as `{{ max "amount" }}`).
   `-instance bankDesign` must be a single `import M(...).*`.
5. **Properties are boolean `val`s** (`val noOverdraft = ...`). They are
   `quint run --invariant` targets for the design, and `invariants:` of the
   binding for recorded runs. The binding's `simulate` section says which
   the model guarantees (`holds`) and which are requirements on the code
   the model lets a bad implementation break (`allows`); `gotogether
   simulate -binding` checks both (gotogether-workflow §1). Its
   `possible` list says what the model must be able to do (a predicate
   some trace reaches, an action some trace takes, `steps:` taken in
   turn): each scenario a conforming run should show, so a model too
   strict for it fails before any code is checked against it.
6. **Restarts.** If the process can crash and restart, give the model one
   parameterless action for it (`action crash = ...`); the binding's
   `restart: crash` inserts it where the recorded process identity changes.
7. **Outcomes as values.** If an operation can fail in ways the design
   cares about, model the outcome as a parameter
   (`writeManifest(x, o: Outcome)` with `type Outcome = Ok | Fail | Ambiguous`)
   rather than leaving failures out.
8. **Hidden state is fine.** The model may have variables the code cannot
   observe (queues, other components); validation lets the model compute
   them. Only what a driver's `State()` returns, or a binding's `expect`
   mentions, is compared.
9. **Keep deterministic actions deterministic.** An action with a hidden
   `nondet` inside it (not a parameter) makes replay ambiguous: replay is a
   deterministic `run`. Lift the choice into a parameter.

## Keep replays cheap

(The gotogether-patterns skill covers what to put in the model:
specifying the requirement, environment guarantees, linearization
points, ghost variables, settle-conditioned properties, mechanism
models.)

Recorded runs can be thousands of steps long. Cost per step depends on
the state and on how invariants are written:

- **History properties.** For "every id is unique" or "no message is
  delivered twice", check each new element when it arrives and record a
  repeat in a ghost variable, with `val ok = not(repeated)`. Don't write
  an invariant that re-scans the whole history in every state: that is
  quadratic over a run.
- **State that grows with the run.** For opaque values (ids, messages)
  some memory of the past is unavoidable. If the values have structure
  (node, incarnation, counter), a second model can check the mechanism
  instead: each incarnation's counter only increases. Its state stays
  bounded. Pair it with a property test of the encoding's injectivity
  (a pure function), and keep the spec-level model for short runs
  (examples/glomers/uniqueids).
- **Big states.** `gotogether validate` writes no ITF for a conforming
  run (it holds the full state at every step); `-itf` asks for it. In
  code, `validate.Options{NoITF: true}`.

## Choreo models

[Choreo](https://github.com/informalsystems/choreo) specs work with every
workflow once each transition is a named action
(examples/twophase/model/twophase.qnt):

```quint
action step_with(v: Node, listener: LocalContext => Set[Transition]): bool =
  choreo::step_with(v, listener, (c, _) => c)
action prepare(node: Node): bool = all { PARTICIPANTS.contains(node), step_with(node, spontaneously_prepares) }
action decideCommit: bool = step_with(COORDINATOR, decides_on_commit)
action step = { nondet node = PARTICIPANTS.oneOf()  any { prepare(node), decideCommit, ... } }
def stage(n: Node): Stage = choreo::s.system.get(n).stage   // for bindings: choreo::s is module-private
```

- Choreo's own `choreo::step(main_listener, ...)` picks the transition
  inside the framework: a recorded step cannot say which, and
  `mbt::actionTaken` is always `step`. Keep it for simulation if you
  like; bind and drive the named actions.
- A guard the listener leaves implicit (a transition that only makes
  sense for participants) belongs in the named action too
  (`PARTICIPANTS.contains(node)`): the recorded node is the code's claim.
- connect: `StatePath: []string{"s", "system"}` compares a driver's
  per-node state with `choreo::s.system`.
- Vendor `choreo.qnt` and `spells/basicSpells.qnt` next to the model
  (Apache-2.0): its import paths are relative.

## Workflow

```sh
quint typecheck model.qnt                                   # always first
quint run model.qnt --main design --invariant prop --max-steps 30 --max-samples 1000
gotogether simulate -binding m.binding.yaml                 # every invariant, holds or allows, once the binding exists
gotogether simulate -binding m.binding.yaml -verify 8       # the same, every trace of up to 8 steps (Apalache: Java)
gotogether simulate -binding m.binding.yaml -graph          # the same, every reachable state (Go evaluator): deadlocks, actions never enabled, eventual vals as fair cycles
quint run model.qnt --main design --mbt --max-steps 5 --n-traces 1 --out-itf /tmp/t.itf.json
# look at mbt::actionTaken and mbt::nondetPicks in /tmp/t.itf.json: every action
# named, every parameter picked under its own name
quint test model.qnt --main design                          # named `run ...Test` scenarios
gotogether scaffold -spec model.qnt -module M -out m.binding.yaml   # binding skeleton; parameters default to same-named step fields
```

Backends: `--backend rust` (default, fast) or `--backend typescript`.
gotogether reads `QUINT_BACKEND`.

## Common errors

| Error | Cause | Fix |
|---|---|---|
| `an anonymous action was taken` | inline `all {}` in `step` | name it (convention 1) |
| `unknown nondet pick "x"` | the `nondet` is named differently from the parameter, or the action has no `nondet` for it | rename (convention 3) |
| `the trace has no mbt::actionTaken` | trace from `quint test` (no `--mbt`) | use `connect.Run`, or `NondetPath` for specs that record the action in state |
| `QNT500 Uninitialized const` | running the parameterised module | `--main` the instance module |
| `QNT101` on a parameter or action named `item`, `to`, `of`, `get`, `put`, `set`, `size` (or another builtin; `QNT008` or a parse error for a keyword: `type`, `val`, `and`, `all`) | quint reserves its builtins' and keywords' names (the whole list: `internal/quintnames`, checked against quint's own) | rename it (`item_`, or `src`/`dest`, `read`/`write`, `value`) and map the step's field in the binding (`item_: '{{.item}}'`): `gotogether scaffold` writes the mapping for a parameter renamed with `_`, and `lint -binding -src` names a missing one |
| `Cannot continue in "then"` (QNT513) in validation | the recorded step is not enabled in the model | an implementation bug, or a wrong binding; see the gotogether-traces skill |

The spec is ground truth. When the code and the model disagree, decide from
the requirements which is wrong; never weaken the model just to make a test
pass without saying so.
