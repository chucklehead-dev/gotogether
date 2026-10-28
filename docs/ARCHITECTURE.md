# Architecture

How gotogether is put together, as it is now: the modules and what may
depend on what, the layers of the core, the path a check takes from a
recorded step to a verdict, the formats between independently installed
pieces, what is published, and where to start reading. AGENTS.md's
layout is the terse index of directories; DESIGN.md is why each piece is
the way it is, in the order it was decided; this page is the map
between them.

## 1. The modules

The repository is one Go workspace (`go.work`) of many modules. The rule
that shapes it: **the core depends on OTel and yaml.v3 only**. Anything
heavier (Hegel and purego, x/tools, Charm's TUI libraries, gRPC,
client-go, Kafka and AWS clients) lives in a module of its own, so a
production binary that records steps links none of it.

```
                         ┌───────────────────────────────────────┐
                         │ core: github.com/chucklehead-dev/      │
                         │ gotogether (OTel, yaml.v3 only)        │
                         └───────────────────────────────────────┘
          ▲            ▲            ▲             ▲            ▲             ▲
          │            │            │             │            │             │
      qhegel        grist        qnav        qanalysis     qgosim ──► qweave   (published)
     (Hegel)     (its stores,   (Charm:     (x/tools:      (the gosim  (x/tools: no
                  sqlstore's    gotogether- quintvet,      fork)       core import)
                  drivers in    nav)        actionmap)
                  the app)
      qenv/simgrpc (gRPC)   qenv/kubesim (client-go)   qenv/simlog/kafkalog (franz-go)   qenv/simlog/kinesislog (AWS SDK)   (published)

      examples/* (one module each, unpublished, replace => ../..)    internal/hegelprops (the core's Hegel properties)
```

- **Published** modules have no `replace` (go install refuses one) and
  require each other at pushed commits; `ci/bump-core.sh` moves them all
  to one core commit together, and internal/ciconfig fails while their
  pins differ. Each one's exported API is recorded in `api/<module>.txt`
  (internal/apisurface), so a change to the surface is a diff in review.
- **Examples** are modules too, each requiring the core through
  `replace => ../..`; CI tests each in its directory. `ci/consumer`
  builds the published modules the way a user's `go get` resolves them,
  without the workspace.
- **qweave** imports no core package at all: it writes woven code that
  calls the core's `woven` package by name, and checks the resolved core
  exports the ABI it was built for (`woven.ABI1`).
- internal/simdeps fails when anything a simulation runs (qenv, qobs,
  qtrace, qload, qmeta, a simulated example) depends on Hegel, purego or
  cgo: gosim translates everything a simulated package imports.

## 2. The core's layers

Each layer imports only the layers above it in this list (internal
helpers aside). `go list -deps` confirms it; the comment of every
package-level variable says whether it is read-only or process-wide
(internal/pkgstate).

