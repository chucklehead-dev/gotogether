# Performance: where a verdict's time goes, and what was done about it

A review of the whole project's cost (core, qhegel, qweave, qanalysis,
qgosim, grist, the CLI, the examples as workloads) on 2026-10-28,
measured, not guessed. Findings are ranked by CPU or wall time saved per
unit of effort; each gives the measurement, the cause, the fix, its risk
and whether it changes behaviour or an API. *Done* items name their
commit.

## How it was measured

- **Every quint process, counted.** A wrapper named `quint` first on PATH
  ran the real one and logged each process's subcommand, module, wall
  and CPU time (user + sys of it and its children: node and the Rust
  evaluator) and the process that started it. `internal/cputime`'s
  per-package CPU (CI's `ci/test-times.txt` lines) says how much a
  package costs; the wrapper says which quint processes make it up.
- **Go's side**: `go test -cpuprofile`, `pprof -top`/`-list`/`-cum`.
- **CI**: per-job and per-step times and the `cputime` lines of runs 427
  (before this work's changes) and 431 (after the first of them).
- The machine was a 4-core container shared with other agents' tests
  (load 5 to 44); every comparison below is CPU seconds, or wall time
  measured back to back at the same load, before and after.

## Where a verdict's time goes

A check of recorded steps is: record (qobs), reconstruct (qtrace), plan
and render a replay module (binding, internal/replay), and `quint test`
it (validate). Everything but the last is noise:

| suite (-short unless said) | CPU s total | of it quint | quint processes |
|---|---|---|---|
| examples/counters, before | 1311 | 753 | 116 |
| examples/counters, after 7bc208e and d6640dc | 449 | 428 | 63 |
| examples/wallet-grist | 336 | 319 | 56 |
| examples/library | 178 | 165 | 26 |
| qtest (full), before / after | 488 / 400 | 459 / 381 | 406 / 336 |
| validate (full), before / after | 1311 / 944 | 1269 / 913 | 1045 / 730 |

Counters' Go side is 2 CPU s of 449 once its pollers stop spinning
(below): simulation, reconstruction, rendering and the Go evaluator's
searches together are under 1%.

**A quint process is a typecheck.** Every `quint test` typechecks the
model and the module before it evaluates anything. Counters' model
(counters.qnt, its generated counters_gen.qnt and grist.qnt, 1047 lines)
takes `quint parse` 2.1 CPU s and `quint typecheck` 4.7; a 107-step
replay module 7.4 to typecheck and 7.9 to typecheck and evaluate, so the
evaluation is about 0.5 s (about 2 ms a step). One trace of 20 steps
costs 5.35 CPU s; three such traces in one process 5.45. The GR2
reviewer's 3.1 of 3.3 s is the same finding. So the cost of a suite is
its number of quint processes, about 6 CPU s each for a grist model and
about 1.2 for validate's small test models.

## Done

### 1. grist pollers spun after their node was killed (found; fixed in 7bc208e)

Counters' Go side was 558 CPU s, 489 of it (88%) in
`grist.(*poller).commitAt`'s retry loop: `runtime.asyncPreempt` was 22%
of all samples, the mark of a loop that never blocks. Once
`gristtest.Node.Kill` cancelled the node's context, Append returned
ErrUnavailable (transient), the backoff's `qenv.Sleep` returned at once
on the done context, and the loop went round until the test binary
ended, taking CPU from every parallel test. Fixed by the GR3 slice 3
reviewer (commitAt returns when its context ends;
TestPollerStopsWithItsNode). Counters on CI: 1469 → 1038 CPU s (run 427).

### 2. A batch of short traces is one quint process (d6640dc)

`CheckBatch` split every batch into about one chunk per CPU, however
short its traces: an exploration of 6 seeds of 20 steps made 4 quint
processes, each typechecking the model for ~0.1 s of evaluation. It now
splits only past about 1000 steps a process (`chunkSize`), where
parallel evaluation repays the extra typecheck; wall time barely moves,
since a process's typecheck is the same time parallel or not. Verdicts
are the same (a batch's promise; a chunk of several traces is on the
TypeScript backend, as before).

| CI, run 427 → 431 | CPU s | wall s |
|---|---|---|
| validate | 1266 → 706 | 493 → 295 |
| qtest | 473 → 295 | 295 → 177 |
| examples/counters | 1038 → 438 | 299 → 144 |
| examples/checkout | 251 → 96 | 67 → 34 |
| examples/twophase | 188 → 61 | 57 → 35 |
| examples/lease | 188 → 72 | 49 → 19 |
| examples/bank | 91 → 37 | 30 → 19 |

The examples part measured the same in run 434 (counters 426). The core
part's runners differ: run 434 measured validate 1218 and qtest 514, but
internal/qeval, unchanged, 307 against 180 in run 431 and 227 in 427.
Against it, validate fell 29% and qtest 20%, as here.

Risk: none to determinism (no simulation changes) or verdicts. Not an
API change. `ci/test-times.txt` was lowered to about 15% above run 431's
lines (2d79047), so the budget guards the gain.

### 3. CI: no part waits behind another (69ab69c, c12f703)

The core part ran its tests (15.5 min), then the race detector (5.9),
the qenv self-checks (2.9) and the TypeScript backend (5.6) in turn: 30
minutes on every push's critical path. Run 431 with the variants split
off: core 8.0, core-variants 12.9, examples 13.0 (their demos 4.3 of
it). The race detector and the demos are now parts of their own too:
run 434's parts took core 13.6 minutes (a slow runner: see item 2),
examples 8.3, core-variants 5.0, race 4.4, demos 3.9, tools 5.6; the
push's wall time is the core part's, from 23 to 31 minutes before. Cost: three more runner set-ups a push.

### 4. Quint-bound tests in parallel, a typecheck per spec (b7606ee)

binding's `TestEvalModuleCompiles` ran a typecheck and a `quint compile`
per binding of the repository, one after another (603 s of wall on CI,
the core's longest package), typechecking counters' model six times;
internal/qeval's `TestAgreesWithQuint` likewise. Their subtests run in
parallel and each spec is typechecked once: TestEvalModuleCompiles 194 →
93 s wall here (296 → 251 CPU s, 92 → 66 typechecks); binding on CI 439
→ 130 s wall, internal/qeval 303 → 128 (runs 427 and 431, item 2 in them
too).

### 5. GOTOGETHER_CHECK's checks were quadratic (1ccb4ff)

`BenchmarkStoreTxn` under GOTOGETHER_CHECK=1 cost 28 µs a transaction
at 1000 and 80 at 4000; `BenchmarkServiceCall` 15 and 32 µs (5.5 and 11
without the check). The Store's history check searched back over every
earlier transaction that reaches the newest commit, for a cycle needing
an rw edge the forward search had already ruled out; the scheduler's
self-check walked every task ever spawned at every step (a Service
spawns one per call). Now both are flat: 11 µs at 4000 either way. Same
checks; TestClassifyAgainstBruteForce and qhistory's brute force pass.
Checkout's -short suite under the check: 106 → 93 CPU s. It matters most
for the nightly's long runs.

### 6. A fault choice without a string built per Rates key (d795465)

`Rates.Of` compared every key with `prefix+"."`, a concatenation per key
at every fault choice, and the scheduler makes one choice per nemesis
family at every step: `BenchmarkSeededFault` 180 → 100 ns.

### 7. The Go evaluator decides passing runs (H1, D1 below as decided)

Before it, a grist suite's quint CPU was mostly the fast-path batch
processes that decide passing runs: counters 231 of 428 CPU s (36
processes), wallet-grist 210 of 319, library 79 of 165. Now a check
asks the evaluator first; a plan it passes is a pass with no quint
process, one it rejects goes to quint's detail replay alone (its report
quint's), one it refuses to quint as before (docs/DESIGN.md §56).

Here, back to back on the same 4-core machine shared with other agents
(load 8-28; CPU s are user + sys of `go test` and its children, quint
processes counted by the PATH wrapper above):

| -short unless said | CPU s before → after | quint processes | of them `quint test` | `quint compile` |
|---|---|---|---|---|
| examples/counters | 443 → 279 (−37%) | 63 → 38 | 56 → 23 | 1 → 9 |
| examples/wallet-grist | 342 → 240 (−30%) | 56 → 40 | 48 → 25 | 1 → 8 |
| examples/library | 178 → 143 (−20%) | 26 → 20 | 19 → 8 | 1 → 6 |
| examples/checkout | 91 → 62 (−32%) | 18 → 9 | 17 → 6 | 0 → 2 |
| validate (full) | 940 → 718 (−24%) | 730 → 557 | 686 → 506 | 20 → 26 |
| qtest (full) | 392 → 204 (−48%) | 332 → 179 | 300 → 133 | 10 → 24 |

(grist's examples on edb8a81's acyclic model layout, the rest on
7e83d66.) Less than the 50-65% D1 estimated, for two reasons the
counts show: the remaining `quint test` processes are failing runs'
detail replays (mutants, the claims that expect a rejection: validate's
own tests are mostly those), and every process that judges runs now
compiles the binding's evaluation module once (`quint compile`, about
as costly as a typecheck): counters' overlay mutants and its packages'
test binaries compile it nine times. D3's cache answers the second.

Soundness before the switch: TestAgreesWithQuint over every model of
the repository (42 models, 16 of them examples', none refused, on
edb8a81), and the shadow (`GOTOGETHER_EVAL=shadow`, each run's plan
judged by both, verdict, passing result and ITF states compared) over
the -short suites of counters, wallet-grist, library, checkout, wallet,
quorumkv, bank, twophase, lease, hello, jobqueue, kvstore, raftkv,
glomers, streams and retrystorm, and the full validate and qtest
packages: about 2,000 runs judged both ways (passes and failures:
eventual vals false at a settle, invariants violated, rejected steps,
mutants' runs, linearized and resolved orders, pending calls, snapshots
and constants, replays past 20 steps in segments), no disagreement but
the five the tamper tests make on purpose. The runs the evaluator
refused (a model's runtime error such as a missing map key, a literal
of a constructor its model lacks) went to quint.

### 8. A day's log judged by entity (I19)

A synthetic payments day (examples/migrate-payments) took 0.6 s at 10²
steps, 63 s at 10³ and over ten minutes at 10⁴, compile cached: 98% of
the profile in the invariants judged after every step over every
charge so far (`walk`'s check), which grows with the log, and cubic for
an invariant over pairs of charges. The fix is not a faster evaluator
but less to judge: `partition:` in the binding judges each entity's
steps alone, once the evaluator's footprint shows the model factors by
it on the run (DESIGN §66). Then 10², 10³, 10⁴ and 10⁵ steps take 0.4,
2.9, 4.9 and 13.5 s (the CLI; with quint's sample of the passes off,
10⁴ 1.1 s and 10⁵ 10.4 s: linear). On the way, three costs linear in
the state or the log at each step went: a lone successor's key (the
whole state written out to deduplicate nothing), a scan of the parts
for a name's collision when splitting by key, and Reconstruct's
unsized slice and maps. Two costs the partition met and capped: quint
typechecking a day's IDS (every charge) in each failing part's replay
(constants no part reads take one part's value: 7 minutes to 4 s for a
10⁴-step log with two failures), and quint's 1-in-50 audit of the
passes (at most 64 of a trace's parts: 37 s to one process at 10⁵).

CI's lines are not changed here: validate's and the example's new tests
add a few CPU seconds, and the next runs set them.

### 9. The hot paths in Go: recording, Reconstruct, the scheduler (I37, I38, I40)

The Go specialist's review measured recording a step at 10-18 µs and
about 40 allocations, Reconstruct at 6 KB a step, a fault choice at 280
ns, and found no benchmark for any of them. Benchmarks first (65bfb9d),
then three changes, each checked against the code it replaced:

- **The step log's encoder** (3047ddb). `MarshalLine` built a map of the
  attributes and let encoding/json reflect over it and sort it.
  `Step.AppendLine` appends the same line in the keys' order, escaping
  as encoding/json does, and leaves a value it does not know (another
  type, NaN or an infinity, an invalid json.Number) to json.Marshal:
  gotogether.steps/1's bytes and errors are json.Marshal's
  (`TestAppendLineAgainstMarshal`, generated steps of every field,
  invalid UTF-8, U+2028, HTML's characters, floats at the e-notation's
  edges, numbers past int64; and the repository's step logs). JSONLSink
  encodes into pooled buffers outside its lock.
- **Reconstruct by index** (075ce15). Its passes work on indices into
  the input; a step is copied once, into its trace, and a gap's times
  travel with the recorder's seqs instead of a map of every record
  (`TestReconstructAgainstReference` compares it with the copying
  implementation on generated bags of every record kind and the
  repository's logs).
- **The scheduler's small costs** (089889b). A rate found by the kind's
  dot-prefixes (`TestRatesOfAgainstBruteForce`); a choice's options
  named from the Sim's fields, the nemesis families' buffers kept from
  step to step; a step's simulation found from its context's task
  before the goroutine's id, which runtime.Stack's header makes cost 3
  µs and more (one lookup, `hooks.Site`, where the discard check and the
  clock made two); the goroutines' simulations in 64 shards. The
  choices are the same, in the same order: explorations' double runs
  and the stored corpora replay (root -short and checkout, hello,
  streams and raftkv under GOTOGETHER_CHECK=1).

Measured with `ci/bench.sh run` (the nightly's comparison): the base
b382007 with these benchmarks against 79a03cb, interleaved, 6 runs each,
on the shared 4-core machine at load 20-35, so times are within ±50%
and only benchstat's significant changes are quoted; allocations are
exact. The base is before I19's presizing of Reconstruct (−27 to −33%
of its bytes).

| benchmark | sec/op before → after | B/op | allocs/op |
|---|---|---|---|
| qtrace MarshalLine | 13.2 µs → 1.63 µs | 2457 → 512 | 45 → 1 |
| qobs Record/PerStep (to io.Discard) | 13.9 µs → 3.84 µs | 3.24 Ki → 1.03 Ki | 49 → 8 |
| qobs Record/Buffered | 16.9 µs → 4.08 µs | 3.24 Ki → 1.03 Ki | 49 → 8 |
| qobs Record/Global (context.Background) | 15.0 µs → 4.96 µs | 3.24 Ki → 1.03 Ki | 49 → 8 |
| qobs Record/Thread (a qobs.Thread's) | 27.8 µs → 18.7 µs | 3.37 Ki → 1.06 Ki | 52.5 → 9 |
| qobs Record/Parallel (4 cores) | 15.5 µs → 3.57 µs | 3.38 Ki → 1.03 Ki | 49 → 8 |
| qobs Record/Memory (no encoding) | 2.1 µs (~) | 1.03 Ki | 8 |
| otelio RecordStepLog | 14.2 µs → 2.75 µs | 2833 → 704 | 42 → 5 |
| qtrace Reconstruct 1k steps | 16.9 ms → 3.7 ms | 4.77 Mi → 0.77 Mi | 3.6k → 1.3k |
| qtrace Reconstruct 10k | 71.9 ms → 20.5 ms | 64.0 Mi → 8.5 Mi | 31k → 10.5k |
| qtrace Reconstruct 100k | 1.30 s → 0.37 s | 851 Mi → 101 Mi | 303k → 101k |
| qtrace Reconstruct 100k, Lamport clock | 1.95 s → 0.79 s | 859 Mi → 107 Mi | 603k → 301k |
| qtrace ReadJSONL 10k steps | 200 ms (~) | 64.5 Mi | 820k |
| choice SeededFault (4 Rates keys) | 120 ns → 57 ns | 0 | 0 |
| choice SeededFaultDefault | 105 ns (~) | 0 | 0 |
| qenv BareSteps | 850 ns (~) | 48 → 0 | 1 → 0 |
| qenv NemesisSteps (4 crashable processes) | 2.35 µs → 1.04 µs | 224 → 0 | 7 → 0 |
| qenv RecordInSim/Context (a task's step) | 26.8 µs → 3.86 µs | 2.68 Ki | 6 → 4 |
| qenv RecordInSim/Background | 34 µs (~) | 2.6 Ki | 7 → 6 |
| qenv StoreTxn (each level) | 7-9 µs (~) | 1.22 Ki → 1.04 Ki | 14 → 10 |
| qenv ServiceCall | 12 µs (~) | 1.39 Ki → 1.20 Ki | 25 → 19 |
| hooks GoroutineID | 3-4 µs (~) | 32 | 1 |

(~: no significant change.) What is left: a step whose task the context
does not carry (context.Background() in a task, a woven call, a
`qobs.Thread`'s check of its goroutine) still pays the goroutine's id,
which Go offers no cheaper way to read; ReadJSONL's 82 allocations a
step (encoding/json's decoding of each line) is the next of the
recording path's costs, for `gotogether validate` on large logs. The
parallel case shows little scaling here (3.6 µs a step on 4 cores
against 4.1 on one), on a machine whose four cores ran other agents'
tests at once; its recorder holds a lock only to stamp the step and to
write it.

### 10. The Go evaluator's explorer (I39)

The Go specialist measured a bank explore at 270 KB and 3,000
allocations a state, a third of its time in the collector, and found no
benchmark of qeval. Benchmarks first (internal/qeval/bench_test.go:
`BenchmarkExplore` on bank, kvstore and payd, 2000/1000/1000 states;
`BenchmarkStep`, one deterministic action as a replay takes it and one
state's transitions; `BenchmarkKey`), then, each measured against the
one before:

- **A struct seen-key** in the search core (`k + "#" + p` was a string
  per transition): 27% fewer bytes on bank, with sized event slices.
- **Binary keys** (key.go): a state's Key, a label's, an action call's
  memo key and Transitions' dedupe key are tagged, length-prefixed
  encodings appended to a reused buffer, looked up without a copy, in
  place of Quint text with every string `strconv.Quote`d. A key is
  collision-free by construction: every value's encoding is
  self-delimiting and decodes to one value (`TestKeyCollisionFree`
  decodes 3000 random states back and checks keys against Compare on
  7000 more, many equal; a string without its length or a Tup and a
  List under one tag fail it). The allocations of an explore halve.
- **Int literals parsed at Load and integer kinds and opcodes** (ops.go):
  no change measurable on these models (evaluation proper is 15% of an
  explore), kept as the base of I50's compiler.
- **The GC at 400% during a search** (gc.go): shared by searches running
  at once, the last restoring the percent; a percent off or higher is
  left alone (`TestRaiseGC`).

Measured with internal/qeval's test binaries at the base (dce024f plus
the benchmarks) and at the end, interleaved, 6 runs each, benchstat, at
load 5-10 on the shared 4-core machine (at load 40 the explore's gain
was 2.7x: the collector's share grows with the load):

| benchmark | sec/op before → after | B/op | allocs/op |
|---|---|---|---|
| Explore bank (2000 states) | 708 ms → 398 ms | 442 Mi → 275 Mi | 5.11M → 2.68M |
| Explore kvstore (1000) | 766 ms → 480 ms | 406 Mi → 308 Mi | 6.20M → 2.99M |
| Explore payd (1000) | 427 ms → 216 ms | 285 Mi → 190 Mi | 3.24M → 1.11M |
| Step bank/deposit | 3.2 µs (~) | 2.26 Ki | 29 |
| Step bank/transitions | 414 µs → 285 µs | 263 Ki → 173 Ki | 2863 → 1793 |
| Step kvstore/transitions | 571 µs (~) | 339 Ki → 277 Ki | 4577 → 2787 |
| Step payd/transitions | 17.5 ms → 12.5 ms | 10.4 Mi → 7.5 Mi | 102k → 46k |
| Key bank | 370 ns → 125 ns | 128 → 64 | 5 → 1 |
| Key kvstore | 410 ns → 231 ns | 136 → 64 | 6 → 1 |
| Key payd | 778 ns → 280 ns | 264 → 64 | 7 → 1 |

(~: no significant change.) The explore is 1.6-2x faster here, short of
the review's 2-3x estimate at this load. What is left, from the profile
after: the labels (each successor's event list and its slice, 36% of
the bytes), `action`'s successor slices and assignments (`merge`, 26%),
and the environments' bindings; they are the closures and slot-resolved
environments of I50. Checked: TestAgreesWithQuint, the differential
tests, the brute-force graph, reach, transitions and lasso tests, the
footprint and partition tests (I19's, migrate-payments' volume test),
and `GOTOGETHER_EVAL=shadow` over the bank, payd and kvstore suites (56
tests, no disagreement). The nightly's benchmarks job installs quint so
that these benchmarks run (they skip without it).

## For the maintainer's decision

### D2. A check's actors in one quint process

`Check` replays each actor of a run in its own process (counters'
TestRealHTTP: two actors, two processes of ~8 CPU s each, the second
mostly a second typecheck of the same model). Grouping a run's actors by
instance into one module, as CheckBatch groups runs, would save a
typecheck per extra actor: validate's single checks were 274 processes
(331 CPU s) of 730. Risk: reports name each actor's module and ITF
files (`observed_<actor>.qnt`), which tests and the CLI read; a change
of artifact names. Recommendation: worth it after D1, which removes most
of these processes anyway.

**After D1 (item 7): not done.** A check's quint processes are now its
failing actors' detail replays only; grouping them saves a typecheck
only for a run with two or more failing actors of one instance, which
no suite measured here has many of, against a change of artifact names
(`observed_<actor>.qnt`, its ITF) that tests, the CLI and users'
scripts read. Left until a measurement shows such runs.

### D3. A content-addressed cache of typechecks and compiles across processes

A model is typechecked once per process (validate's `loadModel`), but
many processes typecheck the same files: counters' overlay mutants run
the test binary again (4 of its 6 typechecks, 21 CPU s, come from those
subprocesses), the CLI per invocation, and every CI run compiles every
binding afresh (binding's 217 CPU s on CI is all quint typechecks and
compiles; internal/qeval's 180 largely). A cache keyed by quint's
version and the hashes of the spec and its imports (validate's
`specFiles` already computes them), written atomically under the user's
cache directory, would answer them; with `actions/cache` it would carry
across CI runs. Risk: a stale entry if the key missed an input (quint's
version, a flag); `GOTOGETHER_CACHE=off` would bypass it. It needs a
`binding` entry point that parses typecheck JSON (an addition to the
API). Recommendation: yes for the in-job case (overlays, the CLI);
across CI runs only with the key reviewed.

**Done for the in-job case** (docs/DESIGN.md §57): typechecks and the
evaluation modules' compiles (which item 7 made every judging process
pay) are kept under the user's cache directory by a key of quint's
version, every file the spec imports and every `.qnt` beside them
(paths and contents), and the arguments; a lock file has processes
starting at once ask quint once between them. Not across CI runs. Same
machine as item 7, -short, a cold cache at the start of each suite:

| | CPU s, item 7 → this | typechecks | compiles |
|---|---|---|---|
| examples/counters | 279 → 217 (warm cache: 178) | 6 → 1 (warm 0) | 9 → 5 (warm 0) |
| examples/wallet-grist | 240 → 177 | 7 → 1 | 8 → 3 |
| examples/library | 143 → 89 | 6 → 1 | 6 → 2 |
| examples/checkout | 62 → 61 | 1 → 1 | 2 → 2 |
| validate, qtest (full) | 718 → 699, 204 → 207 | its tests' specs are mostly temporary files: no gain | |

From before item 7 to here: counters 443 → 217 CPU s (−51%),
wallet-grist 342 → 177 (−48%), library 178 → 89 (−50%), the estimate
D1 made. What is left of a grist suite's quint is its failing runs'
detail replays.

On CI, run 442 (both, 54f0867) against run 431: counters 438 → 280 CPU
s, lease 72 → 30, bank 37 → 23, twophase 61 → 47, validate 706 → 603
and qtest 295 → 181 (internal/qeval, unchanged, 180 → 221: a slower
runner); the core part's wall time 13.6 → 7.4 minutes (run 434's), the
examples part's 8.3 → 7.0. ci/test-times.txt follows run 442.

**webjobs did not get slower.** Measured here with CI's flags (GOWORK=off,
GOTOGETHER_STRICT=1, -short, its -skip, under internal/cputime, a fresh
HOME and cache): before H1 (7e83d66) 133.4 CPU s, at 769cd5e 134.4, with
26 quint processes in both (23 `quint test`, one typecheck, one compile,
all in the parent test binary: under -short the overlay mutants are
client runs checked in it); with a cold Go build cache 136.1 and 136.8.
H1 spares it nothing by design: its binding has `within` obligations,
which are judged on quint's states, so the evaluator decides none of its
runs (validate's runEvaluator; under GOTOGETHER_EVAL=shadow it agrees on
all 15 of its checks, 7 passes among them). Run 442's 179 was a slow
runner: the same code measured 132 in run 443 and 106 in run 444, and
that run's counters (280, against 209 and 169) and checkout (90, against
69 and 48) were as high. Its share of the examples' CPU doubled only
because H1 made the others cheaper. Letting the evaluator decide a run
with `within` obligations, by judging them on its own states, would
save it most of its 125 quint CPU s: a change to what decides a pass,
the maintainer's.

**Done** (docs/DESIGN.md §56, "Bounded responses"). The bindings with
`within` are webjobs' and raftkv's two (library's and fleetrollout's
only name vals "within..."). Here, CI's flags (GOTOGETHER_STRICT=1,
-short, its -skip), a fresh quint cache each, the PATH wrapper counting:

| | CPU s before → after | quint processes | `quint test` |
|---|---|---|---|
| examples/webjobs | 138 → 81 | 26 → 11 | 23 → 8 |
| examples/raftkv | 106 → 43 | 19 → 8 | 14 → 3 |

Before the switch, the shadow judged every run of both examples' -short
and full suites both ways, the obligations judged on each one's states:
webjobs 15 runs -short and 383 full (139 with obligations compared, 2
missed ones among them), raftkv 97 -short (82 with obligations, 12
missed: late leaders after a heal), and in full mode 248 (the same 82 and 12; its TestLinearizeScale, which the nightly skips for its length in shadow, was cut at 2h30m: the tests before it judged, those after it not run), and validate's
and qtest's within, resume, absent and lost tests (162 runs, 82 with
obligations): no disagreement.

**Sampling the passes** (`GOTOGETHER_EVAL_SAMPLE`, §56): the same
flags, counters 235 CPU s at 0, 239 at the default 1/50 (one pass
sampled), 303 at CI's 1/5 (13 passes); webjobs 82, 85 (none), 107 (3).

### D4. A quint session for many replays

quint's REPL loads a model once and could evaluate many replays: the
typecheck that is 90% of every process would be paid once per model
per test binary.

**Unblocked** (grist's acyclic model layout, docs/HISTORY.md). The REPL
of quint 0.33 refused counters' model with QNT101 ("Multiple modules
conflict on the same name: countersCalls"). The cause is a file import
cycle, not multi-module files: reproduced minimally, `a.qnt` holding
`module rules { pure def f(x: int): int = x + 1 }` and `module main {
import rules.*  import gen.* from "./b" ... }`, `b.qnt` holding `module
gen { import rules.* from "./a"  var v: int ... }`; `quint typecheck
a.qnt` and `quint run` accept it, `quint -r a.qnt::main` refuses it
(QNT101 on main and rules), on both backends, and the REPL still exits
0. With `rules` in a third file the REPL loads it. grist's generated
layout was that cycle (counters.qnt imported counters_gen.qnt, which
imported countersRules back); it is now acyclic, and grist's
`TestModelsLoadInREPL` loads every example's model (failing on the old
layout). Upstream may still want the report: typecheck accepts what the
REPL refuses.

**Measured** on counters (4 cores shared with other work, so ±20%), a
kept batch of TestFaithful (4 runs, 99 steps with every check, 16
tests with the eventual vals'): `quint -r counters.qnt::counters` loads
and evaluates all 99 steps true, one input per step, state kept between
inputs.

| | CPU s |
|---|---|
| `quint typecheck counters.qnt` | 4.8 |
| `quint test` of the batch (one process, as CheckBatch runs it) | 11.6 |
| REPL: load alone | 5.2 |
| REPL: per input, `true` | 0.12 (0.09 TypeScript) |
| REPL: per step input (an action with its expect) | 0.20 (0.16 TypeScript) |
| REPL: the batch, a step per input (99) | 25.2 (20.7 TypeScript) |
| REPL: the batch as its 32 segment defs and 16 test runs | 25.7 |

The load is paid once, as hoped, but every input costs about 0.1 CPU s
on counters' model whatever it says (2 ms on a three-line model): the
REPL analyses each input against the whole loaded module. A batch of
counters then costs twice its one `quint test` process, and a session
would save at most the 4.8 s typecheck per batch while paying 2-6 s of
inputs. So the premise holds (the model loads once and evaluates
replays) and the gain does not: not worth a session backend on quint
0.33. It would be if the REPL's per-input analysis were incremental
(upstream), or for models far smaller than grist's. D1 makes it moot
for passing runs.

### D5. Fewer seeds or smaller workloads: not done

Nothing here weakens a claim. Where a suite still costs most
(counters' TestSQLTier, TestHTTPTier, TestRealHTTP: 150 to 290 s of wall
each before item 2), the cost is quint processes, which D1 and D2
address without fewer runs.

## Smaller findings

- **The goroutine lookup** (`hooks.GoroutineID`, runtime.Stack's header)
  costs 1 to 3 µs and was 28% of `BenchmarkServiceCall` (each call
  spawns a task, which registers its goroutine). Go has no cheaper
  portable way: a step recorded by a task now finds it from its context
  (item 9), and what is left would need fewer lookups (a Service
  handling calls in a long-lived task), a change to the double's
  structure.
- **Generated module size** matters little: sharing the eventually
  runs' copies of the steps (a replay module of 499 lines down to 273)
  cut its typecheck by 7%. Not worth the generator's complexity.
- **Backends**: a detail module on Rust or TypeScript costs the same
  (5.5 vs 5.3 CPU s for two runs); the choice is about ITF, not speed.
- **The scheduler** is 630-700 ns a bare step (channel hand-offs between
  task goroutines), with the hang watchdog free (684-703 ns with it, at
  load 0.7). A Store transaction is 5.5 µs, a Service call 10.5 µs.
  None shows in a suite's profile.
- **qtest's and qhegel's Go side**: qtest's full run is 400 CPU s of
  which its own process is under 2 (the profile); explorations, the
  verdict cache and runOne's overhead do not register.
- **The nightly**: run 40477116948 took 175 minutes, fresh-seeds (wallet)
  the longest; four evaluator-shadow jobs ended at their 110-minute
  timeout. Their batches are of 500 seeds, already at 64 runs a process,
  so item 2 helps them less; D1 would remove their quint half. Measure
  after this week's changes before resizing them.
- **CI's tools part** (8.7 min) is mostly `gotogether simulate` over
  every model (5.8 min of `quint run`); the hegel part 3.6.

## Benchmarks: what guards the hot paths

Before: itf's Decode/Encode, otelio's recording, qenv's BareSteps. Added
(d795465): qenv's `BenchmarkStoreTxn` (each isolation level),
`BenchmarkServiceCall`, choice's `BenchmarkSeededFault` and
`BenchmarkSeededSchedule`. They are what found item 5: a quadratic cost
shows as ns/op growing with `-benchtime Nx`. Added with item 9
(65bfb9d): qobs's `BenchmarkRecord` (per step, buffered, global, a
Thread, in parallel, and without encoding), qtrace's `MarshalLine`,
`ReadJSONL` and `Reconstruct` (1k to 100k steps, with and without a
domain clock), hooks' `GoroutineID`, qenv's `NemesisSteps`,
`RecordInSim` and `OwnerLookupParallel`, choice's `SeededFaultDefault`.

**In the nightly** (79a03cb): the `benchmarks` job runs `ci/bench.sh
run` on every root-module package with a benchmark (a new one joins on
its own), at HEAD and at the commit main was at a day before, built
once each and run interleaved on one runner, 6 times: numbers from
another night's machine would differ by more than a regression.
`ci/bench.sh check` fails on a change benchstat finds significant (p <
0.05) past 25% in sec/op (a shared runner's time moves 10-20% between
runs) or 10% in B/op or allocs/op (which do not move). The runs and the
table are the job's artifact. A regression kept is the next night's
base: fix it, or say why in its commit. Per-push CI runs no benchmark
(internal/ciconfig's `TestBenchmarksNightlyOnly`).

Proposed, and not yet in CI:

- run every benchmark once on each push (`go test -run '^$' -bench .
  -benchtime 1x ./...`, seconds), so none rots;
- in the nightly, run them at two lengths (`-benchtime 1000x` and
  `4000x`, with and without GOTOGETHER_CHECK) and fail on ns/op that
  grows with the length: the regression item 5 was, which no CPU line
  would show until a long run timed out;
- the nested modules' benchmarks (grist's `BenchmarkRead`, on
  PostgreSQL too) in the same job;
- the number of quint processes a suite starts, beside its CPU line:
  validate could count them (the wrapper's figures) under an environment
  variable, since a suite's cost is its process count; a test that
  starts many more processes than before is a regression a CPU budget
  twice as high lets through.


## Recording identity consolidation (2026-11-04)

A step recorded with `context.Background()` in a simulation resolved the run's
recorder and later its recording site through two independent stack-header
reads. The operation now carries the lazily read goroutine ID between those
lookups. Ownership, clock and discard state are still resolved at the original
phase; only the stable ID is reused. The original caller context reaches
attribute processing and the sink. Completion starts a fresh lookup, even when
`Call.Done` runs on another task.

Sequentially interleaved runs compared `7e4400e9` with this change on Linux/amd64,
Go 1.26.1, an AMD EPYC 9V74 host. Three pairs used one CPU and exactly 10,000
recordings per case. Medians (shared-host timings, not a production guarantee):

| RecordInSim | Before ns/op | After ns/op | Before B/op | After B/op | Before/after allocs/op |
|---|---:|---:|---:|---:|---:|
| Explicit context | 1,569 | 1,472 | 2,463 | 2,463 | 4 / 4 |
| Background | 17,026 | 7,219 | 2,527 | 2,495 | 6 / 5 |

The background median is about 58% lower; one 32-byte allocation disappears.
The explicit-context control has the same allocation count and no clear timing
regression. Two longer 100,000-operation pairs also leave ordinary buffered
recording at 1,052 B/8 allocations and threaded recording at 1,084 B/9 allocations;
those timing controls show no clear slowdown. Shorter ordinary-recorder runs
were too load-sensitive to support a timing claim. Reproduce each simulation
side in its checkout with:

```sh
GOWORK=off go test ./qenv -run '^$' -bench '^BenchmarkRecordInSim$' -benchtime=10000x -cpu=1 -count=1
```

The correctness controls exercise a formatter that changes the current simulation
before stamping, late discard during normalization, cross-task invocation and
completion, recorder/site precedence and original sink context/span. Resolving
the site before record normalization makes the freshness controls fail. Invoke
retains its existing pre-normalization site phase; Record and Done retain their
post-normalization phase.

This review also found a separate false-issue bug: ownership checks preceded
`when` filtering and could attribute a non-event to another goroutine. Validation
now follows accepted filtering and discard decisions, and checks a context thread
only when that identity survives explicit overrides. Accepted cross-goroutine
misuse still fails. The per-step goroutine lookup required by `qobs.Thread` remains.

## Graph parent-label experiment (2026-11-05)

The I39 review identified duplicate work in `Model.Explore`: a discovering
transition is projected and interned for its edge, then again for the new state's
parent path. On the small complete multi-start graph fixture this means 32
projection calls for 22 transitions (ten non-start states).

The proposed reuse removed those ten calls, but did **not** improve the measured
end-to-end workloads. Keep the production implementation. Operation counts alone
are insufficient justification for this optimization.

The added `BenchmarkExploreBranchLabels` complements `BenchmarkExplore` with a
first-non-step-action projection, using the same bank, kvstore and payd models and
2000/1000/1000 state limits. It exercises prefix projection as well as interning;
it does not model every helper-filtering policy in the higher-level analyzer.
These cases are automatically included by the nightly's existing benchmark run.

The allocation-neutral candidate used the parent's last outgoing edge label.
This is safe because search calls `added` immediately after the discovering
`edge`, whose fresh target ID prevents deduplication. The rejected change was:

```diff
         if nd.parent >= 0 {
-            evs := nd.via
-            if label != nil {
-                evs = label(evs)
-            }
-            g.Via[j] = g.label(evs)
+            out := g.Out[nd.parent]
+            g.Via[j] = out[len(out)-1].Label
         }
```

Four alternating before/after process pairs on one local Intel Xeon Platinum
8573C runner, CPU-1, two iterations per case, produced these median ms/op values.
The baseline was `b19762dc` plus the new benchmark, and the candidate changed only
the parent-label block above. No correctness tests ran during these samples.

| Workload | Baseline ms | Candidate ms | Change | Median allocs/op, both |
|---|---:|---:|---:|---:|
| full labels: bank | 351.26 | 358.04 | +1.9% | 2,636,959 |
| full labels: kvstore | 406.16 | 415.06 | +2.2% | 2,982,402 |
| full labels: payd | 184.63 | 192.38 | +4.2% | 1,104,485 |
| projected labels: bank | 331.96 | 338.20 | +1.9% | 2,629,996 |
| projected labels: kvstore | 396.72 | 409.21 | +3.1% | 2,978,478 |
| projected labels: payd | 174.28 | 181.64 | +4.2% | 1,101,381 |

These short runs do not identify the cause or establish a portable regression
magnitude; compiler/code layout and runner variation remain possible. All six
medians moved against the candidate, so they do not justify landing it. A separate
captured-label variant also introduced one allocation per graph and was rejected.
Samples overlapping correctness tests were discarded rather than combined here.

Graph/path tests, including the existing brute-force oracle, passed during the
experiment; the evaluator suite and native model claims also passed. Correctness
is necessary but does not establish a performance benefit. No runtime, API,
report/schema, dependency or CPU-budget change is retained.

Run the benchmark cases from the repository root (Go and Quint on PATH):

```sh
GOWORK=off go test ./internal/qeval -run '^$' -bench '^BenchmarkExplore' -benchtime=2x -cpu=1
```

For comparisons, compile separate test binaries and alternate their order while
the runner is otherwise idle. The model compilation/setup is outside benchmark
timing; the numbers above describe warm exploration, not CLI cold-start cost.


## Cold historical processor recovery (2026-11-06)

I24's remaining cold-read question is narrower than processor memo ownership:
a fresh application has a processor checkpoint behind the head, but the store's
only snapshot is newer than the first trigger. It must rebuild that trigger's
prefix. Warm owned memo entries already avoid repeating the prefix on later
triggers; this experiment does not change that implementation.

**Decision:** retain the current snapshot contract. A cold rebuild is measurable,
but these runs do not establish a first-read or recovery target that warrants
historical snapshot retention. The newer-snapshot first-ready phase accounts for
about 7–14% of full recovery here, despite containing 90% of the processor folds.
A concrete first-read latency requirement could justify a different decision;
none is supplied by this bounded workload.

`grist/BenchmarkColdProcessorRecovery` uses real named processors over persistent
SQLite, 10,000 or 100,000 counter events, and one or four owners. Each starts at
90% of the head and catches up through the head, with an ordinary head query
after each callback. Head queries are serialized; pinned processor reads can
run concurrently. Runtime snapshot cadence (1000), memo capacity (4096) and
processor batching (100) retain their defaults. Audits are disabled explicitly
to isolate recovery rather than periodic audit refolds. The memo has no eviction
pressure. This is a simple counter fold, not an arbitrary business projection.

Two current-contract conditions share the same events, checkpoints and targets:

- **HEAD snapshot:** persist a genuine head snapshot, which every earlier
  processor read must leave intact. The harness observes every snapshot write,
  so a transient downgrade cannot hide behind a later head query restoring it.
- **Eligible prefix:** persist a genuine snapshot immediately before the first
  trigger, then append the same tail. Today's store keeps one snapshot per key;
  this control cannot also retain the HEAD snapshot. Ordinary head reads must
  rebuild its tail. This is not an implementation or speedup estimate for a
  hypothetical multi-position snapshot store.

Fixture construction uses bulk 1000-event appends, rather than a history of one
command transaction per event; its relay startup has that shape. Setup explicitly
requests fixture snapshot writes, then closes and copies the database. App
construction, copying, opening, result checks and closing are outside timing.
Each sample has a fresh App memo and reopened connection in the same process,
with a likely warm OS page cache. It does not measure cold-machine or full
process startup. Cancellation and database close finish SQL work, but the API
has no join for every App.Start goroutine; final pollers are parked after their
durable checkpoint and cannot begin more callbacks. Small background shutdown
work may still overlap the next sample.

The benchmark reports three distinct extents:

1. `first-get-ns/owner`: mean elapsed time inside each owner's first pinned Get;
   `first-ready-ns/op` additionally includes startup/relay work until all first
   reads finish, before head queries start. Concurrent Get times are not summed
   into a claim about wall-clock recovery.
2. `next100-ns/op`: the next exactly 100 callbacks per owner, with head queries
   and default batching. This is a callback window, not a durable-checkpoint
   window; its final checkpoint may commit afterward.
3. `recovery-ns/op`: startup through successful durable final checkpoint commits
   for every owner. A separate reopened connection checks checkpoints and
   snapshots. Every pinned/head value and expected fold count is asserted.

Three grouped samples per case on Go 1.27.3/linux-amd64, modernc SQLite 1.40.1,
Intel Xeon Platinum 8573C, benchmark concurrency suffix 5 (runner quota 4 CPUs)
produced these medians. No other benchmark/correctness work ran concurrently.
Samples were not alternating independent process pairs; these are descriptive
local values, not statistical confidence or a portable improvement magnitude.
The [native Go output](measurements/cold-processor-2026-11-06.txt) retains all
24 samples, allocation counts and custom metrics.

| Events / owners | First Get ms, HEAD → eligible | All first-ready ms, HEAD → eligible | Full recovery ms, HEAD → eligible |
|---|---:|---:|---:|
| 10k / 1 | 36.96 → 5.20 | 38.67 → 6.68 | 287.72 → 269.18 |
| 10k / 4 | 95.36 → 9.29 | 101.27 → 17.11 | 1117.39 → 1057.01 |
| 100k / 1 | 403.44 → 4.92 | 405.34 → 7.28 | 2821.87 → 2381.95 |
| 100k / 4 | 763.29 → 7.44 | 783.97 → 15.14 | 10669.44 → 9935.13 |

The first processor folds fall from 9000/36000/90000/360000 to 1/4/1/4.
The next hundred callbacks still fold exactly 100 or 400 events in both
conditions. Eligible first reads are not free: four owners return 4408 event
rows through Store.Read, including eligibility probes, anchors and processor
fetches, despite folding only four events. Row metrics count **returned event
rows from Store.Read**, not all SQL, snapshot, checkpoint or StepLog rows.
Timed allocations include recovery/relay/query work, not fixture creation.

A small 2000-event smoke test runs the same harness for both snapshot conditions
and both owner counts in ordinary CI. Its smaller eligible tail does not reach
the snapshot cadence and may keep the prefix snapshot; the head memo still
warms. Race checks pass. Removing only the initial read's newer-snapshot write
guard makes the smoke fail on an attempted downgrade, confirming that the
preservation assertion is active. No runtime, store API/schema, persistence
format, dependency, CPU budget or nightly benchmark roster changes are made.
This nested-module benchmark is manual; the nightly's root-module collector
does not automatically include it.

Reproduce the matrix (fixture setup adds wall time outside the reported extent):

```sh
GOTOOLCHAIN=local GOWORK=off go -C grist test -run '^$' -bench '^BenchmarkColdProcessorRecovery$' -benchtime=1x -count=3 .
```

Before a retention proposal, establish the actual first-read/recovery target,
repeat against that workload with controlled comparisons, and validate relevant
PostgreSQL behavior separately. A position-bounded lookup would change every
store tier; it must preserve fingerprint/checksum/immutable-anchor validation,
pinned positions and read-set conditioning, head-query cost and bounded storage.
Increasing cadence alone cannot make a newer snapshot eligible.


## Evidence discovery streaming consolidation (2026-11-06)

The order-independent discovery correction (#82) preserved coverage but used two
separate format predicates for each JSON file. A matching canonical header
could return early; the other family still scanned the payload. Unrelated ITF,
report and corpus JSON was scanned twice. Standalone two-predicate profiling put
78.5% of sampled CPU and 95.1% of allocation space under encoding/json.Token.

Use the admitted Go 1.27.3 standard `encoding/json/jsontext.Decoder.SkipValue`
instead of boxing every skipped token. A shared multi-family pass routes CLI
files. Duplicate top-level evidence/run headers remain ambiguous; nested headers
do not classify the document. Duplicate names and invalid Unicode use the same
acceptance/replacement semantics as the current authoritative encoding/json
readers. Recognized headers survive later syntax errors so their reader can
refuse them; encountered I/O errors are operational, not absence evidence.

The public `qtest.IsEvidence` and `validate.IsManifest` predicates now return
`(bool, error)`. Migrate directly and check the error; there is no bool-only
compatibility adapter. The previous CLI could exit 0 with healthy evidence
beside an unreadable JSON file. The current CLI exits 2, naming the file. Native
permission and injected-reader controls check this, separately from timing.
Classification still does not replace full reader validation or suite freshness.

The end-to-end control invokes the actual `evidence -history` subprocess,
including startup, directory traversal, discovery, selected saved-report readers
and suite rendering. This command checks/decodes **saved reports**; it does not
freshly validate a history against its model. Outputs go to null streams while
printing work is retained. Setup, fixture copying, CLI builds and an initial
acceptance invocation are untimed. Parent allocations do not represent child
allocations and are not used as an end-to-end memory claim.

Two unchanged-byte mixes were measured:

- **Tracked mix:** two actual Raft corpus entries and Rust/TypeScript ITFs, plus
  a tiny current healthy evidence control: five files, 2,183,729 bytes. This case
  is reproducible from a normal checkout. It is artifact-heavy, not a claim
  about every typical evidence directory.
- **Historical captured tree:** 497 ignored outputs from the HTTP/SQLite and
  three Goo adoption rehearsals, copied without edits, plus the same control:
  498 files, 2,384,368 bytes. Formats include run/3, run/2, report/1, report/2,
  ITF/no-format JSON and arrays. Old manifests retain their warning/skip policy.
  The control keeps the command meaningful; this is a historical byte-shape
  workload, not a fresh supported production suite or independent adopter run.
  Exact ignored outputs are not in the repository; the profiling metadata keeps
  their inventory/content hash. This optional case needs the captured tree.

One fixed baseline-built benchmark harness invoked prebuilt old/current CLIs in
six alternating before/after process pairs. Each sample averaged ten separate
CLI invocations per case. Go 1.27.3/linux-amd64, Intel Xeon Platinum 8573C,
benchmark concurrency suffix 5; process/OS caches were warm. No heavy correctness
or translation job overlapped the pairs. On the same captured tree, baseline
and current stdout/stderr were also verified byte-identical.

| End-to-end workload | Baseline median ms | Current median ms | Old range ms | Current range ms |
|---|---:|---:|---:|---:|
| Tracked corpus/ITF mix | 37.18 | 9.88 | 36.36–39.64 | 9.37–10.46 |
| Historical captured tree | 38.78 | 15.63 | 38.37–40.19 | 15.51–16.04 |

This is roughly 73% and 60% lower command time on these bounded inputs. It
supports retaining the implementation; it does not establish a portable
percentage or every-suite speedup. Native
[baseline](measurements/evidence-discovery-old-2026-11-06.txt) and
[current](measurements/evidence-discovery-new-2026-11-06.txt) output retains every
pair. [Profiling/control metadata](measurements/evidence-discovery-profile-2026-11-06.txt)
includes the CPU/allocation text summaries and captured-tree hash.

Standalone two-predicate profiles isolate parser cost, **not** the CLI's new
single-pass route. Over 100 iterations per case, tracked leaf time went from
29.48 to 10.29 ms, allocations from 8,353,574 to 60,200 B/op and from 663,453 to
175 allocs/op. Historical-tree leaf time went from 28.30 to 13.75 ms, 9,285,218
to 3,118,962 B/op and 334,986 to 16,056 allocs/op. These are allocated bytes,
not retained memory/RSS or child-process allocations.

The source benchmarks join the existing root-module nightly collector
automatically; the captured-tree case is opt-in. Normal runs build the current
CLI outside timing. Select a prebuilt executable to compare versions with one
fixed harness; compile native benchmark binaries before alternating them.

```sh
GOTOOLCHAIN=local GOWORK=off go test ./cmd/gotogether -run '^$' -bench '^BenchmarkEvidenceDiscovery' -benchtime=10x
GOTOOLCHAIN=local GOWORK=off GOTOGETHER_DISCOVERY_BENCH_DIR=/path/to/captured-tree go test ./cmd/gotogether -run '^$' -bench '^BenchmarkEvidenceDiscoveryCLI$' -benchtime=10x
```

Correctness controls retain reordered/escaped/case-folded keys, invalid Unicode,
large numbers, nested and duplicate headers, malformed prefixes/tails, unsupported
majors and strict gaps. Focused race tests, the full CLI short suite and public
API migration checks pass. Current-core gosim admission executes SkipValue via
public validate.IsManifest in a simulated filesystem, checks missing-file error
identity, and deterministically replays an emitted step. The published fork is
unchanged and GOEXPERIMENT is empty; normal encoding/json already includes
jsontext/v2 in this admitted toolchain. No schema, persistent format, external
dependency, fork hook or CPU-budget change is needed.


## Quint runtime admission cost (2026-11-07)

Two order-reversed local pairs compare the pre-admission core at c2ec62f4 with
the runtime-gated implementation. Go test binaries were compiled before timing.
Python `RUSAGE_CHILDREN` deltas measure waited descendant user plus system CPU;
monotonic elapsed time measures wall time. Go 1.27.3, Node 22.23.4 and Quint
0.33.1/evaluator 0.7.1 are the same in both variants. The canonical child
environment removes NODE_PATH/NODE_OPTIONS, and model disk caching is off.

| Workload, per process | Mean CPU before → after | Mean wall before → after |
|---|---|---|
| 20 native trace generations: existing terminal-sample TypeScript and constructor-name Rust controls, each repeated ten times | 16.351 → 17.403s (+6.4%) | 9.871 → 10.891s (+10.3%) |
| Eight native mismatch validations, each with a fresh checker artifact directory and `Eval: "quint"` | 21.193 → 23.865s (+12.6%) | 11.312 → 13.710s (+21.2%) |

All semantic assertions pass. Repeated generation amortizes the admission probe;
fresh-checker work retains a measurable cost. These two local samples cannot
predict hosted budgets tightly or establish a statistical regression threshold.
No CPU budget or benchmark threshold is raised. Unknown interpreters and runtime
overrides deliberately use uncached probes and need separate cost measurements;
only the reviewed stock CLI/interpreter identity uses this optimization.

## Inventory's cold withinCapacity proof and the verifier roster (J7b, 2026-11-10)

The nightly's verifier runs every tracked binding in one `gotogether simulate
-verify 8` invocation within the job's two hours. PR #130's frozen roster
completed 23 bindings, then spent the rest of the time in inventory's
`withinCapacity` proof (interrupted after 50m50s), leaving 27 bindings
unvisited. Measured on `9ff2478e` (Quint 0.33.1, evaluator 0.7.1, Apalache
0.62.4 checksum-verified, OpenJDK 21, 4 cores), inventory's binding alone,
cold, through the CLI.

**Where the time goes.** All of it is Apalache's SMT solving. In the
first 17.5 minutes the CLI process used 1 CPU second, quint's Node process
4, the Apalache JVM 1,065 (one solver thread at 100%); all 215 stack
samples of the checker thread, one per 10 s over 36 minutes, were in Z3's
`solverCheck`. The run was stopped after 44 minutes, at depth 7 of 8.
Nothing in Go is on the path: the Go evaluator, Key, interning and GC take
no measurable share, and quint's confirmation is not involved (a native
proof is Apalache's own). One invariant, one invocation: no proof is
repeated across goals or bindings.

Apalache's per-depth time (its `detailed.log`), the original model:

| depth | transitions | invariant check |
|---|---|---|
| 4 | 5.8 s | 1.4 s |
| 5 | 19.9 s | 21.0 s |
| 6 | 74.9 s | 219.2 s |
| 7 | 276 s | over 33 min, unfinished |

Each step multiplies the invariant query by about ten (and the transitions
by three or four): depth 8 would take hours, so the proof does not finish
in any share of the nightly. Other loads ran on the machine during depth 7
(see below), so its figure is a lower bound for the contended machine.

**The algorithmic problem is the model's encoding, not the toolkit.**
`withinCapacity` is `used(log) <= 2`, and `used` was the sum of `stock`'s
values: a map built by `put` with symbolic keys inside a `foldl` over the
symbolic log, then folded over its `keys()`. Apalache encodes each `put`
over the previous map's possible keys and the set fold over every candidate
key, so the formula grows superlinearly in the log, and the reserve rule
(`reserveOutcome` refuses `full` from `used(log)`) carries the same term
into every transition. Rewritten as the count of reservations no later
cancellation of their order clears (equal on every log: `stock` puts 0 at a
cancellation and adds 1 per reservation), the same check is:

| depth | transitions | invariant check |
|---|---|---|
| 6 | 39.2 s | 7.7 s |
| 7 | 90.4 s | 25.8 s |
| 8 | 149.2 s | 55.3 s |

The whole depth-8 proof completes in 414 s (about 7 minutes, on the same
contended machine), where the original does not finish depth 7's check in
33. Same bound, same instance, same invariant, same Apalache policy: the
model's states and transitions are unchanged, only how one integer is
computed. Equivalence evidence: the induction above; quint's sampler finds
no list of up to 8 events over the model's orders and event types where
the two disagree (20,000 traces); and `quint verify` proves them equal on
every such list of up to 7 events (17 minutes for depth 7's query alone:
the old formulation's cost, which is the point; depth 8's query was
stopped unfinished after 3 hours). The inventory-grist suite passes, in
shadow mode too (`GOTOGETHER_EVAL=shadow`: the Go evaluator and quint agree
on the new definition in every judged run).

Neither Apalache's array encoding (`smt-encoding: arrays`: slower, stuck in
depth 2's transitions after a minute) nor a toolkit-side cache helps: the
work is one cold query per depth.

**Roster fairness.** Independently of any one model, `simulate -timeout D`
now shares D among the bindings: each binding first gets the same share, D
over their number; those that run out are retried after the others with
what is left (the time left over the retries not yet run); a binding that
still does not finish is reported `unfinished` (no verdict, exit 2, never a
pass or a skip). Failures and errors no longer stop the roster. A three-binding run (inventory with the original model first, then
hello and lease) under `-timeout 4m` ended at 240 s: hello and lease passed
in 1.5 s each, inventory was cut at its 80 s share, retried with the 157 s
left, and reported unfinished. The nightly passes the job's time left
(from its recorded start, less five minutes for the reports' upload): the
job's 120 minutes are unchanged.

Two full local rosters (all 51 tracked bindings, `-verify 8`, `-timeout
100m`, the rewritten inventory model, one other Apalache job on the
machine) ended at exactly 6,000 s with every binding reported: 31 passes,
12 declared skips, 8 unfinished, exit 2. The first ran an earlier share
rule (each binding the time left over those not yet attempted): the slow
wallet-grist bindings late in the roster took 12m42s each from what the
fast ones had left and still ran out, and the retries had little. The
equal first share (1m58s) keeps that time for the retries; the second run
had the same eight unfinished, now with larger lower bounds on what each
needs:

| binding | passes | not finished within |
|---|---|---|
| counters (its proof is reused by real and retiring) | | 5m36s |
| counters.real, counters.retiring | | 5m55s each |
| inventory | | 6m30s (414 s alone, on a loaded machine) |
| library, library.calls | | 7m14s each |
| wallet-grist, wallet-grist.calls | | 12m42s (first run), 9m42s |
| counters.calls, counters.terminating, kvstore, payouts | 2m20s, 1m56s, 2m49s, 2m17s on retry | |
| the other 27 passes | 18.7 min in all | |

Those eight need at least 64 minutes between them and the passes 19: 83
of the 100 even if no attempt were lost, and a canceled attempt restarts
its proof from scratch (Apalache cannot resume one), so 81 of the run's
100 minutes went to canceled attempts. The roster fits the job only if
these proofs become cheaper, measured as inventory's was, or by a
maintainer decision on its budget; the scheduler makes the shortfall
visible, binding by binding, instead of hiding 27 bindings behind one.

Measurement caveat: that three-binding run's quint attached to the
measurement's already-running Apalache server (stock Quint's default
endpoint), so its two canceled inventory requests kept solving there after
their clients were killed, on two of the four cores, until the measuring
quint exited. In the nightly each invocation runs alone and starts its own
server, which dies with its process group when its share ends.

## The grist library's folds, measured on counters (2026-11-10)

Counters' default binding proves `uniqueNames` and `averageAsOfTrigger`
together (one native batch) to depth 8. Cold, alone, through the CLI on
`23be6eb2` (Quint 0.33.1, Apalache 0.62.4, 4 cores), it took 3,426 s:
prepare 12 s, the Go evaluator's possibles 20 s, and the rest Apalache's
Z3 queries. Per depth (transitions, then each invariant's check):

| depth | transitions | uniqueNames | averageAsOfTrigger |
|---|---|---|---|
| 5 | 76 s | 23 s | 19 s |
| 6 | 175 s | 77 s | 74 s |
| 7 | 314 s | 174 s | 343 s |
| 8 | 636 s | 527 s | about 950 s |

Unlike inventory's, no single term dominates. Ablations at depth 4 (timing
only, semantics changed): the eight call and effect transitions whose
trigger is drawn from `1..log.length()+1` take about 45% of the time, and
the welcome processor's outcome (an `if` between its success event and
`grist.failed`) about 20%. Apalache's `before` invariant mode and its
`funArrays` encoding were no faster.

Several of the library's definitions quantified over `log.indices()` and
read `log[i]`, which Apalache encodes at a symbolic position as a choice
over every position, and `nextOf` took the minimum of a set of positions
with a set fold. They are now folds over the elements (`conditionHolds`,
`existsTagged`, `nextOf`, `holds`, `holdsUnknown`, `keyStable`,
`callKeyStable`, `keyCalled`, `atMostOnceOutcomesLogged`), the generated
`nextID` accumulates the first free id instead of comparing sets, and a
generated `Replayed` action folds over its records. Equality with the
earlier definitions: `grist/model/testdata/folds.qnt` keeps them and
checks `agree` over a machine that grows logs, effect steps, call keys
and answered ids of every shape they read; `quint verify` proves it for
every trace of up to 10 steps (62 s), quint's sampler checks it in grist's
tests, and three planted mistakes in the new folds are each caught. The
regenerated examples' suites (counters, library, wallet-grist,
inventory-grist, payouts) pass, in shadow mode too, as do grist's suite
and `TestNewIsGreen`.

Counters' depth-8 proof then takes 2,438 s (1.4 times faster), with other
work on the machine for part of it:

| depth | transitions | uniqueNames | averageAsOfTrigger |
|---|---|---|---|
| 5 | 52 s | 12 s | 7 s |
| 6 | 105 s | 35 s | 46 s |
| 7 | 226 s | 91 s | 253 s |
| 8 | 435 s | 582 s | 569 s |

Transitions are a third faster at depth 8; the invariants' checks now
dominate, and they are the application's own. Rewriting them did not help:
`namesUnique` as a fold accumulating seen names, or as a count of each
name's creates, and `averageAsOfTrigger` as one fold with a counter, or
with a nested fold for each average's prefix, were equal or slower at
depth 6. Their cost follows the symbolic state the steps build (a log of
events with lists and tag sets, merged over 22 transitions), not their own
expressions, so counters still needs about 40 minutes cold.

## The grist proofs in a job of their own (2026-11-11)

Measured on main `514e93ff` (the library folds and inventory's map-free
`used` merged), each binding alone and cold through the CLI at depth 8,
four proofs at once on four cores, each with its own Apalache server:

| binding | cold time |
|---|---|
| inventory | 1,049 s |
| counters | 2,177 s |
| counters.real | 2,313 s (no reuse when run alone) |
| counters.retiring | 2,394 s (likewise) |
| library.calls | 4,695 s |
| library | 6,013 s |
| wallet-grist | unfinished: depth 5 in 2,428 s, then depth 6's second invariant query ran 2 h 25 min and was stopped |
| wallet-grist.calls | unfinished: depth 5 in 1,684 s, then depth 6's second query ran 2 h 48 min and was stopped |

The nightly now runs counters (with its real and retiring variants in one
session), library, library.calls, wallet-grist and wallet-grist.calls in
`verify-alone`, one runner per group of `ci/verify-alone.txt`, each limit
1.5 times its measurement plus 10 minutes, cited there. Wallet-grist's
proofs fit no limit GitHub allows: their groups have its 360-minute
ceiling and are expected to end unfinished, red, until their proofs get
cheaper. Wallet-grist's slow query is `noOverdraft`'s sum over the credit
line, arithmetic over every reachable sequence of transfers. Listing the
line instead of folding over its set changed nothing at depth 4. The shared
roster keeps inventory and the other 43 bindings: about 36 minutes of
proofs measured in the earlier rosters, within its 100.
