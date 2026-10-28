---
name: gotogether-workflow
description: End-to-end workflow for checking a Go system against a Quint model with gotogether - model first, simulate its invariants, implement with step recording, bind, prove each check can fail with mutants (.Expect(t, claims...), qhegel.FindViolation), let Hegel explore, then check runs from the real environment - and how to triage each verdict. Use when starting verification of a new component or example, planning which tests to write, deciding whether a failure is the code's, the model's or the binding's, or setting up CI for model-checked tests.
---

# Verifying a Go system against a Quint model

The order matters. Each stage checks the previous one, and skipping one
leaves a class of mistakes undetected. The other skills cover each
stage's mechanics: gotogether-model, gotogether-patterns,
gotogether-traces, gotogether-mbt and gotogether-hegel.

## 1. Model and simulate before writing code

```sh
quint typecheck model/m.qnt
gotogether simulate -binding m.binding.yaml   # every invariant in the binding's simulate section
```

The binding's `simulate` section sorts its invariants in two:
- `holds`: what the design guarantees. No trace of the model's own
  simulation may violate it. One that fails is a design bug, or a wrong
  property.
- `allows`: a requirement on the code that the model lets a bad
  implementation break (at most once, per-key order). Some trace must
  violate it, or checking the code against it is vacuous.

```yaml
simulate:
  constants: {MAX_AMOUNT: '5'}   # the binding's constants, where they are templates
  holds: [noOverdraft]
  allows: [chargedAtMostOnce]
```

A model with no `step` (replay only) says `skip: <why>`. The command
exits 1 on a misclassified invariant, naming it and the fix.

Model first means scenarios first, each with a possibility check, then
safety, then liveness, then code. Write each acceptance scenario as a
`possible` (or, in Go, `qtest.Scenario` checked by `qtest.Possible`):
a model too strict to reach it passes every invariant and judges a code
that never shows it faithful.

```yaml
simulate:
  possible:
    - 'balance > 0'                           # a predicate some trace reaches
    - refund                                  # an action some trace takes
    - name: overdraft refused
      steps: [deposit, 'withdraw(result = "insufficient")']   # in turn, others between
```

Each is reported reached with its witness (the Go evaluator's shortest
path with arguments, else quint's sampled trace, or for a predicate how
many sampled traces it held in), or FAIL: "the model
forbids it" after a search that saw the whole state graph, "not
reached in N sampled traces ... not found within the bound" otherwise.
Find the guard with `gotogether nav -binding`; raise `-samples`,
`-steps` or `simulate.searchStates` only if it is rare, not forbidden.
A grist app's bindings carry one per command outcome and per processor
step, generated.
- Also check that each `holds` property *can* fail. Temporarily break
  the model (drop a guard), or write a `run` test that reaches a
  violating state. A property that never fails checks nothing.
- Add `run ...Test` scenarios for the behaviours you care about, so the
  model has regression tests of its own.

## 2. Implement with recording

Record each model step where it takes effect (gotogether-patterns §3):
`qobs.Record(ctx, "action", err, "param", v, ..., "obs.x", observed)`.
Name step fields after the model's parameters: the binding then needs no
template for them.

## 3. Bind and lint

```sh
gotogether scaffold -spec model/m.qnt -module m -out m.binding.yaml   # parameters default to same-named fields
gotogether lint -binding m.binding.yaml -src ./pkg
gotogether scaffold -system -binding m.binding.yaml -pkg m -out system_test.go   # a System test: fill in Run, then a mutant
```

Add only what isn't a same-named field:
- an `expect` per observation;
- `outcomes` for refusals;
- `restart`;
- `constants`;
- the promised `invariants`.

## 4. Prove each check can fail: mutants

A conforming verdict means nothing until you have seen the same test
reject a bug. For each property and each `expect`:
1. Add a mutant to the implementation: a `Mutation` option, never a
   build tag, so one binary has them all. Code you must not edit (a
   service written against the standard library alone, third-party
   code) takes its mutants as `go test -overlay` files instead: read
   [references/overlays.md](references/overlays.md) for how, and for when
   qweave's `-mutate` fits better.
2. Write the test that states *how* the mutant must show up. "It fails"
   is not enough: a binding error also fails.

