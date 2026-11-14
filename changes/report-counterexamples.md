- `gotogether.report/2`'s model checks carry `counterexample`, the path a
  verdict rests on, as data: `steps` (each a `call`, its `picks` and the
  whole `state` after it), `replay`, and for a hot lasso `cycle`,
  `stutter` and `ends` (`validate.DocCounterexample`). A script reads a
  violated invariant's or a deadlock's path and its length from it, not
  from `detail`'s text (ROADMAP CW4, golem's #144).
