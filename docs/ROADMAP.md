# Roadmap

What is open. What is done (the A–D, U, V, W, R1–R4, X, Y and Z tracks,
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

Order: T1 → T2a–b → T3 → T4 → T5 → T2c → T6 → T9 → T2d → T7, T8; the items
left from X and Y alongside; R5 when the maintainer says.

## Release (R5)

R1–R4 are done (HISTORY, "Release readiness (R)").

- **R5. Tags**, when the maintainer says. Then the pseudo-versions give
  way to them.
- **Left from R1.** Not checked here: golangci-lint's custom build of the
  plugin (no golangci-lint in this environment). The plugin's doc names the
  version to use.

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
- **X7. Storage faults and partitions.**
  - **Left.** jobqueue's journal on a Disk (jobqueue is not on qenv).

Deferred: online streaming verification (resuming quint from a checkpoint
is unsound for nondeterministic models; `gotogether validate -follow` over
rolling windows from `init` if wanted), differential runs (the model is
already the common oracle), DPOR.

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

## Seventh review: whole product (T)

From four reviews after Z (HISTORY, "Seventh review"). In order, bugs
first; each item reviewed against its bullet before the next. Sizes: S
under a week, M two to four, L more.

- **T1. Silent wrong answers (S each).**
  - Real time from what the recorder knows: a completion takes its
    invocation record's time (`"invoked"` becomes an override), steps of
    one recorder are ordered exactly by seq (a completion after another's
    invocation record), no slack between a simulation's steps, and a
    linearized actor no step of which has an invocation time is warned
    of. quorumkv stops passing `"invoked"` from the process's clock.
  - A linearization cut by its budget is no verdict for the run's
    invariants and eventual vals too, not only its conformance.
  - Eventual vals judged in no run say so: an issue on each unsettled
    actor of a binding with `eventually:`, and a claim that would hold
    for nothing (Conforms, None(Violates(eventually ...))) fails when no
    run settled.
  - ResolvePending places a call's effect before the first settle after
    its invocation (the system was quiet there).
  - Faults after Settle returns: they stay off (a probe or an audit after
    it runs on the settled system), said in Settle's doc and the faults
    skill; `Sim.ResumeFaults` starts a faulty phase again.
  - A settle settles a trace only if no other recorder recorded a step
    later in time.
  - The complete search's beam is the budget's, and a cut names its real
    limit.
  - qhistory: a transaction's reads of its own appends in their order;
    garbage in any read, and no "G1b of transaction 0".
  - Among orders that conform, one whose invariants hold is preferred (a
    mutant must not look caught by the order picked).
  - The warning for a step invoked after it completed names the fix
    (Invoke and Done, no "invoked"; Sim.Now). A virtual `qenv.Now` on a
    simulation's creating goroutine was tried and reverted: that
    goroutine's Sleep and Since stay real, and a deadline loop there hung.
- **T2. The cost of a verdict.** a and b done: verdicts-only checks for
  the searches, an exploration's searched side by side, candidates
  memoized by prefix (quorumkv's Repick 297 s to 138 s); left of b, one
  memoizing checker shared with qhegel and across an exploration's runs.
  - **a (S).** A conformance-only search mode in validate (no ITF, the
    TypeScript batch, no detail phase), the per-run searches of an
    exploration in parallel, the complete search given what is left of
    the budget.
  - **b (S–M).** Candidates memoized in a prefix trie (an extension of a
    rejected prefix decided without quint), the complete search starting
    from the guided one's accepted prefixes, one memoizing checker for
    qtest and qhegel.
  - **c (M). Done** (merging prefixes of one state, from the ITF of the
    candidates quint checks whole; five commuting deposits' proof in 146
    orders, not 456). Left: extending a prefix from its state as a
    literal (each candidate replays from the start), states for the
    prefixes the memo accepts (kept apart now), merging in the
    invariants' second search (its checker writes no ITF), ITF only for
    the complete search's batches, and a chunk's stale ITF removed before
    each call. Reviewed: the merge's state is more than the model's when
    the binding infers restarts (the last process), and candidates are
    planned within their history (PlanWithin).
  - **d (L). Done** (DESIGN §45): `internal/qeval` over quint's IR of the
    binding's evaluation module, judging Linearize's candidates, quint
    confirming each search's conclusion; checked against quint on every
    model of the repository and a table of builtins at their edges on
    both backends; reviewed twice (the second found three silent wrong
    verdicts, now fixed: a search that finds no order is quint's alone).
    quorumkv's faithful exploration 20 s to 13 s. A failing search is
    confirmed from its proof in one quint batch (`qtrace.LinearizeProof`):
    the contended mutants 214 s to 62 s. An order (or a resolution) the
    evaluator finds is confirmed by the run's own check
    (`SearchOptions.ConfirmByCheck`, `Search.Confirm`), and
    ResolvePending's placements are judged by the evaluator: the
    contended mutants to 43 s, the faithful exploration to 4 s, wallet's
    pending transfers 44 s to 12 s.
    Also: LinearizeByKey's partitions side by side, and a history real
    time orders alone not searched (`qtrace.ForcedOrder`). Left: each candidate extended
    from its prefix's held state rather than replayed from init (WGL at
    Porcupine's speed); a nightly Hegel property drawing
    states and arguments against quint, and a shadow mode running both;
    quint-confirmed single steps in validate's shape for every binding.
- **T3. The searches in the right layer (M). Done:** validate's
  `Checker.Search` and `Search.Mark`, the CLI's `-linearize`,
  `-linearize-key`, `-resolve-pending`; `quint.assumed` in step logs,
  `InvariantResult.Eventually`, `Run.Recorded`, the chooser protocols on
  Chooser's doc. Kept: `qtrace.Linearize` public (validate calls it), the
  three Linearize options (each reads as what it does; the refusal names
  the conflict with ResolvePending). Left: one goroutine lookup per step.
  As planned: `validate.Checker.Search`
  (Linearize and ResolvePending as generators over one candidate
  engine), called by qtest and by the CLI (`validate -linearize[=key]`);
  `qtrace.Linearize` unexported; one re-sequencing helper; `Run.Recorded`
  beside the judged `Run.Steps`; `Ambiguous` written to step logs;
  `InvariantResult.Eventually`; one `Linearize` option (by key, budget)
  and `ResolvePending(budget)`; the scheduler's task and nemesis chooser
  protocols exported or documented on Chooser; one goroutine lookup per
  step.
- **T4. A newcomer's path to Z7 and Z8 (S each). Done**, but: temporary
  directories (a failing exploration's are kept on purpose, for its
  reproduce line; steered runs' choices pile up under the temp dir).
  As planned: Start-table rows for
  linearizability, liveness and convergence; verdicts.md on Pending,
  Linearize and "no verdict (linearize budget)"; quorumkv's README with
  the code a user copies, PCT's arguments and PCT against Steer; scaffold
  writes a Settle when the binding has `eventually:`; a Mismatch of a
  client history in recorded order suggests Linearize; a run's
  scheduling steps reported (PCT's Steps); claim wording ("some run
  showing a conforming run that violates"); pending and settle counts in
  summaries; `GOTOGETHER_KEEP` keeping step logs; a failure's step log
  as recorded, its judged order beside it; temporary directories
  removed; quint's reserved names in the model skill; the doc drift the
  review listed (explorations linearize, refusal texts, DESIGN's X4).
- **T5. The lessons as structure (S–M). Done**, reviewed: the toolkit's
  own mutation testing (`ci/mutate-core.sh`, `GOTOGETHER_MUTANT`; the
  nightly holds qhistory and qsweep at 0 survivors, internal/depgraph at
  1 equivalent); brute-force comparisons for Linearize (registers,
  invocation records, domain clocks), ResolvePending's placements,
  Exhaust's schedules, the isolation classifier and Cover, each ablated,
  and Linearize against Porcupine in internal/hegelprops; one isolation
  classifier for qhistory and `GOTOGETHER_CHECK` (G-nonadjacent at SI,
  linear per commit); package-level state stating its scope; the
  simulation lint over the core; claim power (with the rate's lower
  bound) and the examples' seeds raised to it; `GOTOGETHER_ALL_FAULTS`;
  a CI time budget; the rule in AGENTS. What it found: Linearize's cut
  search read as a proof, ResolvePending's settle bound and slack,
  qweave dropping an argument, qmut recursing into qenv. Left:
  - a nightly that runs every example under `GOTOGETHER_ALL_FAULTS`
    (Exhausts bounded first), each changed verdict a fix or a reasoned
    Without. Its first run (the examples' short suites) found a healer
    keeping runs unsettled (fixed), hello's client giving up within a
    partition (its model has none: a Without to give), and Exhausts out
    of MaxRuns in hello and lease;
  - qenv in the mutation nightly with `GOTOGETHER_CHECK=1` and its bar
    (a first run without it: 288 of 895 survive, in sim.go 50,
    internal/profile 44, store.go 34, native.go 31, keyed.go 19,
    queue.go 14, the rest under 15 a file), and a bar for qtrace's
    guided-search survivors (56 of 167 survive now, held there as a
    ratchet);
  - the brute forces as Hegel properties, their sizes widened nightly;
- **T6. The exploration engine (M), before tags. Done** (DESIGN §44):
  sources (seeds, corpus, a file, steered, schedules) feeding one engine
  for Explore and Exhaust, whose nominal run now has the determinism
  check; a run's origin in one field; what only a System method sets an
  internal spec; fault names known per run; reviewed (HISTORY, "T6
  review"). Left: finish split into its stages as functions of the
  engine; a corpus for Exhaust (schedules are their own regression);
  `Script.Misnamed` against the run's names, not the process's (a
  nemesis only another test registered passes it); a woven kind's
  options still the process's (static at init); under gosim, kinds a
  run declared but never chose are unknown to it.
- **T7. Capabilities (after T2d).** Indeterminate calls searched within
  the linearization (Jepsen's :info) and a visual of a failing history;
  hidden actions and partial logs in trace validation; bounded-response
  liveness during a run (`within:` in virtual time); mixed-version and
  upgrade runs (`Process.RestartAs`); Hegel parallel state machines that
  linearize; Exhaust with sleep-set DPOR under a declared ownership mode;
  a Script against a real deployment and an Antithesis assertion sink;
  online validation over the set of reachable states; Steer branching
  from where a feature appeared, features as counts; qhistory's strict
  serializability and rw-registers; the determinism lint (map ranges,
  math/rand, multi-case select).
- **T9. An example: wallet (M, the T5 review's). Done** (examples/wallet):
  transfers on a `qenv.Store` with request-id dedupe, an outbox to a
  ledger, retrying clients over a `qenv.Service`, a credit line two
  accounts share; Exhaust (NoDedupe within one fault), isolation levels
  (ReadThenWrite at read committed, WriteSkew at SI, qhistory naming it
  G2 from the store's history), NoOutbox after a settle, ReplicaRead
  linearized per account, ResolvePending for crashed clients,
  `GOTOGETHER_ALL_FAULTS`. Its first faithful version applied a
  duplicated request twice at read committed (the request's record read
  unlocked), now the UnlockedRecord mutant. Reviewed (HISTORY, "T9
  review"): invalid requests are refused in the model and the bank
  (NoValidation), posts carry their moves, commits of unknown outcome are
  on (and Unknown in the history), NoOutbox is found by Exhaust. Left:
  WallClockDedupe (a dedupe window on the process's clock, broken only
  by clock faults); the optional real database. What it taught the core:
  - LinearizeByKey cost three times Linearize on its small per-account
    history (28 s against 9 s for five runs): its partitions were searched
    one after another, each paying a second quint call for the
    invariants, and the probe makes one partition per account, each a
    quint call for one step. Now partitions are searched side by side (28
    to 25 s: an exploration's runs already kept the CPUs busy) and a
    partition real time orders alone is not searched (`qtrace.ForcedOrder`:
    to 15 s). Next: check invariants in the first search, batch the
    partitions' candidates into one quint process.
  - PCT at depth 2 found the wide write-skew window in 1 run of 40,
    uniform seeds in 39; but its `steps` (400) was mis-sized for a run of
    about 60 choices, so the change point mostly fell past the end (sized
    to the run: 16 to 18 of 40). Default PCT's steps to the nominal run's
    choice count, which an exploration knows from its determinism run, and
    say in the skill to size it.
  - `GOTOGETHER_ALL_FAULTS` turns on every nemesis family but not a
    store's commit faults (`Store.CommitFaults`, opt-in per store): "every
    fault family" leaves out the one a dedupe-by-id service exists for.
- **T8. An example: raftkv (M, after T2d).** etcd's raft RawNode as a
  three-node KV on qenv; transfers across keys (no partition helps);
  hundreds of operations linearized; a lease-read mutant under clock
  drift, ReadOnlySafe conforming; a retried CAS without a request id
  (indeterminate calls); election after a heal as a bounded-liveness
  property.

## Dropped on purpose

- REVIEW S2's embeddable `qenv.Layer`: `qenv.As` and Wrapper cover the
  chain.
- REVIEW P2's `Together` and `qhegel.Want`: `Violation.Expect`
  superseded them.
- Renaming the CLI's subcommands.
- Rewriting history to drop a5cc809's 15 MB test binary: .git is 26 MB,
  and the docs cite commit hashes.
