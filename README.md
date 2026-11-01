# gotogether

Test Go code against [Quint](https://quint-lang.org) specifications: the
model drives the code, judges what it did, or both, in tests, in a
simulated environment full of faults, and in production.

Pre-release: nothing is tagged yet. Where the work stands, what is left
and how to resume it: [docs/STATUS.md](docs/STATUS.md).

## Quickstart

```sh
git clone https://github.com/chucklehead-dev/gotogether && cd gotogether
npm install -g @informalsystems/quint     # quint ≥ 0.32; Go ≥ 1.26.1
cd examples/hello && go test -v .         # under a minute, most of it building
```

That ran [examples/hello](examples/hello): a counter behind a simulated
network that loses and duplicates messages, and a client that retries.
Each test runs the system many times under different faults (a *seed*
picks them) and checks every run against a Quint model
(`model/hello.qnt`), one line each:

```
hello_test.go:40: gotogether: TestFaithfulConforms#1: 30 runs, all conform (quint confirmed 2, the Go evaluator alone the other 28); model actions taken 3 of 3; fault kinds faulted 3 of 3 reached; a sample, no proof: a failure 1 run in 10 shows is missed at most 5% of the time, a rarer one more often, one the runs cannot reach always
```

One test plants a bug (a *mutant*: a copy of `hello.go` in
`testdata/mutants/noDedupe` whose counter forgets to dedupe, built in its
place with `go test -overlay`) and expects the model to reject a step.
Under `TestMutants`, this is what a catch looks like: the run, the step
the model refuses and why, and the command that opens it in the
navigator; then [Hegel](https://hegel.dev), a property-based tester,
shrinks the failure to the single fault it needs:

```
hello_test.go:87: gotogether: as expected, some run showing an apply step the model cannot take: 23 of 30 runs (failing: 23 reject apply); the first, seed 1, faults [rpc.counter.request=duplicate rpc.counter.response=lost rpc.counter.delay=slow rpc.counter.response=lost]:
    step 2, apply(id=1): the model's apply(id = 1) is disabled: in apply: not(applied.contains(id)) is false (where applied = Set(1), id = 1)
    navigate: GOTOGETHER_MUTANT=noDedupe gotogether nav -run 'TestMutant#1:seed=1' -at fail (in the test's package directory; -json for agents); GOTOGETHER_VERBOSE=1 prints the whole report
hello_test.go:104: qhegel: shrunk to 5 steps, faults: rpc.counter.request=duplicate:
    step 5, apply(id=3): the model's apply(id = 3) is disabled: in apply: not(applied.contains(id)) is false (where applied = Set(1, 2, 3), id = 3)
```

Those failures are the mutant's, expected: the test passes.
`GOTOGETHER_VERBOSE=1 go test -v .` prints each catch's whole report.

Read `hello.go` and `hello_test.go` (189 lines together), then pick a
workflow from the table below. Agents start from the `gotogether-start`
skill (`gotogether skills install`).

An HTTP service of your own starts green, its wiring written:

```sh
go install github.com/chucklehead-dev/gotogether/cmd/gotogether@latest
gotogether init -kind http stock && cd stock
go test -short -v ./...                   # about 15 s with quint on PATH, after the first build
```

`gotogether init` writes a module: a stock-reserving handler that
records each reservation where it takes effect, a client that retries a
refused connection (and nothing else: a lost response may have taken
effect), its model and binding, a test in a simulation on `qenv/qhttp`
(the server's process started before the client, under lost, duplicated
and late requests, lost responses, refused connections and crashes), one
over a real socket through `qobshttp`, and a mutant (`>` for `>=`) that
`TestMutants` shows caught. Change the model first, then the handler:
the tests say what no longer fits.

If dependency setup fails, `init` retains the project and prints a `resume:`
command for the failed stage onward, followed by verification. Run that command
in a POSIX shell after fixing the reported cause. It uses `GOWORK=off`; rerunning
`init` against the populated directory is unnecessary. `-no-tidy` writes files
and prints the full setup plan, including the versioned dependency download.
`grist new` provides the same recovery workflow (`-no-generate` for files only).

A synchronous Go library can start without a simulated environment:

```sh
gotogether init -kind library snapshots && cd snapshots
go test -short -v ./...
./run-demo.sh; MUTANT=aliasSnapshot ./run-demo.sh
```

This starter copies immutable snapshots from a mutable store. Its observing test
records actual library calls and reads, and the planted aliasing mutant changes
an old snapshot after a later write. The finite model catches the resulting
observed-state mismatch. Production library code uses only the standard library;
recording stays in the test driver. The demo records fresh evidence and replays
its manifest. Adapt the contract and observations explicitly; this does not infer
a model from arbitrary Go code.

A bounded [Goo Read adaptation](internal/adoptiontest/goo-read/README.md) replaces
the starter's toy contract with a real strict-definition API. It freezes authored
inputs, model and observations before a later source contrast. Expected input
rejection is domain data; missing observations and tool errors cannot earn credit
for catching the contrast. The exercise exposed a machine-output gap: graph
completeness and counts were available only in display prose. `simulate -json`
now carries native `graph` facts, per-check `bound` coverage and `possible`
reachability evidence. `validate/model.Report.Doc()` supplies the same canonical
document to Go callers; `notes` and `detail` remain display context.

This agent-assisted rehearsal found no missing runtime adapter within its scope.
The reproduction checks faithful validation, the precise source mismatch,
missing-observation errors and relocated replay while keeping the frozen SDK
baseline intact.

The [large-count rehearsal](internal/adoptiontest/goo-counted-values/README.md)
adds an independent arithmetic contract against Goo's real counted-values
executor. Exact native JSON, TypeScript ITF and replay preserve counts beyond
int64; a frozen model detects a planted wrapping-sum contrast. It keeps backend
range errors and unavailable failure state explicit, without a new adapter.

`qtest.TransitionsConform("actor")` checks captured transitions for an exact
actor (omit actors to check all). It preserves the native report's ordering,
assumptions and verdict; it does not claim invariant, composition, completion or
settlement success. Missing or unjudged transition evidence stays unknown.

To hand off a checked run, use `gotogether bundle -out incident run.json`.
It publishes the native manifest and verified files together; the recorded model
verdict stays unchanged. Replay supported histories with
`gotogether validate -replay incident/run.json -json`.
To prepare a handoff that needs no Quint or Node at replay time, compile the
binding once, then validate with `-compiled model.json -manifest run.json`.
The manifest records the compiled artifact as a checked dependency; bundle and
replay preserve that judge, its native verdict, and any unsupported-check limits.

For an on-call exercise on an existing service, follow the
[jobqueue incident walkthrough](examples/jobqueue/INCIDENT.md): a native JSON
alert, the same captured record in the TUI, portable replay and a verified repair.

For a service you already have,
`gotogether scaffold -system -http -binding b.yaml` writes the same
simulated wiring around your handler.

### Words

- **Model**: a Quint specification of what the system may do; the
  ground truth.
- **Step**: one model action the code took, recorded with its arguments
  and what the code observed (`qobs.Record(ctx, "deposit", err, "amount",
  5, "obs.balance", 35)`).
- **Actor**: one sequence of steps checked as one run of the model (a
  process, a node; `qobs.WithActor`). Reports begin `actor <name>:`
  (`the run:` when there is one, unnamed).
  Not grist's **caller**, the authenticated principal of a command or
  query (`grist.WithCaller`, recorded as the step's `caller` field).
- **Binding**: a YAML file mapping recorded steps to the model's
  actions, constants and invariants (`gotogether scaffold` writes one).
- **Double**: a simulated dependency (Store, Queue, Service, Disk) that
  fails as its contract allows.
- **Nominal**: a choice's no-fault option (option 0); a run with every
  choice nominal is the canonical one.
- **Nemesis**: a fault that acts on its own rather than at a call: a
  process crash, a network partition, a clock jump.
- **Exploration**: one `Explore`, `Exhaust` or `Sweep` call; a test's
  are numbered `TestX#1`, `#2`.
- **Claim**: what a test expects of its runs (`Conforms`, `Rejects`,
  `Mismatch`, `Violates`; over runs `Some`, `All`, `None`), given to
  `.Expect(t, claims...)`. Its outcome on a run is a verdict.
- **Mutant**: a planted bug a test expects the checks to catch: by hand,
  a file of `testdata/mutants/<name>/` built in the package's place
  (`qtest.Mutants`); generated, by `qweave -mutate` (`System.Mutate`).
- **Evidence**: what an exploration reached (the model's actions, fault
  kinds and options, code).
- **Corpus**: saved failing runs, replayed before the seeds.
- **Sweep**: configurations chosen by a covering array.

## What it does

gotogether is the Go counterpart of
[quint-connect](https://github.com/quint-co/quint-connect) (model-based
testing, where Quint traces drive the implementation). It adds:
- **the model checked on its own** (`gotogether simulate`): its
  invariants sampled by quint or, with `-graph`, checked in every
  reachable state by a Go evaluator (the shortest counterexample, as a
  Quint run; deadlocks; actions never enabled; each eventual val's fair
  cycles, a lasso that keeps it false forever), and what it must be able
  to do (`possible`);
- **property-based and state-machine testing with
  [Hegel](https://github.com/hegeldev/hegel-go)**, judged and shrunk by
  the model;
- **trace validation**: Go programs report the model steps they take,
  from tests or production, through OpenTelemetry, a step log or their
  existing spans, and the model checks the recorded run;
- **simulated environments with fault injection** (`qenv`): the system
  runs deterministically against a store, queues and networks that do what
  their contracts allow (lost responses, redeliveries, crashes, an
  isolation level's anomalies), every choice made by Hegel or a seed, and
  every run checked by the model;
- **metastable failures** (`qload`, `qmeta`): whether a system recovers
  after a load spike or a capacity dip, or stays stuck in a retry storm.
  A CTMC predicts it, a simulation confirms it, and Hegel finds the
  smallest trigger.
- **grist, an event-sourced framework checked by its own model**
  (`grist`, its own module): write commands, read models, rules and
  processors in Go; the model's skeleton and both bindings are generated
  from the app's catalog (`<app> model`), so the app writes only its rules
  and invariants in Quint, and `gristsim.System` explores it under
  crashes, lost answers and isolation levels like any other system.
  Generated local draws avoid action-name collisions, so a command named
  `order` can keep an `order` argument; regeneration preserves the public names.
  Named [workload phases](docs/grist-workload-phases.md) require actual outcomes
  and bounded query observations before random clients start.
  `<app> model -check` reports model-maintenance problems together without writing;
  `model -json` emits the same read-only [preflight](docs/grist-model-preflight.md) for agents.
  `<app> outcomes -tenant T -db DB [-json]` inspects stored failed/unknown
  outcomes and original trigger identities without initializing or changing the database.
  `go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new <name>`
  writes an app whose tests pass on their first run
  ([the user guide](docs/grist-guide.md); the design note,
  [docs/grist.md](docs/grist.md); the `grist` skill). For a system of
  Apps, `gristtest.ProtocolSteps` projects native commits and peer-call
  causes into a combined model's history, alongside each App's local
  checks; `CheckProtocolOrders` checks bounded alternative causal orderings
  and distinguishes exhaustive results from hitting the limit
  ([the composition experiment](docs/grist-composition.md)).

```
                    ┌────────────── one Quint model ──────────────┐
 connect            │ quint run --mbt ──► traces ──► Driver       │ the model drives Go (quint-connect)
 qhegel.VaryTraces  │ traces + Hegel draws ──► Driver             │ the model drives, Hegel varies the details
 qhegel.Test        │ Hegel rules ──► Go ──► steps ──► model      │ Hegel drives Go, the model judges and shrinks
 qobs, convert,     │ running Go ──► steps, OTel, spans           │ the code reports, the model checks
 validate           │   ──► binding ──► quint test                │ (tests, CI, staging, production)
 qenv,              │ Go in a simulated environment, faults       │ seeds or Hegel choose schedules and faults,
 System.Explore     │   chosen ──► steps ──► model                │ the model judges every run
                    └─────────────────────────────────────────────┘
```

**Which tool for which question** (the `gotogether-start` skill has the same
table for agents):

| Question | Tool |
|---|---|
| Does my code do what the model's traces do? | `connect.Run`: the model drives the code |
| Does the code accept inputs the model forbids? | `qhegel.Test`, `FindViolation`: Hegel drives, the model judges |
| Do runs of the real system conform? | `qobs`, `//quint:action`, `convert` (OTel spans), a binding, `gotogether validate` |
| Is it correct under crashes, lost responses, redelivery, isolation levels? | `qenv` and `qtest.System`: `Explore`, `Exhaust`, `Sweep`, `Fuzz`, `qhegel.Shrink` |
| Does the whole system survive the faults my model's traces have? | `System.FollowTraces`: each trace's fault actions become the simulation's faults (`qtest.FaultMap`), the rest of the run is the system's, checked by the model |
| What did no run reach? What does the model not constrain? | `qweave -cover`, `qweave -mutate`, `System.Mutate`, `Exploration.Evidence`; over a suite, `gotogether evidence` |
| Which recorded fields does the check actually read? | `qtest.MutateSteps` (`Killed`, `Survived`), `gotogether validate -mutate-steps`: a conforming log with one step dropped, duplicated, swapped, corrupted, retimed or moved to another actor |
| Does what a browser explorer or an end-to-end suite did conform (Bombadil, Playwright, your own driver)? | a request log (`gotogether.requests/1`, package `qdriver`): each request an invocation, completed only by its own answer; [adapters/playwright](adapters/playwright) writes it, `qdriver/bombadil` reads Bombadil's trace; `gotogether validate -binding b.yaml server.jsonl requests.jsonl` |
| Which of a driver's actions does the failure need? | `qtest.ShrinkActions`: a failing journey (Bombadil's, a Go client's) shrunk by reruns on the real system after a `qtest.Resetter`, calibrated for a failure that shows only sometimes, the original's failure kept (`SameFailure`); outside go test, `gotogether shrink -actions journey.json -- DRIVER {actions} {steps}` (any driver, rerun per candidate) |
| I am starting a service: can the framework write the model for me? | grist: `grist new <name>`, then `gristsim.System(binding, App, cfg).Explore`; the generated model judges every run ([examples/counters](examples/counters)) |
| Does it recover after a spike or a slow dependency? | `qmeta`, `qload`, `qhegel.FindTrigger`, alert rules from the chain |
| My model is in Choreo | named transitions over `choreo::step_with`, then any of the above ([examples/twophase](examples/twophase)) |

Action shrinking can reuse a registered system's checks through
`ActionShrink.System`, with `Binding` left empty. The rerun still drives a fresh
action sequence; `System.Run` is not called. The default criterion keeps each
primary actor's first failure and every failed named local/protocol obligation,
including its component and binding. Results and shrink.json retain structured
`ComponentFailures`; missing or unjudged checks cannot count as reproduction.
Peer-agreement and component-conformance-only failures remain outside this slice.

Status: early, not yet released: the API may change. It grew out of a
spike in casselc/observability, where it checks an OTel exporter against
its protocol model (`examples/edgepublish`).

## Install

```sh
go get github.com/chucklehead-dev/gotogether@latest                      # Go ≥ 1.26.1
go get github.com/chucklehead-dev/gotogether/qhegel@latest               # Hegel, in tests (its own module: the core needs no Hegel)
npm install -g @informalsystems/quint                                  # quint ≥ 0.32 on PATH
go install github.com/chucklehead-dev/gotogether/cmd/gotogether@latest   # the CLI: scaffold, lint, validate, skills (gotogether help; each command's -h)
go install github.com/chucklehead-dev/gotogether/qweave/cmd/qweave@latest # weaving: //quint:action, coverage, mutants
go install github.com/chucklehead-dev/gotogether/qnav/cmd/gotogether-nav@latest # the interactive navigator (gotogether nav opens it on a terminal)
go install github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest  # grist: `grist new <name>` writes an app module (its own module: go get …/grist@latest)
gotogether doctor                                                        # every installed piece's version (the module's core, qweave's woven ABI, quint's evaluator), each skew with its fix
```

In CI, the Action (`.github/actions/gotogether`) pins Go, Node, quint
and its evaluator, caches quint's typechecks and compiles, and puts each
result in the job summary; `ci/image/Dockerfile` is the same in an image
(built by CI, published nowhere yet). What to run per push, nightly and
per release: [docs/ci.md](docs/ci.md).

```yaml
- uses: chucklehead-dev/gotogether/.github/actions/gotogether@<commit>
  with:
    working-directory: service
    run: gotogether validate -binding stock.binding.yaml runs/*.jsonl
```

A pipeline with no Node or quint (a canary, a CronJob) validates with a
compiled model: `gotogether compile -binding b.yaml -o model.json` where
quint is, then `gotogether validate -binding b.yaml -compiled model.json
runs/*.jsonl`, the Go evaluator alone judging, a run it cannot decide no
verdict, a stale artifact refused (DESIGN §64).

Every file format is named and versioned (`gotogether.steps/1`,
`gotogether.binding/2`, ...); pre-release, only the current major is
read, another refused with the fix (docs/DESIGN.md §63).

In a module of your own (the examples' `replace` lines are for working
inside this repository):

```sh
go mod init example.com/app
go get github.com/chucklehead-dev/gotogether@latest github.com/chucklehead-dev/gotogether/qhegel@latest
gotogether scaffold -spec model/app.qnt -module app -invariants safe -eventually complete -out app.binding.yaml   # choose your model's names; then set constants
gotogether scaffold -system -binding app.binding.yaml -pkg app -out system_test.go   # -http: an HTTP service on qenv/qhttp
```

Scaffolding requires explicit property roles when the model has Boolean vals.
Use `-invariants` for safety and `-eventually` for completion; unselected vals
remain listed for review and are never silently enabled. `-holds` and `-allows`
optionally classify the selected safety vals for model simulation; when used,
they must cover every selected invariant exactly once. Without that
classification, scaffold leaves simulation guidance commented out. Use
`-no-properties` only for a deliberately conformance-only binding. Unknown or
overlapping selections fail before output is written. `-system` reads these
choices from its binding and refuses property-selection flags.


When recorded argument names match the model, omit their binding mappings:
collection parameters convert their recorded JSON automatically. These defaults
and `toList`, `toSet`, `toMap` preserve integer literals exactly. Fractions and
exponent notation need an explicit integer unit at recording time; conversion
refuses them with the parameter and element path. Evaluator/backend numeric
limits still apply. Use typed `toQuint` for structured values and observation
expressions.

Bindings and span conversion rules have JSON Schemas (`schemas/`,
`gotogether.binding-2.json` and `gotogether.convert-1.json`, one per
format at its current major): an editor with the YAML language server validates a
file as it is written when its first line names the schema (`#
yaml-language-server: $schema=<URL>`, the URLs in package `schemas`),
and `gotogether lint` checks a binding against it (every problem at its
line) before loading it, rule files with `-convert`.


`gotogether lint -json` emits one `gotogether.lint/1` report for the same
inspection as text output: actionable findings, source locations where known,
fixes, inspected inventories, and each requested check as `pass`, `fail` or
`blocked`. Independent targets still run when another target fails. Exit codes
are 0 for clean performed checks, 1 for authored findings, and 2 for unavailable
inputs/tools or an invalid request. `complete` means every requested check ran;
it does not mean the findings passed. The CLI's Go scans are syntactic; binding
checks inspect model metadata and templates. Neither establishes trace
conformance. See [the executable lint walkthrough](docs/lint-onboarding.md).

Lint also detects explicit plain-field templates used for known collection
arguments, such as `groups: '{{.groups}}'` for `List[int]`. Remove a redundant
mapping to use the model-aware default; convert a differently named JSON field
with `toQuint`. The finding carries the parameter, model type and fix in native
JSON. Mapped `.outcome` expressions and general template expressions are outside
this narrow rule; it does not infer arbitrary expression or observation types.

Go: 1.26.x. The core needs 1.26.1 (hegel-go's minimum); the gosim fork
translates one release's standard library, 1.26's, and says so under
another. Until the first tags, `@latest` is the newest commit.

Quint's Rust backend is the default. If quint cannot download its
evaluator, run `ci/install-quint.sh` from a clone, or set
`QUINT_BACKEND=typescript`. hegel-go embeds its engine, so it needs no
Python and no server.

## 1. The model drives Go (`connect`, like quint-connect)

```go
type driver struct{ bank *bank.Bank }

func (d *driver) Step(s *connect.Step) error {
	return connect.Switch{
		"init":    func(*connect.Step) error { d.bank = bank.New(accounts); return nil },
		"deposit": func(s *connect.Step) error {
			return d.bank.Deposit(ctx, connect.MustPick[string](s, "account"), connect.MustPick[int](s, "amount"))
		},
		// ... one case per model action; "_" is the catch-all
	}.Step(s)
}

// Compared with the model's state after every step (decoded with itf.Decode).
func (d *driver) State() (any, error) { return struct{ Balances map[string]int }{d.bank.Balances()}, nil }

func TestModelDrivesBank(t *testing.T) {
	connect.Run(t, connect.Config{Spec: "model/bank.qnt", Main: "bankDesign", MaxSamples: 30, MaxSteps: 25},
		func() connect.Driver { return &driver{} })
}
```

A divergence fails with the step, its picks, the model's next state, the
differing fields (`.Balances[bob]: model 16, implementation 17`), and
`reproduce with QUINT_SEED=0x…`.

| quint-connect (Rust) | gotogether |
|---|---|
| `#[quint_run(spec, main, init, step, max_samples, max_steps, seed)]` | `connect.Run(t, connect.Config{...}, newDriver)` |
| `#[quint_test(spec, test, ...)]` | `connect.Test(t, cfg, "testName", newDriver)` |
| `impl Driver { fn step(&mut self, step: &Step) }` | `Driver.Step(*connect.Step) error` |
| `switch!(step { init => .., A(x, y?) => .. })` | `connect.Switch{"init": .., "A": ..}` with `Pick[T]` / `OptPick[T]` |
| `impl State<D>` (serde) | `State() (any, error)`, decoded by `itf.Decode` |
| `Config { state, nondet }` | `Config.StatePath`, `Config.NondetPath` |
| `QUINT_SEED`, `QUINT_VERBOSE` (compile time) | the same, read at run time, plus `QUINT_MAX_SAMPLES`, `QUINT_MAX_STEPS`, `QUINT_BACKEND`, `QUINT_BIN` |

Each trace gets a fresh driver. See
[skills/gotogether-mbt](skills/gotogether-mbt/SKILL.md) for the TDD
loop.

## 2. Hegel and the model (`qhegel`)

**Hegel drives, the model judges.**
1. A Hegel state machine calls the code with drawn inputs, invalid ones
   included.
2. The model steps the code performs are recorded.
3. Each run must be a behaviour of the model, with the states the code
   reported and the model's invariants holding.

In `Each` mode a rejected run is a Hegel failure, so **Hegel shrinks it to
the shortest run the model rejects**. Verdicts are cached across
shrinking.

```go
type machine struct{ bank *bank.Bank }

func (m *machine) RuleTransfer(tc hegel.TestCase) {
	_ = m.bank.Transfer(ctx, hegel.Draw(tc, anyAccount), hegel.Draw(tc, anyAccount), hegel.Draw(tc, hegel.Integers(-1, 6)))
}

func TestBankAgainstModel(t *testing.T) {
	qhegel.Test(t, qhegel.Config{Binding: "bank.binding.yaml", Mode: qhegel.Each, Opts: []qtest.Option{qtest.Global()}},
		func(ht *hegel.T, _ *qhegel.Case) {
			hegel.RunStateful(ht, &machine{bank: bank.New(accounts)}, hegel.WithStatefulStepCount(12))
		}, hegel.WithTestCases(30))
}
```

Modes, measured on the same 40-case state machine (`qhegel/modes_test.go`):

| Mode | How | All runs pass | A run fails |
|---|---|---|---|
| `Each` | check every case as it ends; Hegel shrinks failures against the model | 22 s | 32 s, shrunk |
| `After` | check all distinct runs at the end, in bulk | 3.3 s | 3.4 s, **not shrunk** |
| `Batched` | generate a round unchecked, check it in bulk, continue while it passes (`Rounds`, `Budget`); on a failure, replay the round in `Each` mode: cached verdicts decide every case up to the failing one, and Hegel shrinks it | 3.4 s | 23 s, shrunk |

Bulk checking (`validate.Checker.CheckBatch`) replays many runs per quint
process, grouped by instance. 24 runs take 1.6 s, against 6.6 s for one
process per run in parallel and about 14 s one at a time. Process
start-up dominates short replays. Bulk checking speeds up generation,
where runs are new; the verdict cache speeds up shrinking, where
candidates share prefixes. `Batched` uses both.

This is the direction that finds code accepting what the model forbids.
Model-driven tests cannot: the model never asks for a disabled step. In
`examples/bank`, a mutant that allows self-transfers passes every model
trace and keeps every balance non-negative, yet Hegel and the model shrink
it to `deposit(bob, 1); transfer(bob, bob, 1)` in about 50 quint checks.

**The model drives, Hegel varies.** `qhegel.VaryTraces` replays Quint
traces inside a Hegel test. Hegel picks the trace and the prefix, and the
driver draws what the model abstracts (payloads, timing, interleaved
invalid requests). Failures shrink to the simplest trace and the simplest
details.

See [skills/gotogether-hegel](skills/gotogether-hegel/SKILL.md) and
[DESIGN.md §9.2](docs/DESIGN.md).

## 3. The code reports, the model checks (`qobs`, `convert`, `validate`)

Instrument the code with model steps:

```go
qobs.Record(ctx, "deposit", err, "account", acct, "amount", n, "obs.balance", bal) // ~20 ns when off
```

or annotate functions (`//quint:action action:deposit account:acct amount:n`)
and build with [Orchestrion](https://github.com/DataDog/orchestrion). The
steps can go to OTel span events, a JSONL step log, or memory. Spans the
application already emits can be **converted** with a rule file instead.

A **binding** says how steps become model calls:

```yaml
spec: model/bank.qnt
module: bank
constants: { ACCOUNTS: 'Set("alice", "bob", "carol")', MAX_AMOUNT: '{{ atLeast 5 (max "amount") }}' }
actions:
  deposit:                                         # account, amount: the step fields of those names
    outcomes: { error: skip }                      # a refused call is no model step
    expect: 'balances.get({{.account}}) == {{.obs.balance}}'
invariants: [noOverdraft]
```

Then check, in a test or from files:

```go
// every verdict is stated the same way: Conforms, Rejects(action), Mismatch(action), Violates(invariant)
qtest.Record(t, "bank.binding.yaml", func(*qobs.Recorder) { payday(bank.New(accounts)) }).Expect(t, qtest.Conforms())
// a mutant, and how it must show up
qtest.Record(t, "bank.binding.yaml", func(*qobs.Recorder) { payday(bank.New(accounts, bank.WithMutation(bank.LostDeposit))) }).Expect(t, qtest.Mismatch("deposit"))
// an HTTP service: its handlers record into the run, in parallel with other tests
qtest.Record(t, "kv.binding.yaml", func(rec *qobs.Recorder) {
	srv := httptest.NewServer(qobshttp.Handler(api, rec))
	defer srv.Close()
	/* drive srv.URL */
}).Expect(t, qtest.Conforms())
```

```sh
gotogether validate -binding bank.binding.yaml steps.jsonl                          # step log, OTLP/JSON or ITF
gotogether validate -binding bank.binding.yaml -convert bank.convert.yaml spans.jsonl   # an app's plain spans
gotogether validate -binding app.binding.yaml server.jsonl requests.jsonl          # a driver's request log beside the server's steps (qdriver: adapters/playwright, Bombadil, your own)
gotogether validate -binding bank.binding.yaml -lost-suffix node-2 steps.jsonl      # a log that lost its tail (killed), which it cannot show
gotogether validate -binding bank.binding.yaml -require-finish steps.jsonl          # a finished run, not a prefix: settled, no call pending
gotogether validate -binding bank.binding.yaml -complete-through '*=2026-10-26T12:00:00Z' steps.jsonl  # a collector's watermark: a step due before it and absent is a defect, after it uncollected
gotogether validate -binding payments.day.binding.yaml day.jsonl                    # partition: id in the binding: a day's log judged charge by charge, every failing one reported (the model checked to factor)
gotogether validate -binding bank.binding.yaml -manifest run.json -run-info driver.json steps.jsonl  # a portable failure (gotogether.run/3)
gotogether validate -replay run.json                                                 # judged again: its hashed inputs unchanged, the same verdict (validate/cli.Replay in Go)
gotogether shrink -binding bank.binding.yaml -actions journey.json -- ./driver -play {actions} -steps {steps}  # a driver's failing actions, shrunk by rerunning it
gotogether nav -binding bank.binding.yaml -follow 'deposit("carol", 2)' -json          # the model, a state at a time: branches, disabled actions with why
gotogether nav -binding bank.binding.yaml -run steps.jsonl -at 3                         # the model before a recorded run's step 4 (validate's navigate: line)
gotogether nav -run 'TestShop#2:seed=17' -at fail -json                                  # an exploration's run by its choices (a failing report's navigate: line)
gotogether nav -run 'TestShop#2:seed=17' -at fail                                        # the same in gotogether-nav, the interactive navigator (qnav), on a terminal
# a manifest records the driver's args, absolute paths and the failing step's ref verbatim: sanitize a log before sharing it (steps -pseudonymize)
```

Manifest recording refuses input, binding, conversion-rule or model files that
changed after they were read or judged. Revalidate after an edit. Source checks
also guard warm caches; files must remain stable while external Quint runs.
These checks do not provide an atomic cross-file snapshot.

Model possibility results retain the resolved goal: a predicate that holds in a
state, or an ordered action sequence that executes. A generated command-outcome
predicate may hold initially because suitable arguments could produce that
outcome; its empty witness records no command execution. In report/2, witness
steps carry action names and exact named Quint literals, including internal
model arguments. Unattributed transitions remain explicit model steps.

Finite internal model choices in ordinary complete traces, including choosing
initial states, use whole-path search. Quint confirms the original initializer
and action witness. `-choice-limit` defaults to 20,000 expanded states, counting
distinct roots. JSON and TUI receipts disclose initialization, search completeness and
selected-path confirmation separately; exhaustion and unsupported scopes give no
verdict. See [model-choice witnesses](docs/model-choice-witnesses.md).

Recorded-state navigation refuses conflicting records or ordering evidence in
the selected actor, including at cursor zero and within a partition. Correct the
capture before navigating it; CLI JSON emits no state node on this input error.
Identical retry copies remain usable, and existing gap/snapshot policies apply.
Model-only navigation needs no capture.

Partitioned reports distinguish source actors from partition judgments. Their
receipts store original whole-history constants once and name any effective
replay substitutions, justified only by this run's evaluated prefixes. JSON and
TUI retain the same instance references even when passing partitions are folded.
The navigator labels this as whole-run evidence, independent of its cursor.

On a terminal, `gotogether nav` opens the interactive navigator
([qnav](qnav)): here a failing run, at the choice before its failing
step, then the run's option, whose step the model rejects, with why.

![gotogether-nav on hello's failing run](qnav/tapes/media/hello.gif)

```
actor default: 10 steps
  conformance: FAIL
    after record run.jsonl:4 the model's state does not match what was observed
      record:   run.jsonl:4, 2026-10-02T09:00:03Z, process bank-1
      observed: deposit(account=carol, amount=2)
      obs:      balance=4
      quint:    deposit("carol", 2)
      expect:   balances.get("carol") == 4
      the step changed, in the model's state:
        balances["carol"]: 3 -> 5
```

A failing record is named once, where it was read, with its wall time,
process (a grist node's pod) and trace; `-v` adds its place in the
replay and the model's whole state. `validate -json` and `simulate
-json` print the report as data (`gotogether.report/2`), `-report
junit=FILE` and `-report sarif=FILE` write it for CI, and the Action's
job summary reads it (`GOTOGETHER_REPORT_DIR`).

Reconstruction orders steps from many recorders. It uses dense sequence
numbers (so a lost step is reported; a log that starts after seq 1 lost
its head, and is judged from a `snapshot` step at it), program-order threads, domain
clocks, and process incarnations, which become the model's restart
action. See [docs/instrumentation.md](docs/instrumentation.md) and
[skills/gotogether-traces](skills/gotogether-traces/SKILL.md). Later snapshots check the
whole replayed state at their record; they cannot reset it or hide a mismatch.

## 4. Simulated environments and fault injection (`qenv`)

A fault is a choice the environment's contract allows. A response can be
lost after the request took effect, a message redelivered, a read served
from a snapshot. `qenv` runs the system's tasks one at a time and switches
between them only at seams. One `Chooser` makes every choice:
- `qhegel.Choices(tc)`: Hegel explores and shrinks;
- `qenv.NewSeeded(seed)`: random runs at per-kind fault rates;
- `qenv.Replay`: repeats a recorded run;
- `qenv.Nominal`: no faults.

Option 0 of every choice is nominal, so shrinking removes faults.

The smallest case is [examples/hello](examples/hello): a counter behind
a network that loses and duplicates messages, a client that retries, and
a mutant that forgets to dedupe (`testdata/mutants/noDedupe/hello.go`, the
file with its bug, planted by hand). The deployment (`Counter.Add` records
`apply(id)`, or `replay(id)` for a request it has seen; `Send` retries
until it hears back):

<!-- from examples/hello/hello.go -->
```go
func Run(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	counter := &Counter{seen: map[int]bool{}}
	svc := qenv.NewService(s, "counter", nil, counter.Add)
	var err error
	s.Spawn("client", func(ctx context.Context) {
		for id := 1; id <= Requests && err == nil; id++ {
			err = Send(ctx, svc, id, 100*time.Millisecond)
		}
		if err == nil {
			qobs.Record(ctx, "done", nil) // every request answered
		}
	})
	if rerr := s.Run(time.Minute); rerr != nil {
		return rerr
	}
	return err
}
```

and its tests: the system bound to the model, an acceptance scenario
checked possible in the model on its own (before any run) and claimed of
the runs, explored by seeds, and, in the mutant's binary, shrunk by Hegel
to the one fault the bug needs. `qtest.Mutants` finds the mutants in
`testdata/mutants`, builds the tests once per mutant with its overlay,
and runs `TestMutant` there, `qtest.Mutant()` naming it:

<!-- from examples/hello/hello_test.go -->
```go
func system() qtest.System {
	return qtest.System{Binding: "hello.binding.yaml", Run: Run, Mutant: qtest.Mutant()}
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

// retried is the acceptance criterion in the model's actions: request 1
// applied, then seen again (its retry after a lost response, or a
// duplicate) and recognised as a replay, then the client done.
var retried = qtest.NamedScenario("a retry recognised",
	qtest.Takes("apply").With("id", "1"), qtest.Takes("replay").With("id", "1"), qtest.Takes("done"))

func TestRetryPossible(t *testing.T) {
	qtest.Model(t, "hello.binding.yaml").Expect(t, qtest.Possible(retried))
}

func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	case "":
		t.Skip("the counter as it is: TestMutants runs this test in each mutant's binary")
	case "noDedupe":
		// ...
		system().WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
			Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()),
				// it never recognises a retry: no run follows the scenario
				qtest.None(retried))
		// ...
		qhegel.Shrink(t, system(), hegel.WithTestCases(50), hegel.WithSeed(1)).
			Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
		// ...
	}
}

func TestMutants(t *testing.T) {
	// ...
	qtest.Mutants{}.Run(t)
}
```

A bigger system deploys more doubles the same way:

```go
s := qenv.New(chooser)
db := qenv.NewStore(s, "shop", qenv.SnapshotIsolation)   // or Serializable, ReadCommitted
q := qenv.NewQueue(s, "payments", nil)                   // at least once: lost acks, redelivery, reordering
pay := qenv.NewService(s, "provider", nil, handler)      // lost or duplicated requests, lost responses
s.Process("payments", true, func(ctx context.Context) { shop.RunPayments(ctx, q) }) // crashes and restarts
s.Run(20 * time.Second); s.Settle(60 * time.Second)      // faults, then quiet
```

The app is written against seams: `qenv.DB` and `qenv.Txn` (an app's
records typed by `qenv.NewTable[T](prefix)`), its own
broker interfaces, `qenv.Go`, `qenv.Now` and `qenv.Sleep`. Production
puts real clients behind them. An HTTP service needs no seam: `qenv/qhttp` serves its
`http.Handler` and gives its `http.Client` a Transport, both unmodified,
with requests and responses as wire bytes over a `qenv.Service`. Nor does a
`database/sql` one: `qenv/qsql` opens a `*sql.DB` over a Store. Steps are recorded where effects happen:
`Txn.OnCommit`, a fake API server's interceptor, the provider. Then:

```go
sys := qtest.System{Binding: "checkout.binding.yaml", Run: func(c qenv.Chooser) error {
	_, err := checkout.Run(c, checkout.Config{Isolation: qenv.SnapshotIsolation, Mutation: shop.LockWithoutWrite})
	return err
}}
skew := qtest.AnyOf(qtest.Rejects("ship"), qtest.Rejects("cancel"))
sys.Explore(t, qtest.ExploreConfig{Seeds: 100}).Expect(t, qtest.Some(skew)) // seeds, checked in one batch
qhegel.Shrink(t, sys, hegel.WithTestCases(50)).Expect(t, skew)                    // Hegel, shrunk to its interleaving
```

A `qtest.System` binds an application to its model once; `Explore`,
`Sweep` (covering arrays), `FollowTraces` (a model's traces' faults) and
`qhegel.Shrink` all run it.

Seeds run in parallel, each recording into its own recorder. The
recorder travels in the chooser's context (`qenv.WithContext`) to every
task of the simulation. A step recorded without it, for example with
`context.Background()`, still lands in its run: qenv knows which
simulation each of its goroutines belongs to (its tasks, and the goroutine
that created it). During such runs recording is strict: a
step with nowhere to go fails the exploration, naming the fix, rather than vanish. That
happens only for a goroutine started with `go`, which `gotogether lint -sim
DIR` flags, along with wall-clock time. Tests are checked too (a
System's `Run` closure lives in one); mark a deliberate real goroutine or
clock read `//qenv:ok <why>`. A Hegel test records the same way,
with `rec.Env(qhegel.Choices(tc))`, which also records the run's
choices: `qenv.Replay(v.Choices)` repeats a shrunk failure outside Hegel.

Code you cannot rewrite against seams (plain `sync.Mutex`,
`sync.RWMutex`, `sync.WaitGroup`, `go` statements) can be woven in at
build time. Import `qenv/weave` in the project's `orchestrion.tool.go`
and build with [qweave](qweave) (`qweave go test ./...`), this
repository's weaver, or with `orchestrion go test`. The root module's lock
and wait group calls then become simulated ones inside a simulation and
the real ones outside it, and a `go` statement marked `//qenv:go` starts a
simulation task. qweave reads the same aspect files as Orchestrion, with
types. It also weaves what Orchestrion misses: method values
(`return mu.RUnlock`) and calls promoted through an embedded lock.
`gotogether lint -weave DIR` lists what Orchestrion would miss.
[examples/edgepublish](examples/edgepublish) runs its publisher this way.

A whole program, standard library and network included, can run in
[gosim](https://github.com/jellevandenhooff/gosim) with its choices made by
a qenv `Chooser` from the test process (Seeded, Hegel, Replay):
[qgosim](qgosim), its own module on a patched gosim. A qenv deployment
moves there unchanged. `qenv.NewNative` runs the same doubles and
processes on plain goroutines and `time`, which gosim simulates, with
processes as gosim machines. Give your run function a `*qenv.Sim`, and
call it with `qenv.New(c)` or `qgosim.New()`.
gosim's translator also weaves Orchestrion aspects. List qobs in a
`gosim.tool.go`, and `//quint:action` records steps with no Orchestrion
build.
[examples/gosim](examples/gosim) has three systems on gosim: the Glomers
broadcast node over TCP on three machines, the checkout saga, and the
edgepublish publisher, unwoven by qenv/weave, each with its model checks
(DESIGN.md §14, §15.1, §15.8).

Each seeded run is checked in one quint batch, and the failing seed's
faults and step log are reported. `qhegel.FindViolation` with
`qhegel.Choices` shrinks a failure to the interleaving and faults it needs.
See [examples/checkout](examples/checkout) (a saga at three isolation
levels) and [examples/fleetrollout](examples/fleetrollout) (a
multicluster-runtime controller, simulated and on real kwok clusters).

## 5. Metastable failures (`qload`, `qmeta`)

Some failures are not wrong answers but a state the system cannot leave.
A spike fills a queue, requests time out, and their retries keep the
queue full after the spike has passed. Isaacs et al. (HotOS '25) and
Alvaro et al. (2025) analyse these in tiers. gotogether has each
tier:
- `qmeta`: a CTMC over (queue length, requests waiting to retry). It
  draws the field (`Chain.SVG`) and gives expected recovery times
  (`HittingTimes`) in seconds. `Calibrate` fits it to simulated
  trajectories.
- `qload`: a load simulation on qenv. Clients can be open-loop or closed
  (a controller polling its objects), with timeouts, retries, backoff and
  budgets, or the retry library under test itself (`ClientConfig.Retrier`,
  on virtual time). Servers have queues and workers, and every run yields
  a time series.
- `qload.Experiment`: the application itself under load. Customers
  (open-loop or a closed population) call it in a qenv simulation, and
  goodput is the rate of one of its own model steps. Probes sample
  backlogs, and the same steps can be checked against the model.
  `qload.Assess` judges a run's recovery: goodput back to the healthy
  rate, within the noise of the counts.
- `qrole` and `qmeta.Region.Rules`: the application marks its load roles
  (`//quint:serve`, `//quint:operation`, `//quint:call`), the chain is
  calibrated on their series, and the states it is unlikely to recover
  from in time become a Prometheus alerting rule over the same gauges,
  with the policy it assumes in its annotations. `qrole.MetricsSink`
  counts every model step by outcome.
- `qhegel.FindTrigger` (any `qload.Target`: a qload simulation,
  `qload.OfSimulation`, or an application, `qload.Of(experiment)`): Hegel searches
  transient triggers (extra load, a slowdown, a duration) for one after
  which goodput does not recover, and shrinks it. Seeds of a trigger run
  in parallel. `qload.Runs`, `Count`, and `Mostly` return errors for invalid
  batches or unjudged seeds; check these before interpreting recovery.
  Trigger-search errors likewise cannot satisfy either trigger claim.
- Sizing (`qmeta.Size`, `LimitFor`, `TimeoutFor`, `RecoveryAfter` of a
  declared `Burst`; `qload.Layered` for a proxy's tries inside a
  client's): the fewest workers, the highest rate a one-at-a-time hop
  survives, a per-attempt timeout, the retry layers that recover. A grist
  app's `<app> sizing -steps FILES -rate 60/s -burst 3x30s` (its own
  binary, `cmd/<app>-sizing`: `gristload.Main`) measures its service
  times from a run's steps and writes deploy/sizing.yaml, every value
  with its reason, tool, margin and caveat
  ([examples/counters](examples/counters), the metastability skill's
  sizing reference).

```go
c := qmeta.New(qmeta.Params{Lambda: 9, Mu: 10, Timeout: 9, Retries: 3, Queue: 100})
h := c.HittingTimes(func(s qmeta.State) bool { return s.U <= 10 })   // seconds to drain, per state
r := qhegel.FindTrigger(t, qload.OfSimulation(cfg, 9, 5*time.Minute), qload.TriggerSpace{MaxExtraRate: 10}).Expect(t, qload.SomeTrigger())
// r.Trigger: "+1.0 req/s for 3m36s" tips immediate retries into a storm
```

The tiers check each other. In
[examples/retrystorm](examples/retrystorm), the simulation showed that
backoff alone makes recovery worse, which the first chain had missed.
Correctness stays with the model: the retry budget's accounting is
checked against Quint, which catches a bug that load tests hardly see.
[examples/crossdb](examples/crossdb) applies the tiers to a Crossplane
provider's requeue policy. [examples/checkout](examples/checkout) puts
the saga itself under load as a `qload.Experiment`, its payment provider
given a `qenv.Capacity` (workers, queue, service times, shedding):

```go
x := qload.Experiment{Rate: 3, Healthy: 3, Deploy: func(env *qload.Env) func(ctx context.Context, i int) {
	d := checkout.Deploy(env.Sim, cfg)   // cfg.Provider.Slowdown = env.Trigger.Factor()
	env.Probe("backlog", d.PayQueue.Pending)
	return func(ctx context.Context, i int) { d.Shop.PlaceOrder(ctx, fmt.Sprintf("o%d", i), "widget", 1) }
}}
r := qload.Assess(x, "markPaid", qload.Trigger{Slowdown: 3, Duration: time.Minute}, 5*time.Minute, seed)
```

## Packages

| Package | What it is |
|---|---|
| `connect` | the model drives Go: `Run`, `Test`, `Switch`, `Pick[T]`, `StateDriver`, `GenerateRun` |
| `qhegel` (module) | Hegel and the model: `Test` (modes `Each`, `After`, `Batched`, `Off`), `FindViolation` (an expected failure, shrunk), `Shrink` (a `qtest.System` under Hegel), `NewCase`, `Choices` (qenv's choices from a test case), `VaryTraces`, `FindTrigger` |
| `itf` | ITF traces: `Parse`, `Decode`/`Encode` (Go ⇄ Quint values), `ParseQuint` (Quint literals) |
| `qobs` | the recorder: `Record`, `Invoke` (a call recorded when invoked, `Call.Done` when it completes), `Settled` (the system was quiet: eventual properties apply), `Thread`, `WithRecorder`, `SlogHandler`; sinks `MemorySink`, `FileSink` and `NewJSONLSink` by durability (`PerStep`: survives a killed process; `Synced`: an fsync per step, a power loss; `Buffered`), `OTelSink`, `Tee`; `Recorder.Close` closes its sink and returns its error; `SetGlobal` for a program, `InstallGlobal` for a test's run; `orchestrion.yml` |
| `qobs/qobshttp` | `Handler(h, rec)`: an HTTP handler's steps recorded into a given run (a test's, a rerun's, a Hegel case's), in parallel with other runs; `Recording.Wait` for its handlers, hijacked ones too |
| `qtrace` | the step schema, `Reconstruct` (order, gaps, restarts, real-time order from `Invoked`), step logs (`ReadJSONL` keeps a torn tail's steps: `TornTail`, the tail declared lost), lost prefixes and tails (`Trace.Lost`, `MarkLost`), `Linearize`, `Filter` (select and pseudonymize; refs dropped when sanitizing unless `KeepRefs`), step refs (`quint.ref`, `qobs.Ref`: a step's link to its artifact, shown at a failing step), `Mutations` (a log's single mutations: drop, dup, swap, corrupt, retime, reactor; `MutateOps`, `MutateLimit`) |
| `otelio` | an OTLP/JSON file exporter and reader; span events to steps |
| `convert` | an application's own spans to steps, by YAML rules |
| `binding` | the YAML binding: lint against `quint typecheck`, the plan, the generated Quint module and ITF; `ScanRecords`/`LintRecords`, `Model.NearestAction` |
| `schemas` | the JSON Schemas of bindings and conversion rules (`Binding`, `Convert`, their URLs), generated from the Go types; `Check` validates a YAML file against one, every problem at its line |
| `validate` | replay in the Go evaluator (passes) or with `quint test` (failures, and what it cannot judge): `Checker` (`Check`, `CheckBatch`, `MutateSteps`: a log's observation mutants), `Cached`, `ValidateITF`; structured verdicts (`Report.Verdict`); run manifests (`NewManifest`, `ReadManifest`, `Verify`, `Bundle`) |
| `validate/receipt` | structured history judgment: check statuses, ordering-search scope and assumed effects, shared by JSON, text and run navigation; [receipts](docs/check-receipts.md) |
| `validate/diagnostic` | model-authored diagnostics with exact evidence references and TUI record inspection; [annotation prototype](docs/model-diagnostics-prototype.md) |
| `validate/model` | the model on its own, before any code: `Simulate` (what `gotogether simulate` prints: quint's sampling, `Verify`, or the Go evaluator's state graph, with the possibles searched then sampled; a `Report` of `Verdict`s, each with its `Bound`, and `Report.Doc()` for canonical structured output), `Analyzer` (`New`, `For` a validate `Checker`: `Graph`, `Possible`, `Simulate`); `qtest.Model`'s claims judge by it |
| `validate/runprofile` | effective execution policy and observed simulator limits, shared by evidence JSON, text and navigation; [run profiles](docs/run-profiles.md) |
| `validate/navproto` | the run navigation protocol between a test binary serving its runs (GOTOGETHER_NAV=serve) and a navigator (`gotogether nav`, gotogether-nav): `Request`, `Response`, `Session`, `Node`, versioned (`Protocol`, `CheckProtocol`) |
| `validate/cli` | the CLI's check of files in Go: `Check` (as `gotogether validate` makes it, and its `Manifest`), `Replay` (a manifest's run judged again: `validate -replay`); its own package, so `validate` brings no OpenTelemetry SDK |
| `qshrink` | a driver's failing actions shrunk outside a test: `Shrink` (ddmin over reruns, `Calibration`, `SameFailure`, `Resetter`, `ErrDiverged`), what `qtest.ShrinkActions` and `gotogether shrink` run |
| `qtest` | tests that check against the model: `System` (`Explore`, with `ExploreConfig.Steer` toward rare states, `Exhaust`, `Sweep`, `Fuzz`, `Mutate` (its report's `Expect`: `PerMutant`, `NoSurvivors`), `FollowTraces`: a trace's faults, `FaultScript`, imposed on the whole system), `Validate`, `Record`, `Runs` (runs made one by one, a real deployment's, under claims over runs); claims (`Conforms`, `TransitionsConform` (captured transitions only, optional exact actors), `Rejects`, `Mismatch`, `Violates`, `Settles`, `Completes`, `Finishes` (a finished journey, not a conforming prefix), `Pending`, `AnyOf`, `Not` (a run missing one); over runs `Some`, `All`, `None`, required but for `Conforms`, `TransitionsConform`, `Settles`, `Completes` and `Finishes` (every run, as their names say; any other alone panics with the fix); `Reaches`, `Takes` (`With` an argument), `Exercised`, `NotVacuous`; `Scenario` and `NamedScenario` (Takes in turn, others between: an acceptance criterion, its miss named per run with the longest prefix taken), `Possible` (a scenario checked in the model alone, no run); `Linearize` for histories recorded by clients, `LinearizeByKey` per independent key, `ResolvePending` for calls never completed; `Violates(...).At`; a shrunk run's `ShrunkTo`); observation mutants of a recorded log (`MutateSteps`; claims `Killed`, `Survived` over `StepOp` selectors); a driver's failing actions shrunk (`ShrinkActions`, `ActionShrink`, `Resetter`, `SameFailure`, `ErrDiverged`, `Calibration`; `AtMostActions`); `Evidence`; `RequireQuint` and `Optional` (skips that strict mode fails); `Mutants` (bugs planted by hand: `testdata/mutants/<name>/` files built in the package's place with `go test -overlay`, each mutant binary's test switching on `Mutant()`) and `Catalog` (a system's blocks by outcome: `Catalog.Exercised`, `Catalog.Take`); options (`Constants`, `Invariants`, `Quint`, `Global`) |
| `qenv` | deterministic simulation: scheduler, virtual time, crashable processes, choosers (`NewSeeded`, `NewPCT`, `Replay`, `Script`, `Swarm`, `FromBytes`); doubles `Store` (isolation levels; `NewTable[T]`, an app's typed records over a Txn), `Queue`, `Service` (with `Capacity`), `Disk` (fsync, power loss, torn writes, a full disk, a failed fsync, corrupt reads); the network (`Sim.Net`: `Partition`, `Heal`, `PartitionNemesis`); opaque IDs (`NewID`: deterministic within a simulation, fresh in production); clocks (`Sim.Clocks`: skew, jumps, drift; `Now`, `Mono`, `Since`, `WaitFor`); pauses and gray failures (`Sim.Stalls`); settling (`Sim.Settle`; `Sim.SettleCore`: faults go on outside a core), scoped no-work waits (`Signal.WaitIdle`; active handlers/retries remain pending); rolling upgrades (`Sim.Upgrades`, `Process.Upgrade`, `RestartAs`, `Version`); nemesis families (`nemesis.crash`, `.partition`, `.jump`, `.pause`, `.slow`, `.upgrade`); `DeclareFaults`, `FaultPoint`, `CoverageOf`; `NewNative` (the same on goroutines, for gosim); `TimeProfile` |
| `qenv/weave` | Orchestrion aspects: `sync` and `//qenv:go` goroutines simulated inside qenv, real outside |
| `woven` | the woven builds' ABI: what code woven by `qenv/weave`'s aspects, `qweave -cover` and `qweave -mutate` calls; never called by hand, stable only within one version |
| `qenv/fault` | the `//quint:fault` aspect: a function's entry a fault choice, with its own errors |
| `qenv/qhttp` | net/http in a simulation: an unmodified `http.Handler` behind a `Server` (`NewServer`, `Handle` at each incarnation, `URL`) and an unmodified `http.Client` over a `Transport` (`NewTransport`: refused connections, `TimeoutError` after virtual time, 503 on overload), over `qenv.Service`'s faults |
| `qenv/qsql` | database/sql in a simulation: `Open` a `*sql.DB` over a qenv Store (a SQL subset, one table at a time and joins by a key; the Store's isolation level and history check), `*Error` with SQLSTATE for retry loops (40001, 40P01, 23505, 08006), `Options` (`BadConn`, `OnCommit` changes), `Table` |
| `qenv/simlog` | a partitioned log in a simulation, Kafka's contract: `New` (`Log.OnCommit` where a commit lands), an idempotent `Producer` (sequences per partition, five requests in flight) and group `Member`s (generations, eager rebalances, fencing: `ErrFenced`); lost and duplicated produces, lost commits, rewound fetches and evictions (`rebalance.<log>.<consumer>`) as faults; its contract checked under `qenv.Sim.Check`; the seams `Writer`, `Reader` and `Record` that its real tiers implement too |
| `qenv/simgrpc` (module) | gRPC in a simulation: a `Server` (`NewServer`, a `grpc.ServiceRegistrar`: generated servers register unchanged) behind a `qenv.Service`, and a `Conn` (`Dial`, a `grpc.ClientConnInterface`: generated clients call unchanged); refused connections, lost and duplicated requests, lost responses and crashes as the status codes a real client sees (a deadline exceeded after `Conn.Timeout` of virtual time); unary calls only |
| `qenv/kubesim` (module) | Kubernetes API servers in a simulation, for controllers on controller-runtime's client: `NewCluster` (a fake client behind interceptors: generation bumped by a spec change, the status left alone by main-resource writes, conflicts on a stale resourceVersion; failed requests and writes applied with their response lost; a Deployment controller whose status lags), `Cluster.OnWrite` (each write where it lands, to record it), `AsWriter` (another writer's writes), `NewDeployment`, `Fleet` (members by name, for a multicluster controller) |
| `qenv/simlog/kafkalog` (module) | `qenv/simlog`'s seams on Kafka over franz-go: `NewWriter` (idempotent or not), `NewReader` (a group member, manual commits, `Reader.OnCommit` when the coordinator accepts one); the same pipeline as on the simulated log |
| `qenv/simlog/kinesislog` (module) | `qenv/simlog`'s seams on Kinesis over the AWS SDK: `Client`, `Writer`, `Reader` over every shard with `Checkpoints` (`Reader.OnCommit`); `kinesislog/kinesisfake`, an in-process endpoint for the calls it makes |
| `qload` | load on qenv: `Experiment` (open- or closed-loop), `Trigger`, `Recovers`/`Assess`/`Mostly` with `Recovered`/`StaysDegraded`, `Target`, `Simulate` (an abstract server and its clients), `Piecewise` shapes |
| `qmeta` | the CTMC: `New`, `HittingTimes`, `RecoveryWithin`, `Mean`, `Calibrate`/`CalibrateTimeout`, `Measure`; sizing (`RecoveryAfter`, `Size`, `LimitFor`, `TimeoutFor`, `Params.Unit`); alerts (`Region`, `Rules`), `Watch` (the flight recorder on alert); imports nothing of the module |
| `qrole` | load roles (`//quint:serve`, `//quint:operation`, `//quint:call`): u and v as OTel gauges, pprof labels |
| `qcover` | code coverage by model step (woven by `qweave -cover`): `Summarize`, `ReadSummary` |
| `qcover/gocover` | a real environment's coverage (binaries built with `-cover`) against a simulation's: `Read` (host-side: it runs `go tool covdata`), `Parse`, `Compare` |
| `qmut` | mutant schemata woven by `qweave -mutate`, one build, a mutant per run |
| `qhistory` | a database's isolation level from its clients' history: Elle's list-append `Workload`, `Record` (a qenv.DB's transactions recorded as a list-append history, `History`), `Check` (G0, G1a, G1b, G1c, G-single, G2, by level; known committed unseen appends still constrain reads; `StrictSerializable` with the transactions' times: the cycles through real time, G-single-realtime and its kin) |
| `qnemesis` | a real deployment's nemesis: `Nemesis` (`Run` a `Schedule`, `Stop` settles), `Process` and `Container` targets (kill, restart by incarnation, pause), `Toxiproxy` links (cut, heal, latency, loss), `Random` (Jepsen's) and `FromScript` schedules, faults recorded as model steps (`Recording`), a journal whose `Script` re-makes them in qenv |
| `qsweep` | covering arrays: `Cover`, `Missing`, `Sequences`, `Factor.Constant` |
| `qrun` | a run's state (`Key[T]`), from the context or the goroutine's run |
| `lint` | structured lint inspection (`Inspect`), performed/blocked checks, actionable findings and evidence |
| `qlint` | lightweight simulation and weaving syntax rules shared with the analyzers |
| `input` | reads a trace input (`Read`): OTLP/JSON, a step log, a driver's request log or ITF, detected by content |
| `qdriver` | an external driver's request log (protocol `gotogether.requests/1`, JSONL a driver writes itself): `Read`, `ReadFile`, `New`, `Log.Steps` (each request an invocation record, completed only by its own response; steps a driver saw; its times, refs and correlation ids), `Log.Warnings`, `Log.Write`, `Sniff`; Playwright's adapter is [adapters/playwright](adapters/playwright) |
| `qdriver/bombadil` | Bombadil's `trace.jsonl` with a page that logs its requests, as a request log (`Log`, `LogFile`; its steps for `qtest.Validate`): `Options` (the spec's extract, its custom actions in `Clicks`, the log's `Fields`, the display as `Show` steps), `States` (experimental) for a mapping of your own |
| `skills` | the agent skills below, embedded, and their install |
| `cmd/gotogether` | the CLI: `init` (a new HTTP service or synchronous library, green as written), `scaffold` (`-system -http`: on qenv/qhttp), `lint` (a binding's schema first), `simulate` (the model alone: which invariants it guarantees, which the code must, and what it must be able to do, its `possible` list, each reached with a witness or failing as too strict; `-verify N`, Apalache to depth N; `-graph`, every reachable state by the Go evaluator, with the shortest counterexamples, deadlocks, the actions never enabled, and each eventual val as fair cycles: a lasso that keeps it false forever), `steps`, `validate`, `itf`, `convert`, `cover`, `evidence` (a suite's explorations, and what no faithful one tried; `-session ID -strict` for CI), `skills` |
| `qweave` (module) | weaves Orchestrion aspects into a `go -overlay`: `qweave go test ./...`, `-cover`, `-mutate` |
| `qnav` (module) | `gotogether-nav`, the navigators' TUI (Charm's v2 libraries): a model's states or a run's choices, enter to take a branch, back, fold, the failing step, save; its VHS tapes are its docs' GIFs ([qnav](qnav)) |
| `qanalysis` (module) | analyzers `quintsim`, `quintweave`, `quintaction`, `quintctx`, `quintraw` (a grist effect's raw HTTP, gRPC or SQL calls) (`cmd/quintvet`, a golangci plugin); `cmd/actionmap` |
| `qgosim` (module) | qenv and whole programs in gosim (the fork, `github.com/chucklehead-dev/gosim`) |
| `grist` (module) | an event-sourced framework checked by its model ([the user guide](docs/grist-guide.md); design: [docs/grist.md](docs/grist.md)): `New`, `NewEvent`, `NewReadModel`, `NewKeyedReadModel`, `NewCommand` (conditioned on its `Reads`, checked against the App; `Unconditioned`; idempotent by `Envelope.ID`, a key's first answer final), `NewQuery`, `NewProcessor` (`Emit`, `Skip`, `Effect` with `AtLeastOnce` or `AtMostOnce`, `EffectKey`, from immutable `Meta.ID`), `Events`, options (`ConditionTries`, `TransientTries`, `EffectTries`, `BatchSize`), `Catalog`; every step recorded by the framework, each commit's from the store's step log (`Relay`, `WithRelay`) |
| `grist/store` | grist's store contract: `Store`, `Batch`, `Condition`, `Marker`, `Selector`, `StepLog` (a commit's step in its transaction, what grist records it from), the errors (`Permanent`), and the reference evaluations `CheckCondition` and `Select` |
| `grist/memstore`, `grist/simstore` | grist's stores: in memory for unit tests; over a `qenv.Store` for simulations |
| `grist/gristtest` | `StoredLogs` checks retained committed histories against today's app and model, without executing effects; `StoredLogsConfig` names fixtures and binding, and `StoredLogResult` retains typed stage evidence and native model reports ([workflow](docs/grist-guide.md#67-versions-and-snapshots-in-tests)) |
| `grist/sqlstore`, `grist/sqlstore/pgnotify` | SQL stores over SQLite, PostgreSQL and qsql; the optional PostgreSQL adapter wakes readers after transactional commit hints and reconnects automatically, with polling retained ([setup](docs/grist-guide.md#4-the-binary-gristmain)) |
| `grist/storetest` | the store contract suite (`Run`, `Checks`): conditions, reads and concurrent appends against brute force, checkpoints, markers, a waiter woken, copies, the step log, tenant isolation |
| `grist/gristsim` | `Run`: a grist app in a simulation as a `qtest.System`'s run (a node, a lossy API or its HTTP API on qhttp, clients recorded under `ClientsActor` beside the store's steps, named prerequisite `Phases` before them, two settles) |
| `grist` HTTP API, `grist/client`, `grist.Main` | `NewHandler` (commands under an `Idempotency-Key`, queries as GET, deny by default: `Authorize`, `Internal`), `client.Client` (calls it, recording each call as a client's; `Policy`, its retry decision for a load simulation), `Main` (an app's binary: `catalog` (`-diff deployed.json`: safe, readers first or breaking against the deployed release), `verify`, `serve` on sqlstore; `sizing` runs the app's sizing binary) |
| `grist/gristload` | sizing from an app's own steps (docs/grist-platform.md §8): `Measure` (service per command, query and declared call; growth with the log; the busiest tenant), `Report` (retries per layer, timeout, replicas, a hot tenant's limit, processors' lag, pool, grace, CPU, memory: each with reason, formula, tool, margin, caveat), `Main` (cmd/<app>-sizing; never in the app's binary) |
| `grist model`, `grist new`, `grist/gristtest` | the generator (an app's binary's `model` command, `grist.Main`): from the App's catalog (commands' `Refuses`, processors' `Emits` or `Delivers`, `NewSite`'s records) the model's skeleton and both bindings, the app writing only its rules and invariants; `go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new <name>`: an app module green on its first run; `gristtest`: `Composition.System` (one-history local, peer and protocol checks registered once and included in normal assertions and JSON/TUI receipts), `Generated` (the CI diff), `RuleMutants` (a rule's generated mutant, for `qtest.Mutants`), `NewTier` (the real tier on SQLite over HTTP, both bindings and the answers' cross-check), `Catalog` (the App's catalog as a `qtest.Catalog`: its `Exercised` the claim that the runs took every block, each command by each outcome), `Scenario` (an acceptance scenario in the catalog's values: `Command.With(args)`, `.Refused(err)`), `Upgrade` (a rolling update from the deployed release, both releases' nodes on one store; `ReadersFirst`, with `grist.WritesVersion` holding a new version's writes back); the bindings' generated `possible` entries, each command outcome and processor step reachable by the model on its own; declared calls (`NewDependency`, `NewCall`, `Keyed`, `Uses`, `Call.Do`, `Handlers`: the caller's view and the dependency's application both recorded by grist and modelled by the generator, gristsim serving each dependency as a `qenv.Service` from `Config.Doubles` or its default double, `DefaultHandlers`; on a real tier `OverHTTP` calling it, `ServeCalls` serving a dependency the team owns with its site recorded, `ServeDependency` beside an app's binary, `TierConfig.Serve` in a test); `NewDep` (typed dependencies that are no calls) |

## Example apps

Each one is a runnable Go module with a Quint model, a binding, mutants
that plant real bugs, a `run-demo.sh` (`MUTANT=<name>` plants one; it
exits 0 when it shows what its README says) and tests showing which
check catches which bug. [examples/README.md](examples/README.md) has
every one's row; they are grouped in tracks, each read in order:

| Track | Read in order | Pick it if |
|---|---|---|
| First steps | [`examples/hello`](examples/hello) → [`examples/bank`](examples/bank) → [`examples/checkout`](examples/checkout) | you are new: the whole loop on a counter, every workflow on a bank, a real service's faults and isolation levels |
| Services | [`examples/kvstore`](examples/kvstore) → [`examples/wallet`](examples/wallet) → [`examples/payd`](examples/payd), then [`examples/grpcledger`](examples/grpcledger), [`examples/streams`](examples/streams), [`examples/jobqueue`](examples/jobqueue), [`examples/lease`](examples/lease), [`examples/webjobs`](examples/webjobs) | you run an HTTP, SQL, gRPC, Kafka or queue-backed service, or a lock under clock faults, or a web app a browser drives |
| Distributed systems | [`examples/twophase`](examples/twophase) → [`examples/quorumkv`](examples/quorumkv) → [`examples/raftkv`](examples/raftkv) → [`examples/glomers`](examples/glomers) → [`examples/gosim`](examples/gosim) | your nodes agree, replicate or gossip |
| Kubernetes | [`examples/requeue`](examples/requeue) → [`examples/fleetrollout`](examples/fleetrollout) → [`examples/crossdb`](examples/crossdb) | you write controllers or Crossplane providers |
| grist | [`examples/counters`](examples/counters) → [`examples/library`](examples/library) → [`examples/wallet-grist`](examples/wallet-grist) → [`examples/payouts`](examples/payouts) → [`examples/inventory-grist`](examples/inventory-grist) | you build on grist ([the user guide](docs/grist-guide.md)), one service or several composing through declared calls |
| Migration | [`examples/migrate-payments`](examples/migrate-payments) → [`examples/edgepublish`](examples/edgepublish) | you have code, tests and logs but no model |
| Metastability | [`examples/retrystorm`](examples/retrystorm), then checkout's and crossdb's load tests; [`examples/sktrace`](examples/sktrace) turns production traces into load | you choose retry, timeout or requeue policies |
| Tutorials | [`docs/tutorials`](docs/tutorials): proofs over the state graph → liveness → scenarios → a grist app on Kubernetes → production at volume | you want a guided, step-by-step page with each command's real output |

```sh
examples/hello/run-demo.sh                       # simulated runs, then gotogether on their step logs
MUTANT=noDedupe examples/hello/run-demo.sh       # the mutant planted: caught, exit 0
examples/counters/run-demo.sh                    # a grist app; MUTANT=blind | dispatchDirect, SERVE=1, ROLLING=1
examples/twophase/run-demo.sh                    # the graph proved, then runs; MUTANT=earlyCommit | ..., MODEL=early-decision
examples/glomers/maelstrom.sh                    # under real Maelstrom, then gotogether on the nodes' step logs
examples/fleetrollout/kwok-demo.sh               # on three kwok clusters; MUTANT=ignoreObservedGeneration, KILL=1
examples/crossdb/kind/demo.sh                    # kind + Crossplane; MUTANT=noClientToken, DROP=0.3, KILL=1
```

## Agent skills and guidance

The skills in [`skills/`](skills) teach a coding agent this toolkit:
- `gotogether-start`: start here. Which tool for which question, a
  glossary, the first hour, and which skill to load next;
- `gotogether-model`: model conventions;
- `gotogether-mbt`: drivers;
- `gotogether-traces`: instrumentation, bindings and verdicts;
- `gotogether-hegel`: Hegel + Quint;
- `gotogether-patterns`: what to put in the model: the requirement
  rather than one solution, the environment's real guarantees,
  linearization points, ghost variables, settle-conditioned properties,
  mechanism models with a lemma;
- `gotogether-workflow`: the order of work, model first (scenarios with
  possibility checks, safety, liveness, then code, bind, mutants, Hegel,
  the real environment) and triage of verdicts;
- `gotogether-migration`: an existing system brought under a model: a
  bounded context, requirements with their provenance from code, tests,
  logs and traces, the existing runs judged before any change;
- `gotogether-faults`: seams, `qenv` deployments, contract profiles,
  `System.Explore`, shrinking with `qhegel.Choices`, and the real environment;
- `gotogether-metastability`: retry storms and other metastable failures: the
  CTMC, the load simulation, trigger search, mitigations.
- `grist`: an event-sourced app on grist, its model generated from its
  catalog: blocks, holes, `gristsim.System` explorations, mutants.

Each SKILL.md is short: a "choose your path" list, steps, checklists, a
results table (each report line, what to do next) and "run it, do not
infer it"; its mechanics are in its `references/`, each linked with when
to read it.

They are in the [Agent Skills](https://agentskills.io) format, one
`SKILL.md` per directory. In this repository they are exposed where agents
look for them: `.agents/skills/`, the vendor-neutral location read by
Cursor, VS Code/Copilot, Codex and others, and `.claude/skills/` for Claude
Code. Install them into your own project, or for your user:

```sh
gotogether skills install                     # .agents/skills and .claude/skills
gotogether skills install -target all         # also .github/skills and .cursor/skills
gotogether skills install -user               # under $HOME instead of the project
```

Repository guidance for agents is in [AGENTS.md](AGENTS.md); `CLAUDE.md`
imports it. How the toolkit is put together (its modules, layers, the
path of a check, its formats, where to start reading) is
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Limits

- **Hidden nondeterminism.** A failure that may or may not have taken
  effect needs the implementation to know which (`QuintOutcome()`).
  Branching replay (Apalache) is not built.
- **Cost of trace checks.** Trace validation is post hoc and bounded by
  run length. A run that passes is decided by the Go evaluator
  (internal/qeval, milliseconds after one `quint compile` per binding,
  for models in its fragment); a failing one is one `quint test` process
  (~0.5–1 s), whose report it is (`GOTOGETHER_EVAL=quint`: quint for
  every run).
- **`connect.Test`.** `quint test` traces carry no action annotations,
  so `connect.Test` needs `NondetPath`.
- **Conversion.** Converted spans cannot reveal lost spans.

More in [DESIGN.md](docs/DESIGN.md) §5 and §9.

## Environment variables

| Variable | What it does |
|---|---|
| `GOTOGETHER_RUN='TestX#2:seed=n'` | runs one run of one exploration alone (the test's second here), as a failing run's report prints it: `:seed=n`, `:schedule=i:v,...` (`System.Exhaust`) or `:corpus=<file>`; only claims about every run are judged |
| `GOTOGETHER_CORPUS=dir` | every exploration replays and saves corpus entries under dir/<test> (ExploreConfig.Corpus) |
| `GOTOGETHER_HEGEL_SEED=n` or `random` | qhegel's searches (FindViolation, Shrink) use this seed instead of the test's own, logged (the nightly run's) |
| `GOTOGETHER_SEEDS=n`, `GOTOGETHER_SEED_OFFSET=k` | runs n seeds from k+1 instead of the test's own: a wider, fresh sample (the nightly workflow) |
| `GOTOGETHER_KEEP=dir` | keeps generated Quint modules, step logs and ITF there, by test (without it they are in the test's temporary directory, and a failure's report names copies it made under `$TMPDIR/gotogether-*`) |
| `GOTOGETHER_SESSION=id` | fresh ID per suite invocation, shared by test processes and `gotogether evidence` (or `-session id`); completeness checks exclude older/unidentified evidence. Use `go test -count=1`. `-history` explicitly aggregates historical evidence with freshness unknown |
| `GOTOGETHER_EVIDENCE=dir` | each exploration's evidence, as `dir/<package>/<test>/x<N>.json` (`gotogether evidence dir`: what no faithful exploration tried; `cover -sim`), and each Validate and Record run's manifest, `run<N>.json`, its files (step log, binding, spec and imports, artifacts) bundled in `run<N>/` so the directory replays elsewhere (`validate -replay`; listed as external). The step logs are whole and unsanitized: mind where the directory is uploaded |
| `GOTOGETHER_STRICT=1` (or `true`) | every toolkit skip fails instead, with its message (`RequireQuint`, `Mutate` with no mutants woven, `SkipIfEmpty`), as does `qtest.Optional(t, reason)`, a test's own skip: for CI jobs that install what their tests need. `gotogether evidence -expect checks.txt` fails on a listed check that wrote no evidence |
| `GOTOGETHER_VERBOSE=1` | shows the whole report of each catch a test expects (three lines otherwise), the evaluator's and quint's counts behind each summary, and Hegel's own output for failures a test expects |
| `GOTOGETHER_PROGRESS=0` | no progress lines: a long exploration otherwise writes one to the terminal every 10 s (go test holds a package's output back until it ends) |
| `GOTOGETHER_CHECK=1` | the simulation's scheduler checks its invariants at every step, every `Store` each commit's dependency cycles against its isolation level, every `Queue` its contract after each operation |
| `GOTOGETHER_SALT` | the salt of `gotogether steps -pseudonymize` |
| `GOTOGETHER_STEPS_DIR` | where the Glomers nodes write their step logs under Maelstrom |
| `QUINT_BACKEND` | `rust` or `typescript`, for connect, validate and qtest alike |
| `QUINT_BIN`, `QUINT_SEED`, `QUINT_VERBOSE`, `QUINT_MAX_SAMPLES`, `QUINT_MAX_STEPS` | connect's `quint run`, as in quint-connect |

## License

MIT (LICENSE). `examples/twophase/model` vendors Choreo, under the
Apache License 2.0 (its `LICENSE.choreo`); the gosim fork is MIT, as
upstream gosim.
