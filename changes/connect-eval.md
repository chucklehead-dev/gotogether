- `connect.Step.Eval(name, args...)`, `connect.Eval[T]` and
  `connect.MustEval[T]` (ROADMAP N3): a driver evaluates a val or a def of
  the model (`balanceOf("alice")`) in the step's post-state with the Go
  evaluator, so a projection compares with the model's own definition
  instead of a copy in Go. `Run` and `Test` compile the model once, at the
  first `Eval` (or with `Config.Require`'s compile); a run that never calls
  it compiles nothing, and a step of a plain `Replay` has no model, which
  its error says. Errors name the step, the definition and the fix (an
  unknown name, a variable, an action, the wrong arity, a state where the
  def is undefined, a construct outside the evaluator).
- A `CheckStep`'s panic (a failed `MustEval` or `MustPick`) fails its step,
  as a handler's does, instead of the test binary.
