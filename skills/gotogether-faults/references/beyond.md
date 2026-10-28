# The real environment, third-party controllers, and what the examples found

From the gotogether-faults skill (its SKILL.md says when to read this).

## 5. Take the same model to the real environment

Run the real binary against real dependencies, and inject the faults you
can. For Kubernetes: kwok clusters with the binary runtime (no Docker),
`kill -9` the controller, `kubectl` to reset. To give a kwok cluster the
timing of production, generate pod Stages from a SimKube trace (`go run
./cmd/kwok-stages -trace f.sktrace -speed 10 > stages.yaml`, then
`kwokctl create cluster --config stages.yaml`; fleetrollout's `TRACE=`),
from the same quantiles as the simulated `PodTimes`. Write step logs and
check them:
`gotogether validate -binding app.binding.yaml logs/*.jsonl`.
- A new process is a restart (`restart:` in the binding).
- Faults on real processes, containers and links, recorded as steps:
  `qnemesis` ([`references/real.md`](real.md)).
- A final fresh read of every component, then `settle`, makes eventual
  properties meaningful there too.

## Third-party controller logic (Crossplane, controller-runtime)

Run the library's real reconciler in the simulation. Don't copy its
logic.
- Give it a manager stub (`GetClient`, `GetScheme`) whose client is a
  fake behind `interceptor.Funcs`.
- Libraries that read the real clock need their timestamps translated at
  that seam: `Sim.ToVirtual` on writes, `Sim.ToReal` on reads and
  responses, for Get, List, Create, Update and Patch. Otherwise their
  timeouts and grace periods never expire.
- Record cluster state with an audit after quiescence (phase, Secrets).
  The same audit works against a real cluster (examples/crossdb).

## What the examples found

- **Locking.** A lock on a parent row doesn't stop write skew under
  snapshot isolation, if the conflicting writes go to other tables. Make
  the conflict a write.
- **Retries.** A client's retry can mask a dual-write bug. The realistic
  form is "commit, respond, publish in the background".
- **In-memory tracking.** It breaks under lost responses, not just
  restarts: an update applied with its response lost is never tracked.
- **External names.** A Crossplane provider whose external name is the
  cloud's generated ID leaks a database after one lost response and a
  delete, with no crash. Make the name deterministic (a client token) and
  the create idempotent; keep the creation grace period for eventually
  consistent clouds.
- **Status.** Trusting a status without `observedGeneration >=
  generation` overruns a rollout budget, both simulated and on real kwok.
