# Status: where the work stands, and how to pick it up

<!-- not built: qtest.NoHotCycle qobs.Sometimes gristtest.StoredLogs -->

For anyone (a person or an agent) resuming this repository cold. Five
minutes here, then [ROADMAP.md](ROADMAP.md) for the detail of whatever
you pick. This page links rather than repeats: when it and ROADMAP
disagree, ROADMAP and the code win, and this page should be fixed.

Structured-lint follow-up (current branch): text and JSON share one
`gotogether.lint/1` report with typed rule-site findings, native source positions,
fixes, inventories and requested checks marked pass/fail/blocked. Independent
checks survive another target's failure. Completeness and exit status are derived
from the report; static inspection does not establish trace conformance. The
[executable walkthrough](lint-onboarding.md) covers planted mistakes, corrections
and blocked/operational controls.

Finite-choice follow-up (PR #10): I4's first bounded slice searches whole
ordinary complete traces and confirms a selected original-action witness with
Quint. JSON and TUI receipts distinguish exhaustive Go search from selected-path
confirmation; unsupported scopes and exhausted bounds give no verdict. See
[model-choice witnesses](model-choice-witnesses.md).

Worker-completion follow-up (merged PR #9): no-work signal waits are scoped;
grist watchers/relays/pollers and simlog senders count accepted work, callbacks,
retry backoffs and paused obligations as active. Short bounds yield Unsettled,
without a settle marker. A separate non-grist HTTP/SQLite cold-start rehearsal
uses published modules and frozen model/binding, with real rerun shrinking and
explicit incomplete/missing-evidence controls. See
[the rehearsal](../internal/adoptiontest/http-sql/README.md).

Diagnostic-evidence follow-up (merged PR #8): explicit binding selectors link
model-authored outstanding intents to captured and native identities, preserving
missing/ambiguous results in JSON. The TUI opens a scoped evidence list and exact
captured-record inspector without moving the cursor or changing judgments. See
[model-diagnostics-prototype.md](model-diagnostics-prototype.md#exact-evidence-links).

Workload-phase follow-up (merged PR #7): gristsim named prerequisites require
actual call outcomes and bounded query observations before later work. They
replace unchecked prologues and inventory pacing sleeps. Failed preparation is
unjudged workload evidence, never a model counterexample or mutant kill. JSON
and navigation/TUI retain phase policy, timings and last observations. See
[grist-workload-phases.md](grist-workload-phases.md).

Model-preflight follow-up (merged PR #6): grist model checks and the generated
witness share one nonwriting, structured catalog/source inspection. Mechanical
changes and handwritten work stay distinct; generated updates retain review
findings. JSON is read-only and discloses blocked checks and unperformed Quint
typechecking. See [grist-model-preflight.md](grist-model-preflight.md).

Run-profile follow-up (merged PR #5): driven explorations resolve execution and
primary validation policies once, preserve per-row profiles in evidence, and show
the actual navigation replay policy. Simulator invocations retain their effective
horizons, caps and reached limits separately. Shared JSON/text/TUI presentation is
documented in [run-profiles.md](run-profiles.md).

Composition follow-up (merged PR #4): `gristtest.Composition.System` registers
local model checkers, catalog peer links and an optional universal protocol check
for every native history, including navigation. `Report.Composition` is canonical;
normal conformance assertions include applicable checks. Wrong histories, wrong
service bindings, unknown obligations and order limits cannot silently pass.
Receipts preserve model-authored diagnostics. See [grist-composition.md](grist-composition.md).
PRs #2, #3, #4, #5, #6, #7, #8 and #9 are merged; the single current navigation contract is nav/2.

Check-receipt first increment (merged PR #3): validation reports and run
navigation share structured statuses, ordering-search limits and assumed effects
through validate/receipt. Whole-run receipts remain independent of cursor state;
all current navigation producers emit required receipts (nav/2, current only). Grist
composition aggregation is included by PR #4. See [check-receipts.md](check-receipts.md).

Evidence freshness follow-up (included in this branch): `GOTOGETHER_SESSION` labels
exploration evidence and run manifests. `evidence -session` filters the
suite before coverage and expectation checks; unscoped completeness checks
require explicit `-history`. Use a fresh ID and `go test -count=1`.

**As of 2026-10-31 (structured-lint branch).** GR4's first composition experiment is implemented
(HISTORY "GR4: the first composition experiment") on the GR3 slice 5
baseline. This prototype branch aligns published modules' core pins at
`760b914b` so scoped worker completion, diagnostic evidence, workload phases, shared preflight-format metadata,
run profiles, finite-choice witness evidence, structured lint emitters, composition and nav/2
contract work outside go.work; the
merged declared-call span increment did not require a core-pin bump. **Nothing is tagged**: every module is pre-release,
published modules require each other at pseudo-versions, and breaking
renames still land (R5 waits for the maintainer).

The first GR4 composition increment is built on that baseline: acyclic
peer graphs, shared services instantiated once, and a three-service
experiment with per-service models plus posting conservation. A combined
protocol model and causal projection (`gristtest.ProtocolSteps`) are built, with missing, duplicate and premature-posting
contrasts. `CheckProtocolOrders` now explores bounded alternative causal
orderings, distinguishing counterexamples, exhaustive checks and limit
exhaustion. See [grist-composition.md](grist-composition.md). The maintainer's direction:
correctness first, then composition and an adoption trial; keep APIs
flexible. The agent-assisted Goo trial now validates the core modeling
workflow in an independently maintained library (not grist adoption):
[PR and passing CI](https://github.com/casselc/goo/pull/1). The first
[GR3 slice 6 increment](grist-call-spans.md) is implemented: a lossless
OTel round-trip over real HTTP for payouts -> bank, including lost replies,
pending invocations and the generated freshKey mutant. Release hardening
is not a gate.

A [model-authored diagnostics prototype](model-diagnostics-prototype.md)
now explores the inventory trial's unanswered-intent reporting friction.
It uses Quint documentation annotations, without an extra binding mapping,
and shares optional structured data across text/JSON/navigation/TUI. This
is provisional work; explicit evidence selectors now link captured and native
identities in JSON/CLI/TUI. Editor source ranges and broader checked-history
coverage remain open. It does not change grist runtime behavior.

## 1. What works now

The toolkit checks Go code against Quint models; [README](../README.md)
is the front door, [ARCHITECTURE.md](ARCHITECTURE.md) the structure.

| Area | What you can do today | Start at |
|---|---|---|
| MBT | Quint traces drive the code (`connect.Run`, `connect.Test`, quint-connect parity) | README §1, [examples/bank](../examples/bank) |
| Hegel | Hegel drives and the model judges and shrinks, or the model drives and Hegel varies (qhegel, its own module) | README §2, skill `gotogether-hegel` |
| Trace validation | steps recorded (`qobs`, `//quint:action`, OTel spans converted) and judged by a binding; `validate -json`, JUnit, SARIF; on-call reports with one record index, wall time, pod, trace id and a state diff | README §3, [instrumentation.md](instrumentation.md), DESIGN §68 |
| Simulation | qenv: virtual time, crashable processes, Store, Queue, Service, Disk, qhttp, qsql, simlog doubles, fault kinds by name; `System.Explore`, Exhaust, PCT, sweeps, mutants (overlays and qmut), coverage by model step | README §4, [examples/hello](../examples/hello), skill `gotogether-faults` |
| Published doubles | `qenv/simgrpc`, `qenv/kubesim`, `qenv/simlog/kafkalog`, `qenv/simlog/kinesislog` as modules; `qenv/simlog` in the core | api/simgrpc.txt etc., ci/consumer |
| Metastability and sizing | qload simulation, qmeta's CTMC, `FindTrigger`, alert rules from qrole gauges; sizing from an app's own steps (`qmeta.Size`, grist's `<app> sizing`) | README §5, [examples/retrystorm](../examples/retrystorm), skill `gotogether-metastability` |
| The Go evaluator | decides passing runs (quint judges failing ones); `simulate -graph` (a complete state graph: invariants proved for the instance, cross-checked against quint's samples), liveness as lassos under per-action weak fairness, deadlocks, possibles and scenarios, partitioned validation of serial logs (`partition:`), `gotogether compile` + `validate -compiled` with no Node or quint | DESIGN §56, §61, §62, §66, §64; [tutorials](tutorials/README.md) |
| Model claims in tests | `qtest.Model` with `qtest.Proves`, `qtest.Refutes`, `qtest.Live`, `qtest.NoDeadlocks`, `qtest.Possible`; explicit quantifiers on run claims (`Some`, `All`, `None`, `qtest.Not`) | qtest/modelclaims.go, DESIGN §67, §69 |
| grist | event-sourced framework checked by its model: events with upcasters and rolling versions, read models with snapshots, commands, rules, queries, processors, declared calls, the HTTP API, `grist.Main`, memstore, simstore, sqlstore (SQLite, PostgreSQL, qsql), the generated model and bindings, `grist new`, gristtest's harnesses; GR10 slices 1, 2, 3, 4, 6 (identity, probes, drain, rolling restarts, failover and `<app> resume`, `catalog -diff`, sizing) | [grist-guide.md](grist-guide.md), [examples/counters](../examples/counters), skill `grist` |
| Packaging | the composite Action (`.github/actions/gotogether`), the image (`ci/image/Dockerfile`, built by CI, published nowhere), `gotogether doctor`, `gotogether init -kind http`, CI tiers | [ci.md](ci.md), DESIGN §64 |
| Analyzers | quintvet (quintsim, quintweave, quintaction, quintctx with fixes), golangci plugin, JSON Schemas for bindings and rule files | qanalysis/, [agents/analysis.md](agents/analysis.md) |
| Tutorials | five guided pages with real output: prove it, liveness, possibles and scenarios, grist on Kubernetes, production at volume | [tutorials/](tutorials/README.md) |

## 2. Done in this push (2026-10-28)

About 200 commits since 2026-10-28 00:00 (`git log --oneline
--since=2026-10-28`). Each line is one item; the commit is the main one
(several items have follow-ups). Detail: HISTORY's entries from "Tenth
review" on.

| Item | What | Commit |
|---|---|---|
| H1 | the Go evaluator decides passing runs; D3 compile cache | 8178000, 54c27fd |
| H2 | a rejection names its false conjunct with values | 4c11232 |
| H3 | `Recorder.Close` closes its sink, returns its error | 2ccdd9c |
| H4 | one vocabulary (grist's caller, Guarantee, mutants in the core) | 1cdd4a4, 36b2876 |
| H5 | the woven ABI, grist's generator hidden | efd04a7, 9cf63bb |
| H6 | the grist path at the front (`gristsim.System`, ShortSeeds, warnings) | 3811902 |
| H7 | upcasting and snapshots; the doubles published | 65e5d23, 3c43801..fe4d368 |
| P1 | blocking rules in grist | 007ec6e |
| P2 | the state-graph explorer (`simulate -graph`) | 9012f7a |
| P3, P4 | possibility and scenario claims | 74fe3b0, c7f7743 |
| P5 | navigators (`gotogether nav`, the qnav TUI) | fc4b2d4, 3298ffa |
| P6 | liveness as cycles; examples/requeue | dc00ab8, f21ca9f |
| P7 | grist's obligations generated | 9ec2918 |
| P8 | skills in pavlov's shape, the migration skill | 6462d5d |
| GR10 s1 | identity, probes, the drain | 1530f20 |
| GR10 s2 | graceful termination, the lagging router, `gristsim.Cluster` | 5b0a0da |
| GR10 s3 | store failover and restore, `grist.ErrRewound`, `resume` (with I25) | f626316, fd98f8a |
| GR10 s4 | versions that roll (with I22) | 2c2747c |
| GR10 s6 | sizing from the app's steps | 4703fc8, 09be377 |
| I1 | a fixed upcaster refolds (fingerprint) | 3b20911 |
| I2 | sound set folds in the evaluator | 4bdec87 |
| I3 | `-graph` cross-checked against quint | 1471d77 |
| I4 (first bounded slice) | finite whole-path search, original-action pinned replay and explicit scope/evidence | current branch; lint 3c92cd5 |
| I5 | ResolvePending keeps invariants if a placement can | fe01988 |
| I6 | qhistory ignores an Unknown transaction's completion | 5cc4785 |
| I7 | cross-recorder real time named in the verdict, a skew setting | 1b80ee0 |
| I8 | liveness says what it checks | 4442276 |
| I9 | verdicts state their bounds | 99009ca |
| I10 | a fault site declared in a run is the run's | 64b99ce |
| I11 | differential tests of qeval against quint; Rust integers read by type | 54326d3, ca859ac |
| I12 (S half) | quint in its own process group, killed on cancel; `t.Context()` | 6fcfdfb |
| I13 | formats and the woven ABI versioned; `gotogether doctor` | 83bca60 |
| I14 | one core version (`ci/bump-core.sh`, the consumer job) | e93b353 |
| I15 (part) | shuffled tests, wider race job, qhegel captures at fd 1 | d0afb10 |
| I16 | explicit quantifiers | 03fa005 |
| I17 | `model.Simulate`, model claims, `qtest.Possible` a claim, `gotogether.binding/2`'s one search bound | c40570f, 55d2504 |
| I18 | validate split: `validate/model`, `validate/navproto`; `qtrace.Record`, the knob rule (DESIGN §71) | 263333b and I18's rest |
| I19 | validation at volume: `partition:` | f27adb0 |
| I20 | on-call reports, `-json` | a985abe |
| I21 | the Action, the image, `compile`/`-compiled` | 1c0e469, 7173581 |
| I22 | versions that roll, the snapshot audit | 2c2747c |
| I23 | sqlstore reads in one query | 1d0166a |
| I24 | grist's reads at scale: the memo, anchored snapshots, metrics, pruning, App options | 3fd4de5 |
| I25 | grist's operations basics | fd98f8a |
| I26 | the generator warns on what an app forgot | a6be970 |
| GR3 s4 | lints for a grist application's own code (lint -sim over its files, quintraw) | 26b8e47 |
| GR3 s5 | peer calls: actors naming the App, `grist.NewPeer`, `gristsim.Config.Peers`, `gristtest.Linked`, examples/payouts | HISTORY "GR3 slice 5" |
| GR4 first | acyclic peer graphs, shared peers, three-service conservation experiment; Linked matches command identity | HISTORY "GR4: the first composition experiment" |
| GR4 protocol | ProtocolSteps and a combined causal model; missing, duplicate and premature posting contrasts | HISTORY "GR4: a combined protocol model" |
| GR4 inventory trial | three-service public-API rehearsal, three bugs, replay and friction report | examples/inventory-grist/TRIAL.md |
| GR4 order search | bounded universal causal ordering checks; counterexample identities and explicit exhaustion | HISTORY "GR4: concurrent protocol orderings" |
| I27, I34 | grist's user guide; ARCHITECTURE.md, example tracks | b382007, c41bd82 |
| I28-I32 | the first hour: a catch shown, progress, reserved names, reports in words, help | 15cf741, c0eb502, eb1520b, acd5fb1 |
| I33, I36 | `gotogether init -kind http`, analyzers that fix, JSON Schemas | 9708379, 79c034c, dce024f |
| I35 | five tutorials | 70f1839 |
| I37-I41 | recording, Reconstruct, qeval, scheduler costs; nightly benchmarks | 3047ddb, 075ce15, cbd8849, 089889b, 65bfb9d |

## 3. Half done: exactly what remains

In the order to take them. Sizes as in ROADMAP (S under a week, M two to
four, L more; agent-days in practice).

1. **I4's broader scope.** The finite ordinary-trace slice is implemented on
   this branch. History/hidden/partition/snapshot/resume/within integration,
   broader IR rendering and tighter enumeration resource bounds remain; select
   further work from adopter evidence. See [the scope](model-choice-witnesses.md).
2. **I35's rest.** (1) a test that every `gotogether` line in a
   tutorial names a real command and flags (as cmd/gotogether's
   TestCommandNeeds); (2) `ci/tutorials.sh` running each `$` line and
   diffing the quoted output, `[...]` a wildcard; (3) the designer's
   pages: wallet-grist's three scenarios with a too-strict model mutant,
   a migrate-payments front page with stage times, wallet to wallet-grist
   as the migration skill's second walk-through, kind for grist (waits
   on GR10 slice 9); run-side hot cycles and strong fairness when built.
3. **I12's M half.** One quint slot limiter per machine (a token
   directory with flock) in place of per-binary `quintSlots`.
4. **I15's rest (S).** A pinned staticcheck job; grist in
   ci/test-times.txt with a -short tier per push and the full run
   nightly; a `GOTOOLCHAIN=go1.25` build of the core (the Go-floor
   decision needs it); ask hegel-go for an `io.Writer` option.
5. **I19's incremental invariants.** A log whose entities interact
   cannot be partitioned and is still judged at the sum of its states;
   judging only the entries a step changed would make entry-wise
   invariants linear. Also the million-step example.
6. **I20's incident example.** An incident walkthrough from alert to
   fix with `-json`, `nav` and `validate -replay`, doubling as the
   runbook; a store-side grist step's pod (needs G8's location).
7. **I25's leftovers**, each concrete in ROADMAP: (1) PostgreSQL
   `LISTEN/NOTIFY` (an `sqlstore.Options` hook, `pg_notify` in the
   append); (2) an admin command listing and re-driving `grist.failed`
   dead letters; (3) `resume -from P` with a model step
   `gristSkipped`; (4) rewinds' memory in the store (a marker per
   rewind); (5) the Store's connection faults; (6) slice 5's DSN reopen
   on an authentication failure. Then the guide's ops section (I27).
8. **I21's publishing.** Publish the image and tag the Action (after
   R5's tags); the CI-budget reference repository; `-manifest` and
   searches under `-compiled`.
9. **H7 and I22 leftovers.** Schema changes beyond upcasting (an event
    split, a field whose meaning changed: grist.md §9); a processor
    reading behind the head refolds from the start when the only
    snapshot is later than its trigger; a new event type stays readers
    first with no API to hold it back (an app's flag, grist-platform
    §3.6); crossdb's kube interceptor onto kubesim (GR7's).
10. **GR10's remaining slices** ([grist-platform.md](grist-platform.md)
    §9; the order there: 8, 9, 10, with 5 and 7 alongside, 11 last):
    - 5, configuration and secrets (M): `FileSource`, `-db-file` with
      reopen, TLS from files, configuration as commands, projections,
      certificate expiry; library; mutants `certOnce`, `flagFromFile`.
    - 7, load and the scaler (M): a gristsim load, an autoscaler in
      qload, sizing's HPA rows.
    - 8, manifests and the lint (M): `<app> manifests`, `-lint`,
      kubeconform, `grist new` given manifests.
    - 9, gotogether-kube and the kind tier (M-L), counters on kind with
      PostgreSQL.
    - 10, the canary gate (S-M), an Argo Rollouts AnalysisTemplate.
    - 11, sharding (M-L, after grist slice 3's leases).
11. **Small leftovers**, one line each in ROADMAP: I1's library
    upcaster-fix example; I2's `examples/shards`; I7's kvstore two-host
    example; I11's multi-variable actions; I23's PostgreSQL benchmark in
    CI; I27's grist/README.md (after grist's tag); I36's gopls note;
    I37/I40's goroutine-id lookups (19-26 µs on Thread and
    Background paths); I39's label allocations (for I50); I41's nested
    modules' benchmarks and examples/perf.

## 4. Next experiments, in recommended order

| # | Item | Size | Why here |
|---|---|---|---|
| 1 | Component-aware shrinking | S-M | preserve the same component/obligation, refusing exhaustion, unchecked evidence and unreached prerequisites; include a non-inventory system |
| 2 | One adopter-driven capability (outbound streams or library starter are candidates) | M | the HTTP/SQLite rehearsal exposed no missing adapter; use concrete next-trial blockers before adding a facade |
| 3 | Further lint/editor/onboarding breadth, only from adopter blockers | S-M | the canonical report and executable lint walkthrough are built; select integration work from observed friction |

Do not start all tracks at once. Worker completion and the bounded HTTP/SQLite
rehearsal are merged in PR #9; the first finite I4 slice is implemented in
PR #10. The first structured core lint report and executable lint walkthrough
are implemented on this branch. Broader onboarding across tracks and editor
integration still need an adoption case. Further inventory/diagnostic expansion,
cycles, clusters and new platform facades wait for evidence. GR10c down-process
settlement has separate semantics and stays separate from GR10b.

The span round-trip and inventory trials retain explicit limits; they do not
establish generic third-party HTTP inference or unaided onboarding. R5 is
available when the maintainer wants tags; no compatibility commitment is needed
for these prerelease iterations. The review and lessons are recorded in
[HISTORY](HISTORY.md#breadth-and-core-lessons-review-2026-10-30).

## 5. Decisions for the maintainer

From ROADMAP's "For the maintainer" (each with its recommendation
there). **Open:**

- **Tags and v0.1 (R5).** v0.1.0 of every published module once I1-I10,
  I12-I14 and I16-I18 are in; tag order gosim, core, qweave, qhegel,
  qanalysis, qgosim, then grist, qnav and the doubles, `<dir>/v0.1.0`.
  Left before then: nothing of the list (whether I12's M half
  and I15's rest must come first is the maintainer's call).
- **The "grist" name.** A codename since grist.md §10.5; recommended:
  keep it and drop "codename", or rename in one commit before an
  adopter. A trademark check is the maintainer's.
- **Stability labels** per package (stable at v0.1: qobs, qobshttp,
  qtrace's Step and Trace, qrun, qrole, woven, the step log, the binding
  YAML; the rest experimental).
- **One core module or a runtime module**: recommended one module at
  v0.x, carve a runtime module after GR4-GR7.
- **The Go floor**: `go 1.25` for the core after I15's go1.25 build;
  qhegel stays at 1.26.1.
- **Governance**: a CHANGELOG by module, a short CONTRIBUTING, the
  weekly breaking renames ending at R5.
- **Upstream reports**, drafted, not filed:
  [quint's REPL import cycle](upstream/quint-repl-import-cycle.md),
  [quint's Rust integers as bignumber.js records](upstream/quint-rust-bignumber-itf.md);
  also worth filing: quint's evaluator not killed with node (I12 works
  around it), hegel-go's output `io.Writer` (I15).

**Already decided** (do not reopen): the format policy (2026-10-28:
pre-release, a reader reads the current major only, a file naming none
read as the current; after v0.1 the maintainer decides; DESIGN §63);
H's and P's items (2026-10-28, as recommended); bare expectations' quantifiers (I16, done); GR10's slice
order (slice 3 beside 4, done); GR10's six decisions of 2026-10-28
(grist-platform.md §10: no Kubernetes Lease; 45 s grace; a rewind fails
closed until `resume`; no app binary links client-go; sizing into
manifests only with `-sizing`; the canary an Argo Rollouts Job analysis
or HTTPRoute weights); GR3's five design questions
(grist-instrumentation.md); the standing decisions at ROADMAP's top
(nothing released, crashes per step, the global recorder opt-in, MIT).

## 6. Known issues and rough edges

- **Generator name shadowing:** a command named `order` with an argument
  named `order` generates a model-only step whose local draw shadows
  the action; Quint typecheck fails (`order(order, ...)`, str versus
  operator). The composition fixture uses `place` instead. Fix the
  generator's local-name allocation; this is a loud typecheck failure,
  not a validated counterexample or a reason to loosen a model.

- **I35's list, reported not fixed** (HISTORY "I35: tutorials"): a
  partitioned report's RESULT counts partitions as "actors" and its
  instance line shows the first partition's constants; a grist
  command-outcome possible reads "reached in the initial state"; the
  `qtest.Possible` witnesses of a grist app carry internal arguments
  (`readPos`, `reads`); a passing negated claim is not in the test's
  summary line; hello's mutant report says "schedule 3" where its nav
  command says `schedule=1:2`; the quint cache grows unbounded (3.2 GB
  seen); `serve -steps DIR/` fails when DIR does not exist.
- **Slow tests.** validate costs about 960 CPU s and internal/qeval
  about 530 on CI (ci/test-times.txt). Locally under load, `go test
  ./validate/` can pass go's default 10-minute timeout: use `-timeout
  30m`, as CI does. counters' faulty `TestPayoutTwoNodes` needs 20
  seeds per push. Under a shared machine, compare ratios, not seconds.
- **Flaky in the past, now guarded:** `TestFaultPoints` (fixed by I10;
  the shuffle job catches the next one). CI's last red runs (548-550)
  were a stale qnav golden and the TypeScript backend's missing diff,
  both fixed.
- **After a core change another published module needs:** push the
  core, then `ci/bump-core.sh <commit>` and push again;
  `TestCorePinsAgree` fails until every pin agrees (AGENTS.md,
  "Requirements and commands").
- **Worktrees share `git stash`.** The stash is per repository, not per
  worktree: an agent in one worktree can pop another's. Commit to a
  branch instead, or name stashes and check `git stash list` first.
- **Stale docs.** docs/grist.md's status paragraph lists GR10 slices 1
  and 2 only (3, 4 and 6 are built too); ROADMAP's I10, I12 and I15 lack
  their "done" marks (section 2).
- **The real tier's assumptions** (the adopter's B4): OTLP files only, no
  collector; spans must not be sampled out; cross-host time assumes 1 ms
  unless `-skew` says otherwise (a per-pair skew is G8's).

## 7. How to resume

1. Read [AGENTS.md](../AGENTS.md) (conventions, gotchas) and the area's
   page under [agents/](agents/); ROADMAP's item for what you pick.
2. Work in a worktree from origin/main, rebase before pushing, push to
   main (`git push origin HEAD:main`). Commit messages end with the
   attribution lines the session gives.
3. Check before pushing:
   ```sh
   go vet ./... && gofmt -l .          # both clean
   go test -short ./...                # the root module, a few minutes (quint on PATH)
   go test -short -timeout 30m ./validate/ ./qtest/   # the slow two, if touched
   GOWORK=off go test ./skills/ ./internal/docsnippets/   # after doc or API changes
   cd examples/hello && go test -short ./...           # each example is its own module
   ```
   A changed exported API: `GOTOGETHER_UPDATE_API=1 go test
   ./internal/apisurface/` and a CHANGELOG line. Other modules'
   commands: [agents/commands.md](agents/commands.md).
4. CI ([ci.yml](../.github/workflows/ci.yml)): `test` (the core: -short
   with CPU budgets, shuffle, race, TypeScript and GOTOGETHER_CHECK
   variants, every example's module), `workspace`, `consumable` and
   `consumer` (outside-in builds), `doubles`, `grist`, `qnav` and its
   tapes, `gosim`, `qweave`, `edgepublish`, the Action's two jobs, the
   image, and per-example jobs. The nightly
   ([nightly.yml](../.github/workflows/nightly.yml), 03:17 UTC): fresh
   seeds, core mutants, fuzz, qeval's differential batch, quint
   verify on every binding, evaluator shadows, volume (10⁵ steps), benchmarks against a
   day before. A package costing twice its ci/test-times.txt line fails.
5. Watch the push's CI to green; a red run is the next task.

**Where the reviews came from.** Each review was a set of agents given a
role (architect, usability designer with newcomers, formal-methods
expert, adopter, Go specialist) at a commit; their reports lived in a
session scratchpad and are not kept. HISTORY records each review's
findings and the synthesis; ROADMAP's I track is the eleventh's (at
`65e5d23`). Its points not yet in any item, or only in a bold idea:

- *Adopter*: ownership cost (someone must write and own each model and
  binding; about 15 terms of vocabulary; the docs are agent-first, so a
  platform team writes its own golden path); grist's production binary
  imports qenv, which auditors will question; 45 go.mod files; no
  timers or sagas against Temporal (GR8), no projections UI against
  event stores. Examples it would want: an open-source controller's
  issue reproduced (I48), a Postgres job-queue library (River-like), grist
  end to end on kind with a managed-style PostgreSQL (GR10 slice 9),
  an OSS service brought under a model in a day.
- *Go specialist*: the goroutine lookup still costs tens of µs on the
  Thread and Background paths; one option style per kind of knob (I18's
  DESIGN rule); gorelease or apidiff once tags exist; examples/perf, a
  hot-service recording benchmark at `-cpu 1,4,8`, an idiomatic
  `database/sql` adapter, qeval at 10⁵-10⁶ states.
- *Formal-methods expert*: refinement, symmetry, partial-order
  reduction, fairness choices, Elle's other workloads, inductive proofs
  and sampling confidence are I42-I45 and I9's nightly line; its
  examples (shards, a lock service live only under strong fairness,
  Elle on SQLite, kvstore under skew, raftkv Exhaust vs PCT) are each
  item's example.
- *Architect*: `SnapshotEvery` the one grist setting in a context (I24: done, App options);
  record kinds as flags on `qtrace.Step` (I18); a runtime module (the
  maintainer's).
- *Designer*: a `grist doctor` checklist (I26: done, the generator's warnings); nav in the
  browser (I47).
