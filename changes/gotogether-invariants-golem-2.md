- `gotogether invariants`, golem's second round (#144):
  - The actions table is read only when it is in play (printed, or the
    document has an actions pair of this model): otherwise no code
    implementing actions is read and none fails the run, and `-require
    recorded` or `driven` is a bad invocation (exit 2). With it in play,
    a connect driver whose file's `connect.Config{Spec: "..."}` names
    another spec is left out, counted in one `note:` line, and an unknown
    action recorded in a file annotated only for other models is noted
    with them instead of failing.
  - The action rows are the actions `step` applies itself: frame actions
    applied inside others, and what `init` applies, are actions code may
    name but no rows. `-exclude` takes actions out of the rows too.
  - A row with nothing of a kind `-require` names is a `FAIL` line; the
    bare rows of a kind not required are one `note:` line a kind (`note:
    5 actions nothing records (recorded is not required): ...`), not a
    warning per row.
  - `-links name` qualifies sites by package only when the tables span
    packages, as line links do, and links each file by its base name.
  - A driver's `.Skip` counts as driven and reads `skipped in` its
    function.
  - The other-model note now reads `note: N annotation names of other
    models (qualified by modules this spec does not have, or actions
    recorded in files annotated only for them), left out of the tables of
    model M: ...`.