| layer | packages | what it does |
|---|---|---|
| values | `itf`, internal/quintnames, internal/formats | Quint values in Go and back, ITF, Quint's reserved names, the formats' table |
| recording | `qtrace` (the `Step` type), `qrun`, `qobs`, `qobs/qobshttp`, `woven`, `qrole` | a step written where it happens: a recorder per run (found through the context or the goroutine), sinks (JSONL files, OTel span events), threads and clocks, invocations and settles |
| input | `otelio`, `convert`, `input`, `qdriver` | other shapes of the same steps: OTLP files, plain spans turned into steps by rules, an external driver's request log |
| reconstruction | `qtrace.Reconstruct` | steps into one trace per actor, ordered (seq, threads, Lamport or domain clocks), invocations matched with completions (pending calls kept), settles, losses |
| binding | `binding`, internal/replay | a trace's steps as the model's actions and values: templates, observations, the generated replay module |
| judging | `validate` (Checker, Report), internal/qeval, internal/quintcmd | is this trace a behaviour of the model: the Go evaluator first, quint for what it cannot decide and for every failure's report; linearization, pending calls, history checks |
| model analysis | `validate`'s Graph, Possible and Nav, `cmd/gotogether simulate` | the model on its own: invariants proved over a complete graph, liveness as cycles, possibilities, step-by-step navigation |
| exploration | `qtest` (System, Explore, Exhaust, Sweep, Mutants, Expect), `qshrink`, `qsweep`, `qcover`, `qmut` | many runs, each judged, claimed over (`Rejects`, `Some(...)`, `Exercised`), shrunk, mutated, swept |
| simulation | `qenv` and its doubles (`qenv/qhttp`, `qenv/qsql`, `qenv/simlog`), `qenv/weave`, `qenv/fault`, `qhistory`, `qnemesis` | the system on virtual time with every nondeterministic choice made by a Chooser: processes, crashes, a transactional Store, queues, lossy RPC; real deployments' nemeses |
| load | `qmeta` (imports nothing of the module), `qload` | a CTMC predicting recovery from a load spike; a load simulation on qenv confirming it |
| model-based tests | `connect` | Quint traces drive the code (quint-connect's API) |
| the CLI | `cmd/gotogether` | every check from the shell: lint, scaffold, simulate, validate, compile, nav, evidence, doctor |

Simulation sits below exploration in the import graph's sense (qtest
imports qenv, not the reverse), but a run's state flows back up through
`qrun`: the recorder, the role registry and the coverage counts of a run
are found from the context, else from the goroutine qenv registered
(`qrun.Key`), never from a package default that parallel runs would
share.

**grist** sits on top, in its own module: its runtime uses `qobs` and
`qtrace` to record, `qenv` (through simstore and qsql) to be simulated,
`qtest` and `validate` (through gristsim and gristtest) to be judged.

## 3. The five types everything flows through

1. **`qtrace.Step`** (and `qtrace.Trace`): one model step as recorded:
   action, arguments, outcome, observations, actor, process, thread,
   seq, clocks, and the records Reconstruct consumes (invocations,
   settles, losses). Every reader goes through `qtrace.Reconstruct`.
2. **`binding.Binding`**: the YAML that maps a trace's steps to the
   model: the spec, the module, constants, actions with their parameters
   and expects, observations, invariants and eventual vals, possibles,
   actor filters.
3. **`validate.Report`** (in a `validate.Judgment`): one trace's verdict
   (conforms, rejects at a step, mismatches an observation, violates an
   invariant, no verdict) with why, the model's state, and the commands
   to reproduce and navigate it.
4. **`qenv.Chooser`** (and `qenv.Choice`): every nondeterministic
   decision of a simulated run (which task runs, which fault, which
   order), named by kind, option 0 always nominal. Seeded, Hegel-drawn,
   scripted, logged or replayed: the same interface, so shrinking,
   coverage, scripts and replay compose.
5. **grist's `grist.Catalog`**: an App's registry as data, the single
   source of its generated model, bindings, obligations, possibles,
   mutants, grist.lock and sizing.

## 4. The data flow of a check

```
  code under test ──qobs.Record / //quint:action / qobshttp / grist's relay──► Recorder ──► Sink
                                                                                   │
              OTLP spans ──otelio / convert──┐            JSONL (gotogether.steps/1)│
              a driver's log ──qdriver───────┼──────────────────────────────────────┘
                                             ▼
                                   []qtrace.Step ──qtrace.Reconstruct──► []*qtrace.Trace (one per actor)
                                                                              │
                                    binding.Binding ──Plan──► the model's actions and values
                                                                              │
                                validate.Checker.Judge: internal/qeval decides a passing trace;
                                quint test (via internal/replay's module) decides and reports a failing one
                                                                              │
                                                              validate.Report (a verdict)
                                                                              │
                      qtest: Record / Validate (one run), System.Explore (many: Claims over Reports)
```

