- `gotogether invariants` generates a design document's table of a
  model's properties (ROADMAP CW5's static half): its boolean vals (or a
  binding's invariants and eventually vals, with `-binding`), each with
  its meaning (its doc comment), where the Go code enforces it
  (`//quint:enforces p` on a declaration's doc comment, or on the line
  above a statement) and which tests check it (`//quint:checks p` on, or
  in, a Test or Fuzz function), as file:line links relative to the
  document. A property is named as the main module sees it, or qualified
  by a module of the spec that sees it (`exposure.limitsHonoured`, `.` or
  `::`). Go source is read with go/parser alone: directories or `dir/...`
  (default `./...`). `-write doc.md` writes the table between its
  `<!-- gotogether:invariants -->` lines; `-check doc.md` exits 1 when it
  is stale (`FAIL doc.md: stale: row p changed: run gotogether
  invariants ...`). A directive naming no property, or annotating
  nothing, is an error at its file:line with the model's properties and
  the fix (exit 1 in every mode); a property nothing enforces or no test
  checks is a `—` cell and a warning, which `-require enforced,checked`
  turns into a failure. Exit 2 is a bad invocation.