```go
qtest.Record(t, "m.binding.yaml", scenario(Faithful)).Expect(t, qtest.Conforms())
qtest.Record(t, "m.binding.yaml", scenario(LostWrite)).Expect(t, qtest.Mismatch("write"))   // expect caught it
qtest.Record(t, "m.binding.yaml", scenario(AckAny)).Expect(t, qtest.Rejects("ack"))         // a step the model can't take
qtest.Record(t, "m.binding.yaml", scenario(NoRetry)).Expect(t, qtest.Violates("converged")) // the design property
```

Keep a table in the README: mutant, check, how it shows up. If no test
catches a mutant, the model or the binding is too weak. Strengthen it, or
document the gap (as glomers does for the scheme model).

Three kinds of mutant, three questions:
- **code mutants** (`Mutation` options; `System.Mutate` over what
  `qweave -mutate` wove): does a run of the buggy code fail? For code
  you own.
- **overlays** (`go test -overlay`, [references/overlays.md](references/overlays.md)):
  the same question for code you must not edit.
- **observation mutants** (`qtest.MutateSteps(t, binding, steps)`,
  `gotogether validate -mutate-steps`): does the check read what was
  recorded? One step of a conforming log dropped, duplicated, swapped,
  a field corrupted, retimed or moved to another actor; a survivor is a
  field no expect or invariant constrains. Run it once a binding is
  written and whenever a recorder gains fields, on a recorded run (no
  program needed, so a production log too). Read
  [`../gotogether-traces/references/observation-mutants.md`](../gotogether-traces/references/observation-mutants.md)
  for the ops, the fates and the survivors that are equivalent by right.

```go
qtest.MutateSteps(t, "m.binding.yaml", steps).Expect(t,
	qtest.Killed(qtest.StepOp("corrupt").Action("write").Field("obs.value")),
	qtest.Survived(qtest.StepOp("dup").Action("read")), // an idempotent read
)
```

## 5. Let Hegel explore

- **Faithful code.** `qhegel.Test` in `Batched` mode: cheap while
  everything passes, and it shrinks on failure.
- **Mutants.**
  `v := qhegel.FindViolation(t, cfg, body, hegel.WithSeed(n))`
  returns the shrunk run; state how it shows with
  `v.Expect(t, qtest.Rejects("transfer"))`, not by reading its
  summary (`qhegel.Shrink(t, sys)` for a System, at its Rates, as its explorations). Skip it under `testing.Short()`: shrinking runs quint
  dozens of times.
- **Faults.** For distributed code, embed `hegelnet.Network`, or your
  own fault rules, so Hegel schedules losses, partitions, timers and
  restarts. For services with stores, queues and remote calls, deploy
  them into a `qenv` simulation (the gotogether-faults skill). Then
  explore seeds with `System.Explore`, and shrink with `qhegel.Choices`.

State what a set of runs must show with claims:
- an expectation alone is about some run (`Conforms()` about every run);
- several are judged one by one;
- `qtest.Some(a, b)` wants one run showing both;
- `qtest.All(e)` wants every run;
- `qtest.None(e)` wants no run: what a bug must not do, or what a row
  without a fault must never show.

A shrunk Hegel failure is stated the same way:
`qhegel.FindViolation(...).Expect(t, qtest.Rejects("ship"))`. Don't
match strings in its summary.

Hegel finds what scripted scenarios don't (glomers kafka: a commit past
the end of the log). When it does, add the shrunk run as a scripted
regression.

## 6. Check the real environment

Run the system where it will run (Maelstrom, a staging cluster, a soak
test). Write step logs (`qobs.FileSink`) or OTel span events, and
validate them:

```sh
gotogether validate -binding m.binding.yaml logs/*.jsonl
```

The model can be wrong about the environment (gotogether-patterns
§2). Only the real environment tells you.

## Triage: whose bug is it?

Read the report: it shows `expect:`, `obs:` and the model's state around
the failing step.