1. **Record.** A step is written where its effect happens: by the code
   (`qobs.Record`, a `//quint:action` annotation woven by Orchestrion or
   qweave), by an adapter (`qobshttp.Handler`, a client's RoundTripper,
   a database's commit feed), by a double (qenv's Store and Service), or
   by grist, whose commits write their step in the same transaction and
   whose relay records it from the store's step log. A recorder writes
   steps to a sink; a simulated run's recorder is its own.
2. **Read.** Files, stdin or OTLP; spans converted by rule files; a
   torn last line kept as a `qtrace.TornTail`.
3. **Reconstruct.** Steps grouped by actor, ordered by recorder and
   seq, threads (program order), Lamport or domain clocks; invocation
   records matched with their completions (`qobs.Invoke`, `Call.Done`),
   the unmatched kept as pending; issues (a gap, a cycle, a duplicate)
   reported as no verdict, never guessed through.
4. **Bind.** Each step becomes a model action with its parameters
   (templates over the step's fields), its expects (observations) and
   the outcome.
5. **Judge.** One path for every consumer (`Checker.Judge` and
   `JudgeBatch`): the Go evaluator (internal/qeval) runs the bound
   actions over the model compiled once per binding (`quint compile`,
   cached) and decides a trace that conforms; anything it cannot decide
   soundly (an operator outside its fragment, a set fold whose order
   matters, a choice inside an action) is handed to quint, and so is
   every failure, whose report is quint's (`quint test` on the replay
   module). Linearize and ResolvePending search orders and placements
   for concurrent and pending calls before judging. `gotogether compile`
   and `validate -compiled` run the evaluator alone, with no quint.
6. **Claim.** A test states what the verdicts must be (`.Expect(t,
   qtest.Conforms(), qtest.Some(qtest.Rejects("apply")))`), and an
   expectation naming an action the model lacks fails the test.

**The simulated path** is the same from step 3 on. `qtest.System.Run`
deploys the system into a `qenv.Sim` driven by a Chooser; the run's
recorder collects its steps; Explore runs seeds in parallel (Exhaust
enumerates choices, Sweep covering arrays, Mutate mutant schemata), each
run judged and kept with its choices, so a failure replays exactly
(`GOTOGETHER_RUN=...`) and shrinks (qhegel, qshrink).

