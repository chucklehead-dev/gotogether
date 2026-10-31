---
name: gotogether-metastability
description: Find and prevent metastable failures (retry storms, a queue that stays full after a spike) in Go services and controllers with gotogether's qload and qmeta - predict recovery with a CTMC (qmeta), confirm with a load simulation on virtual time (qload, the real retry library as a Retrier), search and shrink the smallest trigger with Hegel (qhegel.FindTrigger), compare mitigations, and turn the calibrated chain into alert rules over the app's own load roles (qrole). Use when choosing or reviewing a retry, timeout, backoff, requeue or rate-limit policy, or when a system degrades after a spike or a slow dependency and does not come back.
---

# Metastable failures with qload and qmeta

A metastable failure is a degraded state the system cannot leave after
its trigger is gone: timed-out requests' retries keep the queue full.
Correctness checks do not see it; these check "within W after the
stressor ends, goodput is back to 90% of before", cheapest tier first.

## Choose your path

- Each tier with code, mitigations, what the examples found: [references/workflow.md](references/workflow.md).
- Writing the simulation (Retrier contract, closed loops, triggers, measured service, SimKube workloads): [references/simulation.md](references/simulation.md).
- Loading the application itself: [references/experiment.md](references/experiment.md).
- Roles, calibration, alert rules, the flight recorder: [references/alerts.md](references/alerts.md).
- Sizing workers, rate limits, timeouts and retry layers from the chain, and a grist app's `<app> sizing`: [references/sizing.md](references/sizing.md).

## Steps

1. Predict: `qmeta.New(qmeta.Params{...})`, `HittingTimes`, `SVG`; rank
   policies and find the tipping point.
2. Confirm: `qload.Recovers(cfg, seeds, trigger, within)`, the real
   retry library behind `qload.Retrier`; or `qload.Experiment` on the app.
3. Search: `qhegel.FindTrigger(t, sys, space, ...).Expect(t,
   qload.SomeTrigger())` (fragile) or `NoTrigger()` (the mitigation).
4. Alert: mark the roles, `qmeta.CalibrateTimeout` with λ held, commit
   `Region`'s `Rules`.
5. Check the mitigation's own logic against a small Quint model.
6. Size: `qmeta.Size`, `LimitFor`, `TimeoutFor` against a declared
   `Burst`, layers composed with `qload.Layered`; grist apps run
   `<app> sizing`.

## Checklist

- [ ] The chain's orbit bound (`Orbit`) is well above the storm's size.
- [ ] Controllers are closed loops (`Population`, `Think`).
- [ ] The Experiment has a healthy baseline (`Healthy`).
- [ ] Recovery batch errors are checked before using counts, majorities or recommendations.
- [ ] Service is measured (`qmeta.Measure(...).Exponential()`) before the chain's verdict is trusted.
- [ ] Claims are orderings or orders of magnitude, never the chain's seconds.
- [ ] Trigger searches stay out of `-short`.

## Results

| Result | Means | Next |
|---|---|---|
| recovery times growing by orders of magnitude with λ | the metastable region | find the tipping point; try a mitigation |
| `qload: want ..., got ...` from `Recovers` | the simulation disagrees with the claim | count over seeds (`qload.Mostly`), check the baseline |
| `Experiment.Healthy is not set` | no baseline: a collapsed system would "recover" | set `Healthy` |
| recovery batch or trigger-search error | no verdict, even if valid seeds form a majority | fix the named seed or search configuration |
| `FindTrigger` returns the empty trigger | it collapses on noise alone | a design bound (concurrency past capacity × timeout) |
| `SomeTrigger` holds | the design is fragile; `r.Trigger` is the shrunk one | compare mitigations against it |
| `NoTrigger` fails | a trigger in the space breaks the mitigation | widen the mitigation, or the claim |
| `Exponential()` false | the chain's verdicts may be wrong, not only its numbers | simulate with `Service.Draw` |

## Run it, do not infer it

A policy's recovery is a measurement: run the chain, then the
simulation, then the search, and state the outcome with `Expect`; do not
reason from the retry code alone.