| Verdict | First suspect | Then |
|---|---|---|
| step not a transition (`Rejects`) | the code did something the model forbids | the binding: wrong argument mapping, a refusal not mapped to `skip`, a missing `restart` |
| state mismatch (`Mismatch`) | the code computed a different state | the `expect` or the recorded `obs.*` value is wrong, or recorded at the wrong moment |
| conforms, invariant FAIL (`Violates`) | the design allows the bad behaviour | the property is stronger than the requirement |
| tool error (exit 2) | the binding or the model doesn't fit the steps (a missing field, a type error in a template) | the message names the step, the template and the signature |
| conforms, but the environment's checker fails | the model assumes more of the environment than it gives | the recording misses steps that matter |

A failing run's report ends with `reproduce:` (the run alone) and
`navigate:` (`gotogether nav -run 'TestX#2:seed=17' -at fail`, in the
test's package): the run's choices as a tree, opened at the last choice
before the failing step. A node (`<schedule>@<k>`, Exhaust's schedule
syntax) shows the steps recorded so far, the simulation's tasks, each
actor's model state, the next choice's named options (a fault kind's
declared options, the runnable tasks, the nemeses) and the verdict of the
run each option makes (`-take 0`: nominal after it). Take branches with
`-follow 1,0` or `-take o`; `-save` keeps a branch that fails as a
corpus entry. `-json` for agents: one object per call; `-fold` passes a
linear stretch of choices that are no decision. People use the
interactive navigator (`gotogether-nav`, the qnav module, which
`gotogether nav` opens on a terminal); agents use `-json`. A rejected
step's `why:` (gotogether-traces §4) names the condition the model found
false.

Every verdict, its expectation and its claims over many runs are in
[`references/verdicts.md`](references/verdicts.md).

Don't loosen a check to get green. If the model is wrong, change it
deliberately and say why in the commit and the README.

## CI layout

- `gotogether simulate -binding` for every binding: the models on their
  own, before any code runs. Nightly, `-verify N` makes it a bounded
  proof (quint verify, Apalache) where the model is small enough.
- `go test -short ./...`: scripted scenarios, mutants, `Batched` Hegel
  runs (about a minute).
- `go test ./...`: adds the `FindViolation` shrinking tests.
- A separate job runs the real environment and validates its logs
  (`gotogether validate`). Keep it in CI so model drift shows up.
- Install quint and its Rust evaluator in CI (`ci/install-quint.sh`).
  Tests that need quint start with `qtest.RequireQuint(t)`, so they skip
  without it.
- **Strict mode.** Set `GOTOGETHER_STRICT=1` (or `true`; another value
  but `0` or `false` fails, naming it) in every job that installs
  what its tests need: each toolkit skip then fails with its message
  (`RequireQuint`, `Mutate` with no mutants woven, `SkipIfEmpty`'s, a
  scaffold's unwritten mutant test), so a job that lost quint from PATH
  or a weaving step cannot pass by skipping. Gate your own skips with
  `qtest.Optional(t, "PG_URL not set: no database")`: a skip normally, a
  failure under strict mode. Leave it unset only in a job where a skip is
  legitimate (a laptop's, a matrix row without the tool), and say why.
- **Expected evidence.** A test that skipped, or that `-run` or a build
  tag left out, writes no evidence and the suite's report does not miss
  it. List the explorations that must run in a file (a test's, `TestX` or
  `TestX/sub`, or one, `TestX#2`, either after its package: its import
  path or the end of it, `examples/hello TestX`; `#` comments) and run
  `GOTOGETHER_EVIDENCE=ev go test ./... && gotogether evidence -strict
  -expect checks.txt ev`: a check with no evidence fails, by name.
  Explorations write evidence (Explore, Exhaust, Sweep, Mutate), and
  Validate and Record runs their manifests (`run<N>.json` with their
  files bundled in `run<N>/`, listed as external, named as explorations
  are); a Hegel search cannot be listed. A bundle holds the run's whole
  step log, unsanitized: an uploaded evidence directory publishes it. A test
  that skips clears what an earlier run of it left; use a fresh
  directory per run all the same (a renamed test's stays).
- **A finished run.** Validate a real environment's logs with
  `gotogether validate -require-finish` (or `complete: true` in the
  binding), and claim `qtest.Finishes()` of a driver's runs: a run cut
  short conforms (verdicts reference, "A prefix vs a finished run").
