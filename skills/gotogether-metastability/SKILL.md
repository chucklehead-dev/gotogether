---
name: gotogether-metastability
description: Find and prevent metastable failures (retry storms, a queue that stays full after a spike) in Go services and controllers with gotogether's qload and qmeta - predict recovery with a CTMC (qmeta), confirm with a load simulation on virtual time (qload, the real retry library as a Retrier), search and shrink the smallest trigger with Hegel (qhegel.FindTrigger), compare mitigations, and turn the calibrated chain into alert rules over the app's own load roles (qrole). Use when choosing or reviewing a retry, timeout, backoff, requeue or rate-limit policy, or when a system degrades after a spike or a slow dependency and does not come back.
---

# Metastable failures with qload and qmeta

A metastable failure is a degraded state the system cannot leave after
its trigger is gone: timed-out requests' retries keep the queue full.
Correctness checks do not see it; these check "within W after the
stressor ends, goodput is back to 90% of before". Work in tiers,
cheapest first, each checking the next.

## Steps

1. Predict with the chain: `qmeta.New(qmeta.Params{Lambda, Mu, Workers,
   Timeout, Retries, Queue, Orbit, ...})`, `HittingTimes`, `SVG`. Rank
   policies and find the tipping point; do not trust its absolute times.
2. Confirm in a simulation: `qload.Recovers(cfg, seeds, trigger,
   within)` with the real retry library behind `qload.Retrier`;
   controllers as closed loops (`Population`); a healthy baseline set.
3. Or load the application itself: `qload.Experiment`, its dependency
   given a capacity, a model step's rate as goodput (`qload.Assess`).
4. Search the smallest trigger: `qhegel.FindTrigger(t, sys, space,
   ...).Expect(t, qload.SomeTrigger())` for a fragile design,
   `NoTrigger()` for the mitigation. Keep it out of `-short`.
5. Alert: mark the roles (`//quint:serve`, `//quint:operation`,
   `//quint:call`), calibrate with λ held (`qmeta.CalibrateTimeout`),
   and commit the region's rules (`Region`, `Rules`).
6. Check the mitigation's own logic against a small Quint model (a
   budget's bound as an invariant): storms are too rare to catch it.

## References (when to read each)

- [references/workflow.md](references/workflow.md): each tier with code,
  mitigations, and what the examples found (retrystorm, crossdb,
  checkout).
- [references/simulation.md](references/simulation.md): writing the
  simulation (the Retrier contract, closed loops, triggers, measured
  service times, SimKube workloads).
- [references/experiment.md](references/experiment.md): deploying an
  application into an Experiment.
- [references/alerts.md](references/alerts.md): roles, calibration, the
  rules and the flight recorder.
