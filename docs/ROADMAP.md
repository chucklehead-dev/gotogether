# Roadmap

What is open. What is done (the A–D, U, V, W, R1–R4, X and Y tracks,
Z1–Z4, the move to gotogether) and the reviews that shaped it are in
docs/HISTORY.md; why the code is the way it is, in DESIGN.md.

Standing decisions, with the maintainer:
- Nothing is released yet. Tags wait for the maintainer (R5).
- Crashes are considered at every scheduling step (`NemesisEvery` is
  opt-in); `qenv.Service`'s crash window is on by default.
- The global recorder is opt-in (`qtest.Global()`).
- qhegel becomes a module of its own before any tag, so the core needs
  only OTel and yaml.v3.
- MIT license.

Order: Z5 → Z8 (in the Z track's order below); the items left from X and
Y alongside; R5 when the maintainer says.

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

## Sixth review: whole product (Z)

From four reviews of the whole product after Y (HISTORY, "Sixth
review"): architecture and maintainability, correctness, a newcomer's
usability and docs, missing features and lessons learned. Decided with
the maintainer: all of Z1–Z8, breaking the API freely before tags. In
order, bugs first; each item reviewed against its bullet before the
next.

- **Z1–Z4. Done** (silent wrong answers, reports a user can act on, the
  newcomer's path, the API before tags): HISTORY, "Sixth review: whole
  product (Z1–Z4)".
- **Z5. Maintainability (S–M). Done:** an escaped step fails its own
  run (found through its goroutine's creator), so qtest's tests run in
  parallel (63s → 37s short); CI's test job a matrix, a tidy check, a
  workspace job (whose first run found qweave weaving nothing outside the
  go.work in use, now an error); done items and AGENTS' backstories in
  HISTORY, its commands and one area's gotchas in docs/agents/ (5.3k
  words to 3.8k); the README's package table checked against api/*.txt;
  doc comments' Go under the docs gate. Later: KnownFault's registry per
  run (it is the process's: under parallel tests one test's declarations
  can make another's misspelled Rates key look known); one exploration
  engine fed by run sources (Explore and Exhaust repeat their setup,
  Exploration has seven mode flags), one verdict method per claim, one
  memoizing checker for qtest and qhegel, qload's two recovery worlds
  made one.
- **Z6. A more faithful environment (S each). Done:** a request may
  arrive late (`rpc.<name>.delay`: a late original after its retry),
  which found lease's lock without fencing broken with true clocks and
  crossdb's cloud leaking a database to a create that landed after the
  delete (it now refuses a request past its deadline); pauses and gray
  failures (`Sim.Stalls`); a full disk, a failed fsync (Linux's: its
  writes lost though a later fsync succeeds), a corrupt read; a second
  double of one name panics; an exploration runs its first seed twice
  and fails on a difference; the doubles shown to reorder, redeliver and
  read skew where their contracts allow. Left: a lint for map ranges,
  math/rand and multi-case select in simulated code (the self-check
  catches what they do, not where).
- **Z7. Liveness (S, then M).** Done: Settle reports whether it
  settled (`*Unsettled`, a stuck settle included; `qtest.Settles()`)
  and records a settle in the trace (`qobs.Settled` for a real
  deployment); a binding's `eventually` vals, judged in the last state
  of a trace that recorded a settle, by every check (not in virtual time
  across a run: a val of the last state); a fair settle (tasks in turn);
  `Sim.SettleCore` (faults go on outside a core, VOPR's liveness mode);
  calls recorded at invocation (`qobs.Invoke`, `Call.Done`, pending
  calls in reports, `qtest.Completes()`). `Run(d)` ending at its horizon
  with tasks waiting judges nothing, on purpose: a run's faulty phase
  ends there, and only Settle asks for quiet. Left: `//quint:action`
  advice recording at entry (Invoke) as well as on return; an example
  under SettleCore (a quorum whose convergence is stated of the core);
  eventual properties in connect's MBT; what a pending call may have
  done is Z8's ambiguous steps.
- **Z8. Search and scale (M–L). Done:** PCT as a chooser
  (`qenv.NewPCT`, tasks picked by id, recorded as index choices);
  Linearize per key (`qtest.LinearizeByKey`) and a complete search after
  the guided one, candidates judged with the run's settle; explorations
  run Linearize, steps in a simulation carrying its virtual time; the
  history checker public for real databases (`qhistory`: Elle's
  list-append workload, Adya's anomalies, G-nonadjacent included);
  exploration steered by new steps, observed states, faults and code
  blocks (`ExploreConfig.Steer`); pending calls resolved
  (`qtest.ResolvePending`); examples/quorumkv, which uses Z7's and Z8's
  features and found two of the toolkit's misses (above) and its own
  first bug. **Deferred, for the next review:** footprint-based
  reduction for Exhaust, sound only if tasks share state solely through
  the doubles (Go code between seams touches any memory, a test's
  closures included): it needs an ownership declaration and a DPOR with
  sleep sets, or a passing Exhaust would prove less than it says;
  resolution searched together with linearization (Jepsen's :info in a
  concurrent history), and calls placed at one gap in any order; hidden
  actions (an effect no one recorded); Steer's features as counts (a
  second fault of a kind is never new), cuts at the choice where a new
  feature appeared, and unbounded observations (versions) that make
  every run new; the CLI's validate linearizing (quorumkv's demo runs its
  tests). Online validation stays deferred.

## Dropped on purpose

- REVIEW S2's embeddable `qenv.Layer`: `qenv.As` and Wrapper cover the
  chain.
- REVIEW P2's `Together` and `qhegel.Want`: `Violation.Expect`
  superseded them.
- Renaming the CLI's subcommands.
- Rewriting history to drop a5cc809's 15 MB test binary: .git is 26 MB,
  and the docs cite commit hashes.
