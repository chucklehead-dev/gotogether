- `binding.Model.Vars` is sorted by name instead of following quint's
  typecheck order, which within a toposort layer depends on quint's ids
  (a module written before the model reordered it). The lint findings
  `binding.snapshot.incomplete` and `binding.resume.incomplete` now list
  the unassigned variables by name and name the first of them in their
  example (`u' = ...`), and a replay's snapshot checkpoint compares the
  state's fields in that order; `Plan.StateVars` and
  `Model.CheckpointVars` follow it. Verdicts are unchanged.
