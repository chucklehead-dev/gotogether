# gotogether

Test Go code against [Quint](https://quint-lang.org) specifications: the
model drives the code, judges what it did, or both, in tests, in a
simulated environment full of faults, and in production.

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
(`model/hello.qnt`). One test plants a bug (a *mutant*: the counter
forgets to dedupe) and expects the model to reject a step: its output's
`as expected` blocks are those failures, not the test's.
[Hegel](https://hegel.dev), a property-based tester, then shrinks that
failure to the single fault it needs.

Read `hello.go` and `hello_test.go` (130 lines together), then pick a
workflow from the table below. Agents start from the `gotogether-start`
skill (`gotogether skills install`).

### Words

- **Model**: a Quint specification of what the system may do; the
  ground truth.
- **Step**: one model action the code took, recorded with its arguments
  and what the code observed (`qobs.Record(ctx, "deposit", err, "amount",
  5, "obs.balance", 35)`).
- **Actor**: one sequence of steps checked as one run of the model (a
  process, a node; `qobs.WithActor`). Reports begin `actor <name>:`.
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
- **Mutant**: a planted bug a test expects the checks to catch.
- **Evidence**: what an exploration reached (the model's actions, fault
  kinds and options, code).
- **Corpus**: saved failing runs, replayed before the seeds.
- **Sweep**: configurations chosen by a covering array.

## What it does

gotogether is the Go counterpart of
[quint-connect](https://github.com/quint-co/quint-connect) (model-based
testing, where Quint traces drive the implementation). It adds:
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
| Does it recover after a spike or a slow dependency? | `qmeta`, `qload`, `qhegel.FindTrigger`, alert rules from the chain |
| My model is in Choreo | named transitions over `choreo::step_with`, then any of the above ([examples/twophase](examples/twophase)) |

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
```

In a module of your own (the examples' `replace` lines are for working
inside this repository):

```sh
go mod init example.com/app
go get github.com/chucklehead-dev/gotogether@latest github.com/chucklehead-dev/gotogether/qhegel@latest
gotogether scaffold -spec model/app.qnt -module app -out app.binding.yaml   # then set its constants
gotogether scaffold -system -binding app.binding.yaml -pkg app -out system_test.go
```

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
```

```sh
gotogether validate -binding bank.binding.yaml steps.jsonl                          # step log, OTLP/JSON or ITF
gotogether validate -binding bank.binding.yaml -convert bank.convert.yaml spans.jsonl   # an app's plain spans
```

```
actor default: 10 steps
  conformance: FAIL
    after step 3 the model's state does not match what was observed
      quint:    deposit("carol", 2)
      expect:   balances.get("carol") == 4
      observed: deposit(account=carol, amount=2) (seq 3)
      obs:      balance=4
```

Reconstruction orders steps from many recorders. It uses dense sequence
numbers (so a lost step is reported), program-order threads, domain
clocks, and process incarnations, which become the model's restart
action. See [docs/instrumentation.md](docs/instrumentation.md) and
[skills/gotogether-traces](skills/gotogether-traces/SKILL.md).

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
a mutant that forgets to dedupe. The deployment (`Counter.Add` records
`apply(id)`, or `replay(id)` for a request it has seen; `Send` retries
until it hears back):

<!-- from examples/hello/hello.go -->
```go
func Run(c qenv.Chooser, m Mutation) error {
	s := qenv.New(c)
	defer s.Close()
	counter := &Counter{Mutation: m, seen: map[int]bool{}}
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

and its tests: the system bound to the model, explored by seeds, and shrunk
by Hegel to the one fault the bug needs:

<!-- from examples/hello/hello_test.go -->
```go
func system(m Mutation) qtest.System {
	s := qtest.System{Binding: "hello.binding.yaml", Run: func(c qenv.Chooser) error { return Run(c, m) }}
	if m == NoDedupe {
		s.Mutant = "NoDedupe" // its runs are not the counter's: the suite's evidence leaves them out
	}
	return s
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

func TestNoDedupeCountsRetries(t *testing.T) {
	system(NoDedupe).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: lossy}).
		Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()))
}

