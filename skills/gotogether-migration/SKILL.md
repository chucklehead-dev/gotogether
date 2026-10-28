---
name: gotogether-migration
description: Bring an existing Go system (a team arriving with code, tests, logs and traces, but no model) under a Quint model with gotogether - bound one context, extract its requirements from code, tests, logs and traces with each one's provenance, write the model with its possible scenarios and claims, check the model against the system's existing runs (its tests recorded, its OTel spans converted, its production logs validated) before changing any code, then continue model first. Use when asked to check, harden, rewrite or modernize an existing service with a model, to recover requirements from a legacy system, or to plan a multi-session migration.
---

# Migrating an existing system under a model

The goal is one checkable model per bounded context that specifically
and exhaustively states its business rules, every rule traced to where
it came from, and the system's existing runs judged by it before any
code changes. The model is the specification: requirements live in the
`.qnt` file and the binding, not in a separate document.

## Choose your path

- Choosing and drawing the context: [references/bounded-contexts.md](references/bounded-contexts.md).
- Extracting requirements, with provenance: [references/requirements.md](references/requirements.md).
- From requirements to the model, its scenarios and its first verdicts on existing runs: [references/first-model.md](references/first-model.md).
- Then the model-first loop: the gotogether-workflow skill; the model's conventions and patterns: gotogether-model, gotogether-patterns.

## Steps

1. **Bound one context**: one aggregate or service whose state commits
   in one place; list its operations, its environment (store, queue,
   remote calls) and its owners.
2. **Extract requirements** from the code (guards, error returns,
   transactions), the tests (assertions), the logs and the traces
   (`gotogether convert` for OTel spans, `gotogether steps` to select and
   sanitize), each with its provenance comment: `// req R7 (code:
   pay/charge.go:88; test: pay_test.go TestRefund; log: 2026-10 prod)`.
3. **Write the model**: an action per operation, outcomes and refusals as
   values, the environment as weak as documented; each happy path a
   `possible` scenario covering a whole lifecycle; each "must never" a
   `val` in `holds` or `allows`; each "eventually" an eventual val.
4. **Simulate it alone**: `gotogether simulate -binding` until every
   scenario is reached and every property is classified.
5. **Judge the existing runs** before touching code: the legacy tests
   wrapped in `qtest.Record`, the spans converted, production logs
   validated. Triage each disagreement into code bug, model mistake or
   undocumented requirement, and record the decision with its provenance.
6. **Then change code** under the model (gotogether-workflow).

## Checklists

Each requirement:
- [ ] Has a provenance comment naming its sources' kinds and places.
- [ ] Is a domain outcome (a refund refused), not a CRUD or endpoint step.
- [ ] Is a `possible`, a `val` or an eventual val: checkable, not prose.
- [ ] Rests on two sources, or is marked `assumed` for its owner to confirm.

Before changing code:
- [ ] Every scenario is reached by `gotogether simulate`.
- [ ] Every legacy test's run, and a sample of production's, has a verdict.
- [ ] Every disagreement is decided and its decision recorded.

## Results

| Result | Means | Next |
|---|---|---|
| `possible X: FAIL`, the model forbids it | a requirement the model omits or misstates | re-read its sources; `gotogether nav -binding` to the guard |
| a legacy test's run `Rejects` | the model forbids what a test expects | the test encodes a bug, or the model is wrong: ask the owner, record it |
| a production log `Rejects` or `Mismatch` | real behaviour the model forbids | as above; a defect found before any change |
| `Violates` on a legacy run | the system breaks a stated rule | a bug to report, or a rule nobody holds |
| spans that no `convert` rule matches | operations the model does not name | a rule, or out of the context |
| `issue: error gap`, `lost-prefix` on old logs | logs that cannot be judged whole | a snapshot step, or newer logs |
| a requirement with one source | weakly grounded | a second source, or `assumed` |

## Run it, do not infer it

What the legacy system does is read from its runs: record its tests,
convert its traces and validate its logs. Do not infer a requirement
from code alone when a run can show it, and do not infer what the model
allows: simulate it and walk it with `gotogether nav`.
