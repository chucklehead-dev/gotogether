# Roadmap

What is open. What is done (the A–D, U, V, W, R1–R4, X, Y, Z, T and E tracks,
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

Order: G1 → G2 → G3 → G4 → G5 → G6 → G7 → G8 → G9 → G10 → G11 → G12; the
items left from S, X, Y, T and E alongside; R5 when the maintainer says.

## grist (codename)

The grist track, alongside G: an event-sourced framework checked by its
model, designed in docs/grist.md (§8 for the slices). The maintainer's
aim (2026-10-27): every grist application instrumented automatically,
grist usable for stream publishers and consumers, gRPC services and
Kubernetes controllers as well as HTTP services, several services or
systems simulated together, and all of it onboarded with minimal work
by the user.

- **Slice 1, the thesis: done** (grist/, examples/counters, skills/grist;
  docs/grist.md §0.1-§0.2).
- **Slice 2, the real tier: mostly done.** qsql's composite keys, ranges
  and RETURNING, the typed template function and `actors:` (core);
  sqlstore on PostgreSQL, SQLite and qsql, commits recorded from the
  step log (Relay), PostgreSQL in CI; the HTTP API, grist/client,
  grist.Main; AtMostOnce (docs/grist.md §0.3-§0.6); the generator (§0.9,
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
  recorded, six slices). Slice 1 built (docs/grist.md §0.12): declared
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
  for calls answering no fields. Next: slice 3 (the real tier over HTTP:
  `OverHTTP`, `ServeCalls`).
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
- **Slice 3's rest**: upcasting, snapshots, gristpure, gristhegel,
  leases, screens and Datastar (the read models' cost on long histories
  first: snapshots before a second real app).

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
  flush).
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
  (about 4k words); AGENTS.md's layout and README's example rows
  complete (payd, webjobs, qnemesis, sktrace) and shorter.
- **G12. The cost of a -short verdict (S-M).** validate 1370 CPU s and
  qtest 440, mostly quint: conformance under -short on internal/qeval,
  quint confirming failures and a sample of passes, all of it nightly.

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
