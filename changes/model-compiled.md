- `qtest.Model`'s claims (Possible, Proves, Refutes, Live, NoDeadlocks,
  AllStepActionsReachable), `gotogether simulate` (sampling, `-graph`,
  `-matrix`, possibles) and `model.New` run from the binding's compiled
  model where quint is not installed (`GOTOGETHER_COMPILED`, as for runs;
  `model.SimulateOptions.Compiled` names one): the graph, the searches and
  the sampling are the Go evaluator's alone. What needs quint is reported
  not done, never passed (`model.Report.NotDone`: the graph's cross-check
  against quint's samples, a witness's replay by quint test, `Replayed`
  `not done: ...`); `-verify` is refused. The compiled model's evaluation
  module now carries the simulate section's terminal and possibles'
  predicates: a model compiled before is refused as stale, with
  `gotogether compile -binding B` as the fix. A constant the artifact
  takes as a literal may be overridden with another literal (a variant, a
  `-const`); one it inlined may not (ROADMAP Q1).
- `validate.CompiledChoice` and `validate.CompiledEnv` (the
  `GOTOGETHER_COMPILED` rule, shared by qtest and validate/model), and
  `Checker.CompiledPath`.
