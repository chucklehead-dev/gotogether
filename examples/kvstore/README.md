# examples/kvstore: an HTTP service checked against its model

A key-value service with versioned compare-and-swap:
- `kv/`: the store, the HTTP server and a client;
- `cmd/kvserver`: the service;
- `cmd/kvload`: a concurrent load generator.

`model/kv.qnt` is the service's Quint model.

The service is instrumented the way a production service would be. It
uses no Orchestrion and no build changes:
- the store reports each operation as a model step (`qobs.Record`) from
  inside the critical section that orders it;
- the HTTP server emits one ordinary OTel span per request, with the
  attributes an operator wants anyway.

Run it, put load on it, then check what it recorded:

```sh
./run-demo.sh                          # faithful: both checks pass
MUTATION=stale-cas ./run-demo.sh       # a race: two CAS with the same expected version both win
MUTATION=revision-on-conflict ./run-demo.sh
MUTATION=stale-read ./run-demo.sh      # reads from a lagging snapshot
MUTATION=late-revision-on-conflict ./run-demo.sh   # the revision bug after the first rotation: the last log alone shows it
MUTATION=stale-snapshot ./run-demo.sh  # a rotated log headed by an old state
```

```
$ gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl      (MUTATION=stale-cas)
  conformance: FAIL
    step 112 is not a transition of the model from the state reached
      quint:    cas("a", 39, "y", Ok)
      observed: cas(expected=39, key=a, value=y) (seq 112, span 85ea63b66b1bd28e)
      obs:      revision=46
```

Or by hand:

```sh
go run ./cmd/kvserver -addr 127.0.0.1:8080 -steps steps.jsonl -spans spans.jsonl &
go run ./cmd/kvload -addr 127.0.0.1:8080 -clients 8 -ops 100
kill -INT %1
gotogether validate -binding kv.binding.yaml steps.jsonl                              # the model steps
gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl     # the plain spans
```

The demo then runs the service again with its step log rotated
(`-rotate 120`) and validates the last file alone (below, *A rotated log,
checked alone*); the two rotation mutants show only there.

## What it shows

- **Checking a concurrent service.** The model is sequential. A
  concurrent run conforms if some serial order explains it: every CAS
  outcome, revision and read must be consistent. The recording gives that
  order:
  - steps are recorded under the store's lock;
  - for spans, the server stamps `kv.op`, the store's linearization
    index, and `kv.convert.yaml` uses it as an ordering key
    (`order: { op: attr:kv.op }`). Without it, spans of concurrent
    requests would be ordered by end time, which is not the order the
    store applied them in.
- **Errors that classify themselves.** `ErrConflict` and `ErrNotFound`
  implement `QuintOutcome()`. The binding maps `conflict` and `missing` to
  the model's `Outcome` values, so a CAS's outcome is checked as well as
  its effect.
- **Reads checked against the model.** A read is a model step that
  changes nothing. Its `expect` checks what it returned
  (`obs.found`, `obs.value`, `obs.version`) against the model's store.
- **Long runs.** A 400-step run is replayed in segments, because Quint's
  Rust evaluator limits `.then` nesting to about 50.
- **A rotated log, checked alone.** `kvserver -steps steps.jsonl -rotate
  N` starts a new step log every N operations (`steps.1.jsonl`,
  `steps.2.jsonl`, ...; `kv/rotate.go`). The recorder is the same, so the
  seqs go on across files, and a later file alone has lost its prefix:
  replayed from `init` it would be judged against an empty store, and
  validation says `error lost-prefix`. So the store heads each new file
  with its whole state, a step of the model's `restore(values, versions,
  rev)`, which assigns every state variable from its arguments. It
  records it under the lock that orders operations, right after the N-th,
  so no operation is in flight across the head. The binding names it
  (`snapshot: restore`): a file that starts with it is judged from that
  state (`info lost-prefix: judged from the state recorded by restore at
  seq 153`). The store is a map of records, which a recorded map cannot
  carry (`toMap` renders plain values), so the snapshot records two maps,
  values and versions, and `restore` zips them. A snapshot inside a
  longer trace (all the files together) is skipped: only a log's head is
  trusted as its start. So the head itself is checked only by what
  follows it: a stale one (`StaleSnapshot`) shows in the next operation,
  and not at all in the files read together.

## Tests (`kvstore_test.go`)

| Test | Direction | Shows |
|---|---|---|
| `TestServiceUnderLoadConforms` | 8 concurrent clients; recorded steps checked (`qtest`) | a concurrent run is a serial behaviour of the model |
| `TestServiceSpansConform` | the same load; plain spans converted and checked | spans plus `kv.op` give the same verdict |
| `TestStaleCASCaughtUnderLoad` | the race mutant under load | caught as a CAS the model cannot take |
| `TestClientHistoryLinearizes` | 4 clients record their own operations, with invocation times (`kv.LoadRecorded`) | in recording order the faithful store is rejected; `qtest.Linearize()` finds the order the intervals allow |
| `TestStaleReadNotLinearizable` | `StaleRead`: reads from a snapshot refreshed every fourth read, client-recorded | a read no order real time allows explains (`Mismatch("read")`) |
| `TestRotatedLogConformsAlone` | 4 clients, the log rotated every 40 operations; the last file alone | judged from its head's snapshot it conforms; without the head, the lost prefix's error |
| `TestLateMutantCaughtInLastLog` | `LateRevisionOnConflict`: the revision bug from the first rotation on | the last file alone shows the failed CAS (`Mismatch("cas")`), judged from its head |
| `TestStaleSnapshotCaught` | `StaleSnapshot`: each head is the previous rotation's state | the last file's first operation disagrees with its head (`Mismatch` or `Rejects`) |
| `TestModelDrivesService` | model traces drive the HTTP API (`connect.Run`) | store and revision match the model after every request |
| `TestHegelDrivesService` | a Hegel state machine over HTTP; runs checked after the test | |
| `TestHegelShrinksRevisionOnConflict` | the same machine against a mutant, checked per case | Hegel shrinks to one failed CAS |

```sh
go test ./examples/kvstore/ -v
```
