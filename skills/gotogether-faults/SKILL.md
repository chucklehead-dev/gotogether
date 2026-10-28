---
name: gotogether-faults
description: Test Go services against a Quint model under deterministic fault injection with gotogether's qenv - the app written against seams (qenv.DB/Txn, services, qenv.Go/Now/Sleep), deployed into a simulation (crashes, a store at three isolation levels, an at-least-once queue, lossy RPC, fake Kubernetes API servers), explored by seeds (System.Explore, Exhaust, Sweep, Fuzz) or shrunk by Hegel, then carried to a real environment. Use when correctness depends on retries, idempotency, outboxes, isolation levels, crashes, status lag or lost responses, or a bug shows only in a real environment.
---

# Fault injection with qenv

A fault is a choice the environment's contract allows (a lost response
after the effect, a redelivery, a snapshot read, a crash between two
statements). qenv makes each through one `Chooser`, option 0 nominal, so
a run replays from its choices and Hegel's shrinking removes faults.

## Choose your path

- Writing the app, or a step lands in the wrong place: [references/seams.md](references/seams.md).
- Deploying (restarts, settles, stuck runs, quorums, disks, clocks, partitions): [references/simulation.md](references/simulation.md).
- Exploring, scripting a fault, reproducing, rates, shrinking: [references/explore.md](references/explore.md).
- An HTTP, gRPC or Kafka/Kinesis service: [references/services.md](references/services.md); database/sql: [references/sql.md](references/sql.md).
- Code you cannot change: [references/weaving.md](references/weaving.md); a whole program: [references/gosim.md](references/gosim.md).
- Your functions as fault points: [references/fault-points.md](references/fault-points.md); sweeps, fuzzing, coverage and mutants: [references/sweeps.md](references/sweeps.md), [references/fuzzing.md](references/fuzzing.md), [references/coverage-and-mutants.md](references/coverage-and-mutants.md).
- Production workloads, a real deployment's faults: [references/workloads.md](references/workloads.md), [references/real.md](references/real.md), [references/beyond.md](references/beyond.md).

## Steps

1. Write against seams: `qenv.DB`/`Txn` (`GetForUpdate` for a row read
   then written), `qenv.Table[T]`, interfaces over `qenv.Queue` and
   `qenv.Service`, `qenv.Go`/`Now`/`Sleep`, maps in sorted order.
2. Record each step where its effect happens (`tx.OnCommit`, the service).
3. One `Run(chooser, Config)`: `qenv.New`, doubles, `s.Process`,
   `s.Run(d)`, `return s.Settle(d)`.
4. Explore it as a `qtest.System`: `sys.WithRates(r).Explore(t,
   qtest.ExploreConfig{Seeds: 30, ShortSeeds: 5}).Expect(t,
   qtest.Conforms(), qtest.Reaches("rpc.x.request").Faulted())`.
5. A mutant per claim; shrink its failure: `qhegel.Shrink(t, sys)`.
6. Validate the real environment's step logs with the same binding.

## Checklist

- [ ] `gotogether lint -sim .` is clean (no `go`, wall clock, rand, selects).
- [ ] Every crashable process rebuilds from durable doubles only.
- [ ] A task that waits forever by design calls `qenv.Daemon(ctx)`.
- [ ] Each fault the system must survive is claimed `Reaches(...).Faulted()`.
- [ ] The faithful system conforms and settles; each mutant is caught.

## Results

| Report says | Means | Next |
|---|---|---|
| `N runs, N conform`, the claims holding | the faults claimed happened and the model accepts every run | plant a mutant; a check no mutant fails proves nothing |
| `K reject <action>` (`Rejects`) | a step the model forbids | read `why:`, then `navigate:`; the code or the binding |
| `K show a mismatch after <action>` (`Mismatch`) | an observed state the model did not expect | the `obs.*` value, or where it is recorded |
| `K violate <inv>` (`Violates`) | the design allows the bad run | fix the design, or the property is too strong |
| `qenv: stuck`, naming tasks (`ErrStuck`) | a task waits with nothing to wake it | the lost wake-up is the bug, or mark a daemon |
| `K do not settle` (`qenv: not settled after ...`) | work never finished | a livelock (`None(qtest.Settles())` states it), a longer settle, or a fix |
| `steps were recorded from goroutines ... started with go` | escaped the simulation | `qenv.Go` |
| `ran twice made two runs, first different at ...` | nondeterminism outside the Chooser | `lint -sim`, sorted iteration |
| `scripted faults never happened` | a Script's fault had no choice of its kind | the fault's kind and the run's phase |
| `reproduce: GOTOGETHER_RUN=...` | the failing run alone | rerun it; `-v` |
| panic: `"..." alone is a claim over runs with no quantifier` | an expectation over runs without `Some`, `All` or `None` (only `Conforms`, `Settles`, `Completes`, `Finishes` stand alone) | write the quantifier meant; alone it meant the one the panic names |

Every other verdict: [../gotogether-workflow/references/verdicts.md](../gotogether-workflow/references/verdicts.md).

## Run it, do not infer it

Do not argue about whether a fault can happen or what a run did: run the
exploration, reproduce the failing run with its `reproduce:` line, and
walk its choices with `gotogether nav -run 'TestX#2:seed=17' -at fail
-json`.
