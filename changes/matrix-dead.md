- `simulate.dead` (and a variant's `dead`, which replaces the section's)
  lists the step's actions a design means never to be enabled
  (`binding.Simulation.Dead`, `binding.Variant.Dead`): `simulate -graph`
  fails a listed action the graph takes, a name the step does not call,
  and an action no state enables that the list leaves out, and leaves it
  unchecked where the graph stopped at its counterexamples or was cut
  before a whole level (`model.Report.Dead`, a `dead` check in report/2;
  golem's graph_holds, #144).
- `simulate -matrix` names the failing checks no row shows (a deadlock,
  `dead`, a possible) under its table, `FAIL outside the table: ...`, and
  in `gotogether.matrix/1`'s `others` (`model.MatrixReport.Others`,
  `validate.MatrixDocument.Others`), where a matrix whose cells all passed
  once exited 1 with nothing in the table saying why.