**The model-analysis path** needs no run: `gotogether simulate` samples
the model (quint run), and with `-graph` the Go evaluator builds the
reachable state graph (bounded), proving invariants on a complete graph
(checked against quint's sampling), finding liveness lassos under weak
fairness, and checking each binding's `possible:` entries. `gotogether
nav` walks the same graph, or a recorded run against it.

**grist's pipeline:**

```
  App (Go: events, read models, commands, rules, queries, processors, calls)
    │ App.Catalog()
    ├──► <app> model (grist/internal/gen) ──► model/<app>_gen.qnt, _steps.qnt, grist.qnt, bindings, grist.lock
    │                                           + the app's model/<app>_rules.qnt and <app>.qnt
    ├──► App.Start / NewHandler on a Store (memstore, simstore, sqlstore) ──► each commit's step in its transaction
    │                                                                             │ grist.Relay
    │                                                                             ▼
    │                                                                      qobs recorder ──► the judging path above
    ├──► gristsim.System (a qtest.System on simstore or qsql, the API on qhttp) ──► explorations
    └──► gristtest (Generated, Catalog().Exercised, Scenario, RuleMutants, Tier, Upgrade), gristload (sizing)
```

## 5. The formats

Every format a file or a pipe carries between independently installed
pieces is named `gotogether.<thing>/<major>` (internal/formats, the one
table; DESIGN §63). A reader refuses a newer major with the fix
(upgrade the reader) and reads every older major from its oldest on; an
added optional field is no new major. `gotogether doctor` lists what
this build reads.

| format | what it is | reads |
|---|---|---|
| `gotogether.steps/1` | the step log (JSON lines; OTel span events); an unversioned log is steps/1 | every major, forever: production data |
| `gotogether.requests/1` | an external driver's request log (qdriver) | from 1 |
| `gotogether.binding/1` | a binding's optional `format:` key | from 1 |
| `gotogether.convert/1` | a span conversion rule file's optional `format:` key | from 1 |
| `gotogether.corpus/1` | an exploration's corpus entry in testdata (once `gotogether.run/1`, read as corpus/1 forever) | from 1 |
| `gotogether.choices/1` | encoded choices (`qenv.EncodeChoices`, a binary header) | the current only: another rule is another run |
| `gotogether.run/2` | a validated run's manifest (`validate -manifest`) | the current only: written anew by every run |
| `gotogether.evidence/2` | an exploration's evidence (`GOTOGETHER_EVIDENCE`) | the current only |
| `gotogether.model/1` | a compiled model (`gotogether compile`) | the current only |
| `gotogether.nav/1` | the run navigation protocol between a test binary and gotogether-nav | from 1 |

Beside them, two ABIs: `woven.ABI1`, which qweave's generated code
references by name (a skew fails to compile, naming it), and grist's
`grist.lock` (every stored event version's shape and every read model's
fingerprint), an app's own file that its generator keeps.

## 6. What is published

The core, qhegel, qweave, qanalysis, qgosim, grist, qnav and the four
doubles' modules (simgrpc, kubesim, kafkalog, kinesislog), each with its
API in `api/`, all at one core commit, no tags yet (they wait for the
maintainer: ROADMAP R5). The CLIs: `gotogether` (cmd/gotogether in the
core), `gotogether-nav` (qnav), `quintvet` and `actionmap` (qanalysis),
`qweave`, `grist` (grist/cmd/grist: `grist new`). The skills in
`skills/` are embedded in the core and installed by `gotogether skills
install`. The examples and internal/hegelprops are not published.

## 7. Where to start reading

- **To use the toolkit**: the README's quickstart, then examples/hello
  (its README's table, then `hello.go` and `model/hello.qnt`), then the
  example tracks in [examples/README.md](../examples/README.md).
- **To build on grist**: [the grist guide](grist-guide.md).
- **To change the recording side**: `qtrace/step.go` (the Step), then
  `qobs/qobs.go` (the Recorder), then `qtrace.Reconstruct`.
- **To change judging**: `validate/run.go` (the Checker) and `validate/judge.go` (Judge),
  internal/replay (the replay module), internal/qeval (`eval.go`, and
  `TestAgreesWithQuint`, which every change must keep passing).
- **To change exploration**: `qtest/system.go` and `qtest/explore.go`,
  `qtest/expect.go` for claims.
- **To change the simulation**: DESIGN §10 first, then `qenv/sim.go`
  (the scheduler), qenv/internal/choice (choosers, the byte rule, rates),
  and a double (`qenv/store.go`) as the pattern; run its tests with
  `GOTOGETHER_CHECK=1`.
- **To change grist**: docs/grist.md (the design), then `grist/command.go`,
  `grist/processor.go`, `grist/relay.go`, the generator in
  grist/internal/gen.

Where each area's gotchas are: AGENTS.md's list for the core's, and
docs/agents/ by area (metastability.md for qload, qmeta and qrole;
gosim-weaving.md for qgosim, qweave, qenv/weave, the native runtime,
coverage and mutants; analysis.md for qanalysis and the binding lint;
kube.md for fleetrollout and crossdb; commands.md for each module's
commands and the environment variables).

The guards that keep the structure as described, each a test that fails
when it drifts: internal/apisurface (the API files), internal/docsnippets
(every doc's Go and backticked names), skills' `TestSkillsAvoidDeprecated`
(old names in the docs), internal/pkgstate (package-level state),
internal/simdeps (what a simulation may import), internal/ciconfig (the
pins and CI's tiers), internal/exampleapps (every example app's parts:
examples/README.md, "What every example app has"), qlint's `TestCoreStaysInTheSimulation`.
