- `qobs.MemorySink` keeps its steps in chunks instead of one growing
  slice: a step is copied in once, never again as the run grows. A run of
  10,000 steps allocates 41% less than before (15.8 MiB, where #266's
  larger `qtrace.Step` had taken it from 26.8 to 29.6 MiB), a step
  recorded in a simulation 56% less, and B/op in benchmarks that record
  into one no longer moves with b.N. Runs under 256 steps allocate as
  they did. No API or format change.
