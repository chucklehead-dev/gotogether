# Sizing: workers, limits, timeouts and retries from the chain

From the gotogether-metastability skill (its SKILL.md says when to read
this). The chain answers "which setting recovers from the burst we
declare"; the simulation confirms the answer; neither quotes the
chain's seconds.

## The chain's sizing helpers (`qmeta`)

```go
p := qmeta.Params{Lambda: 38, Mu: 82, Workers: 1, Timeout: 2, Retries: 8, Queue: 500, Orbit: 1000,
	RetryBudget: 2.6, Unit: 5} // Unit: count requests in fives (see below)
b := qmeta.Burst{Rate: 114, Duration: 30} // three times the rate for 30 s, from an empty system
healthy := qmeta.Healthy{MaxU: 16}.Contains // in the chain's units
r := qmeta.RecoveryAfter(p, b, healthy, 60) // P(healthy within 60 s of the burst's end)
w := qmeta.Size(p, b, healthy, 60, 0.99, 8) // the fewest workers that reach 0.99 (w.Workers, w.Recovery)
limit := qmeta.LimitFor(p, 3, 30, healthy, 60, 0.99, p.Mu) // the highest λ a one-worker hop survives
_, _, _ = r, w, limit
```

- `RecoveryAfter` weighs the states the burst ends in by their
  probability: it is the chain's counterpart of the share of seeds
  `qload.Recovers` says recover. Not a minimum over reachable states
  (always 0: the orbit's bound is reachable).
- `Size` bisects the workers; `LimitFor` the rate (the answer for a hop
  replicas cannot speed up: one tenant's commits, a processor on one
  tenant). `TimeoutFor(p, data, svc, b, healthy, within, prob)` is the
  latency floor (p99.9 service plus Erlang C's p99.9 wait,
  `Timeout.Floor`), raised for regular service to the burst's drain,
  then the shortest rung above it that recovers; with `data`
  (`qload.SimulateTrajectory` at `p.Timeout`) it calibrates first. Check the
  trajectory's returned error before fitting: failed or missing samples cannot
  become zeros. Samples use whole seconds; a positive client timeout supplies
  the lookahead needed to classify retrospective orbit observations.
  `-seeds -1` skips recovery confirmations; `-calibrate` independently requests
  simulated calibration trajectories.
- `Params.Unit`: a millisecond service behind a timeout of seconds is a
  queue of hundreds, too many states. In units of g requests the chain
  keeps the drift and loses noise: rank with it, confirm in simulation.
  Healthy sets are in units; bursts, trajectories and rules in requests.
- The chain's limits are below the simulation's (retrystorm
  `TestSizedFromTheChain`: 7.2/s against 9/s for immediate retries):
  assert orderings.

## Retry layers (`qload.Layered`)

A Gateway's or mesh's tries sit inside each client attempt: attempts
multiply, and the client's budget sees only its own retries.
`qload.Layered(3, clientPolicy)` puts both in a simulation; the chain's
`RetryBudget` for it is (G−1) + G × ratio retries per request. grist's
client is the library itself: `(&client.Client{MaxAttempts: 3}).Policy()`.

## A grist app: `<app> sizing` (grist/gristload)

```text
counters sizing -steps testdata/sizing/steps.jsonl -rate 60/s -burst 3x30s \
  -gateway-post 3 -client-attempts 3 -seeds 6 -out deploy/sizing.yaml
```

- A binary of its own: `cmd/<app>-sizing` is `gristload.Main(app)`;
  `<app> sizing` runs it, found beside the app's binary or on PATH. The
  app's binary links no qload or qmeta (assert it with `go list -deps`).
- Input: a real tier's steps, recorded one call at a time (each call's
  duration is then its service); `Measured.InFlight` above 1 means the
  times include waits. Check one recording in, so tests do not depend
  on the machine.
- Output: every value with because, formula, tool, margin, the setting
  in force's verdict (`inForce: advised against ...`) and caveat; the
  chain's fields as SVG beside it. Tests read `Report`'s typed
  `Sizing` (`Get`, `Retries`, `Recommended`, `Commands` and `Trigger`
  for a claim over seeds), not the text.
- Two findings it makes visible: replicas never speed up a hot tenant
  (its commits are one at a time: a rate limit, `tenant.rateLimit`), and
  scaling on a lag a slow dependency causes adds waiting pods
  (`processors.<p>.scaleOnLag: false`).
- The declared load must reach the bottleneck: at counters' 40/s the
  busiest tenant's burst stays under its capacity and every setting
  recovers; at 60/s the mutant `postRetries3` stays degraded (0 of 6)
  where the recommended setting recovers (6 of 6).
