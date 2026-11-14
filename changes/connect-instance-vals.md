- `connect.Config.Require` and `Step.Eval` find the vals and defs of an
  instance module (`module design { import m(N = 3).* }`). quint's compile
  drops every definition `init` and `step` do not use, which there was
  every val: Require now passes its names to quint to keep, and Eval
  compiles again, once, for a name the model at hand lacks. A name quint
  cannot resolve is refused as before, with the fix.
- `connect.Differential` with `IgnoreModel` no longer runs each
  implementation's own `CheckStep`, which usually compares with the model
  and failed on varied inputs.
