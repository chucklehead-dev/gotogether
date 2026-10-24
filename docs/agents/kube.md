# Kubernetes examples (fleetrollout, crossdb)

Gotchas for agents (and humans) before changing fleetrollout or crossdb; AGENTS.md has the general ones.

- kwok's pod stages given in a cluster's config replace all of its default
  pod stages, not its node stages: `KwokStages` carries kwok's fast
  pod-ready, pod-complete and pod-delete stages with only the delays
  added, one stage per option with weight 1 (kwok picks among matching
  stages by weight; one stage's jitter would be uniform, not the trace's
  quantiles). Trace clusters are `fleet-trace-m*`, so `KEEP=1` never
  reuses a cluster with kwok's immediate stages.
- Libraries that read the real clock (crossplane-runtime's create
  annotations and grace period) see no virtual time passing. Translate
  their timestamps at the seam (`Sim.ToVirtual` on write, `Sim.ToReal` on
  read: crossdb's kube interceptor), or their time-based logic is not
  exercised.
