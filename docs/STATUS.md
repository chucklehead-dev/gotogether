# Status: where the work stands, and how to pick it up

<!-- not built: qtest.NoHotCycle qobs.Sometimes -->

Current handoff, reviewed 2026-11-07. [README](../README.md) is the product
entry point; [ARCHITECTURE](ARCHITECTURE.md) explains the structure;
[ROADMAP](ROADMAP.md#current-order) owns priorities and open-item detail.
[HISTORY](HISTORY.md#handoff-consolidation-2026-11-06) preserves the previous
handoff, including original measurements, pins and review provenance.

## Standing decisions

The toolkit and Grist are unreleased. Iterate toward one current API and format;
there is no legacy-compatibility requirement. Frozen adoption artifacts and
historical dependency pins preserve evidence, not support promises. Tags, image
publication and other release work wait for the maintainer.

The shared live Go floor is **1.27.3**, including the gosim fork's translated
runtime. Use current standard-library facilities where they fit; admit runtime
hooks and compiler-dependent libraries with real execution/replay checks before
upgrading. [go-toolchain.md](go-toolchain.md) owns that policy. Read live module
pins from the checked-in go.mod files, not from historical handoff paragraphs.

## What works now

| Area | What you can do today | Start at |
|---|---|---|
| MBT | Quint traces drive the code (`connect.Run`, `connect.Test`, quint-connect parity) | README §1, [examples/bank](../examples/bank) |
| Hegel | Hegel drives and the model judges and shrinks, or the model drives and Hegel varies (qhegel, its own module) | README §2, skill `gotogether-hegel` |
| Trace validation | steps recorded (`qobs`, `//quint:action`, OTel spans converted) and judged by a binding; `validate -json`, JUnit, SARIF; on-call reports with one record index, wall time, pod, trace id and a state diff | README §3, [instrumentation.md](instrumentation.md), DESIGN §68 |
| Simulation | qenv: virtual time, crashable processes, Store, Queue, Service, Disk, qhttp, qsql, simlog doubles, fault kinds by name; `System.Explore`, FaultOrders, Exhaust, PCT, sweeps, mutants (overlays and qmut), coverage by model step | README §4, [examples/hello](../examples/hello), skill `gotogether-faults` |
| Published doubles | `qenv/simgrpc`, `qenv/kubesim`, `qenv/simlog/kafkalog`, `qenv/simlog/kinesislog` as modules; `qenv/simlog` in the core | api/simgrpc.txt etc., ci/consumer |
| Metastability and sizing | qload simulation, qmeta's CTMC, `FindTrigger`, alert rules from qrole gauges; sizing from an app's own steps (`qmeta.Size`, grist's `<app> sizing`) | README §5, [examples/retrystorm](../examples/retrystorm), skill `gotogether-metastability` |
| The Go evaluator | decides passing runs (quint judges failing ones); `simulate -graph` (a complete state graph: invariants proved for the instance, cross-checked against quint's samples), liveness as lassos under per-action weak fairness, deadlocks, possibles and scenarios, partitioned validation of serial logs (`partition:`), `gotogether compile` + `validate -compiled` with no Node or quint | DESIGN §56, §61, §62, §66, §64; [tutorials](tutorials/README.md) |
| Model claims in tests | `qtest.Model` with `qtest.Proves`, `qtest.Refutes`, `qtest.Live`, `qtest.NoDeadlocks`, `qtest.Possible`; explicit quantifiers on run claims (`Some`, `All`, `None`, `qtest.Not`) | qtest/modelclaims.go, DESIGN §67, §69 |
| grist | event-sourced framework checked by its model: events with upcasters and rolling versions, read models with snapshots, commands, rules, queries, processors, declared calls, the HTTP API, `grist.Main`, memstore, simstore, sqlstore (SQLite, PostgreSQL, qsql), the generated model and bindings, `grist new`, gristtest's harnesses and guard-specific rule mutants; GR10 slices 1, 2, 3, 4, 6 (identity, probes, drain, rolling restarts, failover and `<app> resume`, `catalog -diff`, sizing) | [grist-guide.md](grist-guide.md), [examples/counters](../examples/counters), skill `grist` |
| Packaging | the composite Action (`.github/actions/gotogether`), the image (`ci/image/Dockerfile`, built by CI, published nowhere), `gotogether doctor`, `gotogether init -kind http`, CI tiers | [ci.md](ci.md), DESIGN §64 |
| Analyzers | quintvet (quintsim, quintweave, quintaction, quintctx with fixes), golangci plugin, JSON Schemas for bindings and rule files | qanalysis/, [agents/analysis.md](agents/analysis.md) |
| Tutorials | five guided pages with real output: prove it, liveness, possibles and scenarios, grist on Kubernetes, production at volume | [tutorials/](tutorials/README.md) |

## Integrity admission

| Area | Current boundary |
|---|---|
| Quint admission | Managed runtime work and compiled models require stable Quint 0.33.1 or later. CLI support and installer verification of evaluator bytes are distinct checks. |
| Protocol composition | Universal claims over permitted causal orders are explicit in receipts, aggregate reasons and TUI; they remain separate from local conformance. |
| Finite choices | Confirmed passing witnesses report both evaluators; Go-only negatives retain evaluator provenance. Strict Quint/shadow negative proofs are inconclusive without independent confirmation. |
| Verdict caching | Exact-run Hegel caching remains; a selected invariant path or finite-choice search is not a rejected-prefix certificate. |
| History checking | Repeated same-key appends retain operation order and atomic visibility; the independent serialization oracle covers multiple appends per writer. |
| Set folds | Integer sums prove all-order bounded definedness, including intermediate cancellation; unsafe arithmetic refuses to Quint. Native backend controls and an independent permutation oracle cover the boundary. |
| History search | State-capable checks recover exact prefix states once within the candidate allowance; nil-state checks keep verdict caching. Unsupported candidate judgments stop linearization and pending-call enumeration without an absence proof; confirmed full witnesses and independently judged actors retain their results. Finite-choice history search remains unsupported. |
| Unknown-run evidence | Failed unknown-only claims retain the responsible seed, choices and recorded history through the existing corpus/replay/navigation paths; verdicts and evidence counts remain unknown. |
| Native model verification | Native checks retain strict structured-result classification and the owned after-join search policy. Sequential multi-binding runs may reuse only exact validated positive group proofs within one operation, with independent reports and typed producer/request/source/tool provenance. Each binding still checks its own other goals; unsupported tool identities run uncached. The latest frozen 51-binding run completed ten commands (one intentional skip), including real and retiring counters bindings reusing the default proof. It then stopped at a strict edgepublish counterexample-representation mismatch; the remaining 40 bindings are unknown. Full-roster completion remains unqualified. |
| Mutation admission | Core campaigns include every descendant test suite, require all baselines, and correlate source mutations across per-binary local IDs. Attempt and baseline journals now preserve observed exit codes, actual OS signals and explicit timeout/signal-status conventions; operational kills remain distinct from behavioral evidence. TERM-ignoring tests are stopped at the existing 300-second deadline. Limits and survivor-gate policy are unchanged. Full campaign recovery remains separate from admission correctness. |
| CI | Nightly remains red. The retained November 9 run on `b90c3d8b` passed 55 jobs, including benchmarks, browser admission, evaluator shadows, Grist and fresh-seed workloads. Its scheduler mutation campaign completed 1,749 mutations across seven passing baselines, with 99 survivors against the unchanged limit of 22; verification was canceled near its two-hour job limit. That producer predates execution-status metadata, so operational kills cannot be separated into assertion and resource failures. Later router controls and the native search-policy change are not qualified by that run. Trace mutations pass at their existing limit; the original 500-seed wallet campaign passes after exact-prefix recovery. Full verification and a green current-source nightly remain blockers. |
| Priorities | J1/J2 correctness and J7 nightly recovery precede unrelated features. [Issue #97](https://github.com/chucklehead-dev/gotogether/issues/97) is the review record; ROADMAP owns the selected order. |

## Recent progress and lessons

The core property suite reports native Hegel run errors, including health-check
failures, through its private runner. Hegel 0.9.30 is now admitted against the
current graph with these diagnostics active; the earlier opaque failure remains
historical provenance. Store contract checks force conflicting first decisions;
negative controls prove overlap and a real condition violation rather than
assuming a short sleep made the race happen.

`qhegel.Test` uses the same `hegel.TestCase` callback interface as the search
and trace helpers. Per-case resources use `defer`; outer-test cleanup is not a
case lifetime. KV exercises server closure between generated and rejected cases.
The SDK test runner preserves corpus persistence, output and short-mode limits.
A proposed `hegel.Run` migration was rejected because its public API cannot
supply the corpus key. Full native health-check diagnostics remain an
[SDK follow-up](upstream/hegel-run-diagnostics.md), separate from the core's
private property runner.

Hegel searches distinguish an observed candidate from a reproduced shrunk
violation. `FindViolation` and `Shrink`, like `FindTrigger`, require the final
replay's own failure marker; later operational errors and unstable candidates
fail the search rather than returning earlier model evidence. Cached checks and
the fresh final report must retain conclusive model evidence; bounded hidden
search cannot become a violation. These evidence guards remain active with
Hegel 0.9.30 and its libhegel 0.50 engine.

The current tool patch refresh pairs Quint 0.33.1 with evaluator 0.7.1, preserving
short sampled runs and valid constructor-substring names. Evaluator cache reuse
requires byte identity and startup; publication is staged. Kubernetes staged
modules are aligned at 0.37.2 with consumer semantic controls. All 16 live
Hegel graphs select 0.9.30. Its admission covers full observable properties,
stateful/shrinking/reproduction and race checks, plus actual shared-rule
participation in two concurrent groups. See [admission evidence](go-toolchain.md#hegel-0930-admission-2026-11-06).


Navigator CI reuses verified native tools and bounds acquisition with a shared
command budget plus a step deadline. Missing or broken tools fail bootstrap;
golden checks and rendering remain mandatory. Cold mirror speed is outside the
budget guarantee.


`connect.Run` and `connect.Test` give each trace a native child-test lifetime.
Factories receive that trace's `testing.TB`, so context cancellation and cleanup
finish before the next trace starts. KV's existing 30-trace workload no longer
retains all test servers until parent cleanup. Native Go JSON and `-run` expose
stable trace identities; generated counts do not imply filtered/skipped coverage.


- [#83](https://github.com/chucklehead-dev/gotogether/pull/83): current capabilities,
  boundaries and resume rules stay here; dated chronology is preserved in HISTORY.
- [#84](https://github.com/chucklehead-dev/gotogether/pull/84): compiled pending-call
  resolution supports placement-only and ordering/keyed searches without tools.
  Assumed effects remain unobserved and unfinished, bounds survive portable
  replay, and missing binding evidence cannot establish negative proof. See
  DESIGN §106.
- [#85](https://github.com/chucklehead-dev/gotogether/pull/85): cold processor
  recovery is measured rather than used to justify speculative retention work.

Evidence discovery now uses one streaming family pass and standard JSON value
skipping. Recorded end-to-end artifact mixes improve from about 37→10 and
39→16 ms. Unreadable JSON fails operationally; library file predicates return
`(bool, error)` so callers cannot silently confuse a read error with absence.
Current ordering, ambiguity and version policies remain. See
[the measurements](performance.md#evidence-discovery-streaming-consolidation-2026-11-06).

Native Grist shutdown now cancels and joins its owned processors, relays and
tenant watcher before the final relay flush and resource teardown. Processor
effects must honor cancellation. This closes a deterministic ownership gap
found while investigating a CI directory-cleanup failure; that exact flaky
failure was not reproduced locally. Simulation crash lifetimes remain separate.

Cold processor recovery is now measured on SQLite with fresh App/connection
state, default cadence/memo/batching and audits disabled. The first-ready phase
is about 7–14% of full recovery in this counter workload; warm prefixes remain
incremental. Keep snapshot retention unchanged without an actual latency target.
Raw samples, scope and the executable harness are in
[performance.md](performance.md#cold-historical-processor-recovery-2026-11-06).

Edgepublish's MBT tests now exercise Rust traces without a TypeScript-only
fallback. The stand-in drops unused sealed-generation bookkeeping that queued
an exclusive lock behind gated pushes. Store comparisons, seal writes and
generation locking remain; retention/detach and the real exporter stay outside
this example's scope.
Bounded and failed replays also cancel and join their held pushes, ticks and
background seals; teardown is outside the observed prefix.

These slices illustrate the cadence: general capability, focused contract depth,
then consolidation. Correctness findings can justify consecutive consolidation
slices. Further order modes, stream features or diagnostic breadth need a new
caller problem. Agent-assisted Goo/LRU/HTTP rehearsals remain bounded workflow
evidence; they do not establish independent-maintainer adoption.

Query result structs compare scalar lists as well as scalars. A native library
control exposed wrong-book answers passing count-only checks; generated
store/client bindings now compare identity, order and duplicates. Nil lists
normalize to empty model observations; HTTP answers and phase Await retain their
complete JSON semantics. Unsupported shapes fail explicitly unless excluded.
Current [library](../examples/library/TRIAL.md#current-disposition-2026-11-06) and
[wallet](../examples/wallet-grist/TRIAL.md#current-disposition-2026-11-06)
dispositions distinguish completed fixes from remaining limits.

## Remaining boundaries

These are constraints, not an instruction to start every track. Item identifiers
refer to the canonical [ROADMAP](ROADMAP.md); its current-order section chooses
what to investigate next.

| Area | Boundary | Detail |
|---|---|---|
| Model search | Broader finite-choice integration remains separate; compiled completed/pending-history searches use the artifact alone with explicit bounds and assumed effects | I4, I21; [choice scope](model-choice-witnesses.md), DESIGN §106 |
| Process lifecycle | Abrupt owner death can orphan evaluator grandchildren; managed cancellation/admission does not bound those workers | I12 |
| Evaluator scale | Incremental invariants for interacting entities need representative volume and dependency evidence | I19, I50; [performance](performance.md) |
| Grist reads | A first cold historical read may refold when snapshots are newer; SQLite cost is measured, warm ownership is fixed, and retention still needs a concrete latency target | I24; [measurements](performance.md#cold-historical-processor-recovery-2026-11-06) |
| Grist operations | Safe redrive, explicit skip/resume and rewind detection across entirely restarted nodes remain open; read-only outcome inspection and observed-rewind retirement are built | I25; [operations](grist-guide.md) |
| Database capacity | Virtual physical sessions need faithful idle ownership/pool callbacks; logical transaction limits and native PostgreSQL role quotas are distinct implemented checks | I25 |
| Platform | GR10 slices 5, 7–11 remain; manifests precede kind/canary, sharding depends on leases | [platform order](grist-platform.md#9-slices) |
| Onboarding and deployment | Additional walkthroughs/editor integration and captured deployment provenance need concrete caller evidence | I20, I35, I36, G8 |
| Adoption and release | Independent-maintainer use is still needed; tags, publication and governance are maintainer decisions | R5; [ROADMAP](ROADMAP.md#for-the-maintainer) |

## Resume safely

1. Read [AGENTS.md](../AGENTS.md), the relevant [area notes](agents/), and the
   chosen ROADMAP item. Keep capture integrity, conformance, completion, coverage
   and resource bounds separate in claims, JSON and TUI views.
2. Work on a branch in a worktree from origin/main. Open a PR; preserve published
   commit ancestry when integrating main. Use subagents for independent review
   or bounded parallel tasks with clear worktree/file ownership.
3. Use the declared toolchain and run the shared gate plus relevant module checks:

   ```sh
   GOWORK=off ci/preflight.sh
   GOWORK=off go vet ./...
   # Then run tests appropriate to the changed modules; see commands below.
   ```

   [commands.md](agents/commands.md) owns full/short/native/race commands, CPU
   budgets and environment settings. Root tests do not cover every nested module.
   Slow validate/qtest checks need the documented timeout. For a docs-only edit,
   preflight checks docs, skills, API/schema contracts and the complete help surface.
4. A public API/schema change updates its checked snapshot and CHANGELOG. When a
   published module needs a new core change, publish the source commit, then run
   `ci/bump-core.sh <commit>` for **all** published pins and tidy affected modules.
   Do not fix one consumer pin in isolation; installed consumers must work without
   workspace replacements. See AGENTS.md for the exact rules.
5. Merge only after the full CI workflow completes successfully for the exact PR
   head and all expected checks pass. The early repository gate alone is not
   green CI. A new head needs its own checks; failures are the next task.

[CI guidance](ci.md), the [workflow](../.github/workflows/ci.yml) and
[nightly](../.github/workflows/nightly.yml) describe the gates. Compare performance
with controlled measurements rather than treating configured historical CPU
baselines as fresh observations. Worktrees share Git's stash; prefer branch
commits over uncoordinated stash operations.
