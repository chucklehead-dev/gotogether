- `qnemesis.WatchHost` measures a real run's host stalls from the harness,
  outside the system under test: a 10ms ticker on a thread of its own (on
  Linux with that thread's scheduling delay and the cgroup's CPU throttling)
  and an fsync probe on the system's disk, each delay past `Threshold`
  (default 100ms) a `qtrace.Stall`. `qtest.HostStalls(stalls...)` (Validate,
  Record) and `validate.Report.ExcuseStalls` make a bounded response (a
  binding's `within`) that came late no verdict, "no verdict (host stall)",
  when the stalls within its window cover its lateness; any other failure,
  and a response that never came, stand. `Run.Stalled` and
  `Report.Stalled` say a run has no verdict for that alone, and a claim over
  runs every one of which is so fails, naming the stalls. Explorations and
  cases refuse the option: their time is virtual.
