# quorumkv: a replicated store, live and linearizable under faults

Three replicas, each a crashable process with a durable store, and a
client library that coordinates every operation over quorums, as in ABD
for many writers ([quorumkv.go](quorumkv.go)): a put reads R=2 replicas
for the newest version, then writes the next one to all three and waits
for W=2 acknowledgements, leaving a hint for a replica it could not
reach, which a handoff daemon delivers; a get reads R replicas and
writes the newest value it saw back to W before it answers.

It shows the checks a quorum system needs beyond "every step conforms":

| file | what it is |
|---|---|
| [model/quorumkv.qnt](model/quorumkv.qnt) | a linearizable register per key (`write`, `read`), and `observe`: what a probe found at a replica after the system settled; the eventual val `converged` says every replica observed holds the register |
| [quorumkv.binding.yaml](quorumkv.binding.yaml) | a read's observed value is its key's register; `eventually: [converged]` |
| [quorumkv.go](quorumkv.go) | the replicas, the cluster (`Put`, `Get`, `Handoff`), `Run` on a qenv simulation, then the settle and the probe |
| [quorumkv_test.go](quorumkv_test.go) | one `qtest.System` per configuration, explored |

Clients record each call when it is invoked and when it answers
(`qobs.Invoke`, `Call.Done`), its interval in the simulation's virtual
time. Each round trip takes a millisecond of virtual time (`RoundTrip`), as a
network's does: a nominal simulated call takes none, and a quorum's calls
would land in one instant. Every client records into the run's recorder,
whose records order the calls exactly (a call that completed before
another's invocation record precedes it), with no clock involved. The
history is a client's, so it is checked linearized, key by key
(`qtest.LinearizeByKey("key")`: keys are independent in the model). The
probe runs after `Settle`, and records its own settle (`qobs.Settled`) so
the eventual val is judged.

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestFaithful` | none | 10 seeds, one message fault in ten, crashes | every run conforms, settles and completes every call |
| `TestQuorumWithOneDown` | none, then `W3`: a put waits for all three replicas | r3 down for good, `Sim.SettleCore(r1, r2)` (faults go on outside the core) | with W=2 every run settles; with W=3 none does (`None(Settles())`): safe, never live |
| `TestLeakedPut` | `Leak`: a put given up after one failed round, its caller never told | the same exploration | some run leaves a `write` pending (`Some(Pending("write"))`); no step says what it did |
| `TestNoHandoff` | `NoHandoff`: hints dropped | 20 seeds, three message faults in ten, each client ending on a write (a read after it would write back and repair the replica) | every read conforms (quorum reads mask it), and some run violates `eventually converged`: a replica kept the old value (2 runs in 20) |
| `TestBlindRepair` | `BlindRepair`: a get answers without its write-back | one key, one writer and two readers, PCT schedules (`qenv.NewPCT`, depth 2) | some read is a stale read no order explains (`Mismatch("read")`): one reader saw a write that had reached one replica, a later one did not (4 runs in 20) |
| `TestRepick` | `Repick`: a retried put picks a new version | one contended key, seeds | some read mismatches: the value landed twice, around a later write |

`Repick` is the bug of this store's first version, which its first
explorations found: a history no order of its key explains.

## How the checks are written

**Calls, at invocation and answer.** A client records the call when it
starts and the step when it answers; the invocation record's time is the
call's start, and every client records into the run's recorder, so the
history's real-time order is exact:

```go
call := qobs.Invoke(ctx, "read", "key", key)
// ... quorum reads, the write-back ...
call.Done(nil, "obs.val", e.Val) // the observation the binding's expect reads
```

A call whose caller gave up (`Leak`) never answers: it stays pending, and
`qtest.Pending("write")` says some run left one.

**The settle, then a probe.** `Run` settles the cluster (or `SettleCore`
around the replicas that must finish alone), then records what each
replica holds and settles again, so the probe's steps are judged with the
eventual val:

```go
if err := s.Settle(time.Minute); err != nil {
	return err // an *Unsettled is the run's outcome: Settles() judges it
}
for /* each replica and key */ {
	qobs.Invoke(ctx, "observe", "replica", name, "key", key, "value", v).Done(nil)
}
qobs.Settled(ctx) // the probe came after Settle's own record
```

**Linearized per key.** The model treats keys independently, so each
key's history is searched alone:

```go
sys := qtest.System{Binding: "quorumkv.binding.yaml", Opts: []qtest.Option{qtest.LinearizeByKey("key")}, Run: run}
sys.Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
```

A contended key's search may need more than the default budget of 200
orders: `qtest.LinearizeBudget(1000)`; a report that says "no verdict
(linearize budget)" names the budget to raise.

**PCT for a bug that needs one task far ahead.** `BlindRepair` shows only
when a reader runs between a writer's replica writes, and another reader
after it. Uniform seeds rarely schedule that; PCT gives each task a
random priority, runs the highest, and at `depth-1` random points of a
run of about `steps` scheduling choices (points where several tasks
could run) drops the task it would run below every other:

```go
base := qenv.NewSeeded(seed)
base.Rates = sys.FaultRates()          // faults as the System's
chooser := qenv.NewPCT(base, seed, 2, 400) // depth 2: one change point in about 400 choices
```

Depth 2 finds orderings where one task overtakes another once (the
blind read between two writes); a larger depth, longer chains. Use
`ExploreConfig.Steer` instead when the rare thing is a state rather than
an ordering (a fault combination, a branch of the code): it replays runs
that reached something new and varies their tails.

## Run it

```sh
go test -short .           # needs quint on PATH; -short skips the contended-key tests
./run-demo.sh               # the faithful store
MUTANT=noHandoff ./run-demo.sh
```
