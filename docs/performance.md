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
missed: late leaders after a heal), and in full mode 248 (the same 82 and 12; its TestLinearizeScale, which the nightly skips for its length in shadow, was cut at 2h30m with every test before it judged), and validate's
and qtest's within, resume, absent and lost tests (162 runs, 82 with
obligations): no disagreement.


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
  portable way; the fix would be fewer lookups (a Service handling calls
  in a long-lived task), a change to the double's structure.
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
shows as ns/op growing with `-benchtime Nx`.

Proposed, and not yet in CI:

- run every benchmark once on each push (`go test -run '^$' -bench .
  -benchtime 1x ./...`, seconds), so none rots;
- in the nightly, run them at two lengths (`-benchtime 1000x` and
  `4000x`, with and without GOTOGETHER_CHECK) and fail on ns/op that
  grows with the length: the regression item 5 was, which no CPU line
  would show until a long run timed out;
- the number of quint processes a suite starts, beside its CPU line:
  validate could count them (the wrapper's figures) under an environment
  variable, since a suite's cost is its process count; a test that
  starts many more processes than before is a regression a CPU budget
  twice as high lets through.
