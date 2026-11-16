- `connect.Bind`'s check of a binding is per action and includes the
  projection (#144, golem's planted drift): a pick is checked against its
  action's own picks (its parameters, the nondets whose values flow into
  its call in `step` or a guard over it, and those its body binds), not
  every nondet of the model, which `quint run --mbt` records at every
  step. A pick of another action's is refused, naming whose it is
  ("connect.Bind: action attempt, parameter 1 (pick outcome): outcome is
  a pick of failed, not attempt (attempt's picks: d): name a nondet that
  feeds attempt"), for `Observes` picks and `Returns` arguments too, and
  "(its picks: ...)" lists the action's own. Where the step's shape
  hides an action's picks (a match, a let-bound action) its picks stay
  the whole model's.
- `Binding.Run(t, c)` and `Binding.Test(t, c, test)` check the binding
  before any trace is generated (no implementation made, every mismatch
  at once, on the compile `Step.Eval` and `Config.Require` use), then
  replay as `connect.Run` and `connect.Test` do. `connect.Run(t, c,
  b.Driver)` still works and checks at the first trace, after
  generation.
- `Binding.State` takes a `func(T) S` (was `func(T) any`; a `func(T) any`
  still compiles, and is decoded at the first trace's init as before).
  With S a concrete type the projection is checked against the state
  variables' types (at `Config.StatePath`) under `itf.DecodeProjection`'s
  rules ("connect.Bind: State: the projection, Go type T, does not fit
  the model's state (...)"). The check is strict, as the decode is: an
  unexported field, and a field the model's state lacks unless tagged
  `itf:"-"`, are refused. At the top level of the state the decode at
  the first init already refused them; below an element of a set, list
  or map, or an Option, the decode reached them only when a trace held
  such a value, so a projection that passed every run whose collections
  stayed empty (or Options None) is now refused before any trace. A
  second `State` on one binding panics where it is built
  ("connect.Bind: State given twice: give one projection").
