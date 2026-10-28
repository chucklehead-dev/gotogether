# Extracting requirements, with provenance

From the gotogether-migration skill (its SKILL.md says when to read this).

The aim is every business rule of the context, specifically and
exhaustively, each traced to where it was found. A requirement no one
can point at cannot be argued with, and a model whose rules have no
sources becomes one person's understanding.

## Sources, and what each gives

| Source | Look for | Becomes |
|---|---|---|
| code | guards and early returns, error values, transactions and locks, retries and idempotency keys, state transitions | refusals (outcome values), safety vals, linearization points, the environment's contract |
| tests | assertions, the scenarios set up, expected errors | `possible` scenarios, safety vals, mutants (a test that once failed) |
| logs | what happened in production, in what order, how often; errors that recur | scenarios the model must allow, frequencies for rates, defects found before any change |
| traces | OTel spans and their attributes: operations, their arguments, their causes | `convert` rules, then runs the model judges |
| people | the incident reports, the runbooks, the owners' answers | requirements no code states, marked as such |

## Provenance

Put a requirement's sources beside it in the model, one comment each,
with an identifier the team can cite:

```quint
// req R7: a charge is refunded at most once.
//   code: pay/refund.go:41 (the refunded flag checked under the row lock)
//   test: pay/refund_test.go TestDoubleRefund
//   log:  prod 2026-10-02, two refund rows for ch_93 (INC-311)
val refundedAtMostOnce = charges.keys().forall(c => refunds.filter(r => r.charge == c).size() <= 1)
```

- Name each source's kind (`code`, `test`, `log`, `trace`, `owner`) and
  its place (a file and line, a test's name, a log's date and query, a
  trace's id, an owner and the date they said it).
- A requirement found in one source only is marked `assumed` until a
  second source or an owner confirms it.
- A disagreement between sources (the code allows what a test forbids)
  is recorded on the requirement with its decision: `decided: the test is
  right (owner, 2026-10-28); the code is a bug, mutant refundRace`.

## Extracting, in order

1. **Happy paths first**, each a whole lifecycle (a charge created,
   captured, settled, refunded), not a request and its response.
2. **Refusals**: every error a client can see becomes an outcome value
   of its action, and the condition that causes it the action's guard.
3. **Safety**: every "never" the code defends or a test asserts (at most
   once, never negative, never after it was cancelled).
4. **Liveness**: every "eventually" an operation promises (a job is run,
   a ledger entry is posted) becomes an eventual val, judged at a settle.
5. **The environment**: what each dependency may do, from its
   documentation and the retries the code already has.

Requirements are stated as domain outcomes. "POST /refunds returns 409"
is the API's; "a refund of a refunded charge is refused" is the
requirement, and the API's status is the binding's business.

## Logs and traces as evidence

- `gotogether steps -actors ... -since ... -pseudonymize f,g -out
  sample.jsonl logs/*.jsonl` selects and sanitizes a sample (the
  `GOTOGETHER_SALT` keeps pseudonyms stable).
- `gotogether convert -rules spans.yaml -out steps.jsonl spans.json`
  turns existing spans into steps; once a binding exists, `validate`
  takes `-convert` directly.
- A log is evidence of what the system did, never of what it must do:
  a behaviour seen in production is a scenario the model must allow
  (or a defect), not a requirement.
