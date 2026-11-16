- `connect.Bind`'s check before the traces is per action, includes the
  projection, and runs before `quint run` (#144, golem's planted drift):
  a pick is checked against its action's own picks (its parameters, the
  nondets whose values flow into its call in `step` or a guard over it,
  and those its body binds), not every nondet of the model, which
  `quint run --mbt` records at every step. A pick of another action's
  is refused, naming whose it is ("connect.Bind: action attempt,
  parameter 1 (pick outcome): outcome is a pick of failed, not attempt
  (attempt's picks: d): name a nondet that feeds attempt"), for
  `Observes` picks and `Returns` arguments too, and "(its picks: ...)"
  lists the action's own. Where the step's shape hides an action's
  picks (a match, a let-bound action) its picks stay the whole model's.
  `Binding.State` takes a `func(T) S` (was `func(T) any`; a `func(T)
  any` still compiles and works): with S a concrete type the projection
  is checked against the state variables' types (at `Config.StatePath`)
  under `itf.DecodeProjection`'s rules, a field the model lacks, an
  unexported one or one of a type that does not decode a mismatch
  ("connect.Bind: State: the projection, Go type T, does not fit the
  model's state (...)"). When the factory is `b.Driver` itself, Run and
  Test check before generating any trace (the driver is made once for
  the check, the implementation never); any other factory is checked at
  its first trace, as before.
