# Roadmap

What is open. What is done (the A–D, U, V, W, R1–R4, X, Y, Z, T, E and G tracks,
the move to gotogether) and the reviews that shaped it are in
docs/HISTORY.md; why the code is the way it is, in DESIGN.md.

Standing decisions, with the maintainer:
- Nothing is released yet. Tags wait for the maintainer (R5).
- Crashes are considered at every scheduling step (`NemesisEvery` is
  opt-in); `qenv.Service`'s crash window is on by default.
- The global recorder is opt-in (`qtest.Global()`).
- qhegel becomes a module of its own before any tag, so the core needs
  only OTel and yaml.v3.
- MIT license.

Order: H1 → H3 → H2 with P5 → P1, P3, P4, P7 → H4 → H5 → H6 (with P8)
→ H7 → GR10 → P2 → P6 (all done but GR10's slices), then the
eleventh review's I track: I1-I11 first (most in progress), I12-I18
before R5, I22 (done, with GR10 slice 4) and GR10 slice 3 before GR4, I23-I25 before a second real grist app, I26-I36 alongside,
I37-I41 after I13, and I42-I51 each decided before it starts. GR3's
slices 4-6 after H4, GR4-GR9 after H5, H6 and I22, P9 after GR4; G8-G12
and the items left from S, X, Y, T and E alongside; R5 when the
maintainer says (the I track's "For the maintainer").

## Eleventh review: integrity before tags (I)

Five reviews at 65e5d23, after H, P and GR10 slices 1, 2 and 6 (an
architect's, a usability designer's with two newcomers' first hours, a
formal-methods expert's with adversarial models, an adopter's for a
platform team, a Go specialist's with benchmarks and the race detector;
docs/HISTORY.md, "Eleventh review"). Duplicates are merged and conflicts
decided there. Order: silent wrong answers, then what blocks adoption
(stability, scale, on-call reports, packaging), then grist's gaps,
usability, performance, and the bold ideas last; each item reviewed
before the next. Sizes as in S (S under a week, M two to four, L more).
**In progress** means an agent is on it now: do not start it again.
Commit 3b20911 cites the architect's draft numbers: its I1 is I1 here,
its I2 (H7's docs) part of I27, its I3 (versions that roll) I22.

### Silent wrong answers

- **I1. A fixed upcaster refolds (S). In progress** (3b20911 landed
  the fingerprint and the mutant `upcasterFixKeepsSnapshots`). A read
  model's fingerprint held no upcaster, so snapshots folded through a
  buggy upcaster stayed in use after its fix, and `CheckSnapshots` (a
  fresh store) could not see it. Left: library's upcaster-fix example
  (a `buggyUpcaster` mutant, snapshots taken under it, then the fix:
  the regression as an example); the runtime audit was built with I22.
- **I2. Sound set folds in the Go evaluator (S). Done** (4bdec87:
  operators of a commuting shape, else every order up to 10 elements,
  else `Unsupported`; left: the example `examples/shards`).
  qeval accepted a set `fold` that gave the same result sorted and
  reversed, which does not make it order-independent: a round-robin
  shard assignment over `Set(1, 33, 2)` made `simulate -graph` "prove"
  an invariant quint refutes, and a run quint rejects passed "judged by
  the Go evaluator". The review's preference: every permutation up to
  about 6 elements, a whitelist of commutative shapes (sums, unions,
  max/min, puts to distinct keys) above it, `Unsupported` otherwise;
  not quint's own set order, which no quint version promises. Example:
  `examples/shards`, a rebalance protocol over a set fold, `-graph` and
  quint agreeing in its README.
- **I3. `-graph` cross-checked against quint (S). Done.** A
  graph's "holds" rested on the evaluator alone. Every `-graph` run also
  samples quint (`simulate.samples`) and requires each state quint
  visits to be a node of a complete graph: a missing one is an evaluator
  bug and an error. It would have caught I2 in milliseconds.
- **I4. An internal choice the expect observes (S, then M). The S half
  done** (the lint, a failure after the choice no verdict, its issue
  naming the fix); **left: pinned replays (M)**. A bound action that chooses inside (`nondet`, `any {}`)
  whose result the binding's expect reads is sampled by quint's replay:
  the same input gave Mismatch at different steps on three reruns, and
  `lint -binding` said "fits". First a lint and a report line ("bind it
  as a parameter"); then replays with the evaluator's witness pinned,
  as `-graph`'s replays write literal calls, so quint confirms a path
  instead of sampling one (the M half stays here if the agent's fix is
  the lint alone).
- **I5. ResolvePending keeps the invariants if a placement can (S).
  Done** (fe01988, with a brute-force test). It judged invariants on the first placement that conforms,
  so `Violates` held for a run that a later placement keeps (and the
  verdict differed with `Linearize()`). A second search, as
  `orderIssue` has; Violates only when no conforming placement keeps
  them, naming the one that does otherwise.
- **I6. qhistory ignores an Unknown transaction's completion (S).
  Done** (5cc4785). A timed-out transaction may commit later; its `Completed`
  made a real-time edge and a `G-single-realtime` that did not happen.
  Elle treats `:info` as infinity. Session order and lost writes are
  I45's.
- **I7. Real time across recorders named in the verdict (S). Done**
  (the verdict names the orders by clocks and the skew; one skew for
  the history, `qtest.ClockSkew`, `validate -skew`; left: the kvstore
  example, and a skew per pair, G8). Two recorders with neither `Call` nor order keys are
  ordered by wall time within `RealTimeSlack` (1 ms), and a
  `NotLinearizable` proof is then true only under that clock
  assumption, which quint's re-check cannot touch. The verdict names
  the cross-recorder edges it used ("assumes clocks of a and b within
  1 ms"). A declared skew per pair stays G8's, with the adopter's
  production case (NTP skew well over 1 ms). Example: kvstore on two
  hosts 5 ms apart, a false "not linearizable" before, the assumption
  named after.
- **I8. Liveness says what it checks (S). Done.** DESIGN §62 and
  the reports: the check is □◇P (P at quiescence), not ◇P, since a
  lasso's prefix may pass through states where the val holds; "no lasso
  is a false alarm for want of a declaration" holds only under
  per-action weak fairness (per-call or strong fairness oblige more, so
  `live` can be wrong for a system with weaker fairness); "a proof for
  the instance" says "under per-action weak fairness".
- **I9. Verdicts that state their bounds (S). Done** (DESIGN §65; the
  operators-new-since-a-clean-nightly list, `qeval.Unshadowed`, is kept
  by hand: the nightly does not write it). `All(...)` over zero
  runs holds vacuously: fail it. Exhaust's "complete" printed with its
  bound ("complete within 2 delays, 1 fault"). PCT's guarantee stated
  as conditional on `Steps` at least the run's length and `n` counting
  every task ever created. The evaluator's sampled share of passes in
  the exploration's summary, raised for plans that touch operators new
  since the last shadow-clean nightly or any `fold`. A one-line
  confidence for sampling ("a bug reached by 1 trace in K is missed with
  probability (1-1/K)^N"), the nightly's per-claim confidence (Left from
  Y) with it.
- **I10. Coverage scoped to its runs (S).** `Coverage.Unreached` reads
  the process-wide registry of declared sites, so in a user's suite it
  reports sites another test declared; qenv's `TestFaultPoints` fails 4
  of 5 times under `-count=5` and under `-shuffle`. Declarations made in
  a run belong to it (as `hooks.FaultNames` does for kinds), or
  `CoverageOf` takes them from the runs it is given; then a CI job with
  `-shuffle=on -count=2` over the core's fast packages.
- **I11. Differential fuzzing of qeval against quint (M). Done**
  (54326d3, a7d9075, 4f3be99; DESIGN §56, HISTORY "I11"): generated expressions
  and actions over the evaluator's builtins (math/rand, not Hegel: the
  core does not import it), judged by the evaluator and both of quint's
  backends, shrunk; two batches per push, 80 a night. It found a let
  evaluated eagerly, exists/forall stopping at the evaluator's own first
  witness, and setToMap keeping one of a key's two values by the
  evaluator's order, all fixed. Left: actions of more than one variable
  and of a model's own step (TestAgreesWithQuint covers the repository's
  models), and quint's Rust backend writing integers past 2^53 into ITF as
  bignumber.js objects, which the toolkit's ITF reader takes for records
  (the harness reads them back; validate's and connect's readers need the
  variable's type to do the same, or quint a fix).
  What it was: Hegel-generated expressions and actions over the builtin
  table, judged by both, nightly: the next I2 found before a user does.

### Adoption: stability

- **I12. quint's processes die with their check (S, then M).**
  `exec.CommandContext` kills node, not the Rust evaluator it spawns:
  a cancelled check leaves a CPU-bound `quint_evaluator` with ppid 1
  that ignores SIGTERM, inflating neighbours' CPU budgets and outliving
  an interrupted `go test`. One `quintCmd(ctx, dir, args...)` helper for
  every call site (process group, `Cancel` killing it, `WaitDelay`,
  argv, `cmd.Dir`, the backend, the slot), `t.Context()` through qtest's
  and validate's checks in place of `context.Background()`, and a Linux
  test that a cancelled check leaves no evaluator. Then (M) one slot
  limiter per machine (a token directory with flock) instead of
  `quintSlots` per test binary.
- **I13. Formats and ABIs versioned (S each, policy: For the
  maintainer). Done** on the recommended policy (DESIGN §63, HISTORY
  "I13 and I14"): one table, internal/formats; `gotogether.steps/1` as a
  format record (an unversioned log is steps/1), `format:` in bindings
  and rule files, `gotogether.corpus/1`, the nav protocol in
  `RunSession`/`RunNode`, `woven.ABI1` checked by qweave, and `gotogether
  doctor`'s version report (the rest of doctor stays I21's). Left for the
  maintainer: confirm the policy, above all that derived files
  (manifests, evidence) read their current major only. What it was: The step log, the longest-lived artifact, has no
  version (every other format does: `gotogether.run/2`,
  `gotogether.evidence/2`, `gotogether.requests/1`); bindings have no
  `format:` key, so a newer binding fails an older CLI with "unknown
  field"; the woven ABI is called by code a qweave binary generates
  against whatever `woven` the user's module resolves; the
  run-navigation protocol between a test binary and `gotogether-nav`
  has no version. `gotogether.steps/1` (a header record; readers refuse
  a newer major with the fix), an optional `format: gotogether.binding/1`,
  woven code referencing `woven.ABI1` (skew fails to compile, by name)
  with qweave checking the resolved core, a `version` in the nav
  handshake.
- **I14. One core version, built from outside (S). Done** but
  gorelease/apidiff, which wait for tags: `ci/bump-core.sh`,
  `TestCorePinsAgree` (no module may lag), and CI's `consumer` job
  building ci/consumer (simgrpc, simlog, kafkalog, qhegel, qtest.System)
  with no replace and no workspace. What it was: Seven core
  pseudo-versions across the published modules (qgosim's five days and
  three breaking tracks old); `go get` resolves their maximum, which no
  CI job builds, and every example builds through `replace`.
  `ci/bump-core.sh` moves every published module to one core commit; a
  CI job builds and tests them at the maximum of their pins; gorelease
  or apidiff per module once tags exist. Example: an outside-in consumer
  module (no replace, no workspace): an order-sync service publishing
  through kafkalog and served by simgrpc, explored with `qtest.System`,
  the first user of GR5-GR7's doubles.
- **I15. The suite's own guards (S).** The race job widened to qtest
  (whose explorations run seeds in parallel), qhistory, qsweep,
  qnemesis, qload, qshrink and internal/qeval (all pass today, about 4
  CPU minutes); qhegel's capture of Hegel's output by assigning
  `os.Stdout` (a data race with any parallel print) replaced by fd-level
  capture, and an `io.Writer` option asked of hegel-go; a pinned
  staticcheck job; grist in ci/test-times.txt with a -short tier per
  push and the full run nightly (grist 332 s, gristsim 283 s wall under
  -short; counters 253-440 CPU s on the same code); a `GOTOOLCHAIN=go1.25`
  build of the core (the floor: For the maintainer).
- **I16. Explicit quantifiers, one place for claims (S, breaking,
  before tags).** A bare expectation means `All` for `Conforms`,
  `Settles`, `Completes`, `Satisfies` and `NoClientErrors`, and `Some`
  for `Rejects`, `Mismatch`, `Violates`, `Scenario` (Left from Y's
  arity). Decided: a quantifier required except on the claims whose
  names say "every run" (`Conforms`, `Settles`, `Completes`,
  `Finishes`), with a panic naming `Some(...)` or `All(...)`, and every
  failure printing its quantifier. `Mutate(t, cfg).Expect(t,
  PerMutant(claims...), NoSurvivors(...))` as Explore's; gristtest's
  `ClientErrors` folded into the negation.
- **I17. Model claims through one Expect (M, breaking for
  `qtest.Possible`).** Three engines answer "can the model ...?" with
  two semantics: the CLI's `simulate` falls back to quint sampling,
  `qtest.Possible` fails "not searched" where the CLI would sample, and
  a test of `-graph`'s verdicts walks `GraphResult` by hand (twophase's
  and crossdb's tests: the walk AGENTS forbids for runs). One library
  engine, `validate.Simulate` (Left from Y), the CLI calling it;
  `qtest.Model(t, binding).Expect(t, qtest.Proves("consistency"),
  qtest.Refutes("x"), qtest.Possible(scenario), qtest.Live("val"),
  qtest.NoDeadlocks())`; one `search: {states, depth}` bound in the
  binding for `searchStates`, `graphStates` and `graphDepth`. Example:
  `simulate -graph` in hello as the step before any code ("the model
  proved for COUNT=3"), with a model mutant whose invariant fails with
  the shortest counterexample replayed by quint; a bounded `graphDepth`
  run in counters.
- **I18. validate split (M, breaking, before tags).** validate is 86
  exported identifiers and 10.7k lines: runs, model checking, the model
  navigator, the run navigator's wire protocol (qnav imports validate
  for it), manifests, step mutants, all to freeze as one v1 surface.
  Model analysis to a package of its own (`validate/model`), the
  navigation protocol to `validate/navproto` with I13's version, qnav
  on it; validate keeps runs. With it: `qtrace.Step`'s record fields
  (invocation, settle, loss, watermark) said to be Reconstruct's, zero
  in a Trace's steps, or a `Record` type of their own; DESIGN's rule
  for a new knob ("a struct for a run's knobs, an Option for a
  test's").

### Adoption: scale and on-call

- **I19. Validation at production volume (M-L). Done** (DESIGN §66,
  HISTORY "I19"; left: incremental invariants for a log that cannot be
  partitioned, and a million-step example). A synthetic payments
  log took 1.5 s at 90 steps, 5.3 s at 300, 54 s at 900 (all in the Go
  evaluator, compile cached) and over 10 minutes at 9000: about n², and
  a payments service writes millions of steps a day. Reconstruct
  presized first (20-30% less time, 27-33% fewer bytes: two lines);
  then the quadratic profiled and fixed; continue after the first
  failure (every later invariant is "not checked" today, hiding the
  blast radius); a serial log partitioned by entity, declared in the
  binding and checked against the model's declaration; budget tests at
  10⁴ and 10⁵ steps in ci/test-times.txt. Example: a million-step
  synthetic day of payments judged in windows or partitions within a
  stated budget.
- **I20. On-call reports (S-M).** The report serves a model's author,
  not on-call at 3 a.m.: three indices for one record ("step 16",
  "seq 7", ":19"); no wall time, pod, trace or request id on the failing
  line; "enabled instead" lists the instance's synthetic arguments as if
  they were records; state dumps cut mid-value; no machine-readable
  verdict. `validate -json`, JUnit and a GitHub step summary; one record
  index with wall time, process and pod, tenant, command and trace ids;
  the "enabled instead" examples labelled as the model's, or behind
  `-v`; a state diff in place of the dump; a verdict-to-runbook table in
  verdicts.md. Example: an incident walkthrough from alert to fix with
  `-json`, `nav` and `validate -replay`, doubling as the runbook.

### Adoption: packaging

- **I21. Packaging (S, then M). Done** (DESIGN §64, HISTORY "I21:
  packaging"): the Action (`.github/actions/gotogether`, composite: it
  runs in the adopter's job), the image (`ci/image/Dockerfile`, built by
  CI, published nowhere: the maintainer's call), doctor's cache, quint,
  binding and compiled-model checks, docs/ci.md's tiers, and `gotogether
  compile` with `validate -compiled`. Left: publishing the image and
  tagging the Action (after R5's tags), the CI-budget reference
  repository, `-manifest` and searches under `-compiled`. What it was: A GitHub Action (`setup-gotogether`:
  quint and its evaluator pinned and cached, `validate` and `evidence
  -strict` steps with annotations) and a container image with quint
  pinned, so no repository copies `install-quint.sh`; `gotogether
  doctor` (the core's, qweave's, qnav's, qhegel's and quint's versions,
  I13's ABI and protocol skews, the D3 cache, bindings' formats,
  `GOTOGETHER_*` in effect, each skew with its fix); a CI tiering guide
  (per PR: recorded tests' conformance and -short explorations under a
  CPU budget; nightly: seeds, Hegel, mutants) as a reusable workflow.
  Then (M) a compiled-model artifact (`gotogether compile` writes the
  evaluator's IR), so a validate-only pipeline (a canary, a CronJob, an
  incident laptop) needs no Node or quint for passing runs. Example: a
  CI-budget reference repository, ten small model-checked services under
  5 CPU minutes per PR each.

### grist's gaps

- **I22. Versions that roll (M, with GR10 slice 4, before GR4). Done**
  (HISTORY: "Versions that roll"; docs/grist.md §2.1,
  docs/grist-platform.md §3.9). `grist.WritesVersion` (a release reads
  the new version and writes the old through a downcaster, the next
  writes the new), `<app> catalog -diff` (safe, readers first,
  breaking), gristsim's `Config.From` and `gristtest.Upgrade` (both
  releases' nodes on one store, the Cluster's rolling restart the
  rollout; `ReadersFirst`, `WritersFirst`), an older node serving nothing
  from a newer event (503, its processors waiting), counters v2 with the
  `eagerV2` mutant; I1's runtime snapshot audit with it
  (`grist.SnapshotAudit`, two counters on `/metrics`).
- **I23. sqlstore reads in one query (S-M, before grist's tag). Done**
  (HISTORY: "I23: sqlstore reads in one query"). `sqlstore.Read`
  fetched positions from `grist_index`, then one `SELECT` per event:
  31-37 µs an event on SQLite (5,000 events 155 ms), a round trip each
  on PostgreSQL (a 5,000-event rebuild 1.5-5 s). Now one `grist_index
  JOIN grist_events` statement per type of the selector, on every
  dialect (qsql gained a lookup join): 4-8x faster
  (`BenchmarkRead`, grist/sqlstore). Left: PostgreSQL in CI, so its
  numbers are measured there too (I41's nightly benchmarks).
- **I24. grist's reads at scale (M).** A read with snapshots on costs
  about twice `SnapshotEvery` events (dc959e8 kept short logs as they
  were), with no in-process memo though folds are pure; a failed
  `PutSnapshot` is ignored (a read-only role folds from 0 forever,
  silently); `/metrics` says nothing of folds or snapshots; retired
  fingerprints' snapshots are never pruned. A memo keyed by (tenant,
  read model, fingerprint, key); `grist_fold_events` and
  `grist_snapshot_{hits,misses,put_failures}`; pruning after a put,
  deferred while two fingerprints are live in a rollout;
  `grist.Snapshots(every)` an App option in place of the context
  setting, gristsim's field mapped to it (the audit built with I22:
  `grist.SnapshotAudit`, the same setting's shape). Example: counters at 10⁵
  events, snapshots on and off, the fold cost printed.
- **I25. grist's operations basics (M; GR10 slice 3 moved up).** The
  DSN from a file or the environment (a flag shows in `ps` and the pod
  spec; slice 5's first part); structured lifecycle logs (the drain
  logs nothing); request rate, latency and errors by outcome, Go
  runtime metrics and build info; a schema-version table and
  migrations (today `CREATE TABLE IF NOT EXISTS` at boot); retention for
  `grist_steps` and `grist_markers`; `steps -export` (from slice 9) and
  an admin command for `grist.failed` dead letters. A tenant's appends
  serialize on its `grist_heads` row (`UPDATE ... RETURNING`: dense
  positions in commit order, conditions under the lock, by design):
  measure a tenant's append ceiling on PostgreSQL and say it in the
  docs, beside `LISTEN/NOTIFY` for other processes' appends (polled
  every 250 ms today; the adopter's review). GR10 slice 3
  (failover and restore: `ErrRewound`, `<app> resume`) is built next to
  slice 4, before anyone runs grist on managed PostgreSQL. Example: a
  store failover in wallet-grist where `ErrRewound` meets an at-most-once
  effect.
- **I26. The generator warns on what an app forgot (S-M).** A newcomer
  added `ItemRemoved` and no read model's fold of it, in Go and Quint
  alike: generation, Verify and every test passed, and removals never
  lowered stock (the tenth review's newcomer met its twin). `<app>
  model` warns "item-removed is appended by remove-item and folded by no
  read model; tagged item like stock's events: should stock fold it?",
  and likewise rules with no refusal mutant, commands no scenario
  takes, a stale grist.lock: in the generator's warnings, where the
  loop already looks.
- **I27. grist's user guide (M). Done** (HISTORY: "I27 and I34: the
  structure written down"; dc959e8 had landed grist.md §2.1-§2.2 as
  built and the grist skill's versions reference). docs/grist-guide.md
  is the guide the README links: `grist new` and the loop, every block,
  the HTTP API, `grist.Main`'s commands, the generated model and what an
  app writes, testing (Generated, explorations, Exercised, scenarios,
  mutants, the real tier, Upgrade), versioning with the tutorial
  "Changing an event" on library's book-added (the lock's refusal, the
  upcaster, `dropsCopies` caught, the kept v1 log judged, readers
  first), snapshots, production (identity, probes, the drain, -grace,
  rolling restarts, sizing, validating the steps) and a troubleshooting
  table of real messages. grist.md opens with a status table (built or
  designed per section) and points users to the guide; backticked
  `pkg.Ident` in prose is checked against the packages' exports
  (internal/docsnippets' TestProseNames; grist's packages added to its
  map), a design not built behind `<!-- not built: ... -->`; the grist
  skill's references link the guide. Left: grist/README.md as
  pkg.go.dev's page (the guide's first section, once the module is
  tagged), and the guide's ops section to follow I25 (the DSN from a
  file, migrations, failover).

### Usability: the first hour

- **I28. The quickstart shows a catch (S). Done** (15cf741 and
  the I29/I32 commits; HISTORY: "Eleventh review: the first hour"). README promised hello's
  `as expected` blocks and a shrink line; `go test -v` prints neither,
  so a newcomer never sees what a catch looks like. A passing
  expected-failure claim logs its first counterexample in three lines
  (the step, why, the faults Hegel shrank it to); the README quotes the
  real lines; hello's stale numbers fixed (2508 for 4430, 80 lines for
  73).
- **I29. First runs fast and visible (S). Done** (progress on the
  terminal, since go test holds stderr back; a short exploration tops up
  its seeds while a kind its Rates set went unfaulted). `grist new` said to run
  `go test ./...`: 343 s, silent (the README says `-short`, 73 s). It
  prints `go test -short -v ./...` with its time, and "downloading the
  toolkit (first run: about 2 minutes)"; long explorations print a
  progress line on stderr every 10 s when not `-v`; fault-rate claims
  under -short that three seeds cannot fail by chance (S10's stated
  power: a `Reaches` with 3 seeds failed after an unrelated change).
- **I30. Reserved names caught by the toolkit (S). Done** (eb1520b:
  internal/quintnames, checked against quint's own lists). `item` is a quint
  built-in (QNT101 on a REST developer's first model) and AGENTS.md's
  list omits it. The list generated from quint 0.33's built-ins, not by
  hand; `scaffold` and `lint -binding` checking step fields against it
  and suggesting `item_`, as grist's generator does.
- **I31. Reports in the user's words (S-M). Done** (acd5fb1).
  Decided against hiding the
  evaluator's share (I9 wants it): written for a user ("30 runs, all
  conform (quint confirmed 2)"), the breakdown behind
  `GOTOGETHER_VERBOSE`; grist's skeleton helpers out of "never taken";
  "rejected at the first addItem" for a rejection with no step taken;
  one file:line per mutant summary (not `mutants.go:207:`); a passing
  `qtest.Record` logs its summary; "the run" for a single actor;
  "steps" not "calls". A zero-step run whose client was refused (qhttp
  knows: nothing listening yet) says so, is not "a reconstruction
  issue" nor "a deadlock", and `nav -at fail` on it names the last
  client error. Failure copies under `$TMPDIR/gotogether/`, removed as
  said (173 were left in /tmp).
- **I32. help, nav and the CLI's errors (S). Done.** One line per command in
  `gotogether help` (what it answers, one example), flags under `-h`,
  `-graph` on simulate's line; `validate` with no arguments says what it
  needs; nav's `-follow` error shows `#1` and the take form ("bare
  numbers are a run's options"); a disabled action's "closest value" not
  drawn from another action's nondet; run-nav titles in words ("after 4
  choices: client ran 4 times") before the schedule id; "in the then
  branch of if (...)" in `why:`.
- **I33. Scaffold for an HTTP service (M).** `scaffold -system -http`:
  the server's process before the client's, a retrying client, qhttp
  wired; `Exercised()` in a test of its own, its failure suggesting the
  exact `qtest.Exercised("rpc.stock.delay.late") // why: ...` line;
  `possible:` with the call form (`'reserve(result = "refused")'`). A
  REST developer's 10 minutes of wiring to one.
- **I34. The structure written down (S-M). Done** (HISTORY: "I27 and
  I34: the structure written down"). docs/ARCHITECTURE.md (the module
  graph and its dependency rule, the core's layers, the five types, a
  check's data flow with the simulated, model-analysis and grist paths,
  I13's formats, what is published, where to start reading, where each
  area's gotchas are), DESIGN and the README citing it; AGENTS.md's
  layout with qnemesis, internal/qeval and the six examples it lacked;
  the examples in seven tracks (first steps, services, distributed
  systems, Kubernetes, grist, migration, metastability), each in an
  order to read: a track table on the README's front page,
  examples/README.md every row; run-demo.sh's conventions written there
  and made one (`MUTANT=`, exit 0 when the demo showed what its README
  says, a mutant caught included, 1 when not, 2 when it could not run),
  CI's demo lines plain; run-demo.sh for bank, glomers and fleetrollout,
  crossdb's on its simulated tier (`KIND=1` for kind); internal/exampleapps
  checking every example's README test table, track row, model, demo
  and mutant (sktrace exempt as a library, gosim's model). Left: G11's
  rest.
- **I35. Examples and tutorials for the newest features (M).** Each a
  page of the Learn track, its commands run by CI:
  - P2, "From sampling to proof" on hello and twophase (I17's), with
    `-depth` on quorumkv to show the bound;
  - P6: examples/requeue landed (f21ca9f); a run-side counterpart waits
    for `NoHotCycle` (I51), a strong-fairness one for I43;
  - scenarios as acceptance criteria: wallet-grist's three, each
    `qtest.Possible` in the model and `Some(...)` of runs, with a
    too-strict model mutant;
  - GR10, "Your grist app on a cluster" (counters' rolling restart, a
    30 s drain in a 30 s grace shown failing), then kind;
  - a front page for migrate-payments with each stage's time, linked
    from the quickstart as "I have code and no model"; wallet to
    wallet-grist as the migration skill's second walk-through.
- **I36. Analyzers that fix (S-M).** SuggestedFixes in quintsim (`go
  f()` to `qenv.Go`, `time.Now()` to `qenv.Now(ctx)`, `time.Sleep` to
  `qenv.Sleep`); a quintctx analyzer (`context.Background()` in a
  function with a ctx, or a test's check without `t.Context()`); gopls
  use documented (the golangci plugin, or quintvet in a pre-commit
  hook); JSON Schemas for binding and convert YAML.

### Performance

- **I37. Recording a step (S, after I13). Done** (3047ddb; HISTORY:
  "Eleventh review: performance"). `Step.AppendLine` writes
  json.Marshal's bytes in the keys' order, checked against json.Marshal
  on generated steps and the repository's logs: a step to a step log
  13.9 → 3.8 µs, 49 → 8 allocs; `MarshalLine` 13.2 → 1.6 µs. Left: the
  example (an HTTP server through qobshttp, a Tee of a buffered and an
  OTel sink, `-cpu 1,4,8`), and a measurement of the parallel case on
  a machine that is not shared (here, at load 20, 3.6 µs a step on 4
  cores against 4.1 on one); a `qobs.Thread`'s step still reads its
  goroutine's id (19 µs here).
- **I38. Reconstruct by index (S-M). Done** (075ce15). Indices into the
  input, each step copied once: 100k steps 851 → 101 MiB, 1.30 → 0.37 s
  (before I19's presizing), checked against the copying implementation
  on generated bags and the repository's logs.
- **I39. qeval's quick wins (S).** The bank explore is 270 KB and 3,000
  allocs a state: a struct seen-key in place of `k + "#" + p`, sized
  event slices, binary state keys, int literals parsed at Load, an
  integer kind switch, `SetGCPercent(400)` during a search (32% alone),
  an Explore benchmark: an estimated 2-3x for the graph, liveness and
  `Reach`.
- **I40. The scheduler's small costs (S). Done** (089889b). A fault
  choice's rate found by its kind's dot-prefixes (120 → 57 ns), no
  options closure or nemesis maps per step (a bare step 0 allocations,
  a step with crash nemeses 2.3 → 1.0 µs and 7 → 0 allocations), a
  task's step found from its context before its goroutine (a step
  recorded in a simulation 26.8 → 3.9 µs), the goroutines' simulations
  in 64 shards. Left: a step recorded with context.Background() still
  reads the goroutine's id (26 µs here), as does every woven call.
- **I41. Benchmarks in the nightly (M). Done** (65bfb9d, 79a03cb).
  Benchmarks of Record, MarshalLine, ReadJSONL, Reconstruct, the
  goroutine lookup, nemesis steps and a step in a simulation; the
  nightly's `benchmarks` job measures every root-module package's at
  HEAD and at the commit of a day before, interleaved on one runner
  (`ci/bench.sh`), and fails on a significant change past 25% in
  sec/op or 10% in B/op or allocs/op. Left: the nested modules'
  benchmarks (grist's `BenchmarkRead`, on PostgreSQL too; qeval's once
  I39 adds them, which join on their own in the root module), and
  examples/perf (one system's virtual-time and CPU profiles side by
  side with the costs a user should expect).

### Bold ideas (each its own decision before it starts)

- **I42. A model diff and refinement (M each).** Two models, or a model
  and its mutant, compared on the projection a binding observes ("v2
  admits these traces v1 forbade"): migrations and model changes in a
  PR reviewed like code, "never loosen a check" made mechanical; then
  refinement mappings over the graph (every mechanism edge a spec edge
  or a stutter), with twophase's Choreo model refining an atomic-commit
  spec and a mutant breaking it at a named edge.
- **I43. Graphs further (M each).** Symmetry reduction
  (`simulate.symmetric: [PROCS]`), fairness on demand (per-call weak,
  strong for named actions: `simulate.fair`; example: a lock service
  live only under strong fairness of `acquire`), inductive invariants
  through Apalache behind `simulate -prove`, proof certificates (the
  complete graph's states and edges, re-checkable offline).
- **I44. Exhaust with sleep sets, then DPOR (M, then L).** Footprints
  from the doubles' seams (Store keys, queue names). Example: raftkv's
  depth-2 election bug found by Exhaust at Delays=2 within MaxRuns, and
  by PCT at its predicted rate.
- **I45. Elle's rest (S-M each).** Session-order edges, an rw-register
  workload, a committed append no later read shows (a lost write).
  Example: SQLite against a hand-built "SI over read committed" shim
  with a seeded lost update, and a timeout-heavy run for I6.
- **I46. Production capture (M each, after I19).** A trace-capture SDK
  from payd's and streams' shapes (net/http middleware, gRPC
  interceptors, a `database/sql` commit feed, a Kafka consumer hook); an
  OTel Collector processor keeping step-bearing spans whatever the
  sampling, writing step logs per recorder and the watermark (with G8);
  continuous conformance (hourly windows,
  `gotogether_conformance{verdict,action}`); a canary gate for any
  service (an Argo Rollouts AnalysisTemplate, a Flagger webhook),
  generalising GR10 slice 10.
- **I47. A shared report (M).** A manifest bundle rendered as one HTML
  page (the history, the failing step, the states before and after,
  nav's branches), shareable in an incident channel; then `nav -serve`.
- **I48. Controllers' entry (M).** kubesim as "envtest with faults": a
  drop-in client and cluster for controller-runtime tests, contract
  faults first, the model optional. Example: an open-source controller's
  issue reproduced in kubesim by a seed, then on kind.
- **I49. No blank page (M-L).** `gotogether init` (HTTP handler,
  library, simulated service or grist), a model skeleton from a Go type
  (`scaffold -from-go`), a model proposed from recorded REST runs, and
  `gotogether tour` through hello.
- **I50. Quint text off the hot path (L each).** grist's models built as
  qeval's IR from the catalog, the user's rules compiled alone (a cold
  verdict's 7-40 s typecheck gone); qeval compiled to closures with
  slot-resolved environments (5-10x on top of I39); a WASM playground
  on the Go evaluator.
- **I51. Small, each S.** `qtest.NoHotCycle()` over unsettled runs;
  generated constants for a package's fault names; `gristtest.StoredLogs`
  (every kept production log through today's upcasters, folds and
  model); `qobs.Sometimes(name)`, the code's twin of `possible:`; a
  library of ghost-variable modules; the model's facts (holds, terminal,
  fairness, possibles) beside the spec rather than in each binding.

### For the maintainer

Decisions only the maintainer takes, each with the recommendation:

- **Tags and v0.1 (R5).** Recommended: v0.1.0 of every published
  module once I1-I10, I12-I14 and I16-I18 are in (the wrong answers,
  the processes, the formats, one core version, the breaking API moves),
  in R5's order (gosim, the core, qweave, qhegel, qanalysis, qgosim,
  then grist, qnav and the doubles), each `<dir>/v0.1.0`; v0.x for long
  enough that GR4-GR7 settle.
- **Format and ABI policy.** Recommended: every wire format named
  `gotogether.<thing>/<major>`; a reader refuses a newer major with the
  fix and reads every older major it ever read (the step log is
  production data: forever); a breaking change bumps the major, an
  added optional field does not. The step log's attributes keep the
  `quint.` namespace (they describe model steps; renaming breaks every
  log written). The woven ABI's version in a constant's name
  (`woven.ABI1`), bumped on any change, qweave refusing a mismatch. A
  deprecation window of one minor release for exported API after v0.1,
  in a CHANGELOG by module.
- **The "grist" rename.** Where it comes from: docs/grist.md §10.5
  (6fdafe2, 2026-10-27), a default "the maintainer may override":
  "'grist' is a codename ... it also names gristlabs' spreadsheet
  (grist-core); renamed before the module's first tag", echoed by this
  section's heading "grist (codename)" and grist.md's first lines. No
  candidate name and no plan exist anywhere in the repository; the
  adopter's review read the default as a decision. Recommended: decide
  before grist's first tag. Keep "grist" (the import path is under
  gotogether, grist-core is a spreadsheet in TypeScript and Python, and
  a rename touches every app's imports, the `grist:"tag"` struct tag,
  grist.lock, `GRIST_UPDATE`, `grist new`, the skill and the docs),
  dropping "codename"; if it is renamed, in one commit now, before an
  adopter. A trademark check is the maintainer's.
- **Stability labels.** Recommended: a label in each package's doc
  comment, listed in the README. Stable at v0.1: qobs, qobshttp,
  qtrace's Step and Trace, qrun, qrole, woven, the step log and the
  binding YAML. Experimental: qnemesis, qhistory, qsweep, qmeta,
  qdriver, qmut, qshrink, grist's platform parts (gristload, Cluster),
  the navigation protocol until I18.
- **One core module or a runtime module.** qobs, qrole, qrun and woven
  are imported by production binaries, qnemesis or qmeta will break
  more. Recommended: one module at v0.x now (costs nothing); carve a
  runtime module (qobs, qobshttp, qrole, qrun, woven, `qtrace.Step`)
  only after GR4-GR7, when the shared internals' home (`internal/hooks`,
  `internal/wovenrt`) is clear.
- **The Go floor.** The core's `go 1.26.1` was hegel-go's, which the
  core no longer imports; organisations pinning N-1 cannot adopt.
  Recommended: `go 1.25` for the core (and grist if it builds) after I15's
  `GOTOOLCHAIN=go1.25` build passes; qhegel stays at 1.26.1.
- **Bare expectations' quantifier (I16).** Breaking for every example's
  bare claims. Recommended as I16 says, before tags.
- **GR10's slice order.** Recommended: slice 3 (failover and restore)
  beside slice 4, before GR4 (I25): a managed PostgreSQL failover that
  loses an asynchronously replicated tail goes unseen today, which a
  payments adopter calls disqualifying.
- **Governance.** 780 commits in nine days, by agents, one maintainer:
  an adopter's bus factor. Recommended: a CHANGELOG by module, a short
  CONTRIBUTING (how changes are reviewed, what "breaking" means before
  and after v0.1), and the weekly breaking renames ending at R5.
- **Upstream.** hegel-go: an `io.Writer` option for its output (I15).
  quint: the evaluator not killed with node (I12 works around it; worth
  a report), and the REPL's import cycle (H's Upstream).

## Tenth review: one product (H)

Three reviews after GR3 slice 3 (a performance review across every
module, a designer's product review, a newcomer's three tasks end to
end; docs/HISTORY.md), the maintainer's decisions of 2026-10-28, each
item as recommended:

- **H1. The Go evaluator decides passing runs (M). Done** (8178000,
  DESIGN §56; D3 too, §57; D2 left: performance.md says why). After the batch fix
  (d6640dc) a grist suite's quint CPU is mostly the processes that
  decide passing runs (counters 231 of 428 CPU s, wallet-grist 210 of
  319); the evaluator judges counters' real-tier run in 27-76 ms after a
  7 CPU s compile, against quint's 25 s. quint keeps judging failing
  runs (their reports, their states); bindings with hidden actions are
  the evaluator's already; the nightly's evaluator-shadow jobs stay the
  guard. Then one quint process per run instead of per actor (D2:
  validate's single checks were 274 of its 730 processes; renames the
  per-actor files), then a content-addressed cache of typechecks and
  compiles across processes (D3: overlay mutants' test binaries, the
  CLI, CI's binding and qeval packages; one binding entry point added).
  docs/performance.md.
- **H2. Why a step was rejected (M). Done** (DESIGN §58). A rejection names the conjunct
  that is false, with its values (a grist read set: "does not cover
  removeItemNeeds(...): stockReads(b)"); the newcomer's largest time
  sink in two of three tasks. Info lines that do not apply to the
  binding checked are silent (Linearize on a store-side binding,
  "actors: left out").
- **H3. Recorder.Close closes its sink and returns its error (S,
  breaking).** The newcomer's only compile error; a standalone program's
  recording documented (recorder, sink, close).
- **H4. One vocabulary (M, breaking, before tags). Done** (HISTORY:
  "H4: one vocabulary"). grist's actor (the
  authenticated principal) becomes the caller (`WithCaller`, `CallerOf`,
  `Callers`, `CallerFromHeader`); `grist.Delivery` renamed
  (`Guarantee`); gristtest's Exercised and Takes become qtest's claims
  parameterised by the catalog; the overlay mutant harness moves to the
  core (`qtest.Mutants`, `qtest.Mutant()`, `GOTOGETHER_MUTANT`), its
  mutants found from testdata/mutants instead of listed, webjobs' and
  payd's own Overlay helpers deleted; overlays the taught way to plant a
  mutant by hand (hello's in-code enum and scaffold's retired), qmut the
  generated one.
- **H5. Hide what no user calls (S-M, breaking for no user code).
  Done** (DESIGN §60; HISTORY: "H5's first half: the woven ABI", "H5's
  second half: grist's generator"): the woven ABI and storetest's
  checks (32 identifiers), then grist's generator, CLI plumbing and
  limits (18 more, under grist/internal/gen and grist/internal/catalog
  or unexported).
- **H6. The grist path, shorter and at the front (S). Done** (HISTORY:
  "H6: the grist path at the front"): `gristsim.System`,
  `ExploreConfig.ShortSeeds` (the three grist examples' closures and
  twelve examples' `seeds()` gone), `gristsim.Config.Ints` (arguments
  drawn by type, zero and a negative among them: the scaffold's `gen`
  gone), the generator's warnings when a rule's inputs changed, grist in
  README's front section, AGENTS.md, an Install line and `gotogether
  help`, docs/grist.md's §0 notes in HISTORY, the skills trimmed to
  triggers and steps (SKILL.md 20,300 words to 3,400, nothing lost: their
  bodies are references), every demo on `MUTANT=<name>` and failing
  loudly.
- **H7. Upcasting and snapshots before GR4, the doubles published
  before GR5-GR7 (L).** A team's first change to an event struct breaks
  replay of every stored log, and read models refold from 0 on every
  start: grist's upcasting and snapshots (slice 3's rest) come before
  GR4. simgrpc (grpcledger), simlog and its Kafka/Kinesis adapters
  (streams) and kubesim (fleetrollout) become published packages or
  small modules, the examples importing them, before GR5-GR7 need them.
  The first half done (HISTORY: "H7's first half: upcasting and
  snapshots"): versioned events with an upcaster chain checked by
  generated values, old logs judged by today's model, grist.lock,
  snapshots keyed by a fold's fingerprint in every store tier, checked
  against a fold from the start (docs/grist.md §2.1, §2.2); a fixed
  upcaster refolds (its probe in the fingerprint); a field added to a
  version is taken as that version; versions that roll and the runtime
  snapshot audit (I22: HISTORY "Versions that roll"). The second
  half done (HISTORY: "H7's second half: the doubles
  published"): `qenv/simlog` in the core, `qenv/simgrpc`,
  `qenv/kubesim`, `qenv/simlog/kafkalog` and `qenv/simlog/kinesislog`
  modules.
- **Upstream:** quint 0.33's REPL refuses a file that a file it imports
  imports back (QNT101), which typecheck accepts. grist's model layout
  no longer has such a cycle, so D4 is unblocked (and measured: no gain
  on 0.33, docs/performance.md); the report is the maintainer's.

## Behavioral programming's lessons (P)

Three reviews of pavlov (a behavioral-programming library for Clojure:
bthreads that request, wait on and block events, composed append-only;
the same program run by its model checker, with safety bthreads, hot
states, possibility checks and a navigator over execution branches),
docs/HISTORY.md. What it does better is one primitive (block), checks we
lack (possibility, scenarios, liveness as cycles) and exploration (a
navigator); what it gives up is the independent spec, which every bug
the newcomer's three tasks found needed, so "the program is the model"
is not taken. The maintainer's decisions of 2026-10-28, as recommended:

- **P1. Blocking rules in grist (M). Done** (docs/grist.md §2.4.1;
  HISTORY: "P1: blocking rules"). `grist.NewRule(App, name, Event,
  func(r *grist.Reads, e E) error)`: whenever a decision would append an
  event of that type, the rule runs in that command's transaction on its
  Reads, before the append; its reads join the command's condition (a
  concurrent change conflicts and retries), its rejection is the
  command's refusal, added to the command's declared refusals in the
  catalog. Verify refuses a rule on an event no block appends; the
  generator writes a hole per rule and guards every action appending its
  event with it; Exercised takes each rule's refusal; the scaffold's
  mutants drop a rule. A requirement added without editing a command
  ("no deposit until KYC cleared"). Commands first; a processor whose
  Emit a rule blocks fails visibly (block-until for processors later).
- **P2. A state-graph explorer over the Go evaluator (M-L). Done**
  (DESIGN §61; HISTORY: "P2: the state-graph explorer"; `gotogether
  simulate -graph`, `validate.Checker.Graph`). A
  breadth-first graph of a model from Init with qeval's Successors (made
  to return labelled edges: the action and its picks) and Key, dense ids,
  a parent tree for shortest counterexamples, bounded by states and
  depth and saying when it truncated: every invariant in every reachable
  state, deadlocks, actions never enabled, without a quint process
  (about 0.2-0.5 ms a transition: 10^5 states in under a minute). A
  bounded exhaustive alternative to `gotogether simulate`'s sampling;
  grist's growing log needs the depth bound.
- **P3. Possibility claims (S). Done** (DESIGN §59; HISTORY: "P3 and
  P4: possibilities and scenarios"). In the model: a binding's
  `simulate: possible: [P, ...]` (some trace reaches P; quint's
  counterexample to `not(P)`, or P2's search), catching a model too
  strict to reach the scenario a conforming run would prove; the
  generator emits one per command outcome and processor result. In runs:
  Takes and Exercised already cover the code's side.
- **P4. Scenario claims (S). Done** (DESIGN §59). `qtest.Scenario(qtest.Takes("submit"),
  qtest.Takes("verify").Outcome("ok"), qtest.Takes("open"))`: a run's
  steps contain the sequence, others allowed between; `Some`/`All` over
  runs; a failure names the longest prefix matched and where the run
  stopped. grist's `gristtest.Scenario` builds one from the catalog's
  commands with their arguments (and, with P3, the scenario is checked
  possible in the model). A domain expert's acceptance criteria.
- **P5. Navigators, with H2 (M). Done** (DESIGN §58: the Go API, the
  run navigator in test binaries and its sessions, folding, `gotogether
  nav -json`, the failing reports' `navigate:` lines, and the TUI, the
  qnav module's `gotogether-nav` with its VHS tapes). Over a model (P2's graph: a node a
  state, its branches the enabled actions with picks, every disabled one
  with its first false conjunct, H2's evaluation) and over a run (a node
  a prefix of its choices in Exhaust's schedule syntax; its branches the
  next choice's named options; a child a replay with that option forced,
  milliseconds; the steps recorded so far, Sim.Tasks, the model state
  after them). Two front ends over one Go API:
  - for agents, `gotogether nav ... -json` (a node, its path, state,
    branches, disabled actions with their conditions), and failure
    reports print the nav command of the failing node;
  - for people, a TUI on Charm's v2 libraries (bubbletea, lipgloss,
    bubbles): browse branches, fold linear stretches of nominal choices,
    jump to the failing step, see the state's diff from its parent, save
    a found branch to the corpus. The Charm dependencies stay out of the
    core (OTel and yaml.v3 only): the TUI is a module of its own (its own
    go.mod and api file, published like qweave), its binary invoked by
    `gotogether nav` when installed.
- **P6. Liveness as cycles (M, after P2). Done** (DESIGN §62;
  HISTORY: "P6: liveness as cycles" and "P6's review"; `simulate
  -graph`'s eventual vals, `validate.Checker.MarkHot`; the example,
  examples/requeue, f21ca9f; the wording of what it checks is I8's). In the model: a reachable
  cycle in which an eventually val is false throughout is "can stay hot
  forever", reported with its prefix and cycle; weak fairness per action
  family is required (read-only actions loop everywhere), the subtle
  part: every action weakly fair, a stutter no step, `simulate.unfair`
  and `simulate.stops` opting out. In runs (cheap, evidence not proof): an unsettled run whose model
  states repeat with the val false is reported with the cycle's steps
  (retry storms and livelocks made legible).
- **P7. grist's obligations generated (S). Done** (docs/grist.md
  §3.3.1; HISTORY: "P7: grist's obligations generated"). Every processor
  and every Keyed call a processor uses implies an eventual val in the
  generated steps module (`<processor>EventuallyHandled`,
  `<call>EventuallyServed`), judged at a settle by the store's bindings
  beside the user's own; the examples' hand-written duplicates removed.
- **P8. Skills in pavlov's shape (S, with H6). Done** (HISTORY: "P8:
  skills in pavlov's shape"). Each SKILL.md at most about 80 lines with
  a "choose your path" list, steps, checklists, a results table and "run
  it, do not infer it"; `gotogether-migration` (bounded contexts,
  requirements with provenance, the model's `possible` and scenario
  claims, existing runs judged first); `gotogether-workflow` model first.
- **P9. grist apps explored in-process (L, after GR4).** The Go program
  checked against its rules on every input in small domains, with no
  quint process: commands' argument domains as Go enumerators, decisions
  and folds pure (Reads mostly enforces it), effect and call outcomes as
  the declared options, a canonical encoding of read models' states for
  keys, a state's restore a truncation of the log, each step judged by
  qeval on the generated model. Merging states is sound only when every
  block reads through read models.

Not taken: the program as its own model in place of Quint; one global
event selector as a runtime; snapshots of closure state (qenv replays
choices instead); safety as program events; livelock detection by
hashing Go state; a drawn state-space graph.

## grist (codename)

The grist track, alongside G: an event-sourced framework checked by its
model, designed in docs/grist.md (§8 for the slices). The maintainer's
aim (2026-10-27): every grist application instrumented automatically,
grist usable for stream publishers and consumers, gRPC services and
Kubernetes controllers as well as HTTP services, several services or
systems simulated together, and all of it onboarded with minimal work
by the user.

- **Slice 1, the thesis: done** (grist/, examples/counters, skills/grist;
  docs/HISTORY.md (grist §0.1)-§0.2).
- **Slice 2, the real tier: mostly done.** qsql's composite keys, ranges
  and RETURNING, the typed template function and `actors:` (core);
  sqlstore on PostgreSQL, SQLite and qsql, commits recorded from the
  step log (Relay), PostgreSQL in CI; the HTTP API, grist/client,
  grist.Main; AtMostOnce (docs/HISTORY.md (grist §0.3)-§0.6); the generator (§0.9,
  GR2). Left: gristflow, `blind` measured on the real tier, a durable relay cursor
  (with a lost-prefix record in qtrace), and the core item below.
- **A core item grist's measurements ask for** (validate,
  internal/replay): an eventual val is judged by a replay of its own, so
  a binding with one replays every run twice; judging it at the end of
  the fused run would save 7 s of 25 at counters' 999 steps
  (docs/grist.md §10, question 4).

Then, in order, each item reviewed before the next:

- **GR1. A trial by a naive agent (S).** wallet rebuilt on grist
  (examples/wallet-grist) by an agent given only skills/grist,
  docs/grist.md and wallet's README and model, not its code; scored on
  explorations green, wallet's mutants caught, how much of the model
  and binding was written by hand, and every place it guessed. Its
  frictions reorder what follows. Further trials as items land: payd,
  webjobs (with its Playwright journeys judged against the grist
  version), hello as a smoke test.
- **GR2. Onboarding with minimal work (M).** Built (docs/grist.md
  §0.9): the generator (`<app> model`, grist.Main: the Quint skeleton and
  both bindings from the catalog, the app writing its rules and marked
  invariants; the diff in CI, `gristtest.Generated`), `grist new` (an app
  green on first run, `TestNewIsGreen`), grist/gristtest's harnesses
  (`Mutants`, `Tier`), `NewDep` and `NewSite`; counters and wallet-grist
  generated (their bindings no longer hand-written); reviewed (§0.10).
  GR2b (§0.11): `gristtest.Exercised` (every block of the catalog taken,
  each command by each outcome; the summary's "never" list apart from
  grist's helpers), `Takes` by outcome, `gristsim.Config.Prologue`, a
  Delivers processor's Skip in its generated model, a refusal undeclared
  naming `grist.Refuses`. A site record ordered after the commit that
  triggered it: built in GR3 (docs/grist-instrumentation.md §3.3, slices 1-2).
  Left: gristflow
  (an append its reads do not cover, a rejection on an unconditioned
  read, a raw store call), and a trial agent building a new app from
  `grist new` to confirm it writes no binding by hand.
- **GR3. Automatic instrumentation everywhere (M).** Today every block's
  step is recorded by construction; the rest of an application's seams
  are not: outbound HTTP and gRPC calls, stream publishes and consumes,
  timers, and the app's own dependencies. Each adapter (GR5-GR7)
  records its seam with no annotation, the generator binds it, and an
  app that adds a dependency gets its steps without writing code;
  OTel spans of the same seams converted, for apps already traced.
  Designed in docs/grist-instrumentation.md (declared calls, both ends
  recorded, six slices). Slice 1 built (docs/HISTORY.md (grist §0.12)): declared
  calls on the simulated tier (`NewDependency`, `NewCall`, `Keyed`,
  `Uses`, `Call.Do`, `Handlers`, the site placed at its cause), gristsim
  serving each dependency, the generator writing both actions and a rule
  per processor and call; counters' bank ported (`wrongAmount` caught).
  Slice 2 built (§0.13): the default double (`grist.DefaultHandlers`,
  served by gristsim to a dependency given none), counters' and
  library's mailers and wallet-grist's ledger declared calls (no
  hand-written double, site or dedupe rule left in an example), `grist
  new`'s starter declaring a dependency; after its review, the site's
  action judges the request too and the default double stands in only
  for calls answering no fields. Slice 3 built (§0.14): the real tier
  over HTTP (`grist.OverHTTP`, `grist.ServeCalls` recording the site
  with its own recorder, `grist.ServeDependency` and `serve -dependency`
  in `grist.Main`, `gristtest.TierConfig.Serve` losing every fifth
  answer after applying it, `Local` one per tier, a killed node
  committing nothing), counters' `TestRealHTTP` retiring through a bank
  behind `ServeCalls` and judged by its real variant (the mailer a third
  party's), `payTwice` caught from the steps file, the lost answer with
  the node killed probed over HTTP. Next: slice 4 (lints for an app's
  own code).
  Candidate example apps for the declared calls (not built):
  - *payments*, calling a third-party PSP: `authorize` `Keyed`, at least
    once, refusing `declined`; `capture` at most once, reading the
    authorization's ref from the log (which needs an effect's success
    event built from its call's response: today `OnSuccess` is fixed
    before the call). Mutants: a capture of the wrong amount, a PSP
    without dedupe, a PSP replaying a fresh answer, a retrying SDK
    capturing twice.
  - *clinic*, an appointment scheduler: `book`, `cancel`, `reschedule`
    with several declared refusals, keyed read models by doctor and slot
    and by patient, an `Emits` processor, an at-least-once SMS reminder
    and an at-most-once no-show fee.
- **GR4. Several services simulated together (L).** gristsim composes
  several Apps, each with its own store, process and node(s), over
  qhttp/simgrpc/simlog networks with faults per service; a cross-service
  causal link recorded on both sides (the cause and the caller's command
  id carried with a call and an event); per-service bindings with
  `actors:` and a system model (Choreo for the protocol between
  services); multi-node runs of one service (Config.Nodes). Then
  integration between services: a service's event feed with remote
  consumer checkpoints, a packaged inbox/outbox, service identities
  (an actor that is a service, Internal blocks callable by named
  peers).
- **GR5. Stream publishers and consumers (M).** A processor that
  publishes to a stream (Kafka via franz-go, Kinesis, the simulated
  log examples/streams has) with the checkpoint and the publish's
  idempotence (producer ids, sequence) in one discipline; a consumer
  block reading a partitioned stream into commands with per-partition
  checkpoints; kfake/kinesisfake and simlog as the simulated tiers,
  reusing streams' binding lessons (the consumer the one place every
  tier can record).
- **GR6. gRPC services (M).** Commands and queries served over gRPC
  beside HTTP (a generated service from the catalog, or a handler for
  a hand-written proto), and called over gRPC by grist/client; simgrpc
  (examples/grpcledger) as the simulated tier, otelgrpc spans converted.
- **GR7. Kubernetes controllers (L).** A reconciler as a grist
  processor over a watched resource (desired state as events, the
  reconcile's effects at least once with their keys), on
  multicluster-runtime/controller-runtime; kubesim (fleetrollout) as the
  simulated tier, kwok and kind as real tiers, crossdb's and
  fleetrollout's lessons (closed-loop polls, status lag, real-clock
  libraries translated at the seam).
- **GR8. Sagas and timers (M).** Per-entity deadlines (a timer as a
  scheduled event, fired once per entity and slot), periodic tasks
  (Slice 3's), a saga/process-manager block with compensations; the
  model's library defs for them.
- **GR9. checkout as grist services (L).** examples/checkout-grist:
  order, inventory and payment as three grist services, the saga across
  them, reservations that expire, the payment gateway a qenv.Service
  double; checkout's claims (isolation levels, crashes, idempotency,
  the outbox) carried over, plus the system model's; a real tier with
  three processes. The proof that GR4 and GR8 compose.
- **GR10. Platform: Kubernetes-aware, never Kubernetes-bound (L).**
  Designed in docs/grist-platform.md. A core that serves well on any
  orchestrator without importing one (an identity from the environment,
  `/livez` `/readyz` `/metrics` on a side port, a drain schedule
  budgeted by the grace, files that change, `GOMEMLIMIT` from the
  cgroup); the cluster simulated as named qenv choices (a graceful
  terminate, a router whose endpoints lag, hosts and zones, a store that
  fails over or is restored, projected files, a delayed autoscaler in
  qload); `grist/kube` (no client-go) writing annotated manifests and
  the lint's rules; client-go only in executables of their own, reached
  by PATH as `gotogether nav` reaches gotogether-nav (`gotogether kube`:
  the kind tier, a canary gated by the model, the live lint; `<app>
  kube`: GR7's controller, a client of the app's HTTP API);
  sizing from the app's own service times with recovery as the
  criterion (`<app> sizing`). Correctness stays the store's: no
  Kubernetes Lease, ownership fenced in the log. Eleven slices; the
  first two fix today's recorder ids shared by every pod (pid 1) and
  30 s of drain inside the default 30 s grace. Slice 1 done (identity,
  probes, the drain: docs/HISTORY.md, grist §0.15), and slice 2
  (graceful termination, the lagging router, `gristsim.Cluster`,
  `gristtest.NoClientErrors`: grist §0.16), and slice 6 (sizing from
  the app's steps: `qmeta.Size`, `LimitFor`, `TimeoutFor`,
  `qload.Layered`, grist/gristload's report, `<app> sizing` a binary of
  its own: grist §0.17). After H7 (slice 4 needs
  its upcasting; slice 11 grist slice 3's leases). The eleventh review
  recommends slice 3 (failover and restore) next, beside slice 4 (I25;
  the maintainer's). The maintainer's
  decisions (2026-10-28, §10): no Kubernetes Lease, not even a hint; a
  45 s grace generated; a rewind fails closed until `<app> resume`; no
  app binary links client-go; sizing enters manifests only with
  `-sizing`; the canary an Argo Rollouts Job analysis or HTTPRoute
  weights, no controller of ours.
- **GR10b. A poller a daemon only while it waits on the store (S,
  qenv-level).** From P7's review, proposed, not built. A processor's
  poller calls `qenv.Daemon` once, for its whole life, so a settle
  treats it as quiet whether it waits for appends or holds a trigger
  mid-backoff or mid-call: a settle shorter than the work in hand
  (gristsim's minute against the backoff's 320 ms cap and a call's
  second; an effect whose tries outlast it) judges the obligations
  mid-retry and reads a correct app as violating `<p>EventuallyHandled`,
  and a handler that hangs reads as a violation where it is work never
  finished. Marking the poller a daemon only while it waits on the store
  (a daemon mark qenv lets a task take and drop, or a wait that is a
  daemon's while it lasts) makes the settle wait for work in hand, and a
  hung handler reads `Unsettled` naming its task, which
  `qtest.Settles()` judges. Any daemon that holds work between waits
  needs the same distinction, so the mark is qenv's, not grist's.
- **GR10c. `Settle` naming down processes (S, qenv-level).** From P7's
  review, proposed, not built. qenv counts no task of a down process as
  pending, so a plain `Settle` (not `SettleCore`, which faults outside
  its core by design) over a simulation that left a node down for good
  records a settle, and that node's triggers have their obligations
  judged, violated by right: a down node handles nothing. gristsim's
  supervisor restarts every node, so no grist run meets it today; a
  simulation of one's own does, and quorumkv's node down for good under
  `SettleCore` is the shape. Settle could name the processes down at the
  settle in its record (`qobs.Settled`) or its result, so a binding's
  `eventually` vals are judged knowing them, or the settle reported
  `Unsettled` while a process that has work is down.
- **Slice 3's rest**: gristpure, gristhegel, leases, screens and
  Datastar (upcasting and snapshots built in H7, rolling versions in
  I22; rewriting the index at boot dropped: a tag that changes is a new
  event type).

## Release (R5)

R1–R4 are done (HISTORY, "Release readiness (R)").

- **R5. Tags**, when the maintainer says. Then the pseudo-versions give
  way to them.
- **Left from R1.** Not checked here: golangci-lint's custom build of the
  plugin (no golangci-lint in this environment). The plugin's doc names the
  version to use.

## Ninth review: from a demo to a deployment (G)

From two reviews after E (HISTORY, "Ninth review (after E)"): a probe of
the newest code for wrong answers and its seams with the older layers,
and a newcomer recording an HTTP handler, validating its log, replaying
a manifest and shrinking a journey from the docs alone. Bugs first; each
item reviewed before the next. Sizes as in S.

- **G1. Silent wrong answers (S each). Done** (fdfeb6a, b888c48,
  25b2cf0, a3e4962, 9c85996, 6dc3cb4, 0939015, cd38956); the
  `RealTimeSlack` suspicion is G8's, webjobs' within settled.
- **G2. The CLI says what it did (S). Done** (a6735c4).
- **G3. Failures that can be followed (S). Done** (3548401 the strict-mode
  escape; 0d84012 the rest): a failing report's files are copied out of
  the test's TempDir and the report says so, a claim's unknown name says
  did-you-mean before any verdict, skipped steps' survivors say so, and
  the outcome error says to model the refusal before `skip`.
- **G4. A per-run recorder for an HTTP service (S). Done** (3548401:
  `qobshttp`, taught in gotogether-traces).
- **G5. Shrinking and replay outside `go test` (M). Done**: ShrinkActions'
  core is `qshrink`, MutateSteps' `validate.Checker.MutateSteps`, the
  validate command's `validate.Command` and `validate.Replay`; qtest
  keeps its wrappers, the CLI's evidence reads a view of qtest's format,
  and `go list -deps ./cmd/gotogether` has neither `testing` nor qenv.
  `gotogether shrink` reruns a driver command per candidate (SameFailure
  only: no `-keep`).
- **G6. API trim before R5 (S). Done** (f108cd1; 41b894a the review
  of G3–G5 before it): validate's CLI check is package validate/cli
  (`cli.Check`, `Judged` with `Close`, `Replay`, `Replayed{Got, Want,
  Same, Differs}`), which keeps OTLP and the OTel SDK out of qtest;
  `Report.Verdict` for `VerdictOf`, `NewManifest(RunRecord)` for the
  builders, `CurrentTools` for `ModuleVersion`/`QuintVersion`;
  `-require-finish` (`-require-settle` a deprecated alias; manifests
  `gotogether.run/2`); the claims of a run's end side by side in
  verdicts.md; SameFailure's fields from `binding.ObsComparisons`; lists
  and maps corrupted, the fields only removed named; qshrink's `Shown`
  in Result's words, `Keep.Why`, `Result`'s JSON; the evidence view in
  internal/evidence; `JudgeStepMutants` gone, `StepMutationReport`
  embedding validate's.
- **G7. Driver adapters, published (M). Done** (DESIGN §55): the
  request log's protocol (`gotogether.requests/1`) and package
  `qdriver`, read by `gotogether validate` (package input sniffs it);
  Bombadil's reader as `qdriver/bombadil` (webjobs' names in
  `journey.Bombadil`); adapters/playwright, a page's routed requests as
  a request log, tested on recorded events and end to end in webjobs'
  `TestPlaywright`.
- **G8. Across hosts (M).** A step's location separate from its
  incarnation (Left from E); a declared clock skew per pair of
  recorders in place of the fixed 1 ms; a collector-side watermark (an
  OTel processor emitting `quint.complete_through_unix_nano` after a
  flush). The eleventh review: the verdict names the 1 ms assumption
  first (I7, in progress); an adopter's production hosts differ by more
  under NTP, so the declared skew is what a production check needs; the
  collector processor is I46's.
- **G9. qsql for a real service's queries (M).** Inequalities, ORDER BY
  a non-key column with LIMIT, SKIP LOCKED, RETURNING, COUNT, a
  transaction's own isolation; each checked against SQLite
  differentially. The outbox claim and the conditional debit need them.
  Ranges on a key column, ORDER BY the key with LIMIT, RETURNING and
  upserts are done (grist's slice 2, against SQLite in examples/payd);
  ranges on other columns, SKIP LOCKED, COUNT and a transaction's own
  isolation are open.
- **G10. The reviewers' example: examples/shopfront (M-L).** A Go
  storefront on a real database, its steps through an OTel collector
  that drops some, driven by Playwright from another host and by a Go
  client, Toxiproxy between it and the database. Teaches: G7's adapter,
  ordering across hosts by a propagated request id, `-complete-through`
  from the collector (dropped, uncollected, defect), a pseudonymized
  manifest replayed in CI, ShrinkActions with a real Resetter (and a
  mutant that keeps a cache across Reset: what the reset checks catch
  and what they cannot), qnemesis faults remade in qenv, a strict
  nightly. Proves G1's gaps, a killed process's tail as a loss, and a
  manifest's replay on another machine.
- **G11. Skills as installed (S).** Links into the repository rewritten
  on install; `skills install -update`; a test that installs into a
  temporary directory and resolves every link; gotogether-faults split
  (about 4k words). (AGENTS.md's layout and the examples' rows were
  completed with I34.)
- **G12. The cost of a -short verdict (S-M).** validate 1370 CPU s and
  qtest 440, mostly quint: conformance under -short on internal/qeval,
  quint confirming failures and a sample of passes, all of it nightly.
  H1 did most of it (the Go evaluator decides passing runs); grist's
  -short tier and time budget are I15's.

## Left from X

The X track (what a green suite proves) is done and in HISTORY ("What a
green suite proves (X)"). What it left:
- **X4. Real-time order.**
  - **Left.** Every order tried replays from the start, so a few hundred
    steps take minutes. A `quint repl` session per instance was tried and
    was slower (DESIGN §9.7: quint's front end costs 10-40 ms per input);
    batches now use the TypeScript backend. Starting from a state needs
    an IR built in Go for the Rust evaluator, a project of its own. `//quint:action` could note the invocation on
    entry. grpcledger's spans carry their start already; its convert
    rules order by its own index, so nothing there needed it.
- **X5. The doubles' contracts.**
  - **Left.** The services' contracts; the history checker public, for a
    real database tier's recorded transactions.

Deferred: differential runs (the model is already the common oracle),
DPOR. Online validation is decided (DESIGN §50: windows judged as logs
that lost their head).

## Left from Y

The Y track (Y1–Y12) is done and in HISTORY ("Fifth review: what X left
(Y)").

Consider: the nightly's confidence per claim ((1−p̂)ⁿ for Some, the
binomial tail for InAtLeast), and fleetrollout, edgepublish and gosim in
it; simulate's and evidence's logic as library calls
(`validate.Simulate`, `qtest.Suite`); a `Machine` grouping processes for
disks, clocks and partitions; bare expectations' arity (`Conforms` every
run, the rest some) and contradictory pairs; "run 3" not "schedule 3";
`Reaches` by prefix as Rates are.

Taken into the I track: `validate.Simulate` (I17), bare expectations'
arity (I16), the nightly's confidence per claim (I9).

## Eighth review: whole product (S)

From five reviews after T (HISTORY, "Eighth review (after T)"): a probe
for wrong answers, architecture, a newcomer building a leader-elected
counter, capabilities and lessons, and raftkv's own. Bugs first; each
item reviewed before the next. Sizes: S under a week, M two to four, L
more.

- **S1. Silent wrong answers (S each). Done**, but one suspicion: None
  over runs with no verdict (mutants Undecided); claim names the
  binding's; a hidden search cut by its depth; a marked report writing
  into the shared cache; a placed pending call's process; within on
  intervals; ResolvePending's silent fallback; Checker.Key's mode and
  backend; a range counting down refused; raftkv's lost snapshots. Left:
  raftkv may leave a `lead` unrecorded when a vote and a higher term
  arrive in one batch (not reproduced: drain Ready per vote response).
- **S2. The planner's hot path (S), then measure again.** First part
  done: templates parsed once per process, functions bound once per plan,
  a planner per search, and candidates judged from their longest accepted
  prefix's state (raftkv's seed 1, 50 s to 24 s to 15 s; the evaluator
  out of the profile's top). Left: planning (31%) and Reconstruct (11%)
  are still per candidate and linear in it: the search handing the judge
  each candidate's parent, so a candidate costs its new step. A profile of
  raftkv's 200-operation history: 53% of the CPU in `PlanWithin`, 34% in
  template parsing per candidate, 8% in the evaluator; the "extend from a
  held state" plan targets the wrong layer. Parse once per binding,
  intern once per search, render a step once per process numbering. A
  `BenchmarkLinearize200` in the time budget. Success: 58 s to under 25
  s, the verdict unchanged. Then, if replay dominates, extending a
  prefix from its held state (M).
- **S3. One way to judge (M), before tags. Done.** `Checker.Judge(ctx,
  steps, SearchOptions)` and `JudgeBatch` (a `BatchCheck` hook for
  qtest's verdict cache) search, check, confirm, check again and mark,
  on copies of the check's reports; the CLI, Record, Validate and
  explorations call them. Search, Confirm, Mark and ConfirmByCheck
  unexported; the checker's modes one value (`mode`, `within` renamed
  `history`); `decide` takes and returns a `found`.
- **S4. The API before it freezes (M). Done**, but EvalModule: the
  replay modules (RenderModule, RenderBatch, ModuleSpec, Generated,
  Generate) moved to internal/replay; PlanWithin gone (Planner); qtrace's
  four Linearize variants one (`Linearize(steps, LinearizeOptions,
  OrderCheck) (*Linearization, error)`: a typed verdict, the check's
  error returned, no panic through the callback); `qtest.Search` is
  `qtest.Extent` (`Evidence.Exhaust`); qtest's re-exported budgets
  dropped; ignored-option refusals keyed by an identity a misspelling
  cannot compile. Left: `binding.EvalModule` stays exported, since it
  shares the template splitting the planner uses; it moves when
  planning's helpers are split from binding's YAML (a package of its
  own, both internal), or stays as the documented module the Go
  evaluator reads.
- **S5. The suite (S). Done.** The evaluator mode an Options field (the
  variable its default) and validate's tests parallel; a checker's own
  scratch directory, removed by Close; TestCheckBatchMatchesCheck smaller
  under -short; the core packages and wallet, quorumkv, raftkv in the
  time budget; AGENTS' test times. validate's -short suite: about 300 s
  to 140 s.
- **S6. The verdict-path matrix (M). Done.** validate's
  TestVerdictMatrix: conformance, invariant, eventual, within, hidden and
  pending, each a passing and a failing run that differ only in what it
  judges, by Check, shadow, CheckBatch, Judge (with quint alone, and
  linearized) and the key; a consumer's n/a says why, and every field of
  binding.Binding is classified, so a new kind of verdict needs its row.
  The CLI's column (cmd's TestValidateVerdictMatrix, through a step log)
  and the case checker's (qhegel's, its caches in turn); a Hegel property
  in internal/hegelprops, equal keys equal verdicts. Re-planted, they
  catch a key without times, a batch without within, an evaluator
  without eventual vals, the CLI dropping -resolve-pending, and the case
  checker reusing a prefix past an eventual val or keying within without
  times. It found checks sharing a Dir taking each other's verdicts.
- **S7. A newcomer's friction (S each). Done.** `Network.OnHeal`;
  `lint -binding a,b` (LintSources); `Model.Helpers` out of scaffold and
  lint's lists; a duplicated request named in a failing run's report;
  the skills on recording heals, two models of one system, handlers'
  state across a restart and recording from OnCrash, Retry's commit,
  pending calls and the budget, the delay options, the default actor,
  the CLI from a replace module.
- **S8. The toolkit's own mutation bar (M). Done.** qtrace 55 to 16
  survivors of 236, qenv 286 to 21 of 892, every one left read and named
  in the nightly's matrix (its bar): equivalents, a heuristic's
  tie-break, and two releases only gosim's cooperative runtime can show
  (a self-crash test in examples/gosim would). Found on the way: the
  search asked complete orders again (memoized now), a Script's hint
  suggesting the family it rejected. The nightly mutates qenv with four
  jobs.
- **S9. raftkv's catch-up (S). Done.** cluster.qnt records each node's
  state machine (`boot`, `apply`); a node that boots behind owes the
  others' highest index, `within 1s after behind: caughtUp`, with the
  clients writing unrecorded (`Config.Load`) so the logs compact. A
  scenario keeps a follower down past a compaction and loses its
  snapshot by a cut: the faithful node catches up in all 20 runs, and
  `NoSnapshotReport` (no ReportSnapshot, no retry) never does in 18,
  though the cluster settles with a leader. Random loss alone lost a
  snapshot in 3 of 40 (a new leader resends one).
- **S10. A per-PR tier with stated power (S). Done.** Every example's
  -short runs per push in under 60 s on CI (checkout 199 s to 49 s,
  raftkv 54 s, twophase 42 s), each mutant's claim missed there at most
  5% of the time by its exploration's own rate, stated in the test's doc
  comment; the full tests run nightly over fresh seeds. ci/test-times.txt
  holds the -short times.
- **S11. Partial logs (M), then online validation decided. Done**
  (DESIGN §50): a lost tail leaves later failures, eventual vals and
  pending calls unjudged; a lost head starts from a `snapshot:` step or
  the states a `resume:` action admits (searched by the evaluator, quint
  confirming); `-lost-prefix`, `-lost-suffix`; kvstore rotates its log
  with a snapshot at each head; `-follow` not built. Left: a snapshot in
  the middle of a log is skipped, not checked against the replayed
  state; a snapshot argument cannot be a map of records; a resume past
  20000 states, or a failure past 200 confirmed by quint alone.
- **S12. Adoption (L). Done** (DESIGN §51): `qenv/qsql` (database/sql
  over a Store, a one-table SQL subset with PostgreSQL's SQLSTATEs),
  `qenv/qhttp` (RoundTripper and Handler over a Service), `Sim.Hang`;
  examples/payd, a standard-library payments service, unmodified,
  explored and run on SQLite, three overlay mutants caught. Left: pool
  limits and goroutine-starting handlers (qgosim's); a query outside a
  transaction unwound inside the driver is untested; per-transaction
  isolation levels; a snapshot argument of records (S11).
- **S13. A real deployment's nemesis (M–L). Done** (DESIGN §52):
  `qnemesis` (kill, pause, toxiproxy cut, latency, loss; faults as
  steps; a journal re-made as a qenv Script), `qtest.Runs`, raftkv's
  real tier (TCP, bbolt WAL, cmd/raftkv-node) under it: NoRequestID and
  SlowElection caught, per-PR and nightly. Left: NoPersistVote and
  NoSnapshotReport need schedules aimed at an election (a crash after a
  vote) and a snapshot in flight; containers, iptables partitions and
  failpoints; a lost tail judged by searching the lost steps (as hidden
  actions of the killed incarnation) instead of no verdict; qhttp's plain
  context deadlines would be exact with qenv's wake time.

## Left from E

The E track (E1–E8, a run gotogether did not drive) is done: DESIGN §54
(§53 for E7), HISTORY ("E: a run gotogether did not drive"). What it
left:
- **A step's process is per actor.** Two recorders in one actor (a page
  and its server) must share one, or Reconstruct reports an overlap, so
  a client and its server cannot be one actor's distinct locations, and
  a crash's semantics attach to the shared name (webjobs, raftkv share
  one by necessity). Separate location from incarnation.
- **Strict mode and evidence (E2).** The nightly is not strict (its
  fresh-seed runs include the unwoven mutation tests); evidence a
  renamed test left in a reused directory still counts; Hegel checks
  write no evidence.
- **Manifests (E4).** No option sanitizes a manifest itself (sanitize
  the log, then validate it with -manifest).
- **webjobs (E5).** Bombadil's reproduction diverges on a
  timing-dependent page; one host's wall clock orders the page and the
  server.
- **Observation mutants (E6).** A mutant per pair of mutations; retime
  earlier; a corruption of the outcome and the error text.
- **Absence (E7).** A collector-side writer (an OTel exporter that emits
  the watermark record after a flush); a pending call's window (a
  caller's timeout).
- **Shrinking (E8).** Bombadil's `--reproduce` as the rerun (webjobs
  reruns through its Go client, from `bombadiltrace.Actions`);
  candidates judged in batches (each is a quint check, a second or
  two). State the actions leave without a step of its own (a counter
  Reset forgets) passes every check of the reset contract: only a fresh
  environment per rerun is safe, and nothing yet detects the other.

## Left from T

- One memoizing checker shared by qtest and qhegel, across an
  exploration's runs (T2b).
- A claim judged in its history's instance and the run's check in the
  order's (T2d): the same verdict only for a model symmetric in interned
  identifiers.
- One goroutine lookup per step (T3).
- A nightly of every example under `GOTOGETHER_ALL_FAULTS` (T5).
- `Exploration.finish` split into stages; a corpus for Exhaust;
  `Script.Misnamed` against the run's names (T6).
- Pending calls: a partition with pending calls and no steps places
  nothing; no pending effect after an inferred restart; their arguments
  not interned with the history's (T7a).
- Hidden actions confirmed with quint (a proof of "no path"); hidden raft
  messages (T7e, T8).
- wallet's WallClockDedupe; the optional real database (T9).

## Dropped on purpose

- X7's jobqueue journal on a Disk: jobqueue is not on qenv (the eighth
  review).
- REVIEW S2's embeddable `qenv.Layer`: `qenv.As` and Wrapper cover the
  chain.
- REVIEW P2's `Together` and `qhegel.Want`: `Violation.Expect`
  superseded them.
- Renaming the CLI's subcommands.
- Rewriting history to drop a5cc809's 15 MB test binary: .git is 26 MB,
  and the docs cite commit hashes.
