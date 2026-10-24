# Metastability (qload, qmeta, qrole)

Gotchas for agents (and humans) before changing qload, qmeta or qrole, or a load test; AGENTS.md has the general ones.

- A recovery verdict needs a healthy baseline (`Experiment.Healthy`):
  without one `Assess` returns `Recovery.Err`, since a system already
  collapsed before the trigger would "recover" to its collapsed goodput.
- qload metrics are time series of rates; sample counters reset every
  tick. `Sample.Orbit` is known only in hindsight (an attempt is in the
  orbit from its start if it times out) and is filled when the run ends.
- Two v's: the chain's orbit (`qload.Sample.Orbit`, in hindsight:
  attempts that will time out, and requests waiting to retry) and what a
  running system sees (`qrole`'s `Orbit`, `Sample.Waiting`: the waiting
  part). Under immediate retries the second stays at 0 through a storm
  (`TestTwoOrbits`): alerts on it see only u.
- An alert region is defined by the probability of recovering within the
  bound (`RecoveryWithin`), not the mean hitting time, which near a storm
  is dominated by rare tips into it. Calibrate a running system with
  `CalibrateTimeout` (λ held): a fit of λ to storm data drives it to μ.
- The chain's service is exponential. A measured service time far from
  it (`qmeta.Measure(...).Exponential()` false) changes verdicts, not
  just numbers. Constant service made a queue longer than μ × timeout
  absorbing (every request in it times out), where exponential service
  drains it: check such verdicts in a simulation with `Service.Draw`.
- `qmeta`'s chain ranks policies; its absolute times are not the
  simulation's. Keep its orbit bound well above the storm's size, and
  assert orderings or orders of magnitude, not seconds.
- Controllers are closed loops (`ClientConfig.Population`). An open-loop
  abstraction double-counts a failing object's polls and finds storms
  that cannot happen.