func TestHegelShrinks(t *testing.T) {
	qhegel.Shrink(t, system(NoDedupe), qenv.DefaultRates, hegel.WithTestCases(50), hegel.WithSeed(1)).
		Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
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

The app is written against seams: `qenv.DB` and `qenv.Txn`, its own
broker interfaces, `qenv.Go`, `qenv.Now` and `qenv.Sleep`. Production
puts real clients behind them. Steps are recorded where effects happen:
`Txn.OnCommit`, a fake API server's interceptor, the provider. Then:

```go
sys := qtest.System{Binding: "checkout.binding.yaml", Run: func(c qenv.Chooser) error {
	_, err := checkout.Run(c, checkout.Config{Isolation: qenv.SnapshotIsolation, Mutation: shop.LockWithoutWrite})
	return err
}}
skew := qtest.AnyOf(qtest.Rejects("ship"), qtest.Rejects("cancel"))
sys.Explore(t, qtest.ExploreConfig{Seeds: 100}).Expect(t, skew)     // seeds, checked in one batch
qhegel.Shrink(t, sys, qenv.DefaultRates, hegel.WithTestCases(50)).Expect(t, skew)  // Hegel, shrunk to its interleaving
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
  in parallel.

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
| `connect` | the model drives Go: `Run`, `Test`, `Switch`, `Pick[T]`, `StateDriver`, `GenerateRun`; `FaultScript` (a trace's faults imposed on a whole program in qenv) |
| `qhegel` (module) | Hegel and the model: `Test` (modes `Each`, `After`, `Batched`, `Off`), `FindViolation` (an expected failure, shrunk), `Shrink` (a `qtest.System` under Hegel), `NewCase`, `Choices` (qenv's choices from a test case), `VaryTraces`, `FindTrigger` |
| `itf` | ITF traces: `Parse`, `Decode`/`Encode` (Go ⇄ Quint values), `ParseQuint` (Quint literals) |
| `qobs` | the recorder: `Record`, `Thread`, `WithRecorder`, `SlogHandler`; sinks `MemorySink`, `FileSink` and `NewJSONLSink` by durability (`PerStep`: survives a killed process; `Synced`: an fsync per step, a power loss; `Buffered`), `OTelSink`, `Tee`; `SetGlobal` for a program, `InstallGlobal` for a test's run; `orchestrion.yml` |
| `qtrace` | the step schema, `Reconstruct` (order, gaps, restarts, real-time order from `Invoked`), step logs (`ReadJSONL` keeps a torn tail's steps: `TornTail`), `Linearize`, `Filter` (select and pseudonymize) |
| `otelio` | an OTLP/JSON file exporter and reader; span events to steps |
| `convert` | an application's own spans to steps, by YAML rules |
| `binding` | the YAML binding: lint against `quint typecheck`, the plan, the generated Quint module and ITF; `ScanRecords`/`LintRecords` |
| `validate` | replay with `quint test`: `Checker` (`Check`, `CheckBatch`), `Cached`, `ValidateITF`; structured verdicts |
| `qtest` | tests that check against the model: `System` (`Explore`, `Exhaust`, `Sweep`, `Fuzz`, `Mutate`, `FollowTraces`), `Validate`, `Record`; claims (`Conforms`, `Rejects`, `Mismatch`, `Violates`, `AnyOf`; over runs `Some`, `All`, `None`; `Reaches`, `Takes`, `Exercised`, `NotVacuous`; `Linearize` for histories recorded by clients; `Violates(...).At`; a shrunk run's `ShrunkTo`); `Evidence`; options (`Constants`, `Invariants`, `Quint`, `Global`) |
| `qenv` | deterministic simulation: scheduler, virtual time, crashable processes, choosers (`NewSeeded`, `Replay`, `Script`, `Swarm`, `FromBytes`); doubles `Store` (isolation levels), `Queue`, `Service` (with `Capacity`), `Disk` (fsync, power loss, torn writes); the network (`Sim.Net`: `Partition`, `Heal`, `PartitionNemesis`); clocks (`Sim.Clocks`: skew, jumps, drift; `Now`, `Mono`, `Since`, `WaitFor`); nemesis families (`nemesis.crash`, `.partition`, `.jump`); `DeclareFaults`, `FaultPoint`, `CoverageOf`; `NewNative` (the same on goroutines, for gosim); `TimeProfile` |
| `qenv/weave` | Orchestrion aspects: `sync` and `//qenv:go` goroutines simulated inside qenv, real outside |
| `qenv/fault` | the `//quint:fault` aspect: a function's entry a fault choice, with its own errors |
| `qload` | load on qenv: `Experiment` (open- or closed-loop), `Trigger`, `Recovers`/`Assess`/`Mostly` with `Recovered`/`StaysDegraded`, `Target`, `Simulate` (an abstract server and its clients), `Piecewise` shapes |
| `qmeta` | the CTMC: `New`, `HittingTimes`, `RecoveryWithin`, `Mean`, `Calibrate`/`CalibrateTimeout`, `Measure`; alerts (`Region`, `Rules`), `Watch` (the flight recorder on alert); imports nothing of the module |
| `qrole` | load roles (`//quint:serve`, `//quint:operation`, `//quint:call`): u and v as OTel gauges, pprof labels |
| `qcover` | code coverage by model step (woven by `qweave -cover`): `Summarize`; a real environment's coverage against a simulation's (`ReadGoCover`, `Compare`) |
| `qmut` | mutant schemata woven by `qweave -mutate`, one build, a mutant per run |
| `qsweep` | covering arrays: `Cover`, `Missing`, `Sequences`, `Factor.Constant` |
| `qrun` | a run's state (`Key[T]`), from the context or the goroutine's run |
| `qlint` | the simulation and weaving lints (`gotogether lint -sim`, `-weave`) |
| `input` | reads a trace input (`Read`): OTLP/JSON, a step log or ITF, detected by content |
| `skills` | the agent skills below, embedded, and their install |
| `cmd/gotogether` | the CLI: `scaffold`, `lint`, `simulate` (the model alone: which invariants it guarantees, which the code must; `-verify N`, Apalache to depth N), `steps`, `validate`, `itf`, `convert`, `cover`, `evidence` (a suite's explorations, and what no faithful one tried; `-strict` for CI), `skills` |
| `qweave` (module) | weaves Orchestrion aspects into a `go -overlay`: `qweave go test ./...`, `-cover`, `-mutate` |
| `qanalysis` (module) | analyzers `quintsim`, `quintweave`, `quintaction` (`cmd/quintvet`, a golangci plugin); `cmd/actionmap` |
| `qgosim` (module) | qenv and whole programs in gosim (the fork, `github.com/chucklehead-dev/gosim`) |

## Example apps

Each one is a runnable Go app with a Quint model, a binding, mutants that
inject real bugs, a `run-demo.sh` and tests showing which check catches
which bug.

| App | What it is | What it shows |
|---|---|---|
| [`examples/hello`](examples/hello) | the smallest simulated system: a counter behind a lossy network, a client that retries (80 lines) | one `qtest.System` explored by seeds; `NoDedupe` caught only when a fault makes the client retry |
| [`examples/lease`](examples/lease) | a lock that grants leases, the store it guards, two holders, and clock faults (each process's clock skewed, jumping) | a bug no test with true clocks can see: without fencing a clock jump lets an old holder write after its successor, and comparing the lock's expiry with the holder's clock breaks on a skew alone; one jump is enough (`System.Exhaust`) |
| [`examples/grpcledger`](examples/grpcledger) | separate module: a gRPC ledger (idempotent transfers) with generated code, simulated through `simgrpc` (generated client and server unchanged; lost/duplicated requests, lost responses, refused connections, crashes) and served for real (otelgrpc spans converted to steps) | `NoDedupe` only with faults, `NoFundsCheck` from any load, `SplitTxn` (lost update) as a `Mismatch`; sweep of which faults expose the missing dedupe; Hegel shrink to one duplicated request |
| [`examples/streams`](examples/streams) | separate module: an outbox publisher and idempotent consumers on Kafka (franz-go, kfake) and Kinesis (AWS SDK, in-process fake), and a simulated partitioned log with lost produces/commits, rewinds, evictions mid-batch and crashes; one model and binding for all three | `commit-first`, `no-dedupe`, `non-idempotent-producer`, `off-by-one` |
| [`examples/twophase`](examples/twophase) | two-phase commit: a coordinator and participants with durable stores over a lossy network, checked against a [Choreo](https://github.com/informalsystems/choreo) model | a Choreo model with named transitions (`choreo::step_with`) bound, explored, shrunk and driving the code (connect); a coordinator that commits early, a participant that gives up after voting, a decision kept in memory |
| [`examples/checkout`](examples/checkout) | a checkout saga: orders, payments with idempotency keys and voids, shipping, cancellation, a transactional outbox | fault injection with `qenv`: crashable services, a database at three isolation levels, an at-least-once broker, a lossy network; which bug is a bug at which isolation level (lost update, write skew); a double charge; a lost event; Hegel shrinking a write skew; under load, too many payment consumers collapse on their own, a retry budget is bypassed by broker redelivery, deadline propagation recovers, and idempotency keys hold through the storm |
| [`examples/fleetrollout`](examples/fleetrollout) | a multicluster-runtime controller rolling a Deployment across member clusters within a budget (own module) | the same controller simulated (fake clients behind interceptors: status lag, lost responses, crashes) and on three real kwok clusters (`kill -9` included), checked by one evidence-based model; `observedGeneration` and in-memory-state bugs caught in both; on kwok, pods can be timed by a production trace (`TRACE=` in kwok-demo.sh: kwok Stages from its pod lifetimes) |
| [`examples/crossdb`](examples/crossdb) | a Crossplane provider for a database-as-a-service (crossplane-runtime's managed reconciler), with connection details written back as Secrets (own module) | Crossplane's real reconcile logic simulated under lost responses, crashes, throttling and an eventually consistent cloud, then the same provider on kind with full Crossplane composing `AppDatabase`s; why the external name must be deterministic and the create idempotent; a leak with no crash; translating a library's real-clock timestamps at the seam; the provider's requeue policy against a capacity dip in the cloud (`qload`, closed loop) |
| [`examples/retrystorm`](examples/retrystorm) | a client retry library: bounded retries, backoff with jitter, a retry budget | metastable failures in tiers: a CTMC predicts which policies recover from a spike, a simulation (the library on virtual time) confirms it, and Hegel shrinks the smallest trigger; backoff alone made things worse; alerting rules from the chain calibrated on the app's own load roles; the budget's accounting checked against a Quint model, which catches a bug load tests barely see |
| [`examples/glomers`](examples/glomers) | Fly.io's Gossip Glomers: unique ids, broadcast, grow-only counter, kafka-style log, on a Maelstrom runtime and simulator | distributed systems: models of each challenge with their properties; Lamport clocks to merge per-node step logs; Hegel scheduling deliveries, losses and partitions, and shrinking a lost-update bug to three steps; the same nodes under real Maelstrom, their logs conforming to the models |
| [`examples/kvstore`](examples/kvstore) | an HTTP key-value service with compare-and-swap (`kvserver`, `kvload`) | a concurrent service checked as a serial behaviour of its model, from its steps or its plain OTel spans (`kv.op` as a linearization key); a CAS race caught under load; errors that classify themselves; reads checked against the model; Hegel over HTTP |
| [`examples/jobqueue`](examples/jobqueue) | an at-least-once job queue with leases, workers and restarts (`jobqueue`) | all three verdicts: an implementation bug, a design property the run violates (`exactlyOnce`, checked with `-invariants`), and conformance; restarts inferred from incarnations; when model-driven testing does not fit |
| [`examples/bank`](examples/bank) | an in-memory bank (library) | every workflow on one small system: what model-driven testing cannot catch and Hegel + the model can |
| [`examples/edgepublish`](examples/edgepublish) | the spike's real case: an OTel exporter's publish protocol with S3 faults and crashes (own module, Orchestrion) | `//quint:action` annotations; three trace paths with identical verdicts; MBT with gated effects; model → Go → telemetry → model; the publisher, unchanged, in a qenv simulation by weaving its locks and goroutines (`qenv/weave`), where exploration found a race the real exporter had already fixed |
| [`examples/gosim`](examples/gosim) | the Glomers broadcast node over real TCP on three gosim machines, and the checkout saga's deployment on qenv's native runtime, both unchanged (own module, `qgosim`) | whole-program simulation whose every choice (schedule, `select`, map order, faults) comes from a qenv `Chooser` in the test process; runs checked against the same models and bindings; exact replay; `System.Explore` over parallel gosim binaries; Hegel shrinking fire-and-forget gossip's lost value; a mutant's second symptom that gosim's schedules reach |

```sh
examples/glomers/maelstrom.sh                   # under real Maelstrom, then gotogether on the nodes' step logs
examples/kvstore/run-demo.sh                    # MUTATION=stale-cas | revision-on-conflict
examples/jobqueue/run-demo.sh                   # MUTATION=ack-any-lease | forget-done
examples/grpcledger/run-demo.sh                 # a real grpc.Server, otelgrpc spans converted; MUTATION=no-dedupe | no-funds-check
examples/streams/run-demo.sh                    # Kafka (kfake), Kinesis (fake endpoint), simulated log; MUTATION=commit-first | no-dedupe | ...
examples/twophase/run-demo.sh                   # MUTATION=early-commit | impatient | volatile-decision
examples/lease/run-demo.sh                      # MUTATION=no-fencing | wall-interval | absolute-expiry, CLOCKS=1 for skews and jumps, DRIFT=0.25
examples/checkout/run-demo.sh                   # MUTATION=no-locks | lock-without-write | no-idempotency-key | dual-write
examples/fleetrollout/kwok-demo.sh              # MUTATION=ignore-observed-generation | in-memory-progress, KILL=1
examples/crossdb/kind/demo.sh                   # kind + Crossplane; MUTATION=no-client-token | generated-external-name | password-from-create, DROP=0.3, KILL=1
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
- `gotogether-workflow`: the order of work (model, simulate,
  record, bind, mutants, Hegel, real environment) and triage of verdicts;
- `gotogether-faults`: seams, `qenv` deployments, contract profiles,
  `System.Explore`, shrinking with `qhegel.Choices`, and the real environment;
- `gotogether-metastability`: retry storms and other metastable failures: the
  CTMC, the load simulation, trigger search, mitigations.

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
imports it.

## Limits

- **Hidden nondeterminism.** A failure that may or may not have taken
  effect needs the implementation to know which (`QuintOutcome()`).
  Branching replay (Apalache) is not built.
- **Cost of trace checks.** Trace validation is post hoc and bounded by
  run length. Each check is one `quint test` process (~0.5–1 s).
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
| `GOTOGETHER_KEEP=dir` | keeps generated Quint modules, step logs and ITF there, by test |
| `GOTOGETHER_EVIDENCE=dir` | each exploration's evidence, as `dir/<package>/<test>/x<N>.json` (`gotogether evidence dir`: what no faithful exploration tried; `cover -sim`) |
| `GOTOGETHER_VERBOSE=1` | shows Hegel's own output for failures a test expects |
| `GOTOGETHER_CHECK=1` | the simulation's scheduler checks its invariants at every step, every `Store` each commit's dependency cycles against its isolation level, every `Queue` its contract after each operation |
| `GOTOGETHER_SALT` | the salt of `gotogether steps -pseudonymize` |
| `GOTOGETHER_STEPS_DIR` | where the Glomers nodes write their step logs under Maelstrom |
| `QUINT_BACKEND` | `rust` or `typescript`, for connect, validate and qtest alike |
| `QUINT_BIN`, `QUINT_SEED`, `QUINT_VERBOSE`, `QUINT_MAX_SAMPLES`, `QUINT_MAX_STEPS` | connect's `quint run`, as in quint-connect |

## License

MIT (LICENSE). `examples/twophase/model` vendors Choreo, under the
Apache License 2.0 (its `LICENSE.choreo`); the gosim fork is MIT, as
upstream gosim.
