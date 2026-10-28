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
- Sizing (`qmeta.RecoveryAfter`, `Size`, `LimitFor`, `TimeoutFor`)
  weighs the states a `Burst` ends in by their probability; a minimum
  over reachable states is 0 for nearly every setting (the orbit's bound
  is reachable). Each search assumes recovery monotone in what it
  varies (workers, rate, timeout) and is checked against every value on
  small chains (`...AgainstBruteForce`): keep that when changing them.
- `Params.Unit` counts requests in units: states, `Healthy` and the
  region are in units; a `Burst`'s rate, trajectories (`Loss`) and
  `Rules`' thresholds in requests. Rank a hop's settings on one unit (one
  state space): gristload keys it by hop and timeout. Too coarse a unit
  (a queue of a few units) turns storms into noise: keep the queue at
  tens of units.
- A proxy's retries inside a client's attempts multiply
  (`qload.Layered`), and the client's budget sees only its own: in the
  chain, `Retries` is G × attempts − 1 and `RetryBudget` (G−1) + G ×
  ratio. A test that ablates the multiplication must see the mutant
  `postRetries3` recommended.
- grist/gristload is linked only by an app's sizing binary
  (`cmd/<app>-sizing`, run by `<app> sizing`); never import it from the
  app's package or its main.
