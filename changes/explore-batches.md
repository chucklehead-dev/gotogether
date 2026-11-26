- `qtest`: an exploration whose runs' records outgrow a batch (2^17
  records: steps, invocation and effect records) judges them a batch at a
  time while it makes them, and lets go of each passing run's `Steps` and
  `Recorded` once it is judged (nil), so its memory is bounded by a batch,
  not by its seeds: examples/raftkv's TestLinearizeScale at a nightly's
  500 seeds ran its runner out of memory. Reports, choices, coverage,
  Evidence and every claim (Some, All, None) are kept, a failing run keeps
  its records, and a passing run a failing claim shows is made again from
  its choices for its step log. An exploration whose runs fit in a batch
  is judged once at the end, as before. `qtest.KeepRecords()` keeps every
  run's records, for a test that reads them after the exploration.
