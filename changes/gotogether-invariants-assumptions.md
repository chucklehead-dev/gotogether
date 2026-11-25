- `gotogether invariants` writes a third table, of what the model says
  it leaves out (ROADMAP CW3), between `<!-- gotogether:assumptions -->`
  lines: each `// @assume name: statement` line of the model (a plain
  comment quint reads unchanged; `// @assume name = val: ...` when a
  boolean val states it, which then leaves the default properties), the
  code that exercises it (`//quint:exercises name`: a test, a double's
  fault, a generator aimed past it) and the checks that monitor it
  (`//quint:monitors name`). `-require` gains `exercised`, `monitored` and
  `targeted`. A directive naming no assumption, a property directive
  naming an assumption or its val, and an `@assume` line that is
  malformed, repeated, a `///` doc comment, after code, or named as a
  definition the main module sees, are each an error at its line with the
  fix. The failure line for bad annotations now reads `FAIL N annotations
  naming no property, action or assumption of model M, ...`, and the
  missing-table message names the assumptions markers too.
