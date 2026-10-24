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

`Repick` is the bug of this store's first version: its first exploration
found it, as a history no order of its key explains. The same histories
showed what the toolkit's linearization search missed: its guided search
did not reorder two writes before the read that failed, and its
candidates were judged without the probe's eventual val; a complete
search (every order, prefix by prefix) follows a guided one that finds
none, and candidates carry the run's settle.

## Run it

```sh
go test -short .           # needs quint on PATH; -short skips the contended-key tests
./run-demo.sh               # the faithful store
MUTATION=no-handoff ./run-demo.sh
```
