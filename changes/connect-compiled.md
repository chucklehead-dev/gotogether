- connect runs with no quint from a spec's compiled model (ROADMAP Q1):
  `gotogether compile -spec S [-main M]` writes `gotogether.mbt/1` beside
  the spec (`connect.CompiledFor`; `-check` fails when stale), and under
  `GOTOGETHER_COMPILED` (unset: where quint is not installed; `1`: always;
  `0`: never) `connect.GenerateRun`, `Run` and a bound driver's `Run`
  generate their traces with the Go evaluator's random simulation, `quint
  run --mbt`'s semantics, and `Config.Require`, `Step.Eval` and the bound
  check read the artifact. `connect.Test`, a `Config.Args` but
  `--invariant`, and what the evaluator cannot simulate fail with the fix.
  New: `connect.MBTModel`, `MBTSource`, `MBTFormat`, `LoadMBT`,
  `RequireTraces`, `validate.CompileMBT` and `MBTOptions`.
- connect's witness traces (`Config.Require`) name the action taken as
  `quint run --mbt` does: the branch the last `any {}` took ("" for an
  anonymous one), the step's own name where no `any {}` chose. They named
  the outermost action step applied.
- The Go evaluator evaluates `setOfMaps`, `allListsUpTo`, `getOnlyElement`
  and `q::debug`, which it refused; `allLists` and the temporal operators
  are refused with quint's own reason.
