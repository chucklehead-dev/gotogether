---
name: gotogether-faults
description: Test Go services against a Quint model under deterministic fault injection with gotogether's qenv - the app written against seams (qenv.DB/Txn, services, qenv.Go/Now/Sleep), deployed into a simulation (crashes, a store at three isolation levels, an at-least-once queue, lossy RPC, fake Kubernetes API servers), explored by seeds (System.Explore, Exhaust, Sweep, Fuzz) or shrunk by Hegel, then carried to a real environment. Use when correctness depends on retries, idempotency, outboxes, isolation levels, crashes, status lag or lost responses, or a bug shows only in a real environment.
---

# Fault injection with qenv

A fault is a choice the environment's contract allows (a response lost
after the request took effect, a redelivery, a snapshot read, a crash
between two statements). qenv makes every such choice through one
`Chooser`, option 0 always nominal, so a run replays from its choices and
Hegel's shrinking removes faults.

## Steps

1. Write the app against seams: `qenv.DB`/`Txn` (`GetForUpdate` for a
   row read then written), `qenv.Table[T]` for typed records, small
   interfaces over `qenv.Queue` and `qenv.Service`, `qenv.Go`/`Now`/`Sleep`,
   maps iterated in sorted order. `gotogether lint -sim .` flags escapes.
2. Record each step where its effect happens: `tx.OnCommit` for a
   commit, the service for its own operations, `s.Context()` from hooks.
3. Write one `Run(chooser, Config)` deploying the whole system:
   `qenv.New`, the doubles, `s.Process` (crashable, rebuilt from durable
   doubles on restart), `s.Run(d)`, then `return s.Settle(d)`.
4. Make it a `qtest.System` (`gotogether scaffold -system`) and explore:
   `sys.WithRates(r).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 5}).Expect(t, qtest.Conforms(), qtest.Reaches("rpc.x.request").Faulted())`.
5. Plant a mutant per claim (`testdata/mutants/<name>`, `qtest.Mutants`)
   and state how it shows (`Some(Rejects(...))`, `Violates`, `Mismatch`).
6. Shrink a failure to the faults it needs: `qhegel.Shrink(t, sys)`;
   reproduce one run with its report's `reproduce:` line.
7. Carry the model to the real environment: the same binding judges
   step logs from kwok, kind, Maelstrom or `qnemesis`'s faults.

## References (when to read each)

- [references/seams.md](references/seams.md): before writing the app,
  or when a step lands in the wrong place.
- [references/simulation.md](references/simulation.md): deploying,
  restarts, settles, stuck runs, quorums, disks, upgrades, clocks,
  partitions, pauses, rates per instance.
- [references/explore.md](references/explore.md): Explore, Exhaust,
  Scripts, corpora, reproducing, rates, shrinking, profiles.
- [references/beyond.md](references/beyond.md): the real environment,
  third-party controllers, what the examples found.
- [references/services.md](references/services.md) (HTTP, gRPC,
  Kafka/Kinesis), [references/sql.md](references/sql.md) (database/sql
  over a Store), [references/fault-points.md](references/fault-points.md)
  (your own functions as fault points),
  [references/sweeps.md](references/sweeps.md),
  [references/fuzzing.md](references/fuzzing.md),
  [references/coverage-and-mutants.md](references/coverage-and-mutants.md),
  [references/weaving.md](references/weaving.md) (code you cannot
  change), [references/gosim.md](references/gosim.md) (a whole
  program), [references/workloads.md](references/workloads.md),
  [references/real.md](references/real.md) (`qnemesis`).
- [../gotogether-workflow/references/verdicts.md](../gotogether-workflow/references/verdicts.md):
  every verdict and claim.
