- `gotogether invariants` also writes the model's actions table (the
  alignment lint of ROADMAP CW5), between `<!-- gotogether:actions -->`
  lines: a row per action (`init` and `step` left out), "recorded by" (a
  `//quint:action action:a` directive, a `Record` or `Invoke` call with a
  literal action in a file importing qobs) and "driven by" (a literal
  `.Action`, `.Handle` or `.Skip` on a `connect.Bind` chain or a
  `*connect.Binding`, a `connect.Switch` key), each the function and a
  file:line link; qobs and connect are recognised under any import name,
  and only string literals count. `-write` and `-check` cover whichever
  marker pairs the document has (one with the invariants markers alone
  works as before); `-check` names stale action rows as it names
  property rows. A name that is no action of the model is an error at
  its file:line with the nearest action (`.Action("ince", ...) names
  ince, which is no action of model counter: did you mean inc? ...`); an
  action nothing records or drives is a `—` cell and a warning, failed
  by the new `-require` values `recorded` and `driven`. The failure line
  for bad annotations now reads `FAIL N annotations naming no property or
  action of model M, or annotating nothing: ...`.
